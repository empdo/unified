// Package disk reports free space on the filesystems that hold downloads and
// the library.
package disk

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Dir is a directory to report on, with a name to show for it.
type Dir struct {
	Label string
	Path  string
}

// Volume is one filesystem and the directories on it.
type Volume struct {
	Labels []string `json:"labels"`
	Free   uint64   `json:"free"` // available to unprivileged users
	Total  uint64   `json:"total"`
	Error  string   `json:"error,omitempty"`
}

// Free returns the bytes available on the filesystem holding path.
func Free(path string) (uint64, error) {
	free, _, _, err := stat(existing(path))
	return free, err
}

// Volumes groups dirs by the filesystem they are on, in the order given.
func Volumes(dirs []Dir) []Volume {
	var out []Volume
	byDev := map[uint64]int{}
	for _, d := range dirs {
		free, total, dev, err := stat(existing(d.Path))
		if err != nil {
			out = append(out, Volume{Labels: []string{d.Label}, Error: err.Error()})
			continue
		}
		if i, ok := byDev[dev]; ok {
			out[i].Labels = append(out[i].Labels, d.Label)
			continue
		}
		byDev[dev] = len(out)
		out = append(out, Volume{Labels: []string{d.Label}, Free: free, Total: total})
	}
	return out
}

// existing returns path, or its nearest parent that exists, since the
// library directories are only created by the first import.
func existing(path string) string {
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

// Folder is how much space a directory takes, and what in it takes the most.
type Folder struct {
	Size uint64 `json:"size"` // bytes allocated on disk
	// Of Size, the files that are hardlinked elsewhere too, such as library
	// files still seeding from the download directory. They are stored once.
	Linked  uint64  `json:"linked"`
	Files   int     `json:"files"`
	Entries []Entry `json:"entries"` // top-level entries, largest first, hidden ones left out
}

type Entry struct {
	Name string `json:"name"`
	Size uint64 `json:"size"`
}

// Measure adds up the files under dir. A directory that does not exist yet
// is empty.
func Measure(ctx context.Context, dir string) (Folder, error) {
	f := Folder{Entries: []Entry{}}
	sizes := map[string]uint64{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return nil // unreadable: skip it
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		size, links := allocated(fi)
		f.Size += size
		f.Files++
		if links > 1 {
			f.Linked += size
		}
		rel, _ := filepath.Rel(dir, p)
		// Hidden files, like the torrent client's state, count but are not listed.
		if top, _, _ := strings.Cut(filepath.ToSlash(rel), "/"); !strings.HasPrefix(top, ".") {
			sizes[top] += size
		}
		return nil
	})
	for name, size := range sizes {
		f.Entries = append(f.Entries, Entry{name, size})
	}
	sort.Slice(f.Entries, func(i, j int) bool {
		if f.Entries[i].Size != f.Entries[j].Size {
			return f.Entries[i].Size > f.Entries[j].Size
		}
		return f.Entries[i].Name < f.Entries[j].Name
	})
	return f, err
}
