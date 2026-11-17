package flows

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/store"
)

// cancelBefore is the web API's cancel of a merge job whose import is not submitted (#92).
func (x *mergeEnv) cancelBefore(j store.Job) error {
	x.t.Helper()
	_, err := x.st.CancelUnsubmitted(x.ctx, j.ID, x.jobNow(j).State, store.Change{Actor: "user1", Note: "cancelled before the import was submitted"})
	return err
}

// noImportSent checks that nothing was sent to Radarr and no import is recorded as submitted.
func (x *mergeEnv) noImportSent(j store.Job) {
	x.t.Helper()
	x.untouched()
	if imp, err := x.st.Import(x.ctx, j.ID); err == nil && !imp.SubmittedAt.IsZero() {
		x.t.Errorf("an import was recorded as submitted: %+v", imp)
	}
}

// A cancel while the merge task is waiting or running: the job is cancelled, the library and
// Radarr are untouched, and no pass of the engine, even one that began before the cancel,
// submits an import after it, however the merge's result comes in.
func TestCancelDuringTheMergeNeverImports(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result bool // the worker's result is there before the engine looks again
	}{{"task running", false}, {"result arrives after the cancel", true}} {
		t.Run(tc.name, func(t *testing.T) {
			x := newMergeEnv(t)
			j := x.readyJob()
			j = x.confirmMerge(j, nil)
			x.propose(j, 1)
			x.pass(j) // the merge task is submitted
			if tasks := x.q.Tasks(); len(tasks) != 1 || tasks[0].ID != runner.MergeID(j.ID, 1) {
				t.Fatalf("tasks %+v", tasks)
			}
			stale := x.jobNow(j) // a pass that began before the cancel
			if err := x.cancelBefore(j); err != nil {
				t.Fatalf("cancel: %v", err)
			}
			if tc.result {
				x.worker(nil)
			}
			// The passes of the engine that still see the job as importing change nothing.
			for range 5 {
				x.clock.advance(time.Hour)
				if err := x.eng.Importing(x.ctx, stale); err != nil {
					t.Fatalf("stale pass: %v", err)
				}
				x.appWorks()
			}
			if got := x.jobNow(j); got.State != store.Cancelled {
				t.Fatalf("job %s", got.State)
			}
			x.noImportSent(j)
			// The runner's cancelled step drains the tasks (runner tests); here the spool
			// holds at most what the drain removes, and nothing makes the job importing again.
			if err := runner.Discard(x.ctx, x.q, x.jobNow(j), nil); err != nil {
				t.Fatal(err)
			}
			x.noMergeLeftovers(j)
		})
	}
}

// The cancel and the write that submits the import are one race with two outcomes: the
// submission first makes the cancel refused, the cancel first makes the submission refused.
func TestCancelRacingTheSubmission(t *testing.T) {
	// Submission first: Radarr has the import, so the cancel is refused and the job goes on.
	x := newMergeEnv(t)
	j := x.readyJob()
	j = x.confirmMerge(j, nil)
	x.propose(j, 1)
	submitted := func() bool {
		imp, err := x.st.Import(x.ctx, j.ID)
		return err == nil && !imp.SubmittedAt.IsZero()
	}
	for range 10 {
		if submitted() {
			break
		}
		x.pass(j)
		x.worker(nil)
		x.clock.advance(time.Hour)
	}
	if !submitted() || len(x.movies.Imports) != 1 {
		t.Fatalf("the import was not submitted: %d import(s)", len(x.movies.Imports))
	}
	if err := x.cancelBefore(j); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cancel after the submission: %v, want ErrConflict", err)
	}
	if j = x.run(x.eng, j, nil); j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}

	// Cancel first: the submission is refused, nothing is sent, and the merged file of an
	// engine pass that was already running is not imported.
	y := newMergeEnv(t)
	k := y.readyJob()
	k = y.confirmMerge(k, nil)
	y.propose(k, 1)
	y.pass(k)
	if !y.worker(nil) { // the merge is done; the next pass takes its result in and submits
		t.Fatal("no merge task")
	}
	// The user's cancel lands after every check of the pass, just before it submits the
	// import (the engine's storage check is the last step before that).
	cancelled := false
	y.eng.checkStorage = func() error {
		if !cancelled {
			cancelled = true
			if err := y.cancelBefore(k); err != nil {
				t.Errorf("cancel: %v", err)
			}
		}
		return nil
	}
	for i := 0; i < 10 && !cancelled && y.jobNow(k).State == store.Importing; i++ {
		y.pass(k)
		y.clock.advance(time.Hour)
	}
	if !cancelled {
		t.Fatal("the pass did not reach the submission")
	}
	if got := y.jobNow(k); got.State != store.Cancelled {
		t.Fatalf("job %s", got.State)
	}
	y.noImportSent(k)
	if _, err := os.Stat(y.src(k, "files/Some Film (2001).es.mkv")); err != nil {
		t.Errorf("the upload before the runner removes the job's files: %v", err)
	}
}
