package runner

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/extract"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

var errDied = errors.New("the runner died")

// fuse counts the calls into the store and the queue. After `left` calls every call fails
// without doing anything: from the store's and the spool's point of view the runner died
// right after call number N.
type fuse struct {
	mu    sync.Mutex
	calls int
	left  int // -1: never blows
}

func (f *fuse) tick() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.left == 0 {
		return errDied
	}
	f.calls++
	if f.left > 0 {
		f.left--
	}
	return nil
}

func (f *fuse) blown() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.left == 0
}

type fusedStore struct {
	store.Store
	f *fuse
}

func (s fusedStore) CreateJob(ctx context.Context, owner string) (store.Job, error) {
	if err := s.f.tick(); err != nil {
		return store.Job{}, err
	}
	return s.Store.CreateJob(ctx, owner)
}

func (s fusedStore) Job(ctx context.Context, id string) (store.Job, error) {
	if err := s.f.tick(); err != nil {
		return store.Job{}, err
	}
	return s.Store.Job(ctx, id)
}

func (s fusedStore) Jobs(ctx context.Context, flt store.JobFilter) ([]store.Job, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.Store.Jobs(ctx, flt)
}

func (s fusedStore) Transition(ctx context.Context, id string, from, to store.State, c store.Change) (store.Job, error) {
	if err := s.f.tick(); err != nil {
		return store.Job{}, err
	}
	return s.Store.Transition(ctx, id, from, to, c)
}

func (s fusedStore) History(ctx context.Context, id string) ([]store.Event, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.Store.History(ctx, id)
}

func (s fusedStore) DeleteJob(ctx context.Context, id string) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.DeleteJob(ctx, id)
}

func (s fusedStore) AddFile(ctx context.Context, fl store.File) (store.File, error) {
	if err := s.f.tick(); err != nil {
		return store.File{}, err
	}
	return s.Store.AddFile(ctx, fl)
}

func (s fusedStore) UpdateFile(ctx context.Context, fl store.File) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.UpdateFile(ctx, fl)
}

func (s fusedStore) DeleteFile(ctx context.Context, id int64) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.DeleteFile(ctx, id)
}

func (s fusedStore) Files(ctx context.Context, id string) ([]store.File, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.Store.Files(ctx, id)
}

func (s fusedStore) PutUpload(ctx context.Context, u store.Upload) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.PutUpload(ctx, u)
}

func (s fusedStore) Upload(ctx context.Context, id string) (store.Upload, error) {
	if err := s.f.tick(); err != nil {
		return store.Upload{}, err
	}
	return s.Store.Upload(ctx, id)
}

func (s fusedStore) Uploads(ctx context.Context, owner, jobID string) ([]store.Upload, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.Store.Uploads(ctx, owner, jobID)
}

func (s fusedStore) DeleteUpload(ctx context.Context, id string) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.DeleteUpload(ctx, id)
}

func (s fusedStore) Audit(ctx context.Context, e store.AuditEntry) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.Audit(ctx, e)
}

func (s fusedStore) AuditLog(ctx context.Context, id string) ([]store.AuditEntry, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.Store.AuditLog(ctx, id)
}

func (s fusedStore) Ping(ctx context.Context) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.Ping(ctx)
}

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

// crashScenario is one complete run. setup builds the job and the worker's behaviour; act
// is what the outside world does after each round (the job API, the worker coming back).
type crashScenario struct {
	name  string
	setup func(e *env) store.Job
	act   func(e *env, j store.Job)
	final []store.State
}

