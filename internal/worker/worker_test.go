package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/spool"
)

func TestHeartbeat(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Worker.SpoolDir = dir
	cfg.Paths.IntakeRoot = t.TempDir()
	cfg.Worker.HeartbeatInterval = config.Duration(10 * time.Millisecond)

	w := New(cfg, slog.New(slog.DiscardHandler), nil)
	fixed := time.Unix(1_700_000_000, 0)
	w.now = func() time.Time { return fixed }

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	age, err := HeartbeatAge(dir, fixed.Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if age != 90*time.Second {
		t.Fatalf("age %v", age)
	}
}

func TestHeartbeatMissing(t *testing.T) {
	if _, err := HeartbeatAge(t.TempDir(), time.Now()); err == nil {
		t.Fatal("want an error for a missing heartbeat")
	}
}

// recorder keeps every log record, for the tests that count log lines.
type recorder struct {
	mu   *sync.Mutex
	recs *[]slog.Record
	with []slog.Attr
}

func newRecorder() recorder { return recorder{mu: &sync.Mutex{}, recs: &[]slog.Record{}} }

func (r recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r recorder) WithGroup(string) slog.Handler            { return r }

func (r recorder) WithAttrs(as []slog.Attr) slog.Handler {
	r.with = append(slices.Clip(r.with), as...)
	return r
}

func (r recorder) Handle(_ context.Context, rec slog.Record) error {
	rec = rec.Clone()
	rec.AddAttrs(r.with...)
	r.mu.Lock()
	defer r.mu.Unlock()
	*r.recs = append(*r.recs, rec)
	return nil
}

// attrs returns, for every record with the message msg, the value of key.
func (r recorder) attrs(msg, key string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, rec := range *r.recs {
		if rec.Message != msg {
			continue
		}
		rec.Attrs(func(a slog.Attr) bool {
			if a.Key == key {
				out = append(out, a.Value.String())
			}
			return true
		})
	}
	return out
}

type env struct {
	t      *testing.T
	cfg    config.Config
	spool  string
	intake string
	q      *spool.Dir
	logs   recorder
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, spool: filepath.Join(t.TempDir(), "spool"), intake: t.TempDir(), logs: newRecorder()}
	e.cfg = config.Defaults()
	e.cfg.Worker.SpoolDir = e.spool
	e.cfg.Paths.IntakeRoot = e.intake
	e.cfg.Worker.PollInterval = config.Duration(5 * time.Millisecond)
	q, err := spool.Open(e.spool)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	e.q = q
	return e
}

var jobCount atomic.Int64

// job creates a job directory and returns its id.
func (e *env) job() string {
	id := fmt.Sprintf("%032x", jobCount.Add(1))
	if err := os.MkdirAll(filepath.Join(jobdir.Dir(e.intake, id), jobdir.Files), 0o750); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) submit(id, kind, job string) {
	e.t.Helper()
	if err := e.q.Submit(context.Background(), spool.Task{ID: id, Kind: kind, Job: job, Args: json.RawMessage(`{}`)}); err != nil {
		e.t.Fatal(err)
	}
}

// writeTask writes a task file as is, bypassing Submit's checks.
func (e *env) writeTask(dir, name, body string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.spool, dir, name), []byte(body), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// start runs a worker until the returned stop is called (or the test ends).
func (e *env) start(handlers map[string]Handler) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- New(e.cfg, slog.New(e.logs), handlers).Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-errc:
				if err != nil {
					e.t.Error(err)
				}
			case <-time.After(10 * time.Second):
				e.t.Error("the worker did not stop")
			}
		})
	}
	e.t.Cleanup(stop)
	return stop
}

func (e *env) result(id string) spool.Result {
	e.t.Helper()
	var r spool.Result
	eventually(e.t, func() bool {
		var ok bool
		var err error
		r, ok, err = e.q.Result(context.Background(), id)
		if err != nil {
			e.t.Fatal(err)
		}
		return ok
	})
	return r
}

