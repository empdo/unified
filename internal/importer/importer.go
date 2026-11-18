// Package importer places finished downloads into the library by hardlink,
// so the torrent can keep seeding from the download directory.
package importer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"unified/internal/config"
	"unified/internal/release"
	"unified/internal/store"
)

// File is one file of a finished download, with Path relative to the
// download directory.
type File struct {
	Path string
	Size int64
}

type Result struct {
	ItemID int64
	Dest   string
}

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true, ".mov": true, ".wmv": true, ".ts": true, ".webm": true,
}

var subtitleExts = map[string]bool{
	".srt": true, ".ass": true, ".ssa": true, ".sub": true, ".idx": true, ".vtt": true, ".sup": true,
}

// Import links the video files that match the given items into the library,
// along with the subtitle files that come with them, and reports which items
// were satisfied.
func Import(cfg *config.Config, m store.Media, items []store.Item, torrentName string, files []File) ([]Result, error) {
	var videos []File
	for _, f := range files {
		base := strings.ToLower(filepath.Base(f.Path))
		if videoExts[filepath.Ext(base)] && !strings.Contains(base, "sample") {
			videos = append(videos, f)
		}
	}

	// Pick the largest matching file for each item.
	chosen := map[int64]File{}
	for _, v := range videos {
		for _, it := range items {
			if matches(m, it, v, torrentName, len(videos)) && v.Size >= chosen[it.ID].Size {
				chosen[it.ID] = v
			}
		}
	}

	subs := subtitlesFor(m, items, chosen, files)

	var out []Result
	for _, it := range items {
		v, ok := chosen[it.ID]
		if !ok {
			continue
		}
		dest := destination(cfg, m, it, filepath.Ext(v.Path))
		if err := link(filepath.Join(cfg.DownloadDir, v.Path), dest); err != nil {
			return out, fmt.Errorf("importing %s: %w", v.Path, err)
		}
		// Subtitles of whatever this replaced would be out of sync with it.
		for _, old := range sidecars(dest) {
			os.Remove(old)
		}
		stem := strings.TrimSuffix(dest, filepath.Ext(dest))
		taken := map[string]bool{}
		for _, sub := range subs[it.ID] {
			name := subtitleName(stem, v.Path, sub.Path, taken)
			if err := link(filepath.Join(cfg.DownloadDir, sub.Path), name); err != nil {
				return out, fmt.Errorf("importing %s: %w", sub.Path, err)
			}
		}
		out = append(out, Result{ItemID: it.ID, Dest: dest})
	}
	return out, nil
}

// subtitlesFor assigns the download's subtitle files to the items whose
// video was chosen. A subtitle belongs to a video when it is named after it
// in the same directory (Movie.mkv, Movie.en.srt), or, for a show, when its
// name or directory names the episode (Subs/Show.S01E02/2_English.srt).
// When only one video was chosen, every other subtitle goes with it.
func subtitlesFor(m store.Media, items []store.Item, chosen map[int64]File, files []File) map[int64][]File {
	out := map[int64][]File{}
	for _, f := range files {
		if !subtitleExts[strings.ToLower(filepath.Ext(f.Path))] {
			continue
		}
		if id, ok := subtitleItem(m, items, chosen, f); ok {
			out[id] = append(out[id], f)
		}
	}
	return out
}

func subtitleItem(m store.Media, items []store.Item, chosen map[int64]File, sub File) (int64, bool) {
	for id, v := range chosen {
		stem := strings.TrimSuffix(v.Path, filepath.Ext(v.Path))
		if strings.HasPrefix(sub.Path, stem+".") {
			return id, true
		}
	}
	if m.IsShow() {
		info := release.Parse(filepath.Base(sub.Path))
		if info.Episode == 0 {
			info = release.Parse(filepath.Base(filepath.Dir(sub.Path)))
		}
		if info.Episode != 0 {
			for _, it := range items {
				season, episode := it.Scene()
				if _, ok := chosen[it.ID]; ok && info.Season == season && info.Episode == episode {
					return it.ID, true
				}
			}
			return 0, false
		}
	}
	if len(chosen) == 1 {
		for id := range chosen {
			return id, true
		}
	}
	return 0, false
}

// subtitleName names a subtitle after the library video it goes with,
// keeping the language and the forced and hearing-impaired tags found at the
// end of its name, as media servers expect: "Movie (2020).en.forced.srt". A
// name already taken by another subtitle gets a number: "Movie (2020).2.en.srt".
func subtitleName(videoStem, videoPath, subPath string, taken map[string]bool) string {
	ext := strings.ToLower(filepath.Ext(subPath))
	// Tags of a subtitle named after the download's video follow its name.
	from := strings.TrimSuffix(subPath, filepath.Ext(subPath))
	video := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	if rest, ok := strings.CutPrefix(from, video+"."); ok {
		from = rest
	} else if from == video {
		from = ""
	} else {
		from = filepath.Base(from)
	}
	words := strings.FieldsFunc(from, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var lang string
	var forced, sdh bool
	// Read tags from the end until something that is not one.
	for i := len(words) - 1; i >= 0; i-- {
		w := strings.ToLower(words[i])
		switch {
		case w == "forced" || w == "foreign":
			forced = true
		case w == "sdh" || w == "hi" || w == "cc":
			sdh = true
		case languages[w] != "" && lang == "":
			lang = languages[w]
		case strings.Trim(w, "0123456789") == "":
			// Track numbers, as in 2_English.srt.
		default:
			i = 0
		}
	}
	tags := ""
	if lang != "" {
		tags += "." + lang
	}
	if sdh {
		tags += ".sdh"
	}
	if forced {
		tags += ".forced"
	}
	name := videoStem + tags + ext
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s.%d%s%s", videoStem, n, tags, ext)
	}
	taken[name] = true
	return name
}

