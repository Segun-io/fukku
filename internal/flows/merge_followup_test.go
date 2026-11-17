package flows

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/merge"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

// An empty refusal code is not a known refusal: it is transient, so it fails the job.
func TestMergeEmptyRefusalFails(t *testing.T) {
	x := newMergeEnv(t)
	j := x.confirmMerge(x.readyJob(), nil)
	empty := func(o *merge.Output) { *o = merge.Output{Detail: "no code"} }
	if j = x.run(x.eng, j, empty); j.State != store.Failed || j.ErrorCode != CodeMergeFailed {
		t.Fatalf("job %s %s", j.State, j.ErrorCode)
	}
	if _, ok := mergeRefusalOf(x.tgt(j)); ok {
		t.Error("an empty refusal was sent back to the confirmation")
	}
}

// A job that went back after its retention date had nearly passed is not cancelled at once:
// its date is the later of the old one and now plus upload.jobRetention.
func TestMergeRefusalExtendsRetention(t *testing.T) {
	x := newMergeEnv(t)
	j := x.confirmMerge(x.readyJob(), nil)
	old := x.clock.now().Add(time.Hour)
	// The importing job has a date that is about to pass.
	if _, err := x.st.Transition(x.ctx, j.ID, store.Importing, store.Stalled, store.Change{ErrorCode: "x", ExpiresAt: &old}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.st.Transition(x.ctx, j.ID, store.Stalled, store.Importing, store.Change{}); err != nil {
		t.Fatal(err)
	}
	refuse := func(o *merge.Output) { *o = merge.Output{Refusal: merge.RefusalVerifyFailed} }
	j = x.run(x.eng, j, refuse)
	if j.State != store.AwaitingConfirmation {
		t.Fatalf("job %s", j.State)
	}
	want := x.clock.now().Add(x.cfg.Upload.JobRetention.D())
	if j.ExpiresAt.Before(want.Add(-3*time.Hour)) || !j.ExpiresAt.After(old) {
		t.Errorf("expires %s, want about %s (was %s)", j.ExpiresAt, want, old)
	}
}

// failingBack is a store whose BackToConfirmation refuses with ErrConflict while the job is
// still importing (it cannot happen today).
type failingBack struct{ store.Store }

func (failingBack) BackToConfirmation(context.Context, string, store.Target, json.RawMessage, store.AuditEntry, store.Change) (store.Job, error) {
	return store.Job{}, store.ErrConflict
}

// When the store refuses the move but the job is still importing the refused choice, the job
// fails instead of being left with no choice and no result.
func TestMergeRefusalConflictFailsTheJob(t *testing.T) {
	x := newMergeEnv(t)
	j := x.confirmMerge(x.readyJob(), nil)
	eng := x.process(failingBack{x.st}, x.movies, x.q)
	refuse := func(o *merge.Output) { *o = merge.Output{Refusal: merge.RefusalMuxFailed, Detail: "exit 2"} }
	if j = x.run(eng, j, refuse); j.State != store.Failed || j.ErrorCode != CodeMergeFailed {
		t.Fatalf("job %s %s (%s)", j.State, j.ErrorCode, j.ErrorText)
	}
	if v := x.importView(j); v.Merge == nil || v.Merge.Refusal != merge.RefusalMuxFailed {
		t.Errorf("merge view %+v", v.Merge)
	}
	if tasks := x.q.Tasks(); len(tasks) != 0 {
		t.Errorf("tasks left %+v", tasks)
	}
}

// A crash between the move back and the acknowledgement of the result leaves the task's
// result in the spool: the next confirmation clears that id and merges.
func TestMergeRefusalCrashBeforeTheAck(t *testing.T) {
	x := newMergeEnv(t)
	j := x.confirmMerge(x.readyJob(), nil)
	refuse := func(o *merge.Output) { *o = merge.Output{Refusal: merge.RefusalVerifyFailed, Detail: "x"} }
	if j = x.run(x.eng, j, refuse); j.State != store.AwaitingConfirmation {
		t.Fatalf("job %s", j.State)
	}
	// The result the ack never removed, and what the task left on disk.
	id := runner.MergeID(j.ID, j.Attempt)
	if err := x.q.Submit(x.ctx, spool.Task{ID: id, Kind: spool.KindMerge, Job: j.ID, Args: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	x.q.Complete(id, merge.Output{Refusal: merge.RefusalVerifyFailed, Detail: "x"})
	x.leave(j, "merged/1/half.mkv")
	x.preview(j, -600)
	ms := int64(-600)
	j = x.confirmMerge(j, &MergeRequest{OffsetMs: &ms})
	p := x.propose(j, j.Attempt)
	if j = x.run(x.eng, j, nil); j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if len(x.movies.Imports) != 1 || x.movies.Imports[0].Rows[0].Path != p {
		t.Fatalf("imports %+v", x.movies.Imports)
	}
	x.noMergeLeftovers(j)
}

// A second version confirmed after a refusal does not keep it.
func TestVersionConfirmationDropsTheMergeRefusal(t *testing.T) {
	x := newVersionEnv(t)
	j := x.versionJob(palMs, "spa")
	tg, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tg.Options, err = withOption(tg.Options, "mergeRefusal", MergeRefusal{Code: merge.RefusalVerifyFailed}); err != nil {
		t.Fatal(err)
	}
	if err := x.st.SetTarget(x.ctx, j.ID, tg); err != nil {
		t.Fatal(err)
	}
	if v := x.view(j); v == nil || v.MergeRefusal == nil {
		t.Fatalf("setup: view %+v", v)
	}
	if _, err := x.confirmVersion(j, "Latino"); err != nil {
		t.Fatal(err)
	}
	after, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mergeRefusalOf(after); ok {
		t.Error("the merge refusal outlived the version confirmation")
	}
}
