package main

import (
	"testing"

	"unified/internal/store"
)

func TestNormalizeList(t *testing.T) {
	for _, c := range []struct{ kind, in, want string }{
		{store.TraktWatchlist, " someone ", "someone"},
		{store.TraktWatchlist, "https://trakt.tv/users/someone/watchlist", "someone"},
		{store.TraktList, "https://trakt.tv/users/someone/lists/best-films", "someone/best-films"},
		{store.TMDBList, "8123", "8123"},
		{store.TMDBList, "https://www.themoviedb.org/list/8123-best-films?language=en", "8123"},
	} {
		if got, err := normalizeList(c.kind, c.in); err != nil || got != c.want {
			t.Errorf("normalizeList(%s, %q) = %q, %v, want %q", c.kind, c.in, got, err, c.want)
		}
	}
	for _, c := range []struct{ kind, in string }{
		{store.TraktWatchlist, ""}, {store.TraktWatchlist, "a/b"}, {store.TMDBList, "best-films"}, {"imdb", "x"},
	} {
		if got, err := normalizeList(c.kind, c.in); err == nil {
			t.Errorf("normalizeList(%s, %q) = %q, want an error", c.kind, c.in, got)
		}
	}
}

func TestListWant(t *testing.T) {
	for _, c := range []struct {
		want            string
		season          int
		movie, episodes bool
	}{
		{store.WantAll, 3, true, true},
		{store.WantFirst, 1, true, true},
		{store.WantFirst, 2, true, false},
		{store.WantNone, 1, false, false},
	} {
		f := listWant(c.want)
		if f(0, 0) != c.movie || f(c.season, 1) != c.episodes {
			t.Errorf("%s: movie %v, season %d %v", c.want, f(0, 0), c.season, f(c.season, 1))
		}
	}
}
