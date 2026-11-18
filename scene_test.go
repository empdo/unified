package main

import (
	"path/filepath"
	"testing"

	"unified/internal/store"
	"unified/internal/xem"
)

// TMDB lists Frieren as one season of 38 episodes; TVDB and releases split
// it into a season of 28 and one of 10.
func TestSceneNumbersByAbsoluteOrder(t *testing.T) {
	var items []store.Item
	var maps []xem.Mapping
	for ep := 1; ep <= 38; ep++ {
		items = append(items, store.Item{Season: 1, Episode: ep})
		s, e := 1, ep
		if ep > 28 {
			s, e = 2, ep-28
		}
		maps = append(maps, xem.Mapping{
			TVDB:  xem.Episode{Season: s, Episode: e, Absolute: ep},
			Scene: xem.Episode{Season: s, Episode: e, Absolute: e},
		})
	}
	got := sceneNumbers(items, maps)
	if len(got) != 10 {
		t.Fatalf("mapped %d episodes, want 10: %v", len(got), got)
	}
	if got[[2]int{1, 29}] != [2]int{2, 1} || got[[2]int{1, 38}] != [2]int{2, 10} {
		t.Errorf("S01E29, S01E38 -> %v, %v; want S02E01, S02E10", got[[2]int{1, 29}], got[[2]int{1, 38}])
	}
}

// When TMDB numbers a show like TVDB, the XEM entry is used as it stands.
func TestSceneNumbersBySeasonAndEpisode(t *testing.T) {
	items := []store.Item{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}, {Season: 2, Episode: 1}}
	maps := []xem.Mapping{
		{TVDB: xem.Episode{Season: 0, Episode: 1}, Scene: xem.Episode{Season: 1, Episode: 9}}, // a special
		{TVDB: xem.Episode{Season: 1, Episode: 1, Absolute: 1}, Scene: xem.Episode{Season: 1, Episode: 1}},
		{TVDB: xem.Episode{Season: 1, Episode: 2, Absolute: 2}, Scene: xem.Episode{Season: 1, Episode: 2}},
		{TVDB: xem.Episode{Season: 2, Episode: 1, Absolute: 3}, Scene: xem.Episode{Season: 1, Episode: 3}},
	}
	got := sceneNumbers(items, maps)
	if len(got) != 1 || got[[2]int{2, 1}] != [2]int{1, 3} {
		t.Errorf("got %v, want only S02E01 -> S01E03", got)
	}
}

func TestWantForUsesSceneNumbering(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, _ := st.AddMedia(store.Media{Kind: "show", TMDBID: 209867, Title: "Frieren: Beyond Journey's End", Year: 2023})
	for ep := 1; ep <= 4; ep++ {
		st.UpsertItem(id, 1, ep, "", "2023-09-29", true)
	}
	if err := st.SetScene(id, []string{"Sousou no Frieren"}, map[[2]int][2]int{{1, 3}: {2, 1}, {1, 4}: {2, 2}}); err != nil {
		t.Fatal(err)
	}
	m, _ := st.Media(id)

	w, _ := wantFor(st, m, 1, 4)
	if w.Season != 2 || w.Episode != 2 || len(w.Aliases) != 1 {
		t.Errorf("S01E04: want = %+v, want scene S02E02 with the alias", w)
	}
	if pack, _ := wantFor(st, m, 1, 0); pack.Season != 1 || pack.Count != 2 {
		t.Errorf("season 1 pack = %+v, want scene season 1 of 2 episodes", pack)
	}

	// Refreshing with no mapping puts the TMDB numbers back.
	st.SetScene(id, nil, nil)
	m, _ = st.Media(id)
	if w, _ := wantFor(st, m, 1, 4); w.Season != 1 || w.Episode != 4 || len(w.Aliases) != 0 {
		t.Errorf("after clearing: want = %+v", w)
	}
}
