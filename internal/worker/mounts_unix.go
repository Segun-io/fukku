//go:build unix

package worker

import "golang.org/x/sys/unix"

// writable reports whether this process may write p: access(2) with W_OK, which fails with
// EROFS on a read-only mount and ENOENT for a path that is not there.
func writable(p string) bool { return unix.Access(p, unix.W_OK) == nil }

// device is the id of the filesystem p is on.
func device(p string) (uint64, bool) {
	var st unix.Stat_t
	if err := unix.Stat(p, &st); err != nil {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert // Dev is not a uint64 on every platform
}
