package jobdir

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
)

// SpillPrefix starts the names of what is set aside in worker/ for removal: a job's folder
// that is no longer needed, or a part of one. Not a job id, so WorkerSweeper removes it,
// a bounded number of entries per sweep.
const SpillPrefix = ".spill-"

// SweepLimits bound one sweep of worker/ (WorkerSweeper.Sweep).
type SweepLimits struct {
	// MaxEntries is how many entries one sweep looks at; the next sweep carries on.
	MaxEntries int
	// MaxDepth is how many levels of directories a job's folder may have below worker/
	// (worker/<id> is level 1). A directory deeper than that is no folder the worker makes
	// (its deepest are extraction paths of extract.maxPathSegments elements): it is set
	// aside and removed.
	MaxDepth int
}

// SweepResult is what one sweep did.
type SweepResult struct {
	Removed int      // entries removed, or set aside to be removed
	Sample  []string // the first of them, relative to the intake root, for the log
	Done    bool     // the sweep got through all of worker/; else the next one carries on
}

// WorkerSweeper keeps worker/ to what the worker makes for a job, a bounded amount of work
// at a time. It removes, never following a link (#114):
//
//   - every entry of worker/ that is not a directory named by a job id (and so everything
//     set aside with SpillWorker);
//   - in the job folders, every regular file with more than one name (a hard link: the
//     worker makes none, and one there may be a name of a sealed, imported or placed file),
//     everything that is neither a regular file nor a directory, and every directory deeper
//     than Limits.MaxDepth;
//   - a job folder with more than Limits.MaxEntries/2 entries, as a whole (no job needs as
//     many; it is set aside and removed in later sweeps).
//
// Regular files with one name and directories stay: they are the worker's scratch work and
// may be in use. Each directory is read 1024 entries at a time, with one open descriptor per
// level, so neither the number of entries nor the depth makes a sweep run out of memory or
// descriptors; a sweep that reaches Limits.MaxEntries stops, and the next one starts again
// at the job folder it stopped in. The web process runs one at every retention sweep, the
// worker one when it starts. A WorkerSweeper is not safe for concurrent use.
type WorkerSweeper struct {
	Limits SweepLimits
	next   string // the job folder the last sweep stopped in; "" for the start
}

// Sweep runs one bounded sweep of worker/ through intake, an os.Root of the intake root. A
// missing worker/ is not an error.
func (w *WorkerSweeper) Sweep(intake *os.Root) (SweepResult, error) {
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
	done, next, err := s.run(w.next)
	w.next = next
	return SweepResult{Removed: s.removed, Sample: s.sample, Done: done}, err
}

// SpillWorker sets aside rel, a path in the worker's folder of job id (or the folder itself
// when rel is ""), for removal: one rename to a SpillPrefix name in worker/, which a
// WorkerSweeper removes in bounded steps. Unlike a RemoveAll, it takes the same time
// however many entries or levels the worker put there. A missing one is not an error.
func SpillWorker(intakeRoot, id, rel string) error {
	if !ValidID(id) {
		panic("jobdir: malformed job id")
	}
	src := path.Join(Worker, id)
	if rel != "" {
		if _, err := elements(rel); err != nil {
			return err
		}
		src = path.Join(src, rel)
	}
	intake, err := os.OpenRoot(intakeRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = intake.Close() }()
	if _, err := intake.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	// os.Root renames the name itself, never what a link there points to.
	name := spillName()
	if err := intake.Rename(src, path.Join(Worker, name)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	// What a job leaves there is small: removed now, up to a bound; anything bigger is
	// left to the sweeps.
	top, err := openDirs(intake, []string{Worker}, false)
	if err != nil {
		return nil
	}
	defer func() { _ = top.Close() }()
	s := &sweep{limits: SweepLimits{MaxEntries: spillNow, MaxDepth: spillDepth}, budget: spillNow, top: top}
	s.removeTop(name)
	return nil
}

// What SpillWorker removes at once, at most: entries, and levels of directories.
const (
	spillNow   = 10_000
	spillDepth = 48
)

func spillName() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return SpillPrefix + hex.EncodeToString(b)
}

// IsSpill reports whether name is one SpillWorker gave.
func IsSpill(name string) bool { return strings.HasPrefix(name, SpillPrefix) }

// sweep is one run of WorkerSweeper.Sweep.
type sweep struct {
	limits  SweepLimits
	budget  int      // entries still to look at
	top     *os.File // worker/
	removed int
	sample  []string
}

func (s *sweep) note(rel string) {
	s.removed++
	if len(s.sample) < 20 {
		s.sample = append(s.sample, rel)
	}
}
