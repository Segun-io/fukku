package runner

import (
	"testing"

	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/spool/spooltest"
	"github.com/segun-io/fukku/internal/store"
)

// Discarding a job reaches its preview tasks (#53) in every slot, which belong to no
// attempt: a waiting one is cancelled and a result is acknowledged.
func TestDiscardPreview(t *testing.T) {
	ctx := t.Context()
	job := store.Job{ID: "0123456789abcdef0123456789abcdef", Attempt: 2}
	if id := PreviewID(job.ID, PreviewSlots); id != job.ID+"-v16" || !spool.ValidID(id) {
		t.Fatalf("PreviewID %q", id)
	}
	q := &spooltest.Queue{}
	waiting := spool.Task{ID: PreviewID(job.ID, 1), Kind: spool.KindPreview, Job: job.ID}
	done := spool.Task{ID: PreviewID(job.ID, PreviewSlots), Kind: spool.KindPreview, Job: job.ID}
	for _, task := range []spool.Task{waiting, done} {
		if err := q.Submit(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	q.Complete(done.ID, nil)
	if err := Discard(ctx, q, job, nil); err != nil {
		t.Fatal(err)
	}
	if tasks := q.Tasks(); len(tasks) != 0 {
		t.Errorf("waiting after Discard: %+v", tasks)
	}
	if _, ok, _ := q.Result(ctx, done.ID); ok {
		t.Error("result left after Discard")
	}
}

// Only the job's end reaches the preview slots: a retry's drain of earlier attempts (run
// on every pass while probing) does not look at them.
func TestPreviewSlotsOnlyAtTheEnd(t *testing.T) {
	job := store.Job{ID: "0123456789abcdef0123456789abcdef", Attempt: 1}
	if ids := taskIDs(job, nil, 1, 0, false); len(ids) != 0 {
		t.Errorf("a retry's drain: %v", ids)
	}
	n := 0
	for _, id := range taskIDs(job, nil, 1, 1, true) {
		for slot := 1; slot <= PreviewSlots; slot++ {
			if id == PreviewID(job.ID, slot) {
				n++
			}
		}
	}
	if n != PreviewSlots {
		t.Errorf("the job's end reaches %d of %d slots", n, PreviewSlots)
	}
}