// languages maps language names and codes seen in subtitle file names to
// ISO 639-1 codes.
var languages = func() map[string]string {
	m := map[string]string{}
	for _, l := range [][]string{
		{"en", "eng", "english"}, {"sv", "swe", "swedish", "svenska"}, {"no", "nor", "nob", "nb", "norwegian", "norsk"},
		{"da", "dan", "danish", "dansk"}, {"fi", "fin", "finnish", "suomi"}, {"is", "ice", "isl", "icelandic"},
		{"de", "ger", "deu", "german", "deutsch"}, {"fr", "fre", "fra", "french", "francais"},
		{"es", "spa", "spanish", "espanol", "castellano"}, {"it", "ita", "italian", "italiano"},
		{"nl", "dut", "nld", "dutch"}, {"pt", "por", "portuguese", "brazilian"}, {"pl", "pol", "polish"},
		{"ru", "rus", "russian"}, {"uk", "ukr", "ukrainian"}, {"cs", "cze", "ces", "czech"},
		{"sk", "slo", "slk", "slovak"}, {"hu", "hun", "hungarian"}, {"ro", "rum", "ron", "romanian"},
		{"bg", "bul", "bulgarian"}, {"hr", "hrv", "croatian"}, {"sr", "srp", "serbian"}, {"sl", "slv", "slovenian"},
		{"el", "gre", "ell", "greek"}, {"tr", "tur", "turkish"}, {"he", "heb", "hebrew"}, {"ar", "ara", "arabic"},
		{"fa", "per", "fas", "persian"}, {"hi", "hin", "hindi"}, {"ja", "jpn", "japanese"},
		{"zh", "chi", "zho", "chinese"}, {"ko", "kor", "korean"}, {"th", "tha", "thai"},
		{"vi", "vie", "vietnamese"}, {"id", "ind", "indonesian"}, {"ms", "may", "msa", "malay"},
		{"et", "est", "estonian"}, {"lv", "lav", "latvian"}, {"lt", "lit", "lithuanian"},
	} {
		for _, k := range l {
			m[k] = l[0]
		}
	}
	return m
}()

// sidecars lists the subtitle files named after a library video.
func sidecars(video string) []string {
	stem := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video)) + "."
	entries, _ := os.ReadDir(filepath.Dir(video))
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(n, stem) && subtitleExts[strings.ToLower(filepath.Ext(n))] {
			out = append(out, filepath.Join(filepath.Dir(video), n))
		}
	}
	return out
}

func matches(m store.Media, it store.Item, v File, torrentName string, videoCount int) bool {
	if !m.IsShow() {
		return true
	}
	info := release.Parse(filepath.Base(v.Path))
	// A lone file with an uninformative name takes its identity from the torrent.
	if info.Season == 0 && info.Episode == 0 && videoCount == 1 {
		info = release.Parse(torrentName)
	}
	season, episode := it.Scene()
	return info.Season == season && info.Episode == episode
}

func destination(cfg *config.Config, m store.Media, it store.Item, ext string) string {
	name := fmt.Sprintf("%s (%d)", clean(m.Title), m.Year)
	if !m.IsShow() {
		return filepath.Join(cfg.MoviesDir, name, name+ext)
	}
	file := fmt.Sprintf("%s - S%02dE%02d", clean(m.Title), it.Season, it.Episode)
	if t := clean(it.Title); t != "" {
		file += " - " + t
	}
	return filepath.Join(cfg.TVDir, name, fmt.Sprintf("Season %02d", it.Season), file+ext)
}

// clean removes characters that are invalid or awkward in file names.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return -1
		}
		return r
	}, s)
	return strings.TrimRight(strings.TrimSpace(s), ".")
}

// link hardlinks src to dst, falling back to a copy when they are on
// different filesystems. An existing dst is replaced.
func link(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// Delete removes library files, with their subtitles, and the download
// directory copies they were linked from, so the space is freed, along with
// downloads (files of torrents still in the client) given separately.
// Directories left empty are removed. Paths outside the library and download
// directories are left alone.
func Delete(cfg *config.Config, library, downloads []string) error {
	roots := []string{cfg.DownloadDir, cfg.MoviesDir, cfg.TVDir}
	library = append([]string(nil), library...)
	for _, p := range library {
		if within(p, roots) != "" {
			library = append(library, sidecars(p)...)
		}
	}
	var linked []os.FileInfo
	for _, p := range library {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			linked = append(linked, fi)
		}
	}
	remove := append(append([]string(nil), library...), downloads...)
	if len(linked) > 0 {
		filepath.WalkDir(cfg.DownloadDir, func(p string, d os.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			for _, l := range linked {
				if fi.Size() == l.Size() && os.SameFile(fi, l) {
					remove = append(remove, p)
					break
				}
			}
			return nil
		})
	}

	var errs []error
	for _, p := range remove {
		root := within(p, roots)
		if root == "" {
			errs = append(errs, fmt.Errorf("not deleting %s: outside the library and download directories", p))
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
			continue
		}
		for dir := filepath.Dir(p); dir != root && within(dir, roots) == root; dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil {
				break // not empty
			}
		}
	}
	return errors.Join(errs...)
}

// within returns the root that contains path, or "" if none does.
func within(path string, roots []string) string {
	for _, root := range roots {
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return root
		}
	}
	return ""
}
