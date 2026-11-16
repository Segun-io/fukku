package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/spool"
)

// A result that cannot be written (a full disk; here a read-only results/) is retried until
// it can, without a restart.
func TestFinishRetries(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	e := newEnv(t)
	job := e.job()
	results := filepath.Join(e.spool, spool.ResultsDir)
	if err := os.Chmod(results, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(results, 0o750) })
	e.submit("t-1", spool.KindProbe, job)
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	w := New(e.cfg, slog.New(e.logs), map[string]Handler{spool.KindProbe: func(context.Context, *os.Root, json.RawMessage) (any, error) {
		calls.Add(1)
		return "done", nil
	}})
	w.retryMin, w.retryMax = 2*time.Millisecond, 10*time.Millisecond
	errc := make(chan error, 1)
	go func() { errc <- w.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-errc; err != nil {
			t.Error(err)
		}
	}()
	eventually(t, func() bool { return len(e.logs.attrs("result_write_failed", "attempt")) >= 3 })
	if err := os.Chmod(results, 0o750); err != nil {
		t.Fatal(err)
	}
	if r := e.result("t-1"); string(r.Output) != `"done"` {
		t.Fatalf("result %+v", r)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("the handler ran %d times", n)
	}
}

// A worker that stops while it cannot write a result gives up; the task is still claimed
// and runs again at the next start.
func TestFinishGivesUpOnStop(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	e := newEnv(t)
	job := e.job()
	results := filepath.Join(e.spool, spool.ResultsDir)
	if err := os.Chmod(results, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(results, 0o750) })
	e.submit("t-1", spool.KindProbe, job)
	stop := e.start(map[string]Handler{spool.KindProbe: func(context.Context, *os.Root, json.RawMessage) (any, error) {
		return nil, nil
	}})
	eventually(t, func() bool { return len(e.logs.attrs("result_write_failed", "attempt")) >= 1 })
	stop()
	if got := e.files("running"); len(got) != 1 || got[0] != "t-1.json" {
		t.Fatalf("running/ holds %v", got)
	}
}

// A cancel marker left over from an earlier task with the same id (a Cancel that raced with
// that task's end) does not cancel a new submission.
func TestStaleCancelMarker(t *testing.T) {
	e := newEnv(t)
	job := e.job()
	if err := os.WriteFile(filepath.Join(e.spool, spool.CancelDir, "t-1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.submit("t-1", spool.KindProbe, job)
	e.start(map[string]Handler{spool.KindProbe: func(context.Context, *os.Root, json.RawMessage) (any, error) {
		return "ran", nil
	}})
	if r := e.result("t-1"); r.Status != spool.StatusOK || string(r.Output) != `"ran"` {
		t.Fatalf("result %+v", r)
	}
}

// The health check reads the heartbeat safely: a FIFO does not hang it, a link out of the
// spool is not followed, and a huge file is not read.
func TestHeartbeatHostile(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(outside, []byte("1700000000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, plant := range map[string]func(path string) error{
		"fifo":    func(p string) error { return syscall.Mkfifo(p, 0o600) },
		"zero":    func(p string) error { return os.Symlink("/dev/zero", p) },
		"outside": func(p string) error { return os.Symlink(outside, p) },
		"huge":    func(p string) error { return os.WriteFile(p, []byte(strings.Repeat("1", 1<<20)), 0o600) },
		"dir":     func(p string) error { return os.Mkdir(p, 0o750) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := plant(filepath.Join(dir, HeartbeatFile)); err != nil {
				t.Fatal(err)
			}
			errc := make(chan error, 1)
			go func() {
				_, err := HeartbeatAge(dir, time.Now())
				errc <- err
			}()
			select {
			case err := <-errc:
				if err == nil {
					t.Fatal("no error")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("HeartbeatAge hangs")
			}
		})
	}
}
