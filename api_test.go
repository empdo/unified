package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unified/internal/config"
	"unified/internal/store"
)

// newTestServer returns a server with a fresh config file and database. The
// runner has no torrent engine; the handlers under test don't need one.
func newTestServer(t *testing.T, password string) (*server, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `data_dir = "` + dir + `/data"
download_dir = "` + dir + `/downloads"
movies_dir = "` + dir + `/movies"
tv_dir = "` + dir + `/tv"
password = "` + password + `"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	conf, err := config.NewLive(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &server{conf: conf, st: st, run: &runner{wake: make(chan struct{}, 1)}}
	return s, s.routes()
}

func do(h http.Handler, method, path, body, password string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if password != "" {
		r.SetBasicAuth("anyone", password)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLoginAndHealth(t *testing.T) {
	s, h := newTestServer(t, "hunter2")
	for _, c := range []struct {
		path, password string
		want           int
	}{
		{"/api/library", "", 401},
		{"/api/library", "wrong", 401},
		{"/api/library", "hunter2", 200},
		{"/", "hunter2", 200},
		{"/health", "", 200},
	} {
		if got := do(h, "GET", c.path, "", c.password).Code; got != c.want {
			t.Errorf("GET %s with %q: %d, want %d", c.path, c.password, got, c.want)
		}
	}
	s.run.vpnErr = `VPN interface "wg0" not found`
	if got := do(h, "GET", "/health", "", "").Code; got != 503 {
		t.Errorf("health with VPN down: %d, want 503", got)
	}
}

func TestServesApp(t *testing.T) {
	_, h := newTestServer(t, "")
	w := do(h, "GET", "/", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<div id="root">`) {
		t.Fatalf("GET /: %d\n%s", w.Code, w.Body.String())
	}
	if w := do(h, "GET", "/api/nope", "", ""); w.Code != 404 || !strings.Contains(w.Body.String(), "error") {
		t.Errorf("unknown endpoint: %d %s", w.Code, w.Body.String())
	}
}

func TestLibraryAndWanting(t *testing.T) {
	s, h := newTestServer(t, "")
	id, err := s.st.AddMedia(store.Media{Kind: "show", TMDBID: 95396, Title: "Severance", Year: 2022, Poster: "/p.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	for ep := 1; ep <= 3; ep++ {
		s.st.UpsertItem(id, 1, ep, "", "2022-02-18", false)
	}

	var lib []mediaView
	json.Unmarshal(do(h, "GET", "/api/library", "", "").Body.Bytes(), &lib)
	if len(lib) != 1 || lib[0].Poster != "https://image.tmdb.org/t/p/w342/p.jpg" || lib[0].Counts["ignored"] != 3 {
		t.Fatalf("library = %+v", lib)
	}

	if w := do(h, "POST", "/api/media/1/seasons/1", `{"want":true}`, ""); w.Code != 204 {
		t.Fatalf("want season: %d %s", w.Code, w.Body.String())
	}
	var detail struct {
		Seasons []seasonView `json:"seasons"`
	}
	json.Unmarshal(do(h, "GET", "/api/media/1", "", "").Body.Bytes(), &detail)
	if len(detail.Seasons) != 1 || detail.Seasons[0].Episodes[2].State != store.Wanted {
		t.Fatalf("after wanting the season: %+v", detail)
	}

	first := detail.Seasons[0].Episodes[0].ID
	do(h, "POST", "/api/items/"+itoa(first), `{"want":false}`, "")
	json.Unmarshal(do(h, "GET", "/api/media/1", "", "").Body.Bytes(), &detail)
	if detail.Seasons[0].Episodes[0].State != store.Ignored {
		t.Errorf("episode not unwanted: %+v", detail.Seasons[0].Episodes[0])
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s, h := newTestServer(t, "")
	body := `{"tmdbKey":"k","indexers":[{"name":"p","url":"http://x/1/api","apiKey":"a"}],
		"quality":{"resolutions":["2160p","1080p"],"preferCodecs":["x265"],"maxMovieGB":20,"maxEpisodeGB":5,"minSeeders":3},
		"password":"secret"}`
	if w := do(h, "PUT", "/api/settings", body, ""); w.Code != 204 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if got := s.conf.Get(); got.TMDBKey != "k" || got.Quality.Resolutions[0] != "2160p" || got.Password != "secret" {
		t.Fatalf("live config not updated: %+v", got)
	}
	// The password applies at once.
	if w := do(h, "GET", "/api/settings", "", ""); w.Code != 401 {
		t.Errorf("without the new password: %d, want 401", w.Code)
	}
	var got struct {
		Indexers []indexerView `json:"indexers"`
		Quality  qualityView   `json:"quality"`
	}
	json.Unmarshal(do(h, "GET", "/api/settings", "", "secret").Body.Bytes(), &got)
	if len(got.Indexers) != 1 || got.Quality.MinSeeders != 3 {
		t.Errorf("settings read back: %+v", got)
	}
	// Leaving the password out keeps it.
	body = strings.Replace(body, `,
		"password":"secret"`, "", 1)
	do(h, "PUT", "/api/settings", body, "secret")
	if s.conf.Get().Password != "secret" {
		t.Error("password was cleared by a save that did not mention it")
	}
	// Bad input is refused and changes nothing.
	if w := do(h, "PUT", "/api/settings", `{"quality":{"resolutions":[]}}`, "secret"); w.Code != 400 {
		t.Errorf("empty resolutions: %d, want 400", w.Code)
	}
	if s.conf.Get().TMDBKey != "k" {
		t.Error("a refused save changed the config")
	}
}

func TestGrabRelease(t *testing.T) {
	s, h := newTestServer(t, "")
	// Stand in for the runner loop: accept every grab and remember its items.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.run.ctx, s.run.grabs = ctx, make(chan grabRequest)
	got := make(chan []int64, 1)
	go func() {
		for g := range s.run.grabs {
			got <- g.items
			g.reply <- nil
		}
	}()

	id, _ := s.st.AddMedia(store.Media{Kind: "show", TMDBID: 95396, Title: "Severance", Year: 2022})
	s.st.UpsertItem(id, 1, 1, "", "2022-02-18", false)
	s.st.UpsertItem(id, 1, 2, "", "2022-02-25", true)
	s.st.UpsertItem(id, 1, 3, "", "2999-01-01", true) // not aired
	s.st.UpsertItem(id, 2, 1, "", "2025-01-17", true)
	items, _ := s.st.Items(id)
	ids := map[[2]int]int64{}
	for _, it := range items {
		ids[[2]int{it.Season, it.Episode}] = it.ID
	}

	grab := func(season, episode int) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"media": id, "season": season, "episode": episode, "title": "Severance.S01.1080p-GRP", "link": "magnet:?xt=urn:btih:x"})
		return do(h, "POST", "/api/releases/grab", string(body), "")
	}
	if w := grab(1, 0); w.Code != 204 {
		t.Fatalf("season grab: %d %s", w.Code, w.Body.String())
	}
	if g := <-got; len(g) != 2 || g[0] != ids[[2]int{1, 1}] || g[1] != ids[[2]int{1, 2}] {
		t.Errorf("season grab covered %v, want the two aired episodes of season 1", g)
	}
	if w := grab(2, 1); w.Code != 204 {
		t.Fatalf("episode grab: %d %s", w.Code, w.Body.String())
	}
	if g := <-got; len(g) != 1 || g[0] != ids[[2]int{2, 1}] {
		t.Errorf("episode grab covered %v, want S02E01", g)
	}

	// An episode that is already downloading is left out.
	if _, err := s.st.CreateDownload("abc", "x", "magnet:", []int64{ids[[2]int{2, 1}]}); err != nil {
		t.Fatal(err)
	}
	if w := grab(2, 1); w.Code != 409 {
		t.Errorf("grab of a downloading episode: %d, want 409", w.Code)
	}
}

