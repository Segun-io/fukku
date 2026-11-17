//go:build !unix

package worker

import "os"

// writable reports whether p is there and not read-only. This platform has no access(2)
// (the image runs on Linux): the permission bits are all there is.
func writable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().Perm()&0o200 != 0
}
