package importer

import (
	"os"
	"path/filepath"
	"testing"

	"unified/internal/config"
	"unified/internal/store"
)

func TestImportSeasonPack(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{DownloadDir: filepath.Join(root, "dl"), TVDir: filepath.Join(root, "tv")}
	files := []File{
		{Path: "Show.S02.1080p.WEB-DL-GRP/Show.S02E01.1080p.WEB-DL-GRP.mkv", Size: 10},
		{Path: "Show.S02.1080p.WEB-DL-GRP/Show.S02E02.1080p.WEB-DL-GRP.mkv", Size: 10},
		{Path: "Show.S02.1080p.WEB-DL-GRP/Sample/show.s02e01.sample.mkv", Size: 1},
		{Path: "Show.S02.1080p.WEB-DL-GRP/info.nfo", Size: 1},
	}
	for _, f := range files {
		p := filepath.Join(cfg.DownloadDir, f.Path)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := store.Media{Kind: "show", Title: "Show: The Return", Year: 2020}
	items := []store.Item{
		{ID: 1, Season: 2, Episode: 1, Title: "Pilot?"},
		{ID: 2, Season: 2, Episode: 2},
		{ID: 3, Season: 2, Episode: 3}, // not in the pack
	}
	got, err := Import(cfg, m, items, "Show.S02.1080p.WEB-DL-GRP", files)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("imported %d items, want 2: %+v", len(got), got)
	}
	want := filepath.Join(cfg.TVDir, "Show The Return (2020)", "Season 02", "Show The Return - S02E01 - Pilot.mkv")
	if got[0].Dest != want {
		t.Errorf("dest = %q, want %q", got[0].Dest, want)
	}
	src, _ := os.Stat(filepath.Join(cfg.DownloadDir, files[0].Path))
	dst, err := os.Stat(want)
	if err != nil || !os.SameFile(src, dst) {
		t.Errorf("destination is not a hardlink of the source: %v", err)
	}
}

func TestImportSingleEpisodeNamedByTorrent(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{DownloadDir: filepath.Join(root, "dl"), TVDir: filepath.Join(root, "tv")}
	f := File{Path: "Show.S01E04.720p.HDTV-GRP/video.mkv", Size: 5}
	p := filepath.Join(cfg.DownloadDir, f.Path)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("x"), 0o644)

	m := store.Media{Kind: "show", Title: "Show", Year: 2019}
	got, err := Import(cfg, m, []store.Item{{ID: 7, Season: 1, Episode: 4}}, "Show.S01E04.720p.HDTV-GRP", []File{f})
	if err != nil || len(got) != 1 || got[0].ItemID != 7 {
		t.Fatalf("got %+v, %v", got, err)
	}
}
