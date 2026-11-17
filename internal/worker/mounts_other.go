//go:build !unix

package worker

import "os"

// writable reports whether p is there and not read-only. This platform has no access(2)
// (the image runs on Linux): the permission bits are all there is.
func writable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().Perm()&0o200 != 0
}

// device is not known on this platform: every writable folder above the intake root counts
// as the host's.
func device(string) (uint64, bool) { return 0, true }
