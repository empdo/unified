package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"unified/internal/config"
	"unified/internal/store"
	"unified/internal/tmdb"
	"unified/internal/trakt"
)

// Lists are watchlists and lists on Trakt or TMDB. Whatever appears on one is
// added to the library, and with the list's want setting, downloaded. An
// entry is handled once: removed from the library, it is not added again.

// listInterval is how often lists are synced while unified serves.
const listInterval = 6 * time.Hour

// listEntry is a movie or show on a list.
type listEntry struct {
	Kind   string
	TMDBID int
	Title  string
}

// normalizeList checks a list's kind and source and returns the source in
// the form it is stored in, so the same list is not added twice.
func normalizeList(kind, source string) (string, error) {
	source = strings.TrimSpace(source)
	switch kind {
	case store.TraktWatchlist:
		if u, err := url.Parse(source); err == nil && u.Host != "" {
			// https://trakt.tv/users/someone/watchlist
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) >= 2 && parts[0] == "users" {
				source = parts[1]
			}
		}
		if source == "" || strings.Contains(source, "/") {
			return "", errors.New("give the Trakt user name, or the address of their watchlist")
		}
		return source, nil
	case store.TraktList:
		user, slug, err := trakt.ParseList(source)
		if err != nil {
			return "", err
		}
		return user + "/" + slug, nil
	case store.TMDBList:
		id := tmdbListID(source)
		if id == "" {
			return "", errors.New("give the TMDB list's number, or its address on themoviedb.org")
		}
		return id, nil
	}
	return "", fmt.Errorf("unknown list kind %q", kind)
}

var tmdbListPath = regexp.MustCompile(`(?:^|/list/)(\d+)`)

// tmdbListID returns the id in "8123", or in a list's address such as
// https://www.themoviedb.org/list/8123-best-films, "" when there is none.
func tmdbListID(source string) string {
	if u, err := url.Parse(source); err == nil && u.Host != "" {
		source = u.Path
	}
	if m := tmdbListPath.FindStringSubmatch(source); m != nil {
		return m[1]
	}
	return ""
}

// listEntries fetches what is on a list, and its name where the service
// gives one.
func listEntries(ctx context.Context, cfg *config.Config, l store.List) (name string, out []listEntry, err error) {
	switch l.Kind {
	case store.TraktWatchlist, store.TraktList:
		c := trakt.New(cfg.TraktClientID)
		var entries []trakt.Entry
		if l.Kind == store.TraktWatchlist {
			name = l.Source + "'s Trakt watchlist"
			entries, err = c.Watchlist(ctx, l.Source)
		} else {
			name = l.Source + " on Trakt"
			entries, err = c.List(ctx, l.Source)
		}
		for _, e := range entries {
			out = append(out, listEntry{e.Kind, e.TMDBID, e.Title})
		}
	case store.TMDBList:
		var entries []tmdb.ListEntry
		name, entries, err = tmdb.New(cfg.TMDBKey).List(l.Source)
		for _, e := range entries {
			out = append(out, listEntry{e.Kind, e.ID, e.Title})
		}
	default:
		err = fmt.Errorf("unknown list kind %q", l.Kind)
	}
	return name, out, err
}

// listWant decides what of a list's new movie or show is wanted.
func listWant(want string) func(season, episode int) bool {
	return func(season, _ int) bool {
		switch want {
		case store.WantAll:
			return true
		case store.WantFirst:
			return season <= 1 // a movie is season 0
		}
		return false
	}
}

// syncList adds the movies and shows that are new on a list to the library,
// and returns their titles. logf reports entries that could not be added.
func syncList(ctx context.Context, cfg *config.Config, st *store.Store, l store.List, logf func(string, ...any)) ([]string, error) {
	_, entries, err := listEntries(ctx, cfg, l)
	if err != nil {
		st.ListSynced(l.ID, err.Error())
		return nil, err
	}
	var added []string
	for _, e := range entries {
		if ctx.Err() != nil {
			return added, ctx.Err()
		}
		if st.Seen(l.ID, e.Kind, e.TMDBID) {
			continue
		}
		if st.MediaByTMDB(e.Kind, e.TMDBID) == 0 {
			pick, err := tmdb.New(cfg.TMDBKey).Details(e.Kind, e.TMDBID)
			if err != nil {
				logf("list %s: could not look up %s: %v", l.Name, e.Title, err)
				continue // tried again on the next sync
			}
			id, _, _, err := addToLibrary(cfg, st, e.Kind, pick, listWant(l.Want))
			if err != nil {
				logf("list %s: could not add %s: %v", l.Name, pick.Title, err)
				continue
			}
			title := pick.Title
			if m, err := st.Media(id); err == nil {
				title = describe(m, nil)
			}
			st.AddEvent(store.EventAdded, id, title, "from "+l.Name)
			added = append(added, title)
		}
		if err := st.MarkSeen(l.ID, e.Kind, e.TMDBID); err != nil {
			return added, err
		}
	}
	return added, st.ListSynced(l.ID, "")
}

// syncLists syncs every list, and has the runner search for what was added.
func (s *server) syncLists(ctx context.Context) {
	s.listsMu.Lock()
	defer s.listsMu.Unlock()
	lists, err := s.st.Lists()
	if err != nil {
		s.run.logf("could not read the lists: %v", err)
		return
	}
	total := 0
	for _, l := range lists {
		added, err := syncList(ctx, s.conf.Get(), s.st, l, s.run.logf)
		if err != nil {
			s.run.logf("could not sync %s: %v", l.Name, err)
		}
		if len(added) > 0 {
			s.run.logf("added %s from %s", strings.Join(added, ", "), l.Name)
		}
		total += len(added)
	}
	if total > 0 {
		s.run.Wake()
	}
}

// syncListsEvery syncs the lists soon after starting, then every interval.
func (s *server) syncListsEvery(ctx context.Context, interval time.Duration) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.syncLists(ctx)
			timer.Reset(interval)
		}
	}
}
