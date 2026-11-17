//go:build unix

package jobdir

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

// batch is how many entries of a directory are read at a time.
const batch = 1024

// errBudget stops a sweep that has looked at Limits.MaxEntries entries; errTooMany a job
// folder with more entries than any job needs.
var (
	errBudget  = errors.New("jobdir: sweep budget used up")
	errTooMany = errors.New("jobdir: too many entries in a job folder")
)

// run sweeps worker/ (s.top), starting at the job folder resume (all of them when it is "");
// it returns whether it got through, and the job folder to start the next sweep in.
func (s *sweep) run(resume string) (done bool, next string, err error) {
	reached := resume == ""
	var first error
	for {
		entries, rerr := s.top.ReadDir(batch)
		for _, e := range entries {
			name := e.Name()
			var st unix.Stat_t
			if err := unix.Fstatat(int(s.top.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				continue // gone meanwhile
			}
			isDir := st.Mode&unix.S_IFMT == unix.S_IFDIR
			if isDir && ValidID(name) {
				if !reached {
					if name != resume {
						continue
					}
					reached = true
				}
				count := 0
				err := s.keep(s.top, name, path.Join(Worker, name), 1, &count)
				if errors.Is(err, errTooMany) {
					// No job needs as many entries: the whole folder goes, in later sweeps.
					err = s.spill(s.top, name, path.Join(Worker, name))
				}
				if errors.Is(err, errBudget) {
					return false, name, first
				}
				if err != nil && first == nil {
					first = err
				}
				continue
			}
			if !s.take() {
				return false, resume, first
			}
			// Not a job's folder: it goes, whatever it is.
			rel := path.Join(Worker, name)
			if isDir {
				err := s.drain(s.top, name, rel, 1)
				if errors.Is(err, errBudget) {
					return false, resume, first
				}
				if err != nil {
					if first == nil {
						first = err
					}
					continue
				}
			}
			if err := unlinkEntry(s.top, name, isDir); err != nil {
				if first == nil {
					first = &fs.PathError{Op: "remove", Path: rel, Err: err}
				}
				continue
			}
			s.note(rel)
		}
		if errors.Is(rerr, io.EOF) || (rerr == nil && len(entries) == 0) {
			return true, "", first
		}
		if rerr != nil {
			return false, resume, errors.Join(first, rerr)
		}
	}
}

// removeTop removes the entry name of worker/ as far as the budget goes; what is left, a
// later sweep removes.
func (s *sweep) removeTop(name string) {
	var st unix.Stat_t
	if err := unix.Fstatat(int(s.top.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return
	}
	isDir := st.Mode&unix.S_IFMT == unix.S_IFDIR
	if isDir && s.drain(s.top, name, path.Join(Worker, name), 1) != nil {
		return
	}
	_ = unlinkEntry(s.top, name, isDir)
}

// take counts one entry against the budget; false when it is used up.
func (s *sweep) take() bool {
	if s.budget <= 0 {
		return false
	}
	s.budget--
	return true
}

// keep sweeps the job folder name in parent (rel from the intake root, at depth): it removes
// what the worker never makes and keeps its scratch work (see WorkerSweeper). count is the
// job folder's entries so far; past Limits.MaxEntries/2 the whole folder is set aside.
func (s *sweep) keep(parent *os.File, name, rel string, depth int, count *int) error {
	d, err := openSub(parent, name, rel)
	if d == nil {
		return err
	}
	defer func() { _ = d.Close() }()
	fd := int(d.Fd())
	for {
		entries, rerr := d.ReadDir(batch)
		for _, e := range entries {
			if !s.take() {
				return errBudget
			}
			*count++
			if *count > s.limits.MaxEntries/2 {
				return errTooMany
			}
			n, p := e.Name(), path.Join(rel, e.Name())
			var st unix.Stat_t
			if err := unix.Fstatat(fd, n, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				continue
			}
			switch st.Mode & unix.S_IFMT {
			case unix.S_IFDIR:
				if depth+1 > s.limits.MaxDepth { // deeper than any folder the worker makes
					if err := s.spill(d, n, p); err != nil {
						return err
					}
					continue
				}
				if err := s.keep(d, n, p, depth+1, count); err != nil {
					return err
				}
				continue
			case unix.S_IFREG:
				if uint64(st.Nlink) == 1 { //nolint:unconvert // Nlink is not a uint64 on every platform
					continue
				}
			}
			if err := unlinkEntry(d, n, false); err != nil {
				return &fs.PathError{Op: "remove", Path: p, Err: err}
			}
			s.note(p)
		}
		if errors.Is(rerr, io.EOF) || (rerr == nil && len(entries) == 0) {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// drain removes everything in the directory name of parent (rel, at depth), and leaves it
// empty for the caller to remove. Below Limits.MaxDepth a directory is set aside instead
// (moved to the top of worker/), so the descriptors held stay bounded.
func (s *sweep) drain(parent *os.File, name, rel string, depth int) error {
	d, err := openSub(parent, name, rel)
	if d == nil {
		return err
	}
	defer func() { _ = d.Close() }()
	fd := int(d.Fd())
	for {
		entries, rerr := d.ReadDir(batch)
		for _, e := range entries {
			if !s.take() {
				return errBudget
			}
			n, p := e.Name(), path.Join(rel, e.Name())
			var st unix.Stat_t
			if err := unix.Fstatat(fd, n, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				continue
			}
			isDir := st.Mode&unix.S_IFMT == unix.S_IFDIR
			if isDir {
				if depth+1 > s.limits.MaxDepth {
					if err := s.spill(d, n, p); err != nil {
						return err
					}
					continue
				}
				if err := s.drain(d, n, p, depth+1); err != nil {
					return err
				}
			}
			if err := unlinkEntry(d, n, isDir); err != nil {
				return &fs.PathError{Op: "remove", Path: p, Err: err}
			}
			s.note(p)
		}
		if errors.Is(rerr, io.EOF) || (rerr == nil && len(entries) == 0) {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// spill moves the directory name of parent to the top of worker/ under a SpillPrefix name:
// a later sweep removes it from there, a bounded number of levels at a time.
func (s *sweep) spill(parent *os.File, name, rel string) error {
	if err := unix.Renameat(int(parent.Fd()), name, int(s.top.Fd()), spillName()); err != nil && !errors.Is(err, unix.ENOENT) {
		return &fs.PathError{Op: "set aside", Path: rel, Err: err}
	}
	s.note(rel)
	return nil
}

// openSub opens the directory name of parent with O_NOFOLLOW. A nil file and nil error: it
// is gone, or not a directory any more (the next sweep sees what it is).
func openSub(parent *os.File, name, rel string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: rel, Err: err}
	}
	return os.NewFile(uintptr(fd), rel), nil
}

// unlinkEntry removes name from dir: a directory (empty by now) or anything else.
func unlinkEntry(dir *os.File, name string, isDir bool) error {
	flags := 0
	if isDir {
		flags = unix.AT_REMOVEDIR
	}
	err := unix.Unlinkat(int(dir.Fd()), name, flags)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}
