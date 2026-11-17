//go:build linux

package jobdir

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

const (
	batch  = 1024 // entries of a directory read at a time
	window = 1024 // job folders swept in one sweep, at most
)

// errBudget stops a sweep that has looked at Limits.MaxEntries entries or run past
// Limits.MaxDuration; errTooMany a job folder with more entries than any job needs.
var (
	errBudget  = errors.New("jobdir: sweep budget used up")
	errTooMany = errors.New("jobdir: too many entries in a job folder")
)

func (w *WorkerSweeper) sweep(intake *os.Root) (SweepResult, error) {
	info, err := intake.Lstat(Worker)
	if errors.Is(err, fs.ErrNotExist) {
		return SweepResult{Done: true}, nil
	}
	if err != nil {
		return SweepResult{}, err
	}
	if !info.IsDir() {
		return SweepResult{}, &fs.PathError{Op: "sweep", Path: Worker, Err: ErrNotRegular}
	}
	top, err := openDirs(intake, []string{Worker}, false)
	if err != nil {
		return SweepResult{}, err
	}
	defer func() { _ = top.Close() }()
	s := &sweep{limits: w.Limits, budget: w.Limits.MaxEntries, top: top}
	if s.limits.MaxDepth < 2 {
		s.limits.MaxDepth = 2
	}
	if s.limits.MaxDuration > 0 {
		s.deadline = time.Now().Add(s.limits.MaxDuration)
	}
	if err := s.openSpill(); err != nil {
		return SweepResult{}, err
	}
	defer func() { _ = s.spill.Close() }()

	// 1. What was set aside, on half of the budget: nothing else can starve it.
	rest := s.budget - s.budget/2
	s.budget /= 2
	spillDone, err1 := s.emptySpill()
	s.budget += rest
	// 2. worker/ itself.
	done, next, err2 := s.walkTop(w.next, w.Orphan)
	w.next = next
	return SweepResult{Removed: s.removed, Sample: s.sample, Done: spillDone && done && s.asides == 0}, errors.Join(err1, err2)
}

// openSpill opens worker/.spill, making it when it is missing; anything else of that name
// (a link, a file) is removed first.
func (s *sweep) openSpill() error {
	for range 3 {
		d, err := openSub(s.top, Spill, Spill)
		if err != nil {
			return err
		}
		if d != nil {
			s.spill = d
			return nil
		}
		// Missing, or not a directory: what is there (never followed) goes, and it is made.
		_ = unlinkEntry(s.top, Spill, false)
		if err := unix.Mkdirat(int(s.top.Fd()), Spill, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
			return &fs.PathError{Op: "mkdir", Path: Spill, Err: err}
		}
	}
	return &fs.PathError{Op: "open", Path: Spill, Err: ErrNotRegular}
}

// emptySpill removes what is in worker/.spill, as far as the budget goes.
func (s *sweep) emptySpill() (bool, error) {
	for {
		entries, rerr := s.spill.ReadDir(batch)
		for _, e := range entries {
			if !s.take() {
				return false, nil
			}
			err := s.remove(s.spill, e.Name(), path.Join(Spill, e.Name()))
			if errors.Is(err, errBudget) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
		}
		if end(entries, rerr) {
			return rerr == nil || errors.Is(rerr, io.EOF), ignoreEOF(rerr)
		}
	}
}

// remove removes the entry name of dir, a directory with all in it (drain).
func (s *sweep) remove(dir *os.File, name, rel string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil // gone meanwhile
	}
	isDir := st.Mode&unix.S_IFMT == unix.S_IFDIR
	if isDir {
		if err := s.drain(dir, name, rel, 1); err != nil {
			return err
		}
	}
	if err := unlinkEntry(dir, name, isDir); err != nil {
		return &fs.PathError{Op: "remove", Path: rel, Err: err}
	}
	s.note(rel)
	return nil
}

// walkTop sweeps worker/ itself: it sets aside every entry that is no job's folder, and
// sweeps the job folders named after resume, in name order, at most window of them. It
// returns whether it got through, and the name to start the next sweep after.
func (s *sweep) walkTop(resume string, orphan func(string, time.Time) bool) (done bool, next string, err error) {
	var win []string // the smallest job folder names after resume, sorted
	var first error
	for {
		entries, rerr := s.top.ReadDir(batch)
		for _, e := range entries {
			name := e.Name()
			if name == Spill {
				continue
			}
			var st unix.Stat_t
			if err := unix.Fstatat(int(s.top.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				continue // gone meanwhile
			}
			if st.Mode&unix.S_IFMT != unix.S_IFDIR || !ValidID(name) {
				if !s.take() {
					return false, resume, first
				}
				if err := s.setAside(s.top, name, name, st); err != nil && first == nil {
					first = err
				}
				continue
			}
			if name <= resume {
				continue
			}
			i, _ := slices.BinarySearch(win, name)
			if i < window {
				win = slices.Insert(win, i, name)
				if len(win) > window {
					win = win[:window]
				}
			}
		}
		if !s.deadline.IsZero() && time.Now().After(s.deadline) {
			return false, resume, first
		}
		if end(entries, rerr) {
			if rerr != nil && !errors.Is(rerr, io.EOF) {
				return false, resume, errors.Join(first, rerr)
			}
			break
		}
	}
	last := resume
	for _, id := range win {
		if !s.take() {
			return false, last, first
		}
		var st unix.Stat_t
		if err := unix.Fstatat(int(s.top.Fd()), id, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			last = id
			continue
		}
		if orphan != nil && orphan(id, time.Unix(int64(st.Mtim.Sec), int64(st.Mtim.Nsec))) { //nolint:unconvert // the types differ between platforms
			if err := s.setAside(s.top, id, id, st); err != nil && first == nil {
				first = err
			}
			last = id
			continue
		}
		count := 0
		err := s.keep(s.top, id, id, 1, &count)
		if errors.Is(err, errTooMany) {
			// No job needs as many entries: the whole folder goes, in later sweeps.
			err = s.setAside(s.top, id, id, st)
		}
		if errors.Is(err, errBudget) {
			return false, last, first
		}
		if err != nil && first == nil {
			first = err
		}
		last = id
	}
	if len(win) == window { // there may be more after the last one
		return false, last, first
	}
	return true, "", first
}

// keep sweeps the job folder name in parent (rel, at depth): it removes what the worker
// never makes and keeps its scratch work (see WorkerSweeper). count is the job folder's
// entries so far; past Limits.MaxEntries/2 the sweep of it stops with errTooMany.
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
					if err := s.setAside(d, n, p, st); err != nil {
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
			if err := unlinkIn(d, n, false); err != nil {
				return &fs.PathError{Op: "remove", Path: p, Err: err}
			}
			s.note(p)
		}
		if end(entries, rerr) {
			return ignoreEOF(rerr)
		}
	}
}

