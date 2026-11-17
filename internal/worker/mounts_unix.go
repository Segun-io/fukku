//go:build unix

package worker

import "golang.org/x/sys/unix"

// writable reports whether this process may write p: access(2) with W_OK, which fails with
// EROFS on a read-only mount and ENOENT for a path that is not there.
func writable(p string) bool { return unix.Access(p, unix.W_OK) == nil }