func (e *env) files(dir string) []string {
	e.t.Helper()
	entries, err := os.ReadDir(filepath.Join(e.spool, dir))
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, en := range entries {
		out = append(out, en.Name())
	}
	return out
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// quiet waits a few poll intervals, so a worker that would act has had the chance.
func quiet() { time.Sleep(60 * time.Millisecond) }

func wantError(t *testing.T, r spool.Result, code string) {
	t.Helper()
	if r.Status != spool.StatusError || r.Error == nil || r.Error.Code != code {
		t.Fatalf("result %+v, want error code %s", r, code)
	}
}

func TestRunsTask(t *testing.T) {
	e := newEnv(t)
	job := e.job()
	e.submit("t-1", spool.KindExtract, job)
	e.start(map[string]Handler{spool.KindExtract: func(_ context.Context, root, _ *os.Root, args json.RawMessage) (any, error) {
		if string(args) != `{}` {
			return nil, fmt.Errorf("args %s", args)
		}
		if err := root.WriteFile("files/out.txt", []byte("x"), 0o600); err != nil {
			return nil, err
		}
		return map[string]string{"wrote": "files/out.txt"}, nil
	}})
	r := e.result("t-1")
	if r.Status != spool.StatusOK || r.Error != nil || string(r.Output) != `{"wrote":"files/out.txt"}` ||
		r.Kind != spool.KindExtract || r.Job != job || r.Started.IsZero() || r.Finished.Before(r.Started) {
		t.Fatalf("result %+v", r)
	}
	if _, err := os.Stat(filepath.Join(jobdir.Dir(e.intake, job), "files", "out.txt")); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return len(e.files("running")) == 0 })
}

func TestOldestFirst(t *testing.T) {
	e := newEnv(t)
	job := e.job()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"c", "b", "a"} {
		err := e.q.Submit(context.Background(), spool.Task{ID: id, Kind: "k", Job: job, Created: t0.Add(-time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
	}
	e.start(map[string]Handler{"k": func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) {
		return nil, nil
	}})
	eventually(t, func() bool { return len(e.logs.attrs("task_finished", "task")) == 3 })
	finished := e.logs.attrs("task_finished", "task")
	if !slices.Equal(finished, []string{"a", "b", "c"}) {
		t.Fatalf("finished in order %v", finished)
	}
}

// TestCrashHelper is the worker process that TestCrashRunsAgain kills.
func TestCrashHelper(t *testing.T) {
	spoolDir := os.Getenv("FUKKU_TEST_CRASH_SPOOL")
	if spoolDir == "" {
		t.Skip("helper process of TestCrashRunsAgain")
	}
	cfg := config.Defaults()
	cfg.Worker.SpoolDir = spoolDir
	cfg.Paths.IntakeRoot = os.Getenv("FUKKU_TEST_CRASH_INTAKE")
	cfg.Worker.PollInterval = config.Duration(5 * time.Millisecond)
	err := New(cfg, slog.New(slog.DiscardHandler), map[string]Handler{spool.KindProbe: func(_ context.Context, root, _ *os.Root, _ json.RawMessage) (any, error) {
		if err := root.WriteFile("files/started", nil, 0o600); err != nil {
			return nil, err
		}
		time.Sleep(time.Hour) // until the test kills the process
		return "first", nil
	}}).Run(context.Background())
	t.Fatal("the helper worker returned", err)
}

// A worker killed in the middle of a task (a real crash: SIGKILL of the worker process) and
// a new worker on the same spool: the task runs again and exactly one result exists.
func TestCrashRunsAgain(t *testing.T) {
	e := newEnv(t)
	job := e.job()
	e.submit("t-1", spool.KindProbe, job)

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCrashHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "FUKKU_TEST_CRASH_SPOOL="+e.spool, "FUKKU_TEST_CRASH_INTAKE="+e.intake)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(jobdir.Dir(e.intake, job), "files", "started"))
		return err == nil
	})
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if got := e.files("running"); !slices.Equal(got, []string{"t-1.json"}) {
		t.Fatalf("running/ holds %v", got)
	}
	if got := e.files("results"); len(got) != 0 {
		t.Fatalf("results/ holds %v", got)
	}

	var calls atomic.Int32
	e.start(map[string]Handler{spool.KindProbe: func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) {
		calls.Add(1)
		return "second", nil
	}})
	r := e.result("t-1")
	if r.Status != spool.StatusOK || string(r.Output) != `"second"` {
		t.Fatalf("result %+v", r)
	}
	quiet()
	if n := calls.Load(); n != 1 {
		t.Fatalf("the new worker ran the task %d times", n)
	}
	if got := e.files("results"); !slices.Equal(got, []string{"t-1.json"}) {
		t.Fatalf("results/ holds %v", got)
	}
	eventually(t, func() bool { return len(e.files("running")) == 0 })
}

