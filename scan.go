package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"unified/internal/config"
	"unified/internal/importer"
	"unified/internal/release"
	"unified/internal/store"
	"unified/internal/tmdb"
)

// A scan takes movies and shows already in the movie and TV folders into the
// library, so they are not downloaded again. Each entry of the movie folder
// (a folder or a lone video file) is taken to be a movie, and each folder in
// the TV folder a show, named "Title (Year)" or anything the release parser
// understands. Files stay where they are.

// scanEntry is a movie or show found in the library folders, with the TMDB
// matches proposed for it.
type scanEntry struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Name  string `json:"name"`
	Title string `json:"title"` // as read from the name
	Year  int    `json:"year"`
	Files int    `json:"files"` // video files
	Size  int64  `json:"size"`
	// For a show: the episodes told apart by their file names, and the
	// files whose episode could not be told.
	Episodes int      `json:"episodes"`
	Unknown  []string `json:"unknown"`
	// Video files the library has already.
	Known int `json:"known"`
	// From TMDB, best first; empty when nothing matched.
	Matches []scanMatch `json:"matches"`
	Error   string      `json:"error,omitempty"`
}

type scanMatch struct {
	TMDBID    int    `json:"tmdbId"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Poster    string `json:"poster"`
	LibraryID int64  `json:"libraryId,omitempty"`
}

// scanFile is a video file of a scanned movie or show. Season and Episode
// are 0 when not told by the name.
type scanFile struct {
	Path    string
	Size    int64
	Season  int
	Episode int
}

// scanFolders lists what the movie and TV folders hold, without looking
// anything up.
func scanFolders(cfg *config.Config) ([]scanEntry, map[string][]scanFile, error) {
	var entries []scanEntry
	files := map[string][]scanFile{}
	for _, root := range []struct{ kind, dir string }{{"movie", cfg.MoviesDir}, {"show", cfg.TVDir}} {
		des, err := os.ReadDir(root.dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		for _, de := range des {
			name := de.Name()
			if strings.HasPrefix(name, ".") || (!de.IsDir() && (root.kind == "show" || !importer.IsVideo(name))) {
				continue
			}
			path := filepath.Join(root.dir, name)
			e, found := scanOne(root.kind, path)
			if e.Files == 0 {
				continue
			}
			entries = append(entries, e)
			files[path] = found
		}
	}
	return entries, files, nil
}

// scanOne reads one movie or show: its title from the name, and its video
// files.
func scanOne(kind, path string) (scanEntry, []scanFile) {
	name := filepath.Base(path)
	info := release.Parse(strings.TrimSuffix(name, filepath.Ext(name)))
	e := scanEntry{Kind: kind, Path: path, Name: name, Title: cmp.Or(info.Title, name), Year: info.Year, Unknown: []string{}, Matches: []scanMatch{}}
	var files []scanFile
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !importer.IsVideo(p) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		f := scanFile{Path: p, Size: fi.Size()}
		if kind == "show" {
			f.Season, f.Episode = episodeOf(p)
		}
		files = append(files, f)
		return nil
	})
	seen := map[[2]int]bool{}
	for _, f := range files {
		e.Files++
		e.Size += f.Size
		if kind != "show" {
			continue
		}
		if f.Episode == 0 {
			rel, _ := filepath.Rel(path, f.Path)
			e.Unknown = append(e.Unknown, rel)
		} else if k := [2]int{f.Season, f.Episode}; !seen[k] {
			seen[k] = true
			e.Episodes++
		}
	}
	return e, files
}

var seasonDir = regexp.MustCompile(`(?i)^(?:season|series|s)\s*0*(\d+)$`)

// episodeOf tells the season and episode of a show's file from its name, or
// from the folder it is in ("Season 02/Episode 3.mkv"); 0, 0 when it cannot.
func episodeOf(path string) (season, episode int) {
	info := release.Parse(filepath.Base(path))
	season, episode = info.Season, info.Episode
	if m := seasonDir.FindStringSubmatch(filepath.Base(filepath.Dir(path))); m != nil && season == 0 {
		season, _ = strconv.Atoi(m[1])
	}
	if season == 0 || episode == 0 {
		return 0, 0
	}
	return season, episode
}

// scanLibrary lists the movies and shows in the library folders with their
// best TMDB matches, and how many of their files the library knows.
func scanLibrary(ctx context.Context, cfg *config.Config, st *store.Store) ([]scanEntry, error) {
	entries, files, err := scanFolders(cfg)
	if err != nil {
		return nil, err
	}
	known, err := st.ItemPaths()
	if err != nil {
		return nil, err
	}
	for i := range entries {
		for _, f := range files[entries[i].Path] {
			if _, ok := known[f.Path]; ok {
				entries[i].Known++
			}
		}
	}

	// A few lookups at a time, well within TMDB's rate limit.
	client := tmdb.New(cfg.TMDBKey)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i := range entries {
		e := &entries[i]
		if e.Known == e.Files {
			continue // nothing new to take in
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			results, err := client.Search(e.Kind, e.Title)
			if err != nil {
				e.Error = err.Error()
				return
			}
			for _, r := range rankMatches(results, e.Year)[:min(len(results), 5)] {
				e.Matches = append(e.Matches, scanMatch{
					TMDBID: r.ID, Title: r.Title, Year: r.Year, Poster: tmdb.ImageURL(r.Poster, "w92"),
					LibraryID: st.MediaByTMDB(e.Kind, r.ID),
				})
			}
		}()
	}
	wg.Wait()
	return entries, ctx.Err()
}

// rankMatches puts the results from the year the name gives first, keeping
// TMDB's order otherwise. A movie's year may be off by one between sources.
func rankMatches(results []tmdb.Result, year int) []tmdb.Result {
	if year == 0 {
		return results
	}
	near := func(r tmdb.Result) int {
		switch d := r.Year - year; {
		case d == 0:
			return 0
		case d == 1 || d == -1:
			return 1
		}
		return 2
	}
	out := slices.Clone(results)
	slices.SortStableFunc(out, func(a, b tmdb.Result) int { return near(a) - near(b) })
	return out
}

// scanChoice is a scanned movie or show to take into the library, as the
// TMDB movie or show chosen for it.
type scanChoice struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	TMDBID int    `json:"tmdbId"`
}

type scanResult struct {
	Path     string `json:"path"`
	MediaID  int64  `json:"mediaId,omitempty"`
	Title    string `json:"title,omitempty"`
	Imported int    `json:"imported"` // files taken into the library
	Error    string `json:"error,omitempty"`
}

// importScanned adds each chosen movie or show to the library, with its
// files as what the library has. With wantMissing, what has no file is
// wanted; otherwise it is not.
func importScanned(cfg *config.Config, st *store.Store, choices []scanChoice, wantMissing bool) []scanResult {
	var out []scanResult
	for _, c := range choices {
		res, err := importOne(cfg, st, c, wantMissing)
		if err != nil {
			res.Error = err.Error()
		}
		out = append(out, res)
	}
	return out
}

func importOne(cfg *config.Config, st *store.Store, c scanChoice, wantMissing bool) (scanResult, error) {
	res := scanResult{Path: c.Path}
	root := cfg.MoviesDir
	if c.Kind == "show" {
		root = cfg.TVDir
	} else if c.Kind != "movie" {
		return res, errors.New(`kind must be "movie" or "show"`)
	}
	// Only entries of the library folders, as a scan lists them.
	path := filepath.Clean(c.Path)
	if filepath.Dir(path) != filepath.Clean(root) {
		return res, fmt.Errorf("%s is not in %s", c.Path, root)
	}
	_, files := scanOne(c.Kind, path)
	if len(files) == 0 {
		return res, errors.New("no video files there")
	}
	pick, err := tmdb.New(cfg.TMDBKey).Details(c.Kind, c.TMDBID)
	if err != nil {
		return res, err
	}
	// Nothing is wanted until the files are in, or the runner could grab
	// what is already there.
	id, _, _, err := addToLibrary(cfg, st, c.Kind, pick, func(int, int) bool { return false })
	if err != nil {
		return res, err
	}
	m, err := st.Media(id)
	if err != nil {
		return res, err
	}
	res.MediaID, res.Title = id, describe(m, nil)
	items, err := st.Items(id)
	if err != nil {
		return res, err
	}
	for it, f := range matchFiles(m, items, files) {
		if it.State == store.Downloading || it.Path == f.Path {
			continue
		}
		if err := st.SetImported(it.ID, f.Path); err != nil {
			return res, err
		}
		res.Imported++
	}
	if wantMissing {
		items, err = st.Items(id)
		if err != nil {
			return res, err
		}
		for _, it := range items {
			if it.State == store.Ignored {
				if err := st.SetItemWanted(it.ID, true); err != nil {
					return res, err
				}
			}
		}
	}
	if err := st.AddEvent(store.EventScanned, id, res.Title, fmt.Sprintf("%d files in %s", res.Imported, path)); err != nil {
		return res, err
	}
	return res, nil
}

// matchFiles pairs items with the files that hold them: a movie with its
// largest file, episodes by season and episode. Files are numbered as TMDB
// numbers episodes, as media servers name them, or failing that as releases
// do.
func matchFiles(m store.Media, items []store.Item, files []scanFile) map[store.Item]scanFile {
	out := map[store.Item]scanFile{}
	if !m.IsShow() {
		if len(items) == 0 {
			return out
		}
		largest := files[0]
		for _, f := range files[1:] {
			if f.Size > largest.Size {
				largest = f
			}
		}
		out[items[0]] = largest
		return out
	}
	byTMDB, byScene := map[[2]int]store.Item{}, map[[2]int]store.Item{}
	for _, it := range items {
		byTMDB[[2]int{it.Season, it.Episode}] = it
		s, e := it.Scene()
		byScene[[2]int{s, e}] = it
	}
	for _, f := range files {
		if f.Episode == 0 {
			continue
		}
		k := [2]int{f.Season, f.Episode}
		it, ok := byTMDB[k]
		if !ok {
			it, ok = byScene[k]
		}
		// Of two files for one episode, the larger.
		if ok && f.Size >= out[it].Size {
			out[it] = f
		}
	}
	return out
}

// cmdScan lists what the library folders hold that the library lacks, and
// with --import takes the best match of each into the library.
func cmdScan(cfg *config.Config, st *store.Store, args []string) error {
	doImport, wantMissing := false, false
	for _, a := range args {
		switch a {
		case "--import":
			doImport = true
		case "--want-missing":
			wantMissing = true
		default:
			return fmt.Errorf("usage: unified scan [--import] [--want-missing]")
		}
	}
	entries, err := scanLibrary(context.Background(), cfg, st)
	if err != nil {
		return err
	}
	var choices []scanChoice
	for _, e := range entries {
		if e.Known == e.Files {
			continue
		}
		what := fmt.Sprintf("%d files", e.Files)
		if e.Kind == "show" {
			what = fmt.Sprintf("%d episodes", e.Episodes)
			if len(e.Unknown) > 0 {
				what += fmt.Sprintf(", %d files not recognized", len(e.Unknown))
			}
		}
		switch {
		case e.Error != "":
			fmt.Printf("%-5s %s: %s\n", e.Kind, e.Name, e.Error)
		case len(e.Matches) == 0:
			fmt.Printf("%-5s %s: no match on TMDB\n", e.Kind, e.Name)
		default:
			best := e.Matches[0]
			fmt.Printf("%-5s %s -> %s (%d), %s\n", e.Kind, e.Name, best.Title, best.Year, what)
			choices = append(choices, scanChoice{Kind: e.Kind, Path: e.Path, TMDBID: best.TMDBID})
		}
	}
	if len(choices) == 0 {
		fmt.Println("nothing new in the library folders")
		return nil
	}
	if !doImport {
		fmt.Println("\nrun `unified scan --import` to add these, or check the matches in the web UI first")
		return nil
	}
	for _, r := range importScanned(cfg, st, choices, wantMissing) {
		if r.Error != "" {
			fmt.Printf("%s: %s\n", r.Path, r.Error)
		} else {
			fmt.Printf("added [%d] %s: %d files\n", r.MediaID, r.Title, r.Imported)
		}
	}
	return nil
}
