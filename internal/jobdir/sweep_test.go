//go:build linux

package jobdir

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

var testLimits = SweepLimits{MaxEntries: 100_000, MaxDepth: 40}

// sweepAll sweeps until a sweep gets through, at most rounds times, and returns the total
// removed.
func sweepAll(t *testing.T, intake string, lim SweepLimits, rounds int) int {
	t.Helper()
	r, err := os.OpenRoot(intake)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	w := &WorkerSweeper{Limits: lim}
	total := 0
	for i := range rounds {
		res, err := w.Sweep(r)
		if err != nil {
			t.Fatal(err)
		}
		total += res.Removed
		if res.Done && i > 0 && res.Removed == 0 {
			return total
		}
	}
	t.Fatalf("not done after %d sweeps", rounds)
	return total
}

// TestSweepWorker: a name the worker kept of a file in the job goes, and so does anything it
// never makes; its own scratch work stays (#114).
func TestSweepWorker(t *testing.T) {
	intake, job, _ := tree(t)
	wdir, jdir := filepath.Join(intake, Worker, id), Dir(intake, id)
	write(t, filepath.Join(jdir, "files/x.mkv"), "placed")
	write(t, filepath.Join(wdir, "work/previews/c1.mp4"), "clip")
	mustLink(t, filepath.Join(jdir, "files/x.mkv"), filepath.Join(wdir, "work/alias"))
	symlink(t, filepath.Join(jdir, "files/x.mkv"), filepath.Join(wdir, "work/link"))
	if err := syscall.Mkfifo(filepath.Join(wdir, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(intake, Worker, "stray.txt"), "x")
	mustLink(t, filepath.Join(jdir, "files/x.mkv"), filepath.Join(intake, Worker, "not-a-job"))
	write(t, filepath.Join(intake, Worker, "other", "deeper", "alias"), "x")

	if n := sweepAll(t, intake, testLimits, 3); n < 6 {
		t.Errorf("removed %d", n)
	}
	for _, gone := range []string{"work/alias", "work/link", "fifo"} {
		if _, err := os.Lstat(filepath.Join(wdir, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is left: %v", gone, err)
		}
	}
	for _, gone := range []string{"stray.txt", "not-a-job", "other"} {
		if _, err := os.Lstat(filepath.Join(intake, Worker, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is left: %v", gone, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(wdir, "work/previews/c1.mp4")); err != nil || string(data) != "clip" {
		t.Errorf("the worker's own clip: %q %v", data, err)
	}
	if _, err := CheckFile(job, "files/x.mkv", 6); err != nil {
		t.Errorf("the job's file after the sweep: %v", err)
	}
}

// TestSweepWorkerManyEntries: a folder of many entries is swept a bounded number at a time,
// and every sweep makes progress: the next one carries on, until all is gone. A job folder
// with more entries than any job needs goes as a whole.
func TestSweepWorkerManyEntries(t *testing.T) {
	intake, _, _ := tree(t)
	lim := SweepLimits{MaxEntries: 10_000, MaxDepth: 40}
	junk := filepath.Join(intake, Worker, "junk")
	mkdirT(t, junk)
	for i := range 25_000 {
		mustLink(t, writeOnce(t, intake), filepath.Join(junk, strconv.Itoa(i)))
	}
	big := filepath.Join(intake, Worker, strings.Repeat("b", 32))
	mkdirT(t, big)
	for i := range 6_000 { // more than MaxEntries/2: no job has as many
		write(t, filepath.Join(big, strconv.Itoa(i)), "")
	}
	r, err := os.OpenRoot(intake)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	w := &WorkerSweeper{Limits: lim}
	first, err := w.Sweep(r)
	if err != nil {
		t.Fatal(err)
	}
	if first.Done || first.Removed > lim.MaxEntries {
		t.Fatalf("one sweep did %d (done %t): not bounded", first.Removed, first.Done)
	}
	sweepAll(t, intake, lim, 20)
	entries, _ := os.ReadDir(filepath.Join(intake, Worker))
	for _, e := range entries {
		if e.Name() != id {
			t.Errorf("left in worker/: %s", e.Name())
		}
	}
}

// TestSweepWorkerDeepNesting: directories far deeper than any the worker makes are set aside
// and removed, a bounded number of levels per sweep, without running out of descriptors.
func TestSweepWorkerDeepNesting(t *testing.T) {
	intake, _, _ := tree(t)
	wdir := filepath.Join(intake, Worker, id)
	// 3000 levels, built relative to an open directory so no path gets too long.
	d, err := os.Open(wdir)
	if err != nil {
		t.Fatal(err)
	}
	for range 3000 {
		if err := syscall.Mkdirat(int(d.Fd()), "d", 0o755); err != nil {
			t.Fatal(err)
		}
		fd, err := syscall.Openat(int(d.Fd()), "d", syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_ = d.Close()
		d = os.NewFile(uintptr(fd), "d")
	}
	_ = d.Close()
	write(t, filepath.Join(wdir, "work/previews/c1.mp4"), "clip")
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err == nil {
		saved := lim
		lim.Cur = 256 // far fewer than the levels
		if syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim) == nil {
			t.Cleanup(func() { _ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &saved) })
		}
	}
	sweepAll(t, intake, testLimits, 200)
	if _, err := os.Lstat(filepath.Join(wdir, "d")); err != nil {
		t.Fatalf("the levels the worker may make are kept: %v", err)
	}
	depth := 0
	for p := filepath.Join(wdir, "d"); ; p = filepath.Join(p, "d") {
		if _, err := os.Lstat(p); err != nil {
			break
		}
		depth++
	}
	if depth >= testLimits.MaxDepth {
		t.Errorf("%d levels are left below worker/<id>", depth)
	}
	entries, _ := os.ReadDir(filepath.Join(intake, Worker))
	for _, e := range entries {
		if e.Name() != id {
			t.Errorf("left in worker/: %s", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(wdir, "work/previews/c1.mp4")); err != nil {
		t.Errorf("the clip: %v", err)
	}
}

// TestSpillWorker: a job's folder set aside goes at once when it is small, and in later
// sweeps when it is not.
func TestSpillWorker(t *testing.T) {
	intake, _, _ := tree(t)
	write(t, filepath.Join(intake, Worker, id, "merged/1/film.mkv"), "merged")
	if err := SpillWorker(intake, id, "merged/1"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(intake, Worker)); len(entries) != 1 {
		t.Errorf("worker/ holds %v", entries)
	}
	if _, err := os.Lstat(filepath.Join(intake, Worker, id, "merged/1")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("merged/1 is left: %v", err)
	}
	if err := SpillWorker(intake, id, "missing"); err != nil {
		t.Errorf("a missing one: %v", err)
	}
}

func mkdirT(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

var once string

// writeOnce is one file to make many hard links of.
func writeOnce(t *testing.T, intake string) string {
	t.Helper()
	p := filepath.Join(intake, "many.txt")
	if once != p {
		write(t, p, "x")
		once = p
	}
	return p
}
