package flows

import (
	"context"
	"testing"

	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

func (s fusedStore) SetTarget(ctx context.Context, id string, t store.Target) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.SetTarget(ctx, id, t)
}

// fusedQueue is the spool as a process that dies after call N sees it.
type fusedQueue struct {
	spool.Queue
	f *fuse
}

func (q fusedQueue) Submit(ctx context.Context, t spool.Task) error {
	if err := q.f.tick(); err != nil {
		return err
	}
	return q.Queue.Submit(ctx, t)
}

func (q fusedQueue) Result(ctx context.Context, id string) (spool.Result, bool, error) {
	if err := q.f.tick(); err != nil {
		return spool.Result{}, false, err
	}
	return q.Queue.Result(ctx, id)
}

func (q fusedQueue) Ack(ctx context.Context, id string) error {
	if err := q.f.tick(); err != nil {
		return err
	}
	return q.Queue.Ack(ctx, id)
}

func (q fusedQueue) Cancel(ctx context.Context, id string) error {
	if err := q.f.tick(); err != nil {
		return err
	}
	return q.Queue.Cancel(ctx, id)
}

// The flow 2 matched step interrupted after any call into the store or the spool, then run
// again by a new process (with the worker answering whatever task is waiting), ends in
// awaiting-confirmation with the probe stored, one task submitted, and nothing left in the
// spool. It runs for a first attempt and for a retry, which also drains the first attempt.
func TestFlow2StepCrash(t *testing.T) {
	for _, attempt := range []int{1, 2} {
		for n := 0; ; n++ {
			x := newFlow2Env(t)
			j := x.flow2Job(filmMinutes)
			if attempt == 2 {
				// Attempt 1 failed with a result still in the spool; the user retried.
				stale := spool.Task{ID: runner.LibraryProbeID(j.ID, 1), Kind: spool.KindLibraryProbe, Job: j.ID}
				if err := x.q.Submit(x.ctx, stale); err != nil {
					t.Fatal(err)
				}
				x.q.Fail(stale.ID, spool.CodeInternal, "boom")
				var err error
				if j, err = x.st.Transition(x.ctx, j.ID, store.Matched, store.Failed, store.Change{Actor: "system", ErrorCode: "task_failed"}); err != nil {
					t.Fatal(err)
				}
				if j, err = x.st.Transition(x.ctx, j.ID, store.Failed, store.Matched, store.Change{Actor: "user1"}); err != nil {
					t.Fatal(err)
				}
			}
			path := x.record(j).Library.Path
			answer := func() int {
				n := 0
				for _, task := range x.q.Tasks() {
					x.q.Complete(task.ID, media.LibraryProbeOutput{Path: path, OK: true, Media: libProbe(filmMinutes)})
					n++
				}
				return n
			}

			f := &fuse{left: n}
			dying := NewProposals(x.cfg, nil, fusedStore{x.st, f}, x.movies, x.series, nil)
			dying.SetQueue(fusedQueue{x.q, f})
			dying.SetWorkerDown(func() (string, bool) { return "", false })
			var err error
			submitted := 0
			for i := 0; i < 4 && err == nil && x.jobNow(j).State == store.Matched; i++ {
				err = dying.Matched(x.ctx, x.jobNow(j))
				submitted += answer()
			}
			finished := !f.blown()
			if finished && err != nil {
				t.Fatalf("attempt %d: an uninterrupted run failed: %v", attempt, err)
			}
			if s := x.jobNow(j).State; s != store.Matched && s != store.AwaitingConfirmation {
				t.Fatalf("attempt %d, after %d calls: job %s", attempt, n, s)
			}
			// A new process takes over.
			for i := 0; i < 4 && x.jobNow(j).State == store.Matched; i++ {
				if err := x.p.Matched(x.ctx, x.jobNow(j)); err != nil {
					t.Fatalf("attempt %d, after %d calls: the next run: %v", attempt, n, err)
				}
				submitted += answer()
			}
			j = x.jobNow(j)
			if j.State != store.AwaitingConfirmation {
				t.Fatalf("attempt %d, after %d calls: job %s (%s: %s)", attempt, n, j.State, j.ErrorCode, j.ErrorText)
			}
			if got := x.record(j).Library.Probe; got == nil || got.DurationMs != filmMinutes {
				t.Fatalf("attempt %d, after %d calls: probe %+v", attempt, n, got)
			}
			if submitted != 1 {
				t.Fatalf("attempt %d, after %d calls: %d tasks ran, want 1", attempt, n, submitted)
			}
			x.noLeftovers(j)
			if finished {
				if n < 5 {
					t.Fatalf("the step finished after %d calls: the fuse does not see it", n)
				}
				break
			}
		}
	}
}
