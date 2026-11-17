package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"time"

	"unified/internal/config"
	"unified/internal/release"
	"unified/internal/store"
	"unified/internal/tmdb"
	"unified/internal/torznab"
)

// The React app in ui/, built with `pnpm build`. Everything is served under
// relative paths, so it also works behind a path prefix, as Home Assistant
// ingress uses.
//
//go:embed all:ui/dist
var uiFiles embed.FS

type server struct {
	conf *config.Live
	st   *store.Store
	run  *runner
}

func cmdServe(ctx context.Context, st *store.Store, args []string) error {
	addr := "127.0.0.1:8080"
	switch {
	case len(args) == 2 && args[0] == "--addr":
		addr = args[1]
	case len(args) != 0:
		return fmt.Errorf("usage: unified serve [--addr host:port]")
	}
	conf, err := config.NewLive(config.DefaultPath())
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	r, err := newRunner(ctx, conf.Get, st)
	if err != nil {
		ln.Close()
		return err
	}
	defer r.Close()
	s := &server{conf: conf, st: st, run: r}
	srv := &http.Server{Handler: s.routes()}
	go srv.Serve(ln)
	go s.fillArt(ctx)
	fmt.Printf("web UI at http://%s\n", ln.Addr())

	err = r.loop(true)
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdown)
	return err
}

func (s *server) routes() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/status", s.status)
	api.HandleFunc("GET /api/library", s.library)
	api.HandleFunc("POST /api/media", s.addMedia)
	api.HandleFunc("GET /api/media/{id}", s.media)
	api.HandleFunc("DELETE /api/media/{id}", s.removeMedia)
	api.HandleFunc("POST /api/media/{id}/seasons/{season}", s.wantSeason)
	api.HandleFunc("POST /api/items/{id}", s.wantItem)
	api.HandleFunc("GET /api/find", s.find)
	api.HandleFunc("GET /api/releases", s.releases)
	api.HandleFunc("POST /api/releases/grab", s.grabRelease)
	api.HandleFunc("POST /api/search", s.searchNow)
	api.HandleFunc("POST /api/downloads/{id}/fail", s.failDownload)
	api.HandleFunc("GET /api/settings", s.settings)
	api.HandleFunc("PUT /api/settings", s.saveSettings)
	api.HandleFunc("POST /api/indexers/test", s.testIndexer)
	api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, errors.New("no such API endpoint"))
	})
	dist, _ := fs.Sub(uiFiles, "ui/dist")
	api.Handle("/", http.FileServerFS(dist))

	outer := http.NewServeMux()
	outer.HandleFunc("GET /health", s.health)
	outer.Handle("/", s.requireLogin(api))
	return outer
}