// drain removes everything in the directory name of parent (rel, at depth), and leaves it
// empty for the caller to remove. Below Limits.MaxDepth a directory is set aside instead, so
// the descriptors held stay bounded.
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
					if err := s.setAside(d, n, p, st); err != nil {
						return err
					}
					continue
				}
				if err := s.drain(d, n, p, depth+1); err != nil {
					return err
				}
			}
			if err := unlinkIn(d, n, isDir); err != nil {
				return &fs.PathError{Op: "remove", Path: p, Err: err}
			}
			s.note(p)
		}
		if end(entries, rerr) {
			return ignoreEOF(rerr)
		}
	}
}

// setAside removes the entry name of parent (st is its lstat) when it is not a directory,
// and moves it into worker/.spill when it is: a later sweep removes it from there.
func (s *sweep) setAside(parent *os.File, name, rel string, st unix.Stat_t) error {
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		if err := unlinkIn(parent, name, false); err != nil {
			return &fs.PathError{Op: "remove", Path: rel, Err: err}
		}
		s.note(rel)
		return nil
	}
	if err := renameIn(parent, name, s.spill, spillName()); err != nil && !errors.Is(err, unix.ENOENT) {
		return &fs.PathError{Op: "set aside", Path: rel, Err: err}
	}
	s.asides++
	s.note(rel)
	return nil
}

func spillWorker(intakeRoot, id, rel string) error {
	intake, err := os.OpenRoot(intakeRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = intake.Close() }()
	top, err := openDirs(intake, []string{Worker}, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = top.Close() }()
	s := &sweep{limits: SweepLimits{MaxEntries: spillNow, MaxDepth: spillDepth}, budget: spillNow, top: top}
	if err := s.openSpill(); err != nil {
		return err
	}
	defer func() { _ = s.spill.Close() }()

	parent, base := top, id
	if rel != "" {
		el, _ := elements(rel)
		// worker/<id> and every folder on the way, without following a link.
		p, err := openDirs(intake, append([]string{Worker, id}, el[:len(el)-1]...), false)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer func() { _ = p.Close() }()
		parent, base = p, el[len(el)-1]
	}
	var st unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), base, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil // nothing there
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return ignoreGone(unlinkIn(parent, base, false))
	}
	name := spillName()
	if err := renameIn(parent, base, s.spill, name); err != nil {
		return ignoreGone(err)
	}
	// What a job leaves there is small: removed now, up to a bound; anything bigger is left
	// to the sweeps.
	if err := s.drain(s.spill, name, path.Join(Spill, name), 1); err == nil {
		_ = unlinkIn(s.spill, name, true)
	}
	return nil
}

// openSub opens the directory name of parent with O_NOFOLLOW. A nil file and nil error: it
// is gone, or not a directory (the next sweep sees what it is). A directory the worker made
// unreadable is made readable to its owner first (unlock).
func openSub(parent *os.File, name, rel string) (*os.File, error) {
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0)
	if errors.Is(err, unix.EACCES) && unlock(parent, name) == nil {
		fd, err = unix.Openat(int(parent.Fd()), name, flags, 0)
	}
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: rel, Err: err}
	}
	return os.NewFile(uintptr(fd), rel), nil
}

// unlock gives the directory name of parent mode 0700, without following a link: it is
// opened with O_PATH and O_NOFOLLOW, and changed through its /proc/self/fd entry (fchmodat
// with AT_SYMLINK_NOFOLLOW is not on every kernel).
func unlock(parent *os.File, name string) error {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	return unix.Fchmodat(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), 0o700, 0)
}

// unlinkIn removes name from the open directory dir; when dir is not writable (the worker
// made it so), it is made writable to its owner first.
func unlinkIn(dir *os.File, name string, isDir bool) error {
	err := unlinkEntry(dir, name, isDir)
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
		if unix.Fchmod(int(dir.Fd()), 0o700) == nil {
			err = unlinkEntry(dir, name, isDir)
		}
	}
	return err
}

// renameIn renames name in from to to in dir to. When that is refused (the worker took
// write access away from the folder, or from the directory moved, whose ".." changes), both
// are made writable to their owner and it is tried again.
func renameIn(from *os.File, name string, dir *os.File, to string) error {
	err := unix.Renameat(int(from.Fd()), name, int(dir.Fd()), to)
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
		_ = unix.Fchmod(int(from.Fd()), 0o700)
		_ = unlock(from, name)
		err = unix.Renameat(int(from.Fd()), name, int(dir.Fd()), to)
	}
	return err
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

// end reports whether a ReadDir batch is the last.
func end(entries []fs.DirEntry, err error) bool { return err != nil || len(entries) == 0 }

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func ignoreGone(err error) error {
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}
