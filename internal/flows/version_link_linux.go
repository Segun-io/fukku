//go:build linux

package flows

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// linkAt makes name in dir a hard link to the open file src: the file that was opened and
// checked (jobdir.OpenFile), whatever its name is now, never a path resolved again (#113).
// linkat(2) of the descriptor's /proc/self/fd entry with AT_SYMLINK_FOLLOW links the file
// the descriptor refers to; that needs no capability, unlike AT_EMPTY_PATH. It never
// replaces: it fails with EEXIST (fs.ErrExist) when name exists, and with EXDEV across
// mounts. name is one path element resolved from the open directory. The new name is
// checked afterwards to be src.
func linkAt(src *os.File, dir *os.File, name string) error {
	proc := "/proc/self/fd/" + strconv.Itoa(int(src.Fd()))
	if err := unix.Linkat(unix.AT_FDCWD, proc, int(dir.Fd()), name, unix.AT_SYMLINK_FOLLOW); err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	info, err := src.Stat()
	if err != nil {
		return err
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Dev != s.Dev || st.Ino != s.Ino {
		return fmt.Errorf("%s is not the job's file after the link", name)
	}
	return nil
}

// crossDevice reports whether err is EXDEV: the two folders are on different filesystems.
func crossDevice(err error) bool { return errors.Is(err, unix.EXDEV) }
