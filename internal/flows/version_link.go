package flows

import (
	"errors"
	"syscall"
)

// crossDevice reports whether err is EXDEV: the two folders are on different filesystems
// (or different mounts of one), so a version cannot be a hard link between them.
func crossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }
