//go:build unix

package jobdir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// open opens rel inside root as OpenFile does and also returns the directory that holds
// it, opened the same way.
func open(root *os.Root, rel string) (dir, f *os.File, info fs.FileInfo, err error) {
	el, err := elements(rel)
	if err != nil {
		return nil, nil, nil, err
	}
	dir, err = openDirs(root, el[:len(el)-1], false)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() {
		if err != nil {
			_ = dir.Close()
		}
	}()
	base := el[len(el)-1]
	// O_NONBLOCK: a FIFO put there must not hang the open; it is refused below.
	fd, err := unix.Openat(int(dir.Fd()), base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, nil, classify(dir, base, rel, err)
	}
	f = os.NewFile(uintptr(fd), rel)
	info, err = f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%w: %s", ErrNotRegular, rel)
	}
	if err == nil {
		err = unix.SetNonblock(fd, false)
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, nil, err
	}
	return dir, f, info, nil
}

// openDirs opens the directories names below root, one at a time with O_NOFOLLOW, each from
// the one before; with create, a missing one is made (0755). No names is root itself.
func openDirs(root *os.Root, names []string, create bool) (*os.File, error) {
	d, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	for i, name := range names {
		fd, err := unix.Openat(int(d.Fd()), name, flags, 0)
		if errors.Is(err, unix.ENOENT) && create {
			if err = unix.Mkdirat(int(d.Fd()), name, 0o755); err == nil || errors.Is(err, unix.EEXIST) {
				fd, err = unix.Openat(int(d.Fd()), name, flags, 0)
			}
		}
		if err != nil {
			err = classify(d, name, strings.Join(names[:i+1], "/"), err)
			_ = d.Close()
			return nil, err
		}
		_ = d.Close()
		d = os.NewFile(uintptr(fd), strings.Join(names[:i+1], "/"))
	}
	return d, nil
}

// classify turns the error of an openat of name in dir into ErrLink when name is a symbolic
// link (O_NOFOLLOW fails with ELOOP, or ENOTDIR with O_DIRECTORY on some systems), and into
// an fs.PathError otherwise, so that fs.ErrNotExist matches a missing name.
func classify(dir *os.File, name, rel string, err error) error {
	var st unix.Stat_t
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.EMLINK) {
		if serr := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); serr == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK {
			return fmt.Errorf("%w: %s", ErrLink, rel)
		}
	}
	if errors.Is(err, unix.ENOTDIR) {
		return fmt.Errorf("%w: %s is not a directory", ErrNotRegular, rel)
	}
	return &fs.PathError{Op: "open", Path: rel, Err: err}
}

// sameAt checks that name in dir is the open file f (the same inode) and a regular file.
func sameAt(dir *os.File, name string, f *os.File) error {
	var st unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return &fs.PathError{Op: "stat", Path: name, Err: err}
	}
	now, err := f.Stat()
	if err != nil {
		return err
	}
	s, ok := now.Sys().(*syscall.Stat_t)
	if !ok || st.Mode&unix.S_IFMT != unix.S_IFREG ||
		uint64(st.Dev) != uint64(s.Dev) || uint64(st.Ino) != uint64(s.Ino) { //nolint:unconvert // the types differ between platforms
		return fmt.Errorf("%w: %s is another file", ErrChanged, name)
	}
	return nil
}

// unlinkAt removes the name in dir, never a directory.
func unlinkAt(dir *os.File, name string) error {
	if err := unix.Unlinkat(int(dir.Fd()), name, 0); err != nil {
		return &fs.PathError{Op: "unlink", Path: name, Err: err}
	}
	return nil
}

// links is a file's number of hard links (1 when the platform does not say).
func links(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink) //nolint:unconvert // Nlink is not a uint64 on every platform
	}
	return 1
}
