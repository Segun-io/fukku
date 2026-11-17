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

// openNoLink opens top/id (jobs/<id> or worker/<id>) as an os.Root, refusing a symbolic link
// there even when it stays inside the intake root (os.Root would follow that one): the
// folder is first opened with O_NOFOLLOW, and the root must be that same folder (ErrLink
// otherwise), so a link swapped in between is caught too. A link at worker/<id> to
// ../jobs/<another> would otherwise make the worker's folder of one job another job's.
func openNoLink(intake *os.Root, top, id string) (*os.Root, error) {
	d, err := openDirs(intake, []string{top, id}, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	r, err := intake.OpenRoot(path.Join(top, id))
	if err != nil {
		return nil, err
	}
	want, err1 := d.Stat()
	got, err2 := r.Stat(".")
	if err1 != nil || err2 != nil || !os.SameFile(want, got) {
		_ = r.Close()
		return nil, fmt.Errorf("%w: %s", ErrLink, path.Join(top, id))
	}
	return r, nil
}

// link is LinkFile; a test replaces it to swap the worker's name during the move.
var link = LinkFile

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
// (OpenFile), of size bytes unless size is negative, with no other hard link (WantLinks 1),
// and returns its information. The web process calls it right before it hands a job's file
// to Radarr or Sonarr, which open the path themselves: a second name of the file could be
// one the worker made in its own folder through a descriptor it kept, and writes through
// it would change the imported file.
func CheckFile(root *os.Root, rel string, size int64) (fs.FileInfo, error) {
	f, info, err := OpenFile(root, rel)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	if size >= 0 && info.Size() != size {
		return nil, fmt.Errorf("%w: %s is %d bytes, not %d", ErrChanged, rel, info.Size(), size)
	}
	if err := WantLinks(info, rel, 1); err != nil {
		return nil, err
	}
	return info, nil
}

// Links is a file's number of hard links (1 when the platform does not say).
func Links(info fs.FileInfo) uint64 { return links(info) }

// WantLinks refuses (ErrChanged) a file with another number of hard links than want: the
// names the web process made itself (the job's, a staged or placed version). The worker
// never makes a hard link, so any other is one it made to keep a writable name.
func WantLinks(info fs.FileInfo, rel string, want uint64) error {
	if n := links(info); n != want {
		return fmt.Errorf("%w: %s has %d names, not %d (a hard link somewhere else)", ErrChanged, rel, n, want)
	}
	return nil
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

// Seal takes the regular file rel from the worker's directory of a job (from, OpenWorker)
// into the same place in the job's directory (to, Open), whose missing directories it
// creates. It opens the file without following a link and checks it: a regular file of size
// bytes with no other name. It then hard-links that open file into the job (LinkFile: the
// file the descriptor refers to, whatever the worker's name points to by then) and removes
// the worker's name when it is still that file. The file keeps its inode; nothing is copied,
// and nothing but the checked regular file ever gets a name under the job's directory. If the
// file has a name besides the job's when Seal is done (the worker kept one, or made one
// meanwhile), the job's name is removed again and Seal fails.
//
// After Seal the worker can no longer reach the file by a name in the job: the job's
// directory is read-only to it (docs/install.md). A descriptor it kept from before still
// reaches the data, and can give the file a new name in the worker's folder; that is what
// WantLinks, right before each hand-over, and the sweep of the worker's folder are for
// (docs/threat-model.md, §5.5 and §6).
//
// A file that is already in the job and no longer in the worker's directory is a repeat
// after a crash: it is checked as above and Seal succeeds; so does one that is in both, as
// the same file, which a crash between the link and the removal leaves.
func Seal(from, to *os.Root, rel string, size int64) error {
	el, err := elements(rel)
	if err != nil {
		return err
	}
	sd, src, info, err := open(from, rel)
	if errors.Is(err, fs.ErrNotExist) {
		_, err := CheckFile(to, rel, size)
		return err
	}
	if err != nil {
		return err
	}
	defer func() { _ = sd.Close() }()
	defer func() { _ = src.Close() }()
	if info.Size() != size {
		return fmt.Errorf("%w: %s is %d bytes, not %d", ErrChanged, rel, info.Size(), size)
	}
	dd, err := openDirs(to, el[:len(el)-1], true)
	if err != nil {
		return err
	}
	defer func() { _ = dd.Close() }()
	base := el[len(el)-1]
	want := uint64(1)
	if sameAt(dd, base, src) == nil { // linked before a crash: the job's name is the second
		want = 2
	}
	if err := WantLinks(info, rel, want); err != nil {
		if want == 2 { // the job's name, linked before a crash, goes too
			return removeName(dd, base, err)
		}
		return err
	}
	if err := link(src, dd, base); errors.Is(err, fs.ErrExist) {
		if sameAt(dd, base, src) != nil { // a file of the job's, not this one
			return &fs.PathError{Op: "seal", Path: rel, Err: err}
		}
	} else if err != nil {
		return &fs.PathError{Op: "seal", Path: rel, Err: err}
	}
	// The worker's name goes, if it is still this file (a swapped-in name is left: it is the
	// worker's own, in its own folder).
	if sameAt(sd, base, src) == nil {
		if err := unlinkAt(sd, base); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removeName(dd, base, &fs.PathError{Op: "seal", Path: rel, Err: err})
		}
	}
	now, err := src.Stat()
	if err != nil {
		return removeName(dd, base, err)
	}
	if err := WantLinks(now, rel, 1); err != nil {
		return removeName(dd, base, err)
	}
	return nil
}

// removeName removes the job's name of a file Seal refused, and returns err. The job's
// directory is the web process's own, so the name is still the link Seal made.
func removeName(dir *os.File, name string, err error) error {
	if rerr := unlinkAt(dir, name); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return errors.Join(err, rerr)
	}
	return err
}