func crashScenarios() []crashScenario {
	season := func(e *env) {
		e.w.extracts = map[string]extractPlan{
			"archives/Show S01.zip": {
				files:   map[string]string{"Show - S01E01.mkv": "e1", "Show - S01E02.mkv": "e2", "Show - S01E01.en.srt": "s1"},
				skipped: []extract.Skipped{{Name: "../x.mkv", Reason: extract.ReasonTraversal}},
			},
			"archives/bomb.zip": {refused: extract.RefusedRatio},
		}
		e.w.refuse = map[string]string{
			"files/broken.mkv":                 media.RefusalNoVideoStream,
			"files/Show S01/Show - S01E02.mkv": media.RefusalNoDuration,
		}
	}
	files := []upload{archive("Show S01.zip"), archive("bomb.zip"), video("Film.mkv"), video("broken.mkv"), subtitle("Film.srt")}
	return []crashScenario{
		{
			name:  "probed",
			setup: func(e *env) store.Job { season(e); return e.newJob(files...) },
			final: []store.State{store.Probed},
		},
		{
			// The first probe fails; the job API retries the job a round after it failed.
			name: "retried",
			setup: func(e *env) store.Job {
				season(e)
				e.w.fail = func(t spool.Task) *spool.Error {
					if strings.HasSuffix(t.ID, "-1-p") {
						return &spool.Error{Code: spool.CodeInternal, Message: "ffprobe crashed"}
					}
					return nil
				}
				return e.newJob(files...)
			},
			act: func(e *env, j store.Job) {
				if j = e.job(j.ID); j.State == store.Failed && j.Attempt == 1 {
					if e.rounds++; e.rounds < 2 {
						return
					}
					if _, err := e.st.Transition(e.ctx, j.ID, store.Failed, j.Resume, store.Change{Actor: "user1"}); err != nil {
						e.t.Fatal(err)
					}
				}
			},
			final: []store.State{store.Probed},
		},
		{
			// The worker is gone at first and comes back once the job has stalled.
			name: "stalled",
			setup: func(e *env) store.Job {
				season(e)
				e.beat(time.Hour)
				return e.newJob(files...)
			},
			act: func(e *env, j store.Job) {
				if e.job(j.ID).State == store.Stalled {
					e.beat(0)
				}
			},
			final: []store.State{store.Probed},
		},
		{
			// The season zip finds too little free space at first; space is freed once the
			// job has stalled.
			name: "no room",
			setup: func(e *env) store.Job {
				season(e)
				e.w.noRoom = map[string]int{"archives/Show S01.zip": 1}
				e.free = 0
				return e.newJob(files...)
			},
			act: func(e *env, j store.Job) {
				if e.job(j.ID).State == store.Stalled {
					e.free = math.MaxInt64
				}
			},
			final: []store.State{store.Probed},
		},
		{
			// The job API cancels the job while its probe runs.
			name: "cancelled",
			setup: func(e *env) store.Job {
				season(e)
				e.w.hold = func(t spool.Task) bool { return t.Kind == spool.KindProbe }
				j := e.newJob(files...)
				e.addUnfinishedUpload(j)
				return j
			},
			act: func(e *env, j store.Job) {
				if e.job(j.ID).State == store.Probing && len(e.q.Tasks()) > 0 && e.q.Tasks()[0].Kind == spool.KindProbe {
					if _, err := e.st.Transition(e.ctx, j.ID, store.Probing, store.Cancelled, store.Change{Actor: "user1"}); err != nil {
						e.t.Fatal(err)
					}
				}
			},
			final: []store.State{store.Cancelled},
		},
	}
}

// round is one pass, the worker, and the outside world.
func (e *env) round(sc crashScenario, j store.Job) {
	e.r.pass(e.ctx)
	e.w.work()
	if sc.act != nil {
		sc.act(e, j)
	}
}

// settle runs rounds until the job is in a final state and a round changes nothing.
func (e *env) settle(sc crashScenario, j store.Job) string {
	e.t.Helper()
	prev := ""
	for range 60 {
		e.round(sc, j)
		s := e.snapshot(j.ID)
		if s == prev && slices.Contains(sc.final, e.job(j.ID).State) {
			return s
		}
		prev = s
	}
	e.t.Fatalf("%s did not settle:\n%s", sc.name, prev)
	return ""
}

// TestCrashAtEveryCall kills the runner after call number N into the store or the queue,
// for every N of a complete run, and lets a new runner on the same store and spool finish.
// It always ends in the same state as a run without a crash: the same files, each once,
// the same files on disk, and nothing left in the spool.
func TestCrashAtEveryCall(t *testing.T) {
	diedIn := map[store.State]bool{}
	for _, sc := range crashScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			// The reference run, counting calls.
			ref := newEnv(t)
			f := &fuse{left: -1}
			ref.rst, ref.rq = fusedStore{ref.st, f}, fusedQueue{ref.q, f}
			ref.restart()
			rj := sc.setup(ref)
			want := ref.settle(sc, rj)
			total := f.calls
			if total < 20 {
				t.Fatalf("only %d calls in a complete run", total)
			}
			t.Logf("%d calls; final state:\n%s", total, want)

			for n := range total {
				e := newEnv(t)
				f := &fuse{left: n}
				e.rst, e.rq = fusedStore{e.st, f}, fusedQueue{e.q, f}
				e.restart()
				j := sc.setup(e)
				for range 100 {
					if f.blown() {
						break
					}
					e.round(sc, j)
				}
				if !f.blown() {
					t.Fatalf("N=%d: the runner did not reach call %d", n, n+1)
				}
				diedIn[e.job(j.ID).State] = true

				// A new runner, on the same store and spool.
				e.rst, e.rq = e.st, e.q
				e.restart()
				got := e.settle(sc, j)
				if got != want {
					t.Fatalf("died after call %d of %d; final state differs:\n--- got\n%s--- want\n%s", n, total, got, want)
				}
				if strings.Contains(got, "spool ") {
					t.Fatalf("N=%d: left in the spool:\n%s", n, got)
				}
			}
		})
	}
	for _, s := range []store.State{store.Uploaded, store.Probing, store.Stalled, store.Failed, store.Cancelled, store.Probed} {
		if !diedIn[s] {
			t.Errorf("no crash while the job was %s (crashed in %v)", s, diedIn)
		}
	}
}
