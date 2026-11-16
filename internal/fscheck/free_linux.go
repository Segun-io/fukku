//go:build linux

package fscheck

import (
	"math"
	"syscall"
)

// FreeBytes returns the bytes an unprivileged user can still write on dir's filesystem.
func FreeBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
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
