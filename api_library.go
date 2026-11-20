package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"unified/internal/store"
	"unified/internal/tmdb"
)

// History.

// history lists events, newest first: ?media= for one movie or show,
// ?events=grabbed,failed for some kinds, ?before= an event id to page back.
func (s *server) history(w http.ResponseWriter, r *http.Request) {
	q := store.HistoryQuery{Limit: 100}
	q.MediaID, _ = strconv.ParseInt(r.FormValue("media"), 10, 64)
	q.Before, _ = strconv.ParseInt(r.FormValue("before"), 10, 64)
	if n, err := strconv.Atoi(r.FormValue("limit")); err == nil {
		q.Limit = min(max(n, 1), 500)
	}
	if ev := r.FormValue("events"); ev != "" {
		q.Events = strings.Split(ev, ",")
	}
	// One more than asked for tells whether there are more.
	q.Limit++
	events, err := s.st.History(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	more := len(events) == q.Limit
	if more {
		events = events[:q.Limit-1]
	}
	type eventView struct {
		store.Event
		InLibrary bool `json:"inLibrary"`
	}
	out := []eventView{}
	inLibrary := map[int64]bool{}
	if media, err := s.st.AllMedia(); err == nil {
		for _, m := range media {
			inLibrary[m.ID] = true
		}
	}
	for _, e := range events {
		out = append(out, eventView{e, inLibrary[e.MediaID]})
	}
	writeJSON(w, map[string]any{"events": out, "more": more})
}

// frontPage returns the movies and shows that arrived most recently, newest
// first, each with what of it arrived: the stories of the library's front
// page.
func (s *server) frontPage(w http.ResponseWriter, r *http.Request) {
	events, err := s.st.History(store.HistoryQuery{Events: []string{store.EventImported}, Limit: 300})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	type story struct {
		mediaView
		At time.Time `json:"at"` // when the newest of it arrived
		// What arrived, newest first, as the history names it; for a show,
		// the episodes that arrived within a week of the newest.
		Arrived []string `json:"arrived"`
	}
	stories := []*story{}
	byMedia := map[int64]*story{}
	for _, e := range events {
		if st, ok := byMedia[e.MediaID]; ok {
			if st.At.Sub(e.At) < 7*24*time.Hour && !slices.Contains(st.Arrived, e.Title) {
				st.Arrived = append(st.Arrived, e.Title)
			}
			continue
		}
		if len(stories) == 4 {
			continue
		}
		m, err := s.st.Media(e.MediaID)
		if err != nil {
			continue // no longer in the library
		}
		items, err := s.st.Items(m.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		v := newMediaView(m, items)
		v.Overview, v.Poster = m.Overview, tmdb.ImageURL(m.Poster, "w500")
		st := &story{mediaView: v, At: e.At, Arrived: []string{e.Title}}
		byMedia[m.ID] = st
		stories = append(stories, st)
	}
	writeJSON(w, stories)
}

// Scanning the library folders.

func (s *server) scan(w http.ResponseWriter, r *http.Request) {
	if !s.scanMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("a scan is running already"))
		return
	}
	defer s.scanMu.Unlock()
	cfg := s.conf.Get()
	if cfg.TMDBKey == "" {
		writeError(w, http.StatusBadRequest, errors.New("add a TMDB key in the settings first"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	entries, err := scanLibrary(ctx, cfg, s.st)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if entries == nil {
		entries = []scanEntry{}
	}
	writeJSON(w, map[string]any{"entries": entries, "moviesDir": cfg.MoviesDir, "tvDir": cfg.TVDir})
}

func (s *server) scanImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Choices     []scanChoice `json:"choices"`
		WantMissing bool         `json:"wantMissing"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !s.scanMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("a scan is running already"))
		return
	}
	defer s.scanMu.Unlock()
	results := importScanned(s.conf.Get(), s.st, req.Choices, req.WantMissing)
	files := 0
	for _, res := range results {
		files += res.Imported
	}
	s.run.logf("took %d files of %d movies and shows into the library", files, len(results))
	if req.WantMissing {
		s.run.Wake()
	}
	if results == nil {
		results = []scanResult{}
	}
	writeJSON(w, results)
}

// Discovering movies and shows.

// discover pages through one of TMDB's lists: ?kind=movie|show,
// ?list=trending|popular|top_rated|upcoming, ?page= from 1.
func (s *server) discover(w http.ResponseWriter, r *http.Request) {
	kind := r.FormValue("kind")
	if kind != "show" {
		kind = "movie"
	}
	list := r.FormValue("list")
	if list == "" {
		list = tmdb.Trending
	}
	page, _ := strconv.Atoi(r.FormValue("page"))
	results, pages, err := tmdb.New(s.conf.Get().TMDBKey).Browse(kind, list, max(page, 1))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]any{"results": s.resultViews(kind, results), "pages": pages})
}

// Lists.

func (s *server) lists(w http.ResponseWriter, r *http.Request) {
	lists, err := s.st.Lists()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"lists": lists, "trakt": s.conf.Get().TraktClientID != ""})
}

// addList adds a list and syncs it at once, so its movies and shows are in
// the library when it answers.
func (s *server) addList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string `json:"kind"`
		Source string `json:"source"`
		Want   string `json:"want"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	source, err := normalizeList(req.Kind, req.Source)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	switch req.Want {
	case store.WantAll, store.WantFirst, store.WantNone:
	default:
		writeError(w, http.StatusBadRequest, errors.New(`want must be "all", "first" or "none"`))
		return
	}
	l := store.List{Kind: req.Kind, Source: source, Want: req.Want}
	// Fetched once first, so a list that cannot be read is not added.
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	name, entries, err := listEntries(ctx, s.conf.Get(), l)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	l.Name = name
	if l.ID, err = s.st.AddList(l); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			err = errors.New("that list is added already")
		}
		writeError(w, http.StatusConflict, err)
		return
	}
	s.run.logf("added the list %s (%d movies and shows)", l.Name, len(entries))
	s.syncOne(w, r, l)
}

func (s *server) removeList(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.st.RemoveList(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) syncListNow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	l, err := s.st.List(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, errors.New("no such list"))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.syncOne(w, r, l)
}

// syncOne syncs a list and answers with the titles it added.
func (s *server) syncOne(w http.ResponseWriter, r *http.Request, l store.List) {
	s.listsMu.Lock()
	defer s.listsMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	added, err := syncList(ctx, s.conf.Get(), s.st, l, s.run.logf)
	if len(added) > 0 {
		s.run.logf("added %s from %s", strings.Join(added, ", "), l.Name)
		s.run.Wake()
	}
	if added == nil {
		added = []string{}
	}
	out := map[string]any{"id": l.ID, "added": added}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, out)
}
