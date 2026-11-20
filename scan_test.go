package main

import (
	"os"
	"path/filepath"
	"testing"

	"unified/internal/config"
	"unified/internal/store"
	"unified/internal/tmdb"
)

func TestEpisodeOf(t *testing.T) {
	for path, want := range map[string][2]int{
		"/tv/Severance (2022)/Season 02/Severance - S02E03 - Who Is Alive.mkv": {2, 3},
		"/tv/Show/Season 1/Episode 4.mkv":                                      {1, 4},
		"/tv/Show/S03/05.mkv":                                                  {0, 0},
		"/tv/Show/Show 1x02.mkv":                                               {1, 2},
		"/tv/Show/Extras/Making of.mkv":                                        {0, 0},
	} {
		if s, e := episodeOf(path); s != want[0] || e != want[1] {
			t.Errorf("episodeOf(%q) = %d, %d, want %v", path, s, e, want)
		}
	}
}

func TestScanFolders(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{MoviesDir: filepath.Join(dir, "movies"), TVDir: filepath.Join(dir, "tv")}
	write := func(path string, size int) {
		path = filepath.Join(dir, path)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, make([]byte, size), 0o644)
	}
	write("movies/Blade Runner 2049 (2017)/Blade Runner 2049 (2017).mkv", 10)
	write("movies/Blade Runner 2049 (2017)/sample.mkv", 1)
	write("movies/Alien.1979.1080p.mkv", 5)
	write("movies/notes.txt", 1)
	write("movies/Empty/readme.txt", 1)
	write("tv/Severance (2022)/Season 01/Severance - S01E01.mkv", 3)
	write("tv/Severance (2022)/Season 01/Severance - S01E02.mkv", 3)
	write("tv/Severance (2022)/Extras/Behind the scenes.mkv", 2)

	entries, files, err := scanFolders(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]scanEntry{}
	for _, e := range entries {
		got[e.Name] = e
	}
	if len(entries) != 3 {
		t.Fatalf("found %d entries: %+v", len(entries), entries)
	}
	if e := got["Blade Runner 2049 (2017)"]; e.Kind != "movie" || e.Title != "Blade Runner 2049" || e.Year != 2017 || e.Files != 1 {
		t.Errorf("movie folder: %+v", e)
	}
	if e := got["Alien.1979.1080p.mkv"]; e.Title != "Alien" || e.Year != 1979 || e.Files != 1 {
		t.Errorf("lone movie file: %+v", e)
	}
	show := got["Severance (2022)"]
	if show.Kind != "show" || show.Title != "Severance" || show.Episodes != 2 || len(show.Unknown) != 1 {
		t.Errorf("show: %+v", show)
	}

	// The show's files go to its episodes; the extra matches nothing.
	m := store.Media{Kind: "show", Title: "Severance", Year: 2022}
	items := []store.Item{{ID: 1, Season: 1, Episode: 1}, {ID: 2, Season: 1, Episode: 2}, {ID: 3, Season: 1, Episode: 3}}
	matched := matchFiles(m, items, files[show.Path])
	if len(matched) != 2 || filepath.Base(matched[items[1]].Path) != "Severance - S01E02.mkv" {
		t.Errorf("matched %+v", matched)
	}
}

func TestImportScannedRejectsOtherPaths(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{MoviesDir: filepath.Join(dir, "movies"), TVDir: filepath.Join(dir, "tv")}
	for _, path := range []string{"/etc", filepath.Join(dir, "movies"), filepath.Join(dir, "movies", "a", "b"), filepath.Join(dir, "tv", "Show")} {
		res := importScanned(cfg, nil, []scanChoice{{Kind: "movie", Path: path, TMDBID: 1}}, false)
		if len(res) != 1 || res[0].Error == "" {
			t.Errorf("%s was accepted: %+v", path, res)
		}
	}
}

func TestRankMatches(t *testing.T) {
	results := []tmdb.Result{{ID: 1, Year: 1990}, {ID: 2, Year: 2018}, {ID: 3, Year: 2017}}
	got := rankMatches(results, 2017)
	if got[0].ID != 3 || got[1].ID != 2 || got[2].ID != 1 {
		t.Errorf("got %+v", got)
	}
	if got := rankMatches(results, 0); got[0].ID != 1 {
		t.Errorf("without a year the order changed: %+v", got)
	}
}