// requireLogin asks for the configured password with HTTP basic auth. Any
// user name is accepted. The password is read per request, so changing it in
// the settings applies at once.
func (s *server) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pw := s.conf.Get().Password; pw != "" {
			_, given, _ := r.BasicAuth()
			want, got := sha256.Sum256([]byte(pw)), sha256.Sum256([]byte(given))
			if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="unified", charset="UTF-8"`)
				http.Error(w, "login required", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// health is for container health checks: 200 while torrents can run, 503
// while the VPN is down.
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if st := s.run.Snapshot(); st.VPNErr != "" {
		http.Error(w, st.VPNErr, http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintln(w, "ok")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid %s", name))
	}
	return id, err == nil
}

// Status and downloads.

type downloadView struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

func (s *server) status(w http.ResponseWriter, r *http.Request) {
	st := s.run.Snapshot()
	st.Log = st.Log[:min(len(st.Log), 50)]
	dls, err := s.st.Downloads(false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	recent := []downloadView{}
	for i := len(dls) - 1; i >= 0 && len(recent) < 20; i-- {
		if dls[i].State != store.Active {
			recent = append(recent, downloadView{dls[i].ID, dls[i].Name, dls[i].State})
		}
	}
	if st.Active == nil {
		st.Active = []Progress{}
	}
	if st.Seeding == nil {
		st.Seeding = []Progress{}
	}
	if st.Log == nil {
		st.Log = []string{}
	}
	writeJSON(w, map[string]any{
		"status":    st,
		"recent":    recent,
		"tmdb":      s.conf.Get().TMDBKey != "",
		"indexers":  len(s.conf.Get().Indexers),
		"seedRatio": s.conf.Get().SeedRatio,
	})
}

func (s *server) searchNow(w http.ResponseWriter, r *http.Request) {
	s.run.Wake()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) failDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	s.run.Fail(id)
	w.WriteHeader(http.StatusNoContent)
}

// Library.

type mediaView struct {
	ID       int64          `json:"id"`
	Kind     string         `json:"kind"`
	TMDBID   int            `json:"tmdbId"`
	Title    string         `json:"title"`
	Year     int            `json:"year"`
	Poster   string         `json:"poster"` // full image URL, "" when unknown
	Overview string         `json:"overview,omitempty"`
	Counts   map[string]int `json:"counts"` // items per state
}

func newMediaView(m store.Media, items []store.Item) mediaView {
	v := mediaView{
		ID: m.ID, Kind: m.Kind, TMDBID: m.TMDBID, Title: m.Title, Year: m.Year,
		Poster: tmdb.ImageURL(m.Poster, "w342"), Counts: map[string]int{},
	}
	for _, it := range items {
		v.Counts[it.State]++
	}
	return v
}

func (s *server) library(w http.ResponseWriter, r *http.Request) {
	media, err := s.st.AllMedia()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := []mediaView{}
	for i := len(media) - 1; i >= 0; i-- {
		items, err := s.st.Items(media[i].ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, newMediaView(media[i], items))
	}
	writeJSON(w, out)
}

type itemView struct {
	ID      int64  `json:"id"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Title   string `json:"title"`
	AirDate string `json:"airDate"`
	State   string `json:"state"`
	Path    string `json:"path,omitempty"`
}

type seasonView struct {
	Number   int        `json:"number"`
	Episodes []itemView `json:"episodes"`
}

func (s *server) media(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	m, err := s.st.Media(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("not in the library"))
		return
	}
	items, err := s.st.Items(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	v := newMediaView(m, items)
	v.Overview = m.Overview
	out := struct {
		mediaView
		Movie   *itemView    `json:"movie,omitempty"`
		Seasons []seasonView `json:"seasons"`
	}{mediaView: v, Seasons: []seasonView{}}
	for _, it := range items {
		iv := itemView{it.ID, it.Season, it.Episode, it.Title, it.AirDate, it.State, it.Path}
		if !m.IsShow() {
			out.Movie = &iv
			break
		}
		if n := len(out.Seasons); n == 0 || out.Seasons[n-1].Number != it.Season {
			out.Seasons = append(out.Seasons, seasonView{Number: it.Season})
		}
		last := &out.Seasons[len(out.Seasons)-1]
		last.Episodes = append(last.Episodes, iv)
	}
	writeJSON(w, out)
}

