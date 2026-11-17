package spool_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/spool"
)

// A worker crashed between writing results/X.json and removing running/X.json, and the web
// process acknowledged the result before the worker restarted: the task must not run again.
func TestAckRemovesLeftoverClaim(t *testing.T) {
	d, dir := open(t)
	ctx := context.Background()
	submit(t, d, "t-1")
	if ok, err := d.Claim("t-1"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	// What Finish writes, without its second step.
	if err := os.WriteFile(filepath.Join(dir, "results", "t-1.json"), []byte(`{"v":1,"id":"t-1","status":"ok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.Cancel(ctx, "t-1"); err != nil {
		t.Fatal(err)
	}
	if err := d.Ack(ctx, "t-1"); err != nil {
		t.Fatal(err)
	}
	tasks, _, err := d.Running()
	if err != nil || len(tasks) != 0 {
		t.Fatalf("the restarted worker would run %+v (%v)", tasks, err)
	}
	for _, sub := range []string{"tasks", "running", "results", "cancel"} {
		if got := names(t, filepath.Join(dir, sub)); len(got) != 0 {
			t.Errorf("%s/ holds %v", sub, got)
		}
	}
}

// Ack without a result leaves a running task and its cancel marker alone.
func TestAckWithoutResult(t *testing.T) {
	d, dir := open(t)
	ctx := context.Background()
	submit(t, d, "t-1")
	if _, err := d.Claim("t-1"); err != nil {
		t.Fatal(err)
	}
	if err := d.Cancel(ctx, "t-1"); err != nil {
		t.Fatal(err)
	}
	if err := d.Ack(ctx, "t-1"); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(dir, "running")); !slices.Equal(got, []string{"t-1.json"}) {
		t.Fatalf("running/ holds %v", got)
	}
	if got := names(t, filepath.Join(dir, "cancel")); !slices.Equal(got, []string{"t-1"}) {
		t.Fatalf("cancel/ holds %v", got)
	}
}

// A new submission clears a cancel marker left over from an earlier task with its id.
func TestSubmitClearsStaleCancel(t *testing.T) {
	d, dir := open(t)
	if err := os.WriteFile(filepath.Join(dir, "cancel", "t-1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	submit(t, d, "t-1")
	if ok, err := d.CancelRequested("t-1"); ok || err != nil {
		t.Fatalf("cancel requested %v %v", ok, err)
	}
	// A repeated Submit of a running task keeps a real marker.
	if _, err := d.Claim("t-1"); err != nil {
		t.Fatal(err)
	}
	if err := d.Cancel(context.Background(), "t-1"); err != nil {
		t.Fatal(err)
	}
	submit(t, d, "t-1")
	if ok, err := d.CancelRequested("t-1"); !ok || err != nil {
		t.Fatalf("cancel requested %v %v", ok, err)
	}
}

// within fails the test if f does not return in time: a FIFO must never block a reader.
func within(t *testing.T, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked")
	}
}

func TestFIFOsDoNotBlock(t *testing.T) {
	d, dir := open(t)
	for _, p := range []string{"results/fifo.json", "tasks/fifo.json", "running/fifo.json", "elsewhere"} {
		if err := syscall.Mkfifo(filepath.Join(dir, p), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Links inside the spool to a FIFO: the Lstat before the open sees a link, not a FIFO.
	for _, p := range []string{"results/link.json", "tasks/link.json"} {
		if err := os.Symlink(filepath.Join(dir, "elsewhere"), filepath.Join(dir, p)); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"fifo", "link"} {
		within(t, func() {
			assertBadResult(t, d, id)
		})
	}
	within(t, func() {
		tasks, ignored, err := d.Waiting()
		if err != nil || len(tasks) != 0 || len(ignored) != 2 {
			t.Errorf("waiting %+v, ignored %+v, %v", tasks, ignored, err)
		}
	})
	within(t, func() {
		tasks, ignored, err := d.Running()
		if err != nil || len(tasks) != 0 || len(ignored) != 1 {
			t.Errorf("running %+v, ignored %+v, %v", tasks, ignored, err)
		}
	})
	// A FIFO in place of a subdirectory does not block either.
	if err := os.Remove(filepath.Join(dir, "cancel")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "cancel"), 0o600); err != nil {
		t.Fatal(err)
	}
	within(t, func() {
		if _, err := d.CancelRequests(); err == nil {
			t.Error("CancelRequests over a FIFO: no error")
		}
	})
}

func TestReadRegular(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "small"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if data, err := spool.ReadRegular(root, "small", 5); err != nil || string(data) != "12345" {
		t.Fatalf("%q %v", data, err)
	}
	if _, err := spool.ReadRegular(root, "small", 4); err == nil {
		t.Fatal("a file over the limit was read")
	}
	if _, err := spool.ReadRegular(root, "missing", 4); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
}

func TestLockWorker(t *testing.T) {
	d, dir := open(t)
	lock, err := d.LockWorker()
	if err != nil {
		t.Fatal(err)
	}
	other, err := spool.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, err := other.LockWorker(); !errors.Is(err, spool.ErrLocked) {
		t.Fatalf("second lock: %v, want ErrLocked", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := other.LockWorker()
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = again.Close()
}