// While one worker holds the spool, a second one (another replica, or combined mode next
// to a worker) refuses to start, so a task never runs in two workers at once.
func TestSecondWorkerRefused(t *testing.T) {
	e := newEnv(t)
	job := e.job()
	e.submit("t-1", spool.KindProbe, job)
	started := make(chan struct{})
	release := make(chan struct{})
	stop := e.start(map[string]Handler{spool.KindProbe: func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) {
		close(started)
		<-release // a hung worker: its handler ignores the context
		return "first", nil
	}})
	<-started

	var calls atomic.Int32
	err := New(e.cfg, slog.New(e.logs), map[string]Handler{spool.KindProbe: func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) {
		calls.Add(1)
		return "second", nil
	}}).Run(context.Background())
	if !errors.Is(err, spool.ErrLocked) {
		t.Fatalf("second worker: %v, want ErrLocked", err)
	}
	close(release)
	if r := e.result("t-1"); string(r.Output) != `"first"` {
		t.Fatalf("result %+v", r)
	}
	if calls.Load() != 0 {
		t.Fatal("the second worker ran the task")
	}
	// Once the first worker has stopped, a new one can start.
	stop()
	e.start(nil)
	eventually(t, func() bool { return len(e.logs.attrs("worker_started", "spool")) == 2 })
}