func (s *server) addMedia(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind    string `json:"kind"`
		TMDBID  int    `json:"tmdbId"`
		WantAll bool   `json:"wantAll"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Kind != "movie" && req.Kind != "show" {
		writeError(w, http.StatusBadRequest, errors.New(`kind must be "movie" or "show"`))
		return
	}
	cfg := s.conf.Get()
	pick, err := tmdb.New(cfg.TMDBKey).Details(req.Kind, req.TMDBID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	id, _, _, err := addToLibrary(cfg, s.st, req.Kind, pick, func(int, int) bool { return req.WantAll })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.run.Wake()
	writeJSON(w, map[string]int64{"id": id})
}

func (s *server) removeMedia(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.st.RemoveMedia(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type wantRequest struct {
	Want bool `json:"want"`
}

func (s *server) wantSeason(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	season, ok := pathID(w, r, "season")
	if !ok {
		return
	}
	var req wantRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.st.SetSeasonWanted(id, int(season), req.Want); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if req.Want {
		s.run.Wake()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) wantItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req wantRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.st.SetItemWanted(id, req.Want); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if req.Want {
		s.run.Wake()
	}
	w.WriteHeader(http.StatusNoContent)
}

// fillArt looks up posters and overviews for media added before they were
// stored.
func (s *server) fillArt(ctx context.Context) {
	media, err := s.st.AllMedia()
	if err != nil {
		return
	}
	for _, m := range media {
		key := s.conf.Get().TMDBKey
		if m.Poster != "" || key == "" || ctx.Err() != nil {
			continue
		}
		d, err := tmdb.New(key).Details(m.Kind, m.TMDBID)
		if err != nil {
			s.run.logf("could not fetch poster for %s: %v", m.Title, err)
			continue
		}
		s.st.SetArt(m.ID, d.Poster, d.Overview)
	}
}

// Search.

func (s *server) find(w http.ResponseWriter, r *http.Request) {
	kind := r.FormValue("kind")
	if kind != "show" {
		kind = "movie"
	}
	results, err := tmdb.New(s.conf.Get().TMDBKey).Search(kind, r.FormValue("q"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	media, err := s.st.AllMedia()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	inLibrary := map[int]int64{}
	for _, m := range media {
		if m.Kind == kind {
			inLibrary[m.TMDBID] = m.ID
		}
	}
	type resultView struct {
		TMDBID    int    `json:"tmdbId"`
		Title     string `json:"title"`
		Year      int    `json:"year"`
		Poster    string `json:"poster"`
		Overview  string `json:"overview"`
		LibraryID int64  `json:"libraryId,omitempty"`
	}
	out := []resultView{}
	for _, res := range results[:min(len(results), 20)] {
		out = append(out, resultView{
			res.ID, res.Title, res.Year, tmdb.ImageURL(res.Poster, "w342"), res.Overview, inLibrary[res.ID],
		})
	}
	writeJSON(w, out)
}

func (s *server) releases(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.FormValue("media"), 10, 64)
	m, err := s.st.Media(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("not in the library"))
		return
	}
	season, _ := strconv.Atoi(r.FormValue("season"))
	episode, _ := strconv.Atoi(r.FormValue("episode"))
	want, err := wantFor(s.st, m, season, episode)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	cfg := s.conf.Get()
	errs := []string{}
	collect := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	if len(cfg.Indexers) == 0 {
		collect("No indexers are configured.")
	}
	type candidateView struct {
		Title      string `json:"title"`
		Indexer    string `json:"indexer"`
		Size       int64  `json:"size"`
		Seeders    int    `json:"seeders"`
		Resolution string `json:"resolution"`
		Source     string `json:"source"`
		Codec      string `json:"codec"`
		Group      string `json:"group"`
		Reject     string `json:"reject,omitempty"`
		Link       string `json:"link"`
	}
	out := []candidateView{}
	for _, c := range release.Rank(cfg.Quality, want, searchAll(r.Context(), cfg, want, collect), s.st.Blocked) {
		out = append(out, candidateView{
			c.Title, c.Indexer, c.Size, c.Seeders,
			c.Info.Resolution, c.Info.Source, c.Info.Codec, c.Info.Group, c.Reject, c.Link,
		})
	}
	writeJSON(w, map[string]any{"query": want.Query(), "errors": errs, "candidates": out})
}

// grabRelease downloads a release picked on the releases page, even one the
// ranking rejected. It is for the same movie, episode or season (episode 0)
// the page searched for, and covers those items that are not downloading
// already. For a season, that is the episodes aired so far.
func (s *server) grabRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Media   int64  `json:"media"`
		Season  int    `json:"season"`
		Episode int    `json:"episode"`
		Title   string `json:"title"`
		Link    string `json:"link"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Title == "" || req.Link == "" {
		writeError(w, http.StatusBadRequest, errors.New("title and link are required"))
		return
	}
	m, err := s.st.Media(req.Media)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("not in the library"))
		return
	}
	items, err := s.st.Items(m.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	today := time.Now().Format("2006-01-02")
	var ids []int64
	for _, it := range items {
		covered := !m.IsShow() ||
			it.Season == req.Season && (it.Episode == req.Episode ||
				req.Episode == 0 && it.AirDate != "" && it.AirDate <= today)
		if covered && it.State != store.Downloading {
			ids = append(ids, it.ID)
		}
	}
	if len(ids) == 0 {
		writeError(w, http.StatusConflict, errors.New("already downloading; mark that download as failed first"))
		return
	}
	if err := s.run.Grab(r.Context(), ids, req.Title, req.Link); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Settings.

type indexerView struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	APIKey string `json:"apiKey"`
}

