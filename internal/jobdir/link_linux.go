//go:build linux

package jobdir

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// LinkFile makes name in dir a hard link to the open file src: the file that was opened and
// checked (OpenFile), whatever its name points to now, never a path resolved again (#113).
// It is linkat(2) of the descriptor's /proc/self/fd entry with AT_SYMLINK_FOLLOW, which
// links the file the descriptor refers to and needs no capability; AT_EMPTY_PATH would do
// the same, but before Linux 6.10 it needs CAP_DAC_READ_SEARCH, which the containers drop.
// It never replaces: it fails with EEXIST (fs.ErrExist) when name exists, and with EXDEV
// across mounts. name is one path element resolved from the open directory. The new name
// is checked afterwards to be src, and removed again when it is not (ErrChanged). Without
// /proc it fails with an error that says so.
func LinkFile(src *os.File, dir *os.File, name string) error {
	proc := "/proc/self/fd/" + strconv.Itoa(int(src.Fd()))
	if err := unix.Linkat(unix.AT_FDCWD, proc, int(dir.Fd()), name, unix.AT_SYMLINK_FOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			if _, serr := os.Stat("/proc/self/fd"); serr != nil {
				return fmt.Errorf("link %s: /proc is not mounted, and a file is linked through /proc/self/fd: %w", name, err)
			}
		}
		return err
	}
	if err := sameAt(dir, name, src); err != nil {
		// Not the file that was linked: the name goes again (it was made just now).
		return removeName(dir, name, err)
	}
	return nil
}
