package jobdir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// The checks of a job's file before the web process hands it to Radarr or Sonarr, links it
// into the library or moves it into the job (#113). The worker can write its own directory
// of the job (worker/<id>), and, in a deployment that does not follow docs/install.md, the
// job's directory too, so a name there may be a symbolic link, or lead through one, even one
// that stays inside the os.Root (which follows those). Every element of the path is opened
// with O_NOFOLLOW from the one before (OpenFile), so no link is ever followed, and the file
// that was checked is the open file, from which links and moves are then made.
var (
	// ErrLink means a symbolic link is on the way to the file, or is the file.
	ErrLink = errors.New("jobdir: a symbolic link is on the way")
	// ErrNotRegular means the name is not a regular file (a directory, FIFO or device).
	ErrNotRegular = errors.New("jobdir: not a regular file")
	// ErrBadPath means the path is not a clean, slash-separated, job-relative path.
	ErrBadPath = errors.New("jobdir: not a clean job-relative path")
	// ErrChanged means the file is not the one that was reported or checked: another size,
	// another name for the same file somewhere (a hard link), or another file after a move.
	ErrChanged = errors.New("jobdir: the file is not the one that was checked")
)

// rename is renameNoReplace; a test replaces it to swap the name during the move.
var rename = renameNoReplace

// elements splits rel, a job-relative path such as files/Season 01/e1.mkv, into its
// elements. It must be clean, local and slash-separated: no "", ".", ".." element, no
// leading slash, no backslash.
func elements(rel string) ([]string, error) {
	if rel == "" || path.Clean(rel) != rel || !filepath.IsLocal(rel) || strings.ContainsAny(rel, `\`) {
		return nil, fmt.Errorf("%w: %q", ErrBadPath, rel)
	}
	el := strings.Split(rel, "/")
	for _, e := range el {
		if e == "" || e == "." || e == ".." {
			return nil, fmt.Errorf("%w: %q", ErrBadPath, rel)
		}
	}
	return el, nil
}

// OpenFile opens the regular file rel inside root for reading. Every directory on the way
// and the file itself are opened with O_NOFOLLOW, each from the one before, so a symbolic
// link anywhere is refused (ErrLink), and so is anything that is not a regular file
// (ErrNotRegular). A missing element is an error wrapping fs.ErrNotExist. The caller closes
// the file; what it then does with the file (link it, hand its name to an app) is about
// this file and no other.
func OpenFile(root *os.Root, rel string) (*os.File, fs.FileInfo, error) {
	dir, f, info, err := open(root, rel)
	if err != nil {
		return nil, nil, err
	}
	_ = dir.Close()
	return f, info, nil
}

// CheckFile checks that rel inside root is a regular file reached without a symbolic link
// (OpenFile), of size bytes unless size is negative, and returns its information. The web
// process calls it right before it hands a job's file to Radarr or Sonarr, which open the
// path themselves.
func CheckFile(root *os.Root, rel string, size int64) (fs.FileInfo, error) {
	f, info, err := OpenFile(root, rel)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	if size >= 0 && info.Size() != size {
		return nil, fmt.Errorf("%w: %s is %d bytes, not %d", ErrChanged, rel, info.Size(), size)
	}
	return info, nil
}

// MakeDir opens the directory rel inside root, creating the elements that are missing
// (0755), with every element opened with O_NOFOLLOW from the one before: a symbolic link
// on the way is refused (ErrLink), as is an element that is not a directory. rel "." is
// root itself.
func MakeDir(root *os.Root, rel string) (*os.File, error) {
	if rel == "." {
		return root.Open(".")
	}
	el, err := elements(rel)
	if err != nil {
		return nil, err
	}
	return openDirs(root, el, true)
}

// Seal moves the regular file rel from the worker's directory of a job (from, OpenWorker)
// to the same place in the job's directory (to, Open), whose missing directories it creates:
// a rename, so the file keeps its inode and nothing is copied. It refuses a file reached
// through a symbolic link, one that is not size bytes, and one that has another name (a
// hard link the worker could still write through). The rename never replaces anything; what
// it moved is checked again afterwards, and taken back out of the job when it is not the
// file checked before (the worker swapped the name meanwhile).
//
// After Seal the worker can no longer change the file through any name: the job's directory
// is read-only to it (docs/install.md). Only a descriptor it kept open from before reaches
// the file's data: what it wrote itself, whose content it chooses anyway
// (docs/threat-model.md, §6).
//
// A file that is already in the job and no longer in the worker's directory is a repeat
// after a crash: it is checked as above and Seal succeeds.
func Seal(from, to *os.Root, rel string, size int64) error {
	el, err := elements(rel)
	if err != nil {
		return err
	}
	sd, src, info, err := open(from, rel)
	if errors.Is(err, fs.ErrNotExist) {
		got, err := CheckFile(to, rel, size)
		if err != nil {
			return err
		}
		if links(got) != 1 {
			return fmt.Errorf("%w: %s has another name", ErrChanged, rel)
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = sd.Close() }()
	defer func() { _ = src.Close() }()
	if info.Size() != size {
		return fmt.Errorf("%w: %s is %d bytes, not %d", ErrChanged, rel, info.Size(), size)
	}
	if links(info) != 1 {
		return fmt.Errorf("%w: %s has another name", ErrChanged, rel)
	}
	dd, err := openDirs(to, el[:len(el)-1], true)
	if err != nil {
		return err
	}
	defer func() { _ = dd.Close() }()
	base := el[len(el)-1]
	if err := rename(sd, base, dd); err != nil {
		return &fs.PathError{Op: "seal", Path: rel, Err: err}
	}
	if err := sameAt(dd, base, src, size); err != nil {
		if rerr := to.RemoveAll(rel); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return err
	}
	return nil
}
