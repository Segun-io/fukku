//go:build !unix

package jobdir

import (
	"errors"
	"io/fs"
	"os"
)

// The checks of a job's file open every element with O_NOFOLLOW, which this platform does
// not have (the image runs on Linux): nothing is opened, so nothing is handed over.
var errUnsupported = errors.New("jobdir: opening a job's file without following links is not supported on this platform")

func open(*os.Root, string) (dir, f *os.File, info fs.FileInfo, err error) {
	return nil, nil, nil, errUnsupported
}

func openDirs(*os.Root, []string, bool) (*os.File, error) { return nil, errUnsupported }

func sameAt(*os.File, string, *os.File, int64) error { return errUnsupported }

func links(fs.FileInfo) uint64 { return 1 }
