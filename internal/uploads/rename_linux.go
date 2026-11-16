//go:build linux

package uploads

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames src to dst inside root and fails with EEXIST (fs.ErrExist) when
// dst exists, atomically. The two directories are opened through root, so the rename stays
// inside it; the names passed to renameat2 are single path elements.
func renameNoReplace(root *os.Root, src, dst string) error {
	sd, err := root.Open(filepath.Dir(src))
	if err != nil {
		return err
	}
	defer func() { _ = sd.Close() }()
	dd, err := root.Open(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer func() { _ = dd.Close() }()
	return unix.Renameat2(int(sd.Fd()), filepath.Base(src), int(dd.Fd()), filepath.Base(dst), unix.RENAME_NOREPLACE)
}
