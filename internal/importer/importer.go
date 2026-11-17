// Package importer places finished downloads into the library by hardlink,
// so the torrent can keep seeding from the download directory.
package importer

import (
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
	return info.Season == it.Season && info.Episode == it.Episode
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
