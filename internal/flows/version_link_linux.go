//go:build linux

package flows

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// linkAt makes name in dir a hard link to srcName in srcDir. linkat(2) never replaces: it
// fails with EEXIST (fs.ErrExist) when name exists, and with EXDEV across filesystems. Both
// names are single path elements resolved from the open directories, and a symbolic link
// as the source is linked as it is, never followed.
func linkAt(srcDir *os.File, srcName string, dir *os.File, name string) error {
	return unix.Linkat(int(srcDir.Fd()), srcName, int(dir.Fd()), name, 0)
}

// crossDevice reports whether err is EXDEV: the two folders are on different filesystems.
func crossDevice(err error) bool { return errors.Is(err, unix.EXDEV) }