// A worker that is stopped cancels its tasks, writes no result for them and runs them again
// at the next start.
func TestShutdownRunsAgain(t *testing.T) {
	e := newEnv(t)
	job := e.job()
	e.submit("t-1", spool.KindProbe, job)
	started := make(chan struct{})
	stop := e.start(map[string]Handler{spool.KindProbe: func(ctx context.Context, _, _ *os.Root, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	<-started
	stop()
	if got := e.files("results"); len(got) != 0 {
		t.Fatalf("results/ holds %v after a shutdown", got)
	}
	if got := e.files("running"); !slices.Equal(got, []string{"t-1.json"}) {
		t.Fatalf("running/ holds %v", got)
	}
	e.start(map[string]Handler{spool.KindProbe: func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) {
		return nil, nil
	}})
	if r := e.result("t-1"); r.Status != spool.StatusOK {
		t.Fatalf("result %+v", r)
	}
}

// Corrupt JSON, a foreign file, another version and a half-written temporary file are
// ignored, logged once, never deleted, and do not stop the worker.
func TestIgnoredFiles(t *testing.T) {
	e := newEnv(t)
	job := e.job()
	bad := map[string]string{
		"tasks/corrupt.json":   `{"v":1,"id":"corrupt","kind":`,
		"tasks/README":         "not a task",
		"tasks/newer.json":     `{"v":2,"id":"newer","kind":"probe","job":"` + job + `","args":{}}`,
		"tasks/.tmp-a1b2c3d4":  `{"v":1,"id":"half","ki`,
		"running/broken.json":  `[`,
		"running/.tmp-a1b2c3d": `{`,
	}
	for name, body := range bad {
		dir, file, _ := strings.Cut(name, "/")
		e.writeTask(dir, file, body)
	}
	e.submit("good", spool.KindProbe, job)
	var calls atomic.Int32
	e.start(map[string]Handler{spool.KindProbe: func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) {
		calls.Add(1)
		return nil, nil
	}})
	if r := e.result("good"); r.Status != spool.StatusOK {
		t.Fatalf("result %+v", r)
	}
	// Many polls later, each bad file has been logged exactly once.
	quiet()
	logged := e.logs.attrs("spool_ignored", "file")
	slices.Sort(logged)
	want := []string{"running/broken.json", "tasks/README", "tasks/corrupt.json", "tasks/newer.json"}
	if !slices.Equal(logged, want) {
		t.Fatalf("spool_ignored logged for %v, want %v", logged, want)
	}
	for name, body := range bad {
		data, err := os.ReadFile(filepath.Join(e.spool, name))
		if err != nil || string(data) != body {
			t.Errorf("%s: changed or deleted (%v)", name, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("handler ran %d times", n)
	}
}

func TestCancelWaiting(t *testing.T) {
	e := newEnv(t)
	job := e.job()
	e.submit("t-1", spool.KindProbe, job)
	e.submit("t-2", spool.KindProbe, job)
	if err := e.q.Cancel(context.Background(), "t-1"); err != nil {
		t.Fatal(err)
	}
	e.start(map[string]Handler{spool.KindProbe: func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) {
		return nil, nil
	}})
	e.result("t-2")
	quiet()
	if _, ok, _ := e.q.Result(context.Background(), "t-1"); ok {
		t.Fatal("the cancelled waiting task ran")
	}
	if got := e.logs.attrs("task_started", "task"); !slices.Equal(got, []string{"t-2"}) {
		t.Fatalf("started %v, want only t-2", got)
	}
}

func TestCancelRunning(t *testing.T) {
	e := newEnv(t)
	job := e.job()
	e.submit("t-1", spool.KindProbe, job)
	started := make(chan struct{})
	var handlerErr atomic.Value
	e.start(map[string]Handler{spool.KindProbe: func(ctx context.Context, _, _ *os.Root, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		handlerErr.Store(ctx.Err())
		return nil, ctx.Err()
	}})
	<-started
	if err := e.q.Cancel(context.Background(), "t-1"); err != nil {
		t.Fatal(err)
	}
	wantError(t, e.result("t-1"), spool.CodeCancelled)
	if err, _ := handlerErr.Load().(error); !errors.Is(err, context.Canceled) {
		t.Fatalf("handler context: %v", err)
	}
	if err := e.q.Ack(context.Background(), "t-1"); err != nil {
		t.Fatal(err)
	}
	// The worker removes running/t-1.json right after writing the result.
	eventually(t, func() bool { return len(e.files("running")) == 0 })
	for _, dir := range []string{"tasks", "running", "results", "cancel"} {
		if got := e.files(dir); len(got) != 0 {
			t.Errorf("%s/ holds %v", dir, got)
		}
	}
}

// A cancel that arrives while the task is claimed but not started (for example across a
// restart) stops it before its handler runs.
func TestCancelBeforeStart(t *testing.T) {
	e := newEnv(t)
	job := e.job()
	e.submit("t-1", spool.KindProbe, job)
	if ok, err := e.q.Claim("t-1"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if err := e.q.Cancel(context.Background(), "t-1"); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	e.start(map[string]Handler{spool.KindProbe: func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) {
		calls.Add(1)
		return nil, nil
	}})
	wantError(t, e.result("t-1"), spool.CodeCancelled)
	if calls.Load() != 0 {
		t.Fatal("the handler ran")
	}
}

func TestTimeout(t *testing.T) {
	e := newEnv(t)
	e.cfg.Worker.TaskTimeout = config.Duration(50 * time.Millisecond)
	job := e.job()
	e.submit("t-1", spool.KindProbe, job)
	e.start(map[string]Handler{spool.KindProbe: func(ctx context.Context, _, _ *os.Root, _ json.RawMessage) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	wantError(t, e.result("t-1"), spool.CodeTimeout)
}

func TestErrorCodes(t *testing.T) {
	e := newEnv(t)
	job := e.job()
	missing := fmt.Sprintf("%032x", 1<<40) // well-formed, but no directory
	e.writeTask("tasks", "bad-job.json", `{"v":1,"id":"bad-job","kind":"ok","job":"../other","created":"2026-01-01T12:00:00Z","args":{}}`)
	e.writeTask("tasks", "upper-job.json", `{"v":1,"id":"upper-job","kind":"ok","job":"0123456789ABCDEF0123456789ABCDEF","created":"2026-01-01T12:00:00Z","args":{}}`)
	e.submit("no-dir", "ok", missing)
	e.submit("unknown", "nope", job)
	e.submit("panics", "panic", job)
	e.submit("fails", "fail", job)
	e.submit("bad-args", "badargs", job)
	e.submit("output", "unmarshalable", job)
	e.start(map[string]Handler{
		"ok":    func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) { return nil, nil },
		"panic": func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) { panic("boom") },
		"fail": func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) {
			return nil, errors.New("disk on fire")
		},
		"badargs": func(_ context.Context, _, _ *os.Root, args json.RawMessage) (any, error) {
			return nil, fmt.Errorf("args %s: %w", args, ErrBadTask)
		},
		"unmarshalable": func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) {
			return func() {}, nil
		},
	})
	for id, code := range map[string]string{
		"bad-job": spool.CodeBadTask, "upper-job": spool.CodeBadTask, "no-dir": spool.CodeBadTask,
		"unknown": spool.CodeUnknownKind, "panics": spool.CodeInternal, "fails": spool.CodeInternal,
		"bad-args": spool.CodeBadTask, "output": spool.CodeInternal,
	} {
		r := e.result(id)
		wantError(t, r, code)
		if r.Error.Message == "" {
			t.Errorf("%s: no message", id)
		}
	}
	if r := e.result("fails"); r.Error.Message != "disk on fire" {
		t.Errorf("message %q", r.Error.Message)
	}
	if got := e.logs.attrs("task_panicked", "panic"); !slices.Equal(got, []string{"boom"}) {
		t.Errorf("task_panicked %v", got)
	}
	// Nothing was created for the malformed job ids.
	entries, err := os.ReadDir(filepath.Join(e.intake, jobdir.Jobs))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != job {
		t.Fatalf("jobs/ holds %v", entries)
	}
}

