package importer

import (
	"os"
	"path/filepath"
	"strings"
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

// Releases of a show with a scene mapping are named by the scene numbering.
func TestImportSceneNumbering(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{DownloadDir: filepath.Join(root, "dl"), TVDir: filepath.Join(root, "tv")}
	f := File{Path: "Frieren.Beyond.Journeys.End.S02E05.1080p.WEB.H264-GRP.mkv", Size: 5}
	os.MkdirAll(cfg.DownloadDir, 0o755)
	os.WriteFile(filepath.Join(cfg.DownloadDir, f.Path), []byte("x"), 0o644)

	m := store.Media{Kind: "show", Title: "Frieren", Year: 2023}
	items := []store.Item{{ID: 1, Season: 1, Episode: 33, SceneSeason: 2, SceneEpisode: 5}}
	got, err := Import(cfg, m, items, f.Path, []File{f})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v; want one import", got, err)
	}
	// The library keeps TMDB's numbering.
	if want := filepath.Join(cfg.TVDir, "Frieren (2023)", "Season 01", "Frieren - S01E33.mkv"); got[0].Dest != want {
		t.Errorf("dest = %q, want %q", got[0].Dest, want)
	}
}

func TestDelete(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{DownloadDir: filepath.Join(root, "dl"), TVDir: filepath.Join(root, "tv"), MoviesDir: filepath.Join(root, "movies")}
	write := func(p string) string {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("video"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	src := write(filepath.Join(cfg.DownloadDir, "Show.S01.Pack", "Show.S01E01.mkv"))
	lib := filepath.Join(cfg.TVDir, "Show (2020)", "Season 01", "Show - S01E01.mkv")
	if err := link(src, lib); err != nil {
		t.Fatal(err)
	}
	other := write(filepath.Join(cfg.TVDir, "Other (2019)", "Season 01", "Other - S01E01.mkv"))
	partial := write(filepath.Join(cfg.DownloadDir, "Show.S01E02", "Show.S01E02.mkv"))
	outside := write(filepath.Join(root, "elsewhere.mkv"))

	err := Delete(cfg, []string{lib, outside}, []string{partial})
	if err == nil {
		t.Error("deleting a file outside the library gave no error")
	}
	for _, p := range []string{lib, src, partial, filepath.Join(cfg.TVDir, "Show (2020)"), filepath.Join(cfg.DownloadDir, "Show.S01.Pack")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	for _, p := range []string{other, outside, cfg.TVDir, cfg.DownloadDir} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was deleted: %v", p, err)
		}
	}
}

func TestImportSubtitles(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{DownloadDir: filepath.Join(root, "dl"), TVDir: filepath.Join(root, "tv")}
	files := []File{
		{Path: "Show.S01.Pack/Show.S01E01.mkv", Size: 10},
		{Path: "Show.S01.Pack/Show.S01E01.en.srt", Size: 1},
		{Path: "Show.S01.Pack/Show.S01E02.mkv", Size: 10},
		{Path: "Show.S01.Pack/Subs/Show.S01E02/2_English.srt", Size: 1},
		{Path: "Show.S01.Pack/Subs/Show.S01E02/3_English [SDH].srt", Size: 1},
		{Path: "Show.S01.Pack/Subs/Show.S01E02/4_Swedish.Forced.srt", Size: 1},
		{Path: "Show.S01.Pack/Subs/Show.S01E02/5_English.srt", Size: 1},
		{Path: "Show.S01.Pack/Subs/Show.S01E03/2_English.srt", Size: 1}, // its episode is not wanted
	}
	for _, f := range files {
		p := filepath.Join(cfg.DownloadDir, f.Path)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	season := filepath.Join(cfg.TVDir, "Show (2020)", "Season 01")
	// Left over from an earlier release of episode 1.
	stale := filepath.Join(season, "Show - S01E01.fr.srt")
	os.MkdirAll(season, 0o755)
	os.WriteFile(stale, []byte("old"), 0o644)

	m := store.Media{Kind: "show", Title: "Show", Year: 2020}
	items := []store.Item{{ID: 1, Season: 1, Episode: 1}, {ID: 2, Season: 1, Episode: 2}}
	if _, err := Import(cfg, m, items, "Show.S01.Pack", files); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(season)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	want := []string{
		"Show - S01E01.en.srt",
		"Show - S01E01.mkv",
		"Show - S01E02.2.en.srt",
		"Show - S01E02.en.sdh.srt",
		"Show - S01E02.en.srt",
		"Show - S01E02.mkv",
		"Show - S01E02.sv.forced.srt",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("library has\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Deleting the videos takes their subtitles, and the downloaded copies, with them.
	err := Delete(cfg, []string{filepath.Join(season, "Show - S01E01.mkv"), filepath.Join(season, "Show - S01E02.mkv")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(season); !os.IsNotExist(err) {
		t.Errorf("season directory still exists")
	}
	if _, err := os.Stat(filepath.Join(cfg.DownloadDir, files[3].Path)); !os.IsNotExist(err) {
		t.Errorf("downloaded subtitle still exists")
	}
}

func TestSubtitleName(t *testing.T) {
	for _, c := range []struct{ video, sub, want string }{
		{"It.2017/It.2017.mkv", "It.2017/It.2017.srt", "It (2017).srt"},
		{"It.2017/It.2017.mkv", "It.2017/It.2017.eng.HI.srt", "It (2017).en.sdh.srt"},
		{"M/M.mkv", "M/Subs/English (Forced).ass", "It (2017).en.forced.ass"},
		{"M/M.mkv", "M/Subs/Movie.2017.1080p.srt", "It (2017).srt"},
		{"M/M.mkv", "M/Subs/svenska.SRT", "It (2017).sv.srt"},
	} {
		if got := subtitleName("It (2017)", c.video, c.sub, map[string]bool{}); got != c.want {
			t.Errorf("subtitleName(%q) = %q, want %q", c.sub, got, c.want)
		}
	}
}
