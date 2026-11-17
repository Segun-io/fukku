//go:build linux

package jobdir

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames name in from to the same name in to, atomically, and fails with
// EEXIST (fs.ErrExist) when it exists there. Both are open directories and name is one path
// element, so nothing is resolved by path.
func renameNoReplace(from *os.File, name string, to *os.File) error {
	return unix.Renameat2(int(from.Fd()), name, int(to.Fd()), name, unix.RENAME_NOREPLACE)
}