// The handler's root reaches the job directory and nothing else.
func TestRootIsConfined(t *testing.T) {
	e := newEnv(t)
	job, other := e.job(), e.job()
	jobs := filepath.Join(e.intake, jobdir.Jobs)
	if err := os.WriteFile(filepath.Join(jobs, other, "secret"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(jobs, other, "secret"), filepath.Join(jobs, job, "files", "link")); err != nil {
		t.Fatal(err)
	}
	e.submit("t-1", "escape", job)
	e.start(map[string]Handler{"escape": func(_ context.Context, root, _ *os.Root, _ json.RawMessage) (any, error) {
		got := map[string]string{}
		for _, name := range []string{"../other", "../" + other + "/secret", "files/link", "/etc/hostname", "files"} {
			f, err := root.Open(name)
			if err != nil {
				got[name] = "refused"
				continue
			}
			_ = f.Close()
			got[name] = "opened"
		}
		return got, nil
	}})
	r := e.result("t-1")
	var got map[string]string
	if err := json.Unmarshal(r.Output, &got); err != nil {
		t.Fatal(err, r)
	}
	want := map[string]string{"../other": "refused", "../" + other + "/secret": "refused", "files/link": "refused", "/etc/hostname": "refused", "files": "opened"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s, want %s", k, got[k], v)
		}
	}
}

// worker.concurrency: 2 runs two tasks at once and never three.
func TestConcurrency(t *testing.T) {
	e := newEnv(t)
	e.cfg.Worker.Concurrency = 2
	job := e.job()
	const n = 6
	for i := range n {
		e.submit(fmt.Sprintf("t-%d", i), spool.KindProbe, job)
	}
	var active, peak atomic.Int32
	started := make(chan struct{}, n)
	release := make(chan struct{})
	e.start(map[string]Handler{spool.KindProbe: func(context.Context, *os.Root, *os.Root, json.RawMessage) (any, error) {
		now := active.Add(1)
		for {
			p := peak.Load()
			if now <= p || peak.CompareAndSwap(p, now) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return nil, nil
	}})
	for round := range n / 2 {
		for range 2 {
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatalf("round %d: fewer than two tasks started", round)
			}
		}
		quiet()
		select {
		case <-started:
			t.Fatalf("round %d: a third task started", round)
		default:
		}
		release <- struct{}{}
		release <- struct{}{}
	}
	for i := range n {
		if r := e.result(fmt.Sprintf("t-%d", i)); r.Status != spool.StatusOK {
			t.Fatalf("result %+v", r)
		}
	}
	if p := peak.Load(); p != 2 {
		t.Fatalf("peak %d tasks at once, want 2", p)
	}
}
