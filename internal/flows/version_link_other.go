//go:build !linux

package flows

import (
	"errors"
	"os"
)

// linkAt is not available on this platform (the image runs on Linux): second versions are
// placed with linkat(2), which never replaces a file.
func linkAt(*os.File, string, *os.File, string) error {
	return errors.New("placing a second version is not supported on this platform")
}

func crossDevice(error) bool { return false }