func TestSeedRatioSetting(t *testing.T) {
	s, h := newTestServer(t, "")
	body := `{"quality":{"resolutions":["1080p"]},"seedRatio":1.5}`
	if w := do(h, "PUT", "/api/settings", body, ""); w.Code != 204 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if got := s.conf.Get().SeedRatio; got != 1.5 {
		t.Errorf("seed ratio = %v, want 1.5", got)
	}
	// Leaving it out keeps it; a negative ratio is refused.
	do(h, "PUT", "/api/settings", `{"quality":{"resolutions":["1080p"]}}`, "")
	if w := do(h, "PUT", "/api/settings", `{"quality":{"resolutions":["1080p"]},"seedRatio":-1}`, ""); w.Code != 400 {
		t.Errorf("negative ratio: %d, want 400", w.Code)
	}
	if got := s.conf.Get().SeedRatio; got != 1.5 {
		t.Errorf("seed ratio = %v after saves that should not change it, want 1.5", got)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestHistoryAndLists(t *testing.T) {
	s, h := newTestServer(t, "")
	s.st.AddEvent(store.EventAdded, 0, "Film (2020)", "")
	s.st.AddEvent(store.EventGrabbed, 0, "Film (2020)", "Film.2020.1080p")
	w := do(h, "GET", "/api/history?limit=1", "", "")
	var hist struct {
		Events []store.Event `json:"events"`
		More   bool          `json:"more"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &hist); err != nil || w.Code != 200 {
		t.Fatalf("history: %d %s", w.Code, w.Body)
	}
	if len(hist.Events) != 1 || hist.Events[0].Event != store.EventGrabbed || !hist.More {
		t.Errorf("history: %+v", hist)
	}

	for _, body := range []string{
		`{"kind": "imdb", "source": "x", "want": "all"}`,
		`{"kind": "tmdb-list", "source": "not a list", "want": "all"}`,
		`{"kind": "tmdb-list", "source": "8123", "want": "some"}`,
	} {
		if w := do(h, "POST", "/api/lists", body, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := do(h, "GET", "/api/lists", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"lists":[]`) {
		t.Errorf("lists: %d %s", w.Code, w.Body)
	}
}
