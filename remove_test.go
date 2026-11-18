package main

import (
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
