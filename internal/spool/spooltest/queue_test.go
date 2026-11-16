package spooltest

import (
	"context"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/spool"
)

func TestQueue(t *testing.T) {
	Run(t, func(*testing.T) Backend { return &Queue{} })
}

func TestQueueClock(t *testing.T) {
	at := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	q := &Queue{Now: func() time.Time { return at }}
	ctx := context.Background()
	if err := q.Submit(ctx, spool.Task{ID: "a", Kind: spool.KindExtract, Job: Job}); err != nil {
		t.Fatal(err)
	}
	if got := q.Tasks()[0].Created; !got.Equal(at) {
		t.Fatalf("created %v", got)
	}
	q.Fail("a", spool.CodeInternal, "boom")
	r, _, _ := q.Result(ctx, "a")
	if !r.Started.Equal(at) || !r.Finished.Equal(at) {
		t.Fatalf("result %+v", r)
	}
}

func TestFinishUnknownPanics(t *testing.T) {
	q := &Queue{}
	for name, f := range map[string]func(){
		"complete": func() { q.Complete("nope", nil) },
		"fail":     func() { q.Fail("nope", spool.CodeInternal, "") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s of an unknown task did not panic", name)
				}
			}()
			f()
		}()
	}
}