type qualityView struct {
	Resolutions  []string `json:"resolutions"`
	PreferCodecs []string `json:"preferCodecs"`
	MaxMovieGB   float64  `json:"maxMovieGB"`
	MaxEpisodeGB float64  `json:"maxEpisodeGB"`
	MinSeeders   int      `json:"minSeeders"`
}

func (s *server) settings(w http.ResponseWriter, r *http.Request) {
	c := s.conf.File()
	live := s.conf.Get()
	indexers := []indexerView{}
	for _, ix := range c.Indexers {
		indexers = append(indexers, indexerView{ix.Name, ix.URL, ix.APIKey})
	}
	writeJSON(w, map[string]any{
		"tmdbKey":  c.TMDBKey,
		"indexers": indexers,
		"quality": qualityView{
			Resolutions: nonNil(c.Quality.Resolutions), PreferCodecs: nonNil(c.Quality.PreferCodecs),
			MaxMovieGB: c.Quality.MaxMovieGB, MaxEpisodeGB: c.Quality.MaxEpisodeGB, MinSeeders: c.Quality.MinSeeders,
		},
		"seedRatio":       c.SeedRatio,
		"passwordSet":     live.Password != "",
		"passwordFromEnv": config.FromEnv("password"),
		// Shown for reference; these change in the config file and need a restart.
		"fixed": map[string]any{
			"dataDir":         c.DataDir,
			"downloadDir":     c.DownloadDir,
			"moviesDir":       c.MoviesDir,
			"tvDir":           c.TVDir,
			"listenPort":      c.ListenPort,
			"vpnInterface":    live.VPNInterface,
			"vpnFromEnv":      config.FromEnv("vpn_interface"),
			"configPath":      config.DefaultPath(),
			"restartRequired": "Changes to these need an edit of the config file and a restart.",
		},
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TMDBKey  string        `json:"tmdbKey"`
		Indexers []indexerView `json:"indexers"`
		Quality  qualityView   `json:"quality"`
		// nil leaves it as it is.
		SeedRatio *float64 `json:"seedRatio"`
		// nil leaves the password as it is; "" removes it.
		Password *string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Quality.Resolutions) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("accept at least one resolution"))
		return
	}
	err := s.conf.Update(func(c *config.Config) error {
		c.TMDBKey = req.TMDBKey
		c.Indexers = nil
		for _, ix := range req.Indexers {
			if ix.URL == "" {
				return fmt.Errorf("indexer %q has no URL", ix.Name)
			}
			c.Indexers = append(c.Indexers, config.Indexer{Name: ix.Name, URL: ix.URL, APIKey: ix.APIKey})
		}
		c.Quality = config.Quality{
			Resolutions: req.Quality.Resolutions, PreferCodecs: req.Quality.PreferCodecs,
			MaxMovieGB: req.Quality.MaxMovieGB, MaxEpisodeGB: req.Quality.MaxEpisodeGB, MinSeeders: req.Quality.MinSeeders,
		}
		if req.SeedRatio != nil {
			c.SeedRatio = *req.SeedRatio
		}
		if req.Password != nil {
			c.Password = *req.Password
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.run.logf("settings saved")
	s.run.Wake()
	go s.fillArt(context.Background())
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) testIndexer(w http.ResponseWriter, r *http.Request) {
	var ix indexerView
	if !readJSON(w, r, &ix) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := torznab.Test(ctx, config.Indexer{Name: ix.Name, URL: ix.URL, APIKey: ix.APIKey}); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
