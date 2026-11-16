//go:build !linux

package uploads

import (
	"errors"
	"os"
)

// renameNoReplace is not available on this platform (the image runs on Linux): without hard
// links, an upload cannot be moved without the risk of replacing a file, so it is not moved.
func renameNoReplace(*os.Root, string, string) error {
	return errors.New("rename without replace is not supported on this platform")
}
