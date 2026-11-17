//go:build !linux

package jobdir

import (
	"errors"
	"os"
)

// renameNoReplace is not available on this platform (the image runs on Linux): a rename
// that cannot refuse an existing name could replace a file of the job, so nothing is moved.
func renameNoReplace(*os.File, string, *os.File) error {
	return errors.New("rename without replace is not supported on this platform")
}
