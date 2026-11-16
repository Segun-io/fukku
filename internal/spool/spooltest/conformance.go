package spooltest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/spool"
)

// Backend is a spool.Queue plus the worker's side, as a test needs it.
type Backend interface {
	spool.Queue
	// Tasks returns the submitted tasks that have not finished, oldest Created first.
	Tasks() []spool.Task
	// Complete finishes a waiting task with status ok and output as JSON.
	Complete(id string, output any)
	// Fail finishes a waiting task with status error.
	Fail(id, code, message string)
}

var _ Backend = (*Queue)(nil)

// Job is a well-formed job id for tests.
const Job = "0123456789abcdef0123456789abcdef"

// Run is the conformance suite of spool.Queue. open returns a new, empty backend.
func Run(t *testing.T, open func(t *testing.T) Backend) {
	ctx := context.Background()
	task := func(id string, created time.Time) spool.Task {
		return spool.Task{ID: id, Kind: spool.KindProbe, Job: Job, Created: created, Args: json.RawMessage(`{"paths":["files/a.mkv"]}`)}
	}
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("submit twice gives one task", func(t *testing.T) {
		q := open(t)
		for range 2 {
			if err := q.Submit(ctx, task("a-1", t0)); err != nil {
				t.Fatal(err)
			}
		}
		got := q.Tasks()
		if len(got) != 1 || got[0].ID != "a-1" || got[0].V != spool.Version || got[0].Kind != spool.KindProbe ||
			got[0].Job != Job || !got[0].Created.Equal(t0) || string(got[0].Args) != `{"paths":["files/a.mkv"]}` {
			t.Fatalf("tasks %+v", got)
		}
	})

	t.Run("submit fills in created", func(t *testing.T) {
		q := open(t)
		if err := q.Submit(ctx, task("a-1", time.Time{})); err != nil {
			t.Fatal(err)
		}
		if got := q.Tasks(); len(got) != 1 || got[0].Created.IsZero() {
			t.Fatalf("tasks %+v", got)
		}
	})

	t.Run("submit after the result gives none", func(t *testing.T) {
		q := open(t)
		if err := q.Submit(ctx, task("a-1", t0)); err != nil {
			t.Fatal(err)
		}
		q.Complete("a-1", map[string]int{"n": 1})
		if err := q.Submit(ctx, task("a-1", t0)); err != nil {
			t.Fatal(err)
		}
		if got := q.Tasks(); len(got) != 0 {
			t.Fatalf("tasks %+v", got)
		}
		r, ok, err := q.Result(ctx, "a-1")
		if err != nil || !ok {
			t.Fatalf("result %v %v", ok, err)
		}
		if r.V != spool.Version || r.ID != "a-1" || r.Kind != spool.KindProbe || r.Job != Job ||
			r.Status != spool.StatusOK || r.Error != nil || string(r.Output) != `{"n":1}` {
			t.Fatalf("result %+v", r)
		}
	})

	t.Run("submit after ack is a new task", func(t *testing.T) {
		q := open(t)
		if err := q.Submit(ctx, task("a-1", t0)); err != nil {
			t.Fatal(err)
		}
		q.Complete("a-1", nil)
		if err := q.Ack(ctx, "a-1"); err != nil {
			t.Fatal(err)
		}
		if err := q.Submit(ctx, task("a-1", t0)); err != nil {
			t.Fatal(err)
		}
		if got := q.Tasks(); len(got) != 1 {
			t.Fatalf("tasks %+v", got)
		}
	})

	t.Run("tasks are oldest first", func(t *testing.T) {
		q := open(t)
		for i, id := range []string{"c", "a", "b"} {
			if err := q.Submit(ctx, task(id, t0.Add(time.Duration(-i)*time.Minute))); err != nil {
				t.Fatal(err)
			}
		}
		var ids []string
		for _, tk := range q.Tasks() {
			ids = append(ids, tk.ID)
		}
		if len(ids) != 3 || ids[0] != "b" || ids[1] != "a" || ids[2] != "c" {
			t.Fatalf("order %v", ids)
		}
	})

	t.Run("no result yet", func(t *testing.T) {
		q := open(t)
		if err := q.Submit(ctx, task("a-1", t0)); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a-1", "unknown"} {
			if _, ok, err := q.Result(ctx, id); ok || err != nil {
				t.Fatalf("%s: ok %v, err %v", id, ok, err)
			}
		}
	})

	t.Run("fail", func(t *testing.T) {
		q := open(t)
		if err := q.Submit(ctx, task("a-1", t0)); err != nil {
			t.Fatal(err)
		}
		q.Fail("a-1", spool.CodeTimeout, "too slow")
		r, ok, err := q.Result(ctx, "a-1")
		if err != nil || !ok {
			t.Fatalf("result %v %v", ok, err)
		}
		if r.Status != spool.StatusError || r.Error == nil || r.Error.Code != spool.CodeTimeout ||
			r.Error.Message != "too slow" || r.Output != nil {
			t.Fatalf("result %+v", r)
		}
	})

	t.Run("ack", func(t *testing.T) {
		q := open(t)
		if err := q.Submit(ctx, task("a-1", t0)); err != nil {
			t.Fatal(err)
		}
		q.Complete("a-1", nil)
		for range 2 {
			if err := q.Ack(ctx, "a-1"); err != nil {
				t.Fatal(err)
			}
		}
		if _, ok, err := q.Result(ctx, "a-1"); ok || err != nil {
			t.Fatalf("after ack: ok %v, err %v", ok, err)
		}
		if err := q.Ack(ctx, "unknown"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cancel a waiting task", func(t *testing.T) {
		q := open(t)
		for _, id := range []string{"a-1", "a-2"} {
			if err := q.Submit(ctx, task(id, t0)); err != nil {
				t.Fatal(err)
			}
		}
		if err := q.Cancel(ctx, "a-1"); err != nil {
			t.Fatal(err)
		}
		if got := q.Tasks(); len(got) != 1 || got[0].ID != "a-2" {
			t.Fatalf("tasks %+v", got)
		}
		if _, ok, err := q.Result(ctx, "a-1"); ok || err != nil {
			t.Fatalf("result: ok %v, err %v", ok, err)
		}
		if err := q.Cancel(ctx, "unknown"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cancel after the result", func(t *testing.T) {
		q := open(t)
		if err := q.Submit(ctx, task("a-1", t0)); err != nil {
			t.Fatal(err)
		}
		q.Complete("a-1", nil)
		if err := q.Cancel(ctx, "a-1"); err != nil {
			t.Fatal(err)
		}
		if r, ok, err := q.Result(ctx, "a-1"); !ok || err != nil || r.Status != spool.StatusOK {
			t.Fatalf("result %+v, ok %v, err %v", r, ok, err)
		}
		if err := q.Ack(ctx, "a-1"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("malformed input is refused", func(t *testing.T) {
		q := open(t)
		bad := map[string]spool.Task{
			"empty id":      {Kind: spool.KindProbe, Job: Job},
			"upper case id": {ID: "A", Kind: spool.KindProbe, Job: Job},
			"path in id":    {ID: "../x", Kind: spool.KindProbe, Job: Job},
			"long id":       {ID: strings.Repeat("a", 97), Kind: spool.KindProbe, Job: Job},
			"no kind":       {ID: "a", Job: Job},
			"bad job":       {ID: "a", Kind: spool.KindProbe, Job: "../other"},
			"other version": {V: spool.Version + 1, ID: "a", Kind: spool.KindProbe, Job: Job},
		}
		for name, tk := range bad {
			if err := q.Submit(ctx, tk); err == nil {
				t.Errorf("%s: Submit accepted %+v", name, tk)
			}
		}
		if got := q.Tasks(); len(got) != 0 {
			t.Fatalf("tasks %+v", got)
		}
		longest := strings.Repeat("a", 96)
		if err := q.Submit(ctx, task(longest, t0)); err != nil {
			t.Fatalf("a 96-character id: %v", err)
		}
		for _, id := range []string{"", "../x", "a/b", "A", "a.json"} {
			if _, _, err := q.Result(ctx, id); err == nil {
				t.Errorf("Result(%q) accepted", id)
			}
			if err := q.Ack(ctx, id); err == nil {
				t.Errorf("Ack(%q) accepted", id)
			}
			if err := q.Cancel(ctx, id); err == nil {
				t.Errorf("Cancel(%q) accepted", id)
			}
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		q := open(t)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if err := q.Submit(cctx, task("a-1", t0)); err == nil {
			t.Fatal("Submit with a cancelled context succeeded")
		}
		if got := q.Tasks(); len(got) != 0 {
			t.Fatalf("tasks %+v", got)
		}
	})
}
