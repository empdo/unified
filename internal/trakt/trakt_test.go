package trakt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseList(t *testing.T) {
	for in, want := range map[string][2]string{
		"someone/favourites":                                            {"someone", "favourites"},
		"https://trakt.tv/users/someone/lists/favourites":               {"someone", "favourites"},
		"https://trakt.tv/users/someone/lists/favourites?sort=rank,asc": {"someone", "favourites"},
	} {
		user, slug, err := ParseList(in)
		if err != nil || user != want[0] || slug != want[1] {
			t.Errorf("ParseList(%q) = %q, %q, %v", in, user, slug, err)
		}
	}
	for _, in := range []string{"", "someone", "a/b/c"} {
		if _, _, err := ParseList(in); err == nil {
			t.Errorf("ParseList(%q) accepted", in)
		}
	}
}

func TestWatchlist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/someone/watchlist/movie,show" || r.Header.Get("trakt-api-key") != "id" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`[
			{"type": "movie", "movie": {"title": "Film", "year": 2020, "ids": {"tmdb": 1}}},
			{"type": "show", "show": {"title": "Show", "year": 2021, "ids": {"tmdb": 2}}},
			{"type": "movie", "movie": {"title": "Unknown", "year": 2022, "ids": {"tmdb": null}}}
		]`))
	}))
	defer srv.Close()
	c := New("id")
	c.Base = srv.URL
	got, err := c.Watchlist(context.Background(), "someone")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (Entry{"movie", 1, "Film", 2020}) || got[1] != (Entry{"show", 2, "Show", 2021}) {
		t.Errorf("got %+v", got)
	}
}
