package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"unified/internal/config"
	"unified/internal/store"
)

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DownloadDir: filepath.Join(dir, "dl"), MoviesDir: filepath.Join(dir, "movies"), TVDir: filepath.Join(dir, "tv")}
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r := &runner{cfg: func() *config.Config { return cfg }, st: st, active: map[int64]*activeDownload{}, seeding: map[int64]*activeDownload{}}

	id, _ := st.AddMedia(store.Media{Kind: "movie", TMDBID: 1, Title: "Film", Year: 2020})
	st.UpsertItem(id, 0, 0, "Film", "", true)
	items, _ := st.Items(id)
	file := filepath.Join(cfg.MoviesDir, "Film (2020)", "Film (2020).mkv")
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file, []byte("x"), 0o644)
	st.SetImported(items[0].ID, file)

	// Deleting the files keeps the movie, no longer wanted.
	if err := r.remove(removeRequest{media: id, deleteFiles: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("file still exists")
	}
	items, _ = st.Items(id)
	if len(items) != 1 || items[0].State != store.Ignored || items[0].Path != "" {
		t.Errorf("after deleting files: %+v", items)
	}

	if err := r.remove(removeRequest{media: id, deleteFiles: true, fromLibrary: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Media(id); err == nil {
		t.Error("movie is still in the library")
	}
}

func TestRemoveSeason(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DownloadDir: filepath.Join(dir, "dl"), MoviesDir: filepath.Join(dir, "movies"), TVDir: filepath.Join(dir, "tv")}
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r := &runner{cfg: func() *config.Config { return cfg }, st: st, active: map[int64]*activeDownload{}, seeding: map[int64]*activeDownload{}}

	id, _ := st.AddMedia(store.Media{Kind: "show", TMDBID: 2, Title: "Show", Year: 2020})
	for _, se := range [][2]int{{1, 1}, {1, 2}, {2, 1}} {
		st.UpsertItem(id, se[0], se[1], "", "2020-01-01", true)
	}
	items, _ := st.Items(id)
	files := map[int64]string{}
	var downloaded []string
	for _, it := range items {
		src := filepath.Join(cfg.DownloadDir, "Show.Pack", fmt.Sprintf("Show.S%02dE%02d.mkv", it.Season, it.Episode))
		lib := filepath.Join(cfg.TVDir, "Show (2020)", fmt.Sprintf("Season %02d", it.Season), fmt.Sprintf("Show - S%02dE%02d.mkv", it.Season, it.Episode))
		os.MkdirAll(filepath.Dir(src), 0o755)
		os.MkdirAll(filepath.Dir(lib), 0o755)
		os.WriteFile(src, []byte("x"), 0o644)
		if err := os.Link(src, lib); err != nil {
			t.Fatal(err)
		}
		st.SetImported(it.ID, lib)
		files[it.ID] = lib
		downloaded = append(downloaded, src)
	}

	// Delete season 1.
	if err := r.remove(removeRequest{media: id, items: []int64{items[0].ID, items[1].ID}, deleteFiles: true}); err != nil {
		t.Fatal(err)
	}
	for i, it := range items {
		_, libErr := os.Stat(files[it.ID])
		_, dlErr := os.Stat(downloaded[i])
		if gone := os.IsNotExist(libErr) && os.IsNotExist(dlErr); gone != (it.Season == 1) {
			t.Errorf("S%02dE%02d: library %v, download %v", it.Season, it.Episode, libErr, dlErr)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.TVDir, "Show (2020)", "Season 01")); !os.IsNotExist(err) {
		t.Error("the empty season folder was left behind")
	}
	items, _ = st.Items(id)
	for _, it := range items {
		want := store.Imported
		if it.Season == 1 {
			want = store.Ignored
		}
		if it.State != want {
			t.Errorf("S%02dE%02d is %s, want %s", it.Season, it.Episode, it.State, want)
		}
	}
}

func TestSearchStateWithoutIndexers(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DownloadDir: filepath.Join(dir, "dl"), MoviesDir: filepath.Join(dir, "movies"), TVDir: filepath.Join(dir, "tv")}
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r := &runner{cfg: func() *config.Config { return cfg }, st: st, searches: map[int64]ItemSearch{}}

	id, _ := st.AddMedia(store.Media{Kind: "movie", TMDBID: 1, Title: "Film", Year: 2020})
	st.UpsertItem(id, 0, 0, "Film", "", true)
	if err := r.grabWanted(); err != nil {
		t.Fatal(err)
	}
	items, _ := st.Items(id)
	if s := r.Searches()[items[0].ID]; s.State != "notFound" || s.Note != "No indexers are set up" {
		t.Errorf("search state = %+v", s)
	}
	if got := r.Snapshot().Search; got.Current != nil || got.Queued != 0 {
		t.Errorf("search status = %+v", got)
	}
}

func TestCancelItems(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DownloadDir: filepath.Join(dir, "dl"), MoviesDir: filepath.Join(dir, "movies"), TVDir: filepath.Join(dir, "tv")}
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r := &runner{cfg: func() *config.Config { return cfg }, st: st, active: map[int64]*activeDownload{}, seeding: map[int64]*activeDownload{}}

	id, _ := st.AddMedia(store.Media{Kind: "show", TMDBID: 3, Title: "Show", Year: 2020})
	st.UpsertItem(id, 1, 1, "", "2020-01-01", true)
	st.UpsertItem(id, 1, 2, "", "2020-01-01", true)
	items, _ := st.Items(id)
	// Episode 1 is in the library and being replaced; episode 2 is new.
	lib := filepath.Join(cfg.TVDir, "Show (2020)", "Season 01", "Show - S01E01.mkv")
	os.MkdirAll(filepath.Dir(lib), 0o755)
	os.WriteFile(lib, []byte("x"), 0o644)
	st.SetImported(items[0].ID, lib)
	if _, err := st.CreateDownload("abc", "Show.S01", "magnet:?xt=urn:btih:abc", []int64{items[0].ID, items[1].ID}); err != nil {
		t.Fatal(err)
	}

	if err := r.remove(removeRequest{media: id, items: []int64{items[0].ID, items[1].ID}, cancel: true}); err != nil {
		t.Fatal(err)
	}
	items, _ = st.Items(id)
	if items[0].State != store.Imported || items[0].Path != lib || items[1].State != store.Ignored {
		t.Errorf("after cancelling: %+v", items)
	}
	if _, err := os.Stat(lib); err != nil {
		t.Errorf("the library file was deleted: %v", err)
	}
}
