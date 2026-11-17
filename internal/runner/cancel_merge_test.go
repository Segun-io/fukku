package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// A merge job cancelled before its import is submitted (#92): the runner stops the merge tasks
// of every attempt, removes merged/ and the partial file in work/ with the rest of the job's
// directory, and acknowledges a result the worker writes after that.
func TestCancelledMergeJob(t *testing.T) {
	e := newEnv(t)
	j := e.newJob(video("a.mkv"))
	j, err := storetest.Drive(e.ctx, e.st, j.ID, store.Importing)
	if err != nil {
		t.Fatal(err)
	}
	dir := jobdir.Dir(e.cfg.Paths.IntakeRoot, j.ID)
	for rel, data := range map[string]string{
		"merged/1/film.mkv":          "a merge of an earlier attempt",
		"merged/2/film.mkv":          "the merge's verified file",
		"work/.merge-1234.partial":   "the partial file of a running merge",
		"work/.merge-1234.partial.l": "its lock",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The merge task of attempt 2 is still running (the fake queue's tasks are unfinished
	// ones), the one of attempt 1 left a result.
	for _, a := range []int{1, 2} {
		if err := e.q.Submit(e.ctx, spool.Task{ID: MergeID(j.ID, a), Kind: spool.KindMerge, Job: j.ID}); err != nil {
			t.Fatal(err)
		}
	}
	e.q.Complete(MergeID(j.ID, 1), nil)
	// The job failed once and was retried: attempt 2, importing again.
	if _, err := e.st.Transition(e.ctx, j.ID, store.Importing, store.Failed, store.Change{Actor: "system", ErrorCode: "merge_failed"}); err != nil {
		t.Fatal(err)
	}
	if j, err = e.st.Transition(e.ctx, j.ID, store.Failed, store.Importing, store.Change{Actor: "user1"}); err != nil || j.Attempt != 2 {
		t.Fatalf("retry: %+v, %v", j, err)
	}

	// The job API's cancel of a job that has no import yet.
	if _, err := e.st.CancelUnsubmitted(e.ctx, j.ID, store.Importing, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	e.run(j.ID, store.Cancelled)
	if e.onDisk(j.ID) != nil {
		t.Errorf("job directory left: %v", e.onDisk(j.ID))
	}
	if l := e.leftovers(j.ID); len(l) != 0 {
		t.Errorf("left in the spool: %v", l)
	}

	// The worker's result for the merge comes in after all that: acknowledged by the sweep,
	// and nothing is written back (the job's directory is gone).
	if err := e.q.Submit(e.ctx, spool.Task{ID: MergeID(j.ID, 2), Kind: spool.KindMerge, Job: j.ID}); err != nil {
		t.Fatal(err)
	}
	e.q.Complete(MergeID(j.ID, 2), nil)
	if l := e.leftovers(j.ID); len(l) != 1 {
		t.Fatalf("leftovers %v", l)
	}
	e.clk.Advance(2 * sweepEvery)
	e.run(j.ID, store.Cancelled)
	if l := e.leftovers(j.ID); len(l) != 0 {
		t.Errorf("late result left: %v", l)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("job directory: %v", err)
	}
}
