package jobdir

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"time"
)

// Spill is the folder of worker/ where what is to be removed is set aside: a job's folder
// that is no longer needed or a part of one (SpillWorker), anything in worker/ that is no
// job's folder, and directories deeper than any the worker makes. A WorkerSweeper empties it
// first at every sweep, a bounded number of entries at a time. Not a job id, so never a job.
const Spill = ".spill"

// SweepLimits bound one sweep of worker/ (WorkerSweeper.Sweep).
type SweepLimits struct {
	// MaxEntries is how many entries one sweep looks at; the next sweep carries on. Half is
	// for emptying Spill, the rest for worker/ itself.
	MaxEntries int
	// MaxDepth is how many levels of directories a job's folder may have below worker/
	// (worker/<id> is level 1). A directory deeper than that is no folder the worker makes
	// (its deepest are extraction paths of extract.maxPathSegments elements): it is set
	// aside and removed.
	MaxDepth int
	// MaxDuration is how long one sweep may take (0: no bound); the next one carries on.
	MaxDuration time.Duration
}

// SweepResult is what one sweep did.
type SweepResult struct {
	Removed int      // entries removed, or set aside to be removed
	Sample  []string // the first of them, relative to worker/, for the log
	Done    bool     // the sweep got through all of worker/; else the next one carries on
}

// WorkerSweeper keeps worker/ to what the worker makes for a job, a bounded amount of work
// at a time. It never follows a link, and:
//
//   - empties Spill first, on half of the entry budget, so that nothing else in worker/ can
//     keep what was set aside there from being removed;
//   - sets aside every entry of worker/ that is not a directory named by a job id, and every
//     job folder Orphan names;
//   - in the job folders, removes every regular file with more than one name (a hard link:
//     the worker makes none, and one there may be a name of a sealed, imported or placed
//     file, #114) and everything that is neither a regular file nor a directory, and sets
//     aside every directory deeper than Limits.MaxDepth;
//   - sets aside, as a whole, a job folder with more than Limits.MaxEntries/2 entries (no job
//     needs as many: config validation keeps that above twice extract.maxEntries).
//
// Regular files with one name and directories stay: they are the worker's scratch work and
// may be in use. A folder the worker made unreadable (mode 000) is made readable to its owner
// again (the two processes share a user id). Each directory is read 1024 entries at a time
// with one open descriptor per level, every entry looked at (an empty job folder too) counts
// against Limits.MaxEntries, and a sweep also stops at Limits.MaxDuration. Job folders are
// swept in name order, at most 1024 per sweep, and the next sweep starts after the last one
// done (or the next name, if that one is gone), so every folder is reached over successive
// sweeps whatever else is there. The web process runs one at every retention sweep, the
// worker one when it starts. A WorkerSweeper is not safe for concurrent use.
type WorkerSweeper struct {
	Limits SweepLimits
	// Orphan, when set, reports whether the job folder id, last modified at modified,
	// belongs to no job any more and may go (the web process's retention). It is not called
	// for every folder in every sweep, only for those in the sweep's window.
	Orphan func(id string, modified time.Time) bool
	next   string // job folders after this name are swept next; "" for the start
}

// Sweep runs one bounded sweep of worker/ through intake, an os.Root of the intake root. A
// missing worker/ is not an error.
func (w *WorkerSweeper) Sweep(intake *os.Root) (SweepResult, error) {
	return w.sweep(intake)
}

// SpillWorker sets aside rel, a path in the worker's folder of job id (or the folder itself
// when rel is ""), for removal: one rename into Spill, which a WorkerSweeper empties in
// bounded steps; what is small is removed at once. worker/<id> and every folder on the way
// to rel are opened one element at a time without following a link (a link there is
// refused, ErrLink), and the rename is of the name in the folder so opened: nothing outside
// worker/ is ever moved. When rel is "" and worker/<id> is a link, the link itself is
// removed. A missing one is not an error.
func SpillWorker(intakeRoot, id, rel string) error {
	if !ValidID(id) {
		panic("jobdir: malformed job id")
	}
	if rel != "" {
		if _, err := elements(rel); err != nil {
			return err
		}
	}
	return spillWorker(intakeRoot, id, rel)
}

// What SpillWorker removes at once, at most: entries, and levels of directories.
const (
	spillNow   = 10_000
	spillDepth = 48
)

func spillName() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sweep is one run of WorkerSweeper.Sweep.
type sweep struct {
	limits   SweepLimits
	budget   int       // entries still to look at
	deadline time.Time // zero: none
	ticks    int
	top      *os.File // worker/
	spill    *os.File // worker/.spill
	removed  int
	sample   []string
	asides   int // directories moved into Spill: the sweep is not done until they are gone
}

func (s *sweep) note(rel string) {
	s.removed++
	if len(s.sample) < 20 {
		s.sample = append(s.sample, rel)
	}
}

// take counts one entry against the budget, and looks at the clock now and then; false when
// the sweep must stop.
func (s *sweep) take() bool {
	if s.budget <= 0 {
		return false
	}
	s.budget--
	s.ticks++
	if !s.deadline.IsZero() && s.ticks%256 == 0 && time.Now().After(s.deadline) {
		s.budget = 0
		return false
	}
	return true
}
