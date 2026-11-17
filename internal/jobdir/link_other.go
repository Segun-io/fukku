//go:build !linux

package jobdir

import (
	"errors"
	"os"
)

// LinkFile is not available on this platform (the image runs on Linux): a file is linked
// from its open descriptor through /proc/self/fd, so that what was checked is what is
// linked, and nothing else is linked instead.
func LinkFile(*os.File, *os.File, string) error {
	return errors.New("linking an open file is not supported on this platform")
}
