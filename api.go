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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"unified/internal/config"
	"unified/internal/disk"
	"unified/internal/notify"
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

	// One list sync, and one scan of the library folders, at a time.
	listsMu sync.Mutex
	scanMu  sync.Mutex

	// The last storage report; walking the library takes a moment.
	storageMu   sync.Mutex
	storageAt   time.Time
	storageLast []folderView
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
	go s.syncListsEvery(ctx, listInterval)
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
	api.HandleFunc("DELETE /api/media/{id}/files", s.deleteFiles)
	api.HandleFunc("POST /api/media/{id}/seasons/{season}", s.wantSeason)
	api.HandleFunc("DELETE /api/media/{id}/seasons/{season}/files", s.deleteSeasonFiles)
	api.HandleFunc("POST /api/media/{id}/seasons/{season}/cancel", s.cancelSeason)
	api.HandleFunc("POST /api/items/{id}", s.wantItem)
	api.HandleFunc("DELETE /api/items/{id}/files", s.deleteItemFiles)
	api.HandleFunc("GET /api/find", s.find)
	api.HandleFunc("GET /api/releases", s.releases)
	api.HandleFunc("POST /api/releases/grab", s.grabRelease)
	api.HandleFunc("POST /api/search", s.searchNow)
	api.HandleFunc("POST /api/downloads/{id}/fail", s.failDownload)
	api.HandleFunc("GET /api/settings", s.settings)
	api.HandleFunc("PUT /api/settings", s.saveSettings)
	api.HandleFunc("POST /api/indexers/test", s.testIndexer)
	api.HandleFunc("POST /api/notify/test", s.testNotify)
	api.HandleFunc("GET /api/storage", s.storage)
	api.HandleFunc("GET /api/debug/peers", s.debugPeers)
	api.HandleFunc("GET /api/history", s.history)
	api.HandleFunc("POST /api/scan", s.scan)
	api.HandleFunc("POST /api/scan/import", s.scanImport)
	api.HandleFunc("GET /api/discover", s.discover)
	api.HandleFunc("GET /api/lists", s.lists)
	api.HandleFunc("POST /api/lists", s.addList)
	api.HandleFunc("DELETE /api/lists/{id}", s.removeList)
	api.HandleFunc("POST /api/lists/{id}/sync", s.syncListNow)
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
	if st.Queued == nil {
		st.Queued = []Progress{}
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
	// The download bringing it, while it is downloading.
	DownloadID int64 `json:"downloadId,omitempty"`
	// Where the search for it stands, while it is wanted.
	Search *ItemSearch `json:"search,omitempty"`
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
	searches := s.run.Searches()
	for _, it := range items {
		iv := itemView{ID: it.ID, Season: it.Season, Episode: it.Episode, Title: it.Title, AirDate: it.AirDate, State: it.State, Path: it.Path}
		if it.State == store.Downloading {
			iv.DownloadID = it.Download.Int64
		}
		if search, ok := searches[it.ID]; ok && it.State == store.Wanted {
			iv.Search = &search
		}
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
	if m, err := s.st.Media(id); err == nil {
		s.st.AddEvent(store.EventAdded, id, describe(m, nil), "")
	}
	s.run.Wake()
	writeJSON(w, map[string]int64{"id": id})
}

// removeMedia removes a movie or show from the library, and with ?files=1
// deletes its files too.
func (s *server) removeMedia(w http.ResponseWriter, r *http.Request) {
	s.remove(w, r, r.FormValue("files") == "1", true)
}

// deleteFiles deletes the files of a movie or show but keeps it in the
// library, no longer wanted.
func (s *server) deleteFiles(w http.ResponseWriter, r *http.Request) {
	s.remove(w, r, true, false)
}

func (s *server) remove(w http.ResponseWriter, r *http.Request, deleteFiles, fromLibrary bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.st.Media(id); err != nil {
		writeError(w, http.StatusNotFound, errors.New("not in the library"))
		return
	}
	if err := s.run.Remove(r.Context(), id, deleteFiles, fromLibrary); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteSeasonFiles deletes the files of a season's episodes and stops
// wanting them.
func (s *server) deleteSeasonFiles(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	season, ok := pathID(w, r, "season")
	if !ok {
		return
	}
	items, err := s.st.Items(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var ids []int64
	for _, it := range items {
		if it.Season == int(season) {
			ids = append(ids, it.ID)
		}
	}
	if len(ids) == 0 {
		writeError(w, http.StatusNotFound, errors.New("no such season"))
		return
	}
	if err := s.run.DeleteItems(r.Context(), id, ids); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cancelSeason stops the season's running downloads and stops wanting its
// missing episodes. Episodes in the library stay.
func (s *server) cancelSeason(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	season, ok := pathID(w, r, "season")
	if !ok {
		return
	}
	items, err := s.st.Items(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var downloading []int64
	for _, it := range items {
		if it.Season == int(season) && it.State == store.Downloading {
			downloading = append(downloading, it.ID)
		}
	}
	// First, so nothing new is grabbed for the season meanwhile.
	if err := s.st.SetSeasonWanted(id, int(season), false); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.run.CancelItems(r.Context(), id, downloading); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteItemFiles deletes the file of one episode or movie and stops wanting
// it.
func (s *server) deleteItemFiles(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	it, err := s.st.Item(id)
	if err != nil {
		writeError(w, http.StatusNotFound, errors.New("no such episode"))
		return
	}
	if err := s.run.DeleteItems(r.Context(), it.MediaID, []int64{id}); err != nil {
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
	writeJSON(w, s.resultViews(kind, results[:min(len(results), 20)]))
}

type resultView struct {
	TMDBID    int    `json:"tmdbId"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Poster    string `json:"poster"`
	Overview  string `json:"overview"`
	LibraryID int64  `json:"libraryId,omitempty"`
}

// resultViews shows TMDB results, with the library's id for those in it.
func (s *server) resultViews(kind string, results []tmdb.Result) []resultView {
	out := []resultView{}
	for _, res := range results {
		out = append(out, resultView{
			res.ID, res.Title, res.Year, tmdb.ImageURL(res.Poster, "w342"), res.Overview, s.st.MediaByTMDB(kind, res.ID),
		})
	}
	return out
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
	rels, _ := searchAll(r.Context(), cfg, want, collect, nil)
	for _, c := range release.Rank(cfg.Quality, want, rels, s.st.Blocked) {
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
	// The release is named in scene numbering, so it covers the items in
	// the scene season or episode the page searched for.
	want, err := wantFor(s.st, m, req.Season, req.Episode)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	today := time.Now().Format("2006-01-02")
	var ids []int64
	for _, it := range items {
		season, episode := it.Scene()
		covered := !m.IsShow() ||
			season == want.Season && (episode == want.Episode ||
				want.Episode == 0 && it.AirDate != "" && it.AirDate <= today)
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

// transferView is how much bandwidth and how many connections torrents use.
type transferView struct {
	MaxDownloadKBps    int    `json:"maxDownloadKBps"`
	MaxUploadKBps      int    `json:"maxUploadKBps"`
	AltDownloadKBps    int    `json:"altDownloadKBps"`
	AltUploadKBps      int    `json:"altUploadKBps"`
	AltSchedule        string `json:"altSchedule"`
	MaxActiveDownloads int    `json:"maxActiveDownloads"`
	MaxActiveSeeds     int    `json:"maxActiveSeeds"`
	MaxConnections     int    `json:"maxConnections"`
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
		"tmdbKey":       c.TMDBKey,
		"traktClientId": c.TraktClientID,
		"indexers":      indexers,
		"quality": qualityView{
			Resolutions: nonNil(c.Quality.Resolutions), PreferCodecs: nonNil(c.Quality.PreferCodecs),
			MaxMovieGB: c.Quality.MaxMovieGB, MaxEpisodeGB: c.Quality.MaxEpisodeGB, MinSeeders: c.Quality.MinSeeders,
		},
		"seedRatio": c.SeedRatio,
		"minFreeGB": c.MinFreeGB,
		"transfer": transferView{
			c.MaxDownloadKBps, c.MaxUploadKBps, c.AltDownloadKBps, c.AltUploadKBps, c.AltSchedule,
			c.MaxActiveDownloads, c.MaxActiveSeeds, c.MaxConnections,
		},
		"notifyURL":       c.NotifyURL,
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
		// nil leaves these as they are.
		TraktClientID *string       `json:"traktClientId"`
		SeedRatio     *float64      `json:"seedRatio"`
		MinFreeGB     *float64      `json:"minFreeGB"`
		NotifyURL     *string       `json:"notifyURL"`
		Transfer      *transferView `json:"transfer"`
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
		if req.MinFreeGB != nil {
			c.MinFreeGB = *req.MinFreeGB
		}
		if req.NotifyURL != nil {
			c.NotifyURL = *req.NotifyURL
		}
		if req.TraktClientID != nil {
			c.TraktClientID = strings.TrimSpace(*req.TraktClientID)
		}
		if t := req.Transfer; t != nil {
			c.MaxDownloadKBps, c.MaxUploadKBps = t.MaxDownloadKBps, t.MaxUploadKBps
			c.AltDownloadKBps, c.AltUploadKBps, c.AltSchedule = t.AltDownloadKBps, t.AltUploadKBps, strings.TrimSpace(t.AltSchedule)
			c.MaxActiveDownloads, c.MaxActiveSeeds, c.MaxConnections = t.MaxActiveDownloads, t.MaxActiveSeeds, t.MaxConnections
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

func (s *server) testNotify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := notify.Send(r.Context(), req.URL, "unified", "Notifications work."); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// Storage.

type folderView struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	disk.Folder
	// The filesystem the folder is on, and the other folders on it.
	Free       uint64   `json:"free"`
	Total      uint64   `json:"total"`
	SameDiskAs []string `json:"sameDiskAs"`
	Error      string   `json:"error,omitempty"`
}

// storageCache is how long a storage report is reused.
const storageCache = 30 * time.Second

// storage reports what each of the download, movie and TV folders takes,
// with their largest entries, and the free space on their disks.
func (s *server) storage(w http.ResponseWriter, r *http.Request) {
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	if time.Since(s.storageAt) > storageCache || r.URL.Query().Has("fresh") {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		folders, err := measureFolders(ctx, s.conf.Get())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		s.storageAt, s.storageLast = time.Now(), folders
	}
	writeJSON(w, map[string]any{
		"folders":    s.storageLast,
		"disk":       s.run.Snapshot().Disk,
		"measuredAt": s.storageAt,
	})
}

func measureFolders(ctx context.Context, c *config.Config) ([]folderView, error) {
	dirs := []disk.Dir{{Label: "Downloads", Path: c.DownloadDir}, {Label: "Movies", Path: c.MoviesDir}, {Label: "TV", Path: c.TVDir}}
	volumes := disk.Volumes(dirs)
	var out []folderView
	for _, d := range dirs {
		f := folderView{Label: d.Label, Path: d.Path, SameDiskAs: []string{}}
		for _, v := range volumes {
			if slices.Contains(v.Labels, d.Label) {
				f.Free, f.Total, f.Error = v.Free, v.Total, v.Error
				for _, l := range v.Labels {
					if l != d.Label {
						f.SameDiskAs = append(f.SameDiskAs, l)
					}
				}
			}
		}
		folder, err := disk.Measure(ctx, d.Path)
		if ctx.Err() != nil {
			return nil, fmt.Errorf("measuring %s took too long", d.Path)
		}
		if err != nil && f.Error == "" {
			f.Error = err.Error()
		}
		f.Folder = folder
		f.Entries = f.Entries[:min(len(f.Entries), 100)]
		out = append(out, f)
	}
	return out, nil
}

// debugPeers is the torrent client's own report on every torrent and peer,
// as plain text: for telling a dead swarm from a problem in unified.
func (s *server) debugPeers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, `Each peer's "flags: A:B:C" (on its reqq line):
  A is us:   i = we want data from them, c = we choke them
  B is how:  U = uTP (else TCP), E/e = encrypted
  C is them: i = they want data from us, c = they choke us, so they send nothing
"last helpful" is when they last sent us useful data.

`)
	s.run.eng.WriteStatus(w)
}
