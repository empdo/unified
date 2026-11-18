//go:build !unix

package disk

import (
	"errors"
	"os"
)

func stat(path string) (free, total, dev uint64, err error) {
	return 0, 0, 0, errors.New("free space is not reported on this system")
}

func allocated(fi os.FileInfo) (size, links uint64) { return uint64(fi.Size()), 1 }
