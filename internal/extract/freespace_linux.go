//go:build linux

package extract

import (
	"math"
	"os"
	"syscall"
)

// freeSpace returns the bytes an unprivileged user can still write on dir's filesystem.
func freeSpace(dir *os.File) (int64, error) {
	rc, err := dir.SyscallConn()
	if err != nil {
		return 0, err
	}
	var st syscall.Statfs_t
	var serr error
	if err := rc.Control(func(fd uintptr) { serr = syscall.Fstatfs(int(fd), &st) }); err != nil {
		return 0, err
	}
	if serr != nil {
		return 0, serr
	}
	if st.Bsize <= 0 {
		return 0, nil
	}
	bsize := uint64(st.Bsize)
	if st.Bavail > math.MaxInt64/bsize {
		return math.MaxInt64, nil
	}
	return int64(st.Bavail * bsize), nil //nolint:gosec // bounded by the check above
}
