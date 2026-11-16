package runner

import (
	"testing"

	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/spool/spooltest"
	"github.com/segun-io/fukku/internal/store"
)

// Discarding a job reaches its preview task (#53), which belongs to no attempt: a waiting
// one is cancelled and a result is acknowledged.
func TestDiscardPreview(t *testing.T) {
	ctx := t.Context()
	job := store.Job{ID: "0123456789abcdef0123456789abcdef", Attempt: 2}
	id := PreviewID(job.ID)
	if id != job.ID+"-v" || !spool.ValidID(id) {
		t.Fatalf("PreviewID %q", id)
	}
	q := &spooltest.Queue{}
	task := spool.Task{ID: id, Kind: spool.KindPreview, Job: job.ID}
	if err := q.Submit(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := Discard(ctx, q, job, nil); err != nil {
		t.Fatal(err)
	}
	if tasks := q.Tasks(); len(tasks) != 0 {
		t.Errorf("waiting after Discard: %+v", tasks)
	}
	if err := q.Submit(ctx, task); err != nil {
		t.Fatal(err)
	}
	q.Complete(id, nil)
	if err := Discard(ctx, q, job, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := q.Result(ctx, id); ok {
		t.Error("result left after Discard")
	}
}
