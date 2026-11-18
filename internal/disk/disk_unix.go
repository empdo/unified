//go:build unix

package disk

import (
	"os"
	"syscall"
)

func stat(path string) (free, total, dev uint64, err error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, 0, 0, err
	}
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, 0, 0, err
	}
	bsize := uint64(fs.Bsize)
	return uint64(fs.Bavail) * bsize, uint64(fs.Blocks) * bsize, uint64(st.Dev), nil
}

// allocated is the space a file takes on disk, which for a download still in
// progress can be less than its size, and its number of hardlinks.
func allocated(fi os.FileInfo) (size, links uint64) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Blocks) * 512, uint64(st.Nlink)
	}
	return uint64(fi.Size()), 1
}
