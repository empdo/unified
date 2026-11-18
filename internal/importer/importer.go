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

// Import links the video files that match the given items into the library
// and reports which items were satisfied.
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
		out = append(out, Result{ItemID: it.ID, Dest: dest})
	}
	return out, nil
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

// Delete removes library files and the download directory copies they were
// linked from, so the space is freed, along with downloads (files of torrents
// still in the client) given separately. Directories left empty are removed.
// Paths outside the library and download directories are left alone.
func Delete(cfg *config.Config, library, downloads []string) error {
	roots := []string{cfg.DownloadDir, cfg.MoviesDir, cfg.TVDir}
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
