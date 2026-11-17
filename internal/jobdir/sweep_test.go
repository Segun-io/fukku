//go:build linux

package jobdir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var testLimits = SweepLimits{MaxEntries: 100_000, MaxDepth: 40}

// sweepAll sweeps with w until a sweep is done, at most rounds times, and returns the total
// removed and how many sweeps it took.
func sweepAll(t *testing.T, intake string, w *WorkerSweeper, rounds int) (removed, sweeps int) {
	t.Helper()
	r, err := os.OpenRoot(intake)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	for i := range rounds {
		res, err := w.Sweep(r)
		if err != nil {
			t.Fatal(err)
		}
		if res.Removed > w.Limits.MaxEntries {
			t.Fatalf("sweep %d did %d, more than %d", i, res.Removed, w.Limits.MaxEntries)
		}
		removed += res.Removed
		if res.Done {
			return removed, i + 1
		}
	}
	t.Fatalf("not done after %d sweeps", rounds)
	return 0, 0
}

// onlyJobs fails unless worker/ holds the job folders want and an empty .spill.
func onlyJobs(t *testing.T, intake string, want ...string) {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(intake, Worker))
	for _, e := range entries {
		switch {
		case e.Name() == Spill:
			if left, _ := os.ReadDir(filepath.Join(intake, Worker, Spill)); len(left) != 0 {
				t.Errorf("left in .spill: %d entries", len(left))
			}
		case !contains(want, e.Name()):
			t.Errorf("left in worker/: %s", e.Name())
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
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

	if n, _ := sweepAll(t, intake, &WorkerSweeper{Limits: testLimits}, 5); n < 6 {
		t.Errorf("removed %d", n)
	}
	for _, gone := range []string{"work/alias", "work/link", "fifo"} {
		if _, err := os.Lstat(filepath.Join(wdir, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is left: %v", gone, err)
		}
	}
	onlyJobs(t, intake, id)
	if data, err := os.ReadFile(filepath.Join(wdir, "work/previews/c1.mp4")); err != nil || string(data) != "clip" {
		t.Errorf("the worker's own clip: %q %v", data, err)
	}
	if _, err := CheckFile(job, "files/x.mkv", 6); err != nil {
		t.Errorf("the job's file after the sweep: %v", err)
	}
}

// TestSweepWorkerManyEntries: many entries are swept a bounded number at a time, every sweep
// makes progress and the next one carries on until all is gone. A job folder with more
// entries than any job needs goes as a whole.
func TestSweepWorkerManyEntries(t *testing.T) {
	intake, _, _ := tree(t)
	lim := SweepLimits{MaxEntries: 10_000, MaxDepth: 40}
	junk := filepath.Join(intake, Worker, "junk")
	mkdirT(t, junk)
	one := filepath.Join(intake, "many.txt")
	write(t, one, "x")
	for i := range 25_000 {
		mustLink(t, one, filepath.Join(junk, strconv.Itoa(i)))
	}
	big := strings.Repeat("b", 32)
	for i := range 6_000 { // more than MaxEntries/2: no job has as many
		write(t, filepath.Join(intake, Worker, big, strconv.Itoa(i)), "")
	}
	if _, sweeps := sweepAll(t, intake, &WorkerSweeper{Limits: lim}, 20); sweeps < 3 {
		t.Errorf("done in %d sweeps: not bounded", sweeps)
	}
	onlyJobs(t, intake, id)
}

// TestSweepWorkerManyJobFolders: empty folders named like jobs count against the budget,
// and the sweeps go through them in name order, so every one is reached; Orphan sends the
// ones of no job away.
func TestSweepWorkerManyJobFolders(t *testing.T) {
	intake, _, _ := tree(t)
	for i := range 3000 {
		mkdirT(t, filepath.Join(intake, Worker, fmt.Sprintf("%032x", i+1)))
	}
	keep := fmt.Sprintf("%032x", 2999)
	w := &WorkerSweeper{Limits: SweepLimits{MaxEntries: 10_000, MaxDepth: 40}, Orphan: func(name string, _ time.Time) bool {
		return name != id && name != keep
	}}
	if _, sweeps := sweepAll(t, intake, w, 20); sweeps < 2 {
		t.Errorf("done in %d sweeps: more than one window of folders", sweeps)
	}
	sweepAll(t, intake, w, 20) // what was set aside goes
	onlyJobs(t, intake, id, keep)
}

// TestSweepWorkerResumesAfterAFolderThatIsGone: the next sweep starts after the name the
// last one stopped at, also when that folder is gone meanwhile.
func TestSweepWorkerResumesAfterAFolderThatIsGone(t *testing.T) {
	intake, _, _ := tree(t)
	for i := range 2*window + 10 {
		mkdirT(t, filepath.Join(intake, Worker, fmt.Sprintf("%032x", i+1)))
	}
	r, err := os.OpenRoot(intake)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	var seen []string
	w := &WorkerSweeper{Limits: testLimits, Orphan: func(name string, _ time.Time) bool { seen = append(seen, name); return false }}
	if res, err := w.Sweep(r); err != nil || res.Done {
		t.Fatalf("first sweep: %+v %v", res, err)
	}
	stopped := w.next
	if err := os.Remove(filepath.Join(intake, Worker, stopped)); err != nil {
		t.Fatal(err)
	}
	seen = nil
	if _, err := w.Sweep(r); err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 || seen[0] <= stopped {
		t.Fatalf("the second sweep started at %v, not after %s", seen[:min(1, len(seen))], stopped)
	}
}

// TestSweepWorkerDeepNesting: directories far deeper than any the worker makes are set aside
// and removed, a bounded number of levels per sweep, without running out of descriptors.
func TestSweepWorkerDeepNesting(t *testing.T) {
	intake, _, _ := tree(t)
	wdir := filepath.Join(intake, Worker, id)
	d, err := os.Open(wdir)
	if err != nil {
		t.Fatal(err)
	}
	for range 3000 { // built relative to an open directory, so no path gets too long
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
	sweepAll(t, intake, &WorkerSweeper{Limits: testLimits}, 400)
	depth := 0
	for p := filepath.Join(wdir, "d"); ; p = filepath.Join(p, "d") {
		if _, err := os.Lstat(p); err != nil {
			break
		}
		depth++
	}
	if depth == 0 || depth >= testLimits.MaxDepth {
		t.Errorf("%d levels are left below worker/<id>", depth)
	}
	onlyJobs(t, intake, id)
	if _, err := os.Stat(filepath.Join(wdir, "work/previews/c1.mp4")); err != nil {
		t.Errorf("the clip: %v", err)
	}
}

// TestSweepWorkerUnreadableFolders: folders the worker made unreadable or unwritable (mode
// 000, 0500) do not stop the sweep.
func TestSweepWorkerUnreadableFolders(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any folder")
	}
	intake, job, _ := tree(t)
	wdir, jdir := filepath.Join(intake, Worker, id), Dir(intake, id)
	write(t, filepath.Join(jdir, "files/x.mkv"), "placed")
	mkdirT(t, filepath.Join(wdir, "work/locked"))
	mustLink(t, filepath.Join(jdir, "files/x.mkv"), filepath.Join(wdir, "work/locked/alias"))
	write(t, filepath.Join(intake, Worker, "junk", "sub", "x"), "x")
	for _, p := range []string{filepath.Join(wdir, "work/locked"), filepath.Join(intake, Worker, "junk", "sub")} {
		if err := os.Chmod(p, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(p, 0o755) })
	}
	if err := os.Chmod(filepath.Join(intake, Worker, "junk"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(intake, Worker, "junk"), 0o755) })
	sweepAll(t, intake, &WorkerSweeper{Limits: testLimits}, 5)
	if _, err := os.Lstat(filepath.Join(wdir, "work/locked/alias")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the name behind a mode 000 folder is left: %v", err)
	}
	onlyJobs(t, intake, id)
	if _, err := CheckFile(job, "files/x.mkv", 6); err != nil {
		t.Error(err)
	}
}

// TestSpillWorker: a job's folder, or a part of it, set aside goes at once when it is small.
func TestSpillWorker(t *testing.T) {
	intake, _, _ := tree(t)
	write(t, filepath.Join(intake, Worker, id, "merged/1/film.mkv"), "merged")
	if err := SpillWorker(intake, id, "merged/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(intake, Worker, id, "merged/1")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("merged/1 is left: %v", err)
	}
	onlyJobs(t, intake, id)
	if err := SpillWorker(intake, id, "missing"); err != nil {
		t.Errorf("a missing one: %v", err)
	}
	if err := SpillWorker(intake, id, ""); err != nil {
		t.Fatal(err)
	}
	onlyJobs(t, intake)
}

// TestSpillWorkerNeverLeavesWorker: a link in the middle of the path, or at worker/<id>,
// that leads to another job's folder is refused; nothing of that job moves (#113).
func TestSpillWorkerNeverLeavesWorker(t *testing.T) {
	const other = "fedcba9876543210fedcba9876543210"
	for name, plant := range map[string]func(t *testing.T, intake string){
		"a link in the middle": func(t *testing.T, intake string) {
			symlink(t, filepath.Join("..", "..", Jobs, other), filepath.Join(intake, Worker, id, "files"))
		},
		"worker/<id> as a link": func(t *testing.T, intake string) {
			if err := os.RemoveAll(filepath.Join(intake, Worker, id)); err != nil {
				t.Fatal(err)
			}
			symlink(t, filepath.Join("..", Jobs, other), filepath.Join(intake, Worker, id))
		},
	} {
		t.Run(name, func(t *testing.T) {
			intake, _, _ := tree(t)
			write(t, filepath.Join(intake, Jobs, other, "files", "files", "upload.mkv"), "theirs")
			write(t, filepath.Join(intake, Jobs, other, "merged", "1", "film.mkv"), "theirs")
			plant(t, intake)
			for _, rel := range []string{"files/files", "merged/1", "merged"} {
				if err := SpillWorker(intake, id, rel); err != nil && !errors.Is(err, ErrLink) {
					t.Errorf("SpillWorker(%s): %v", rel, err)
				}
			}
			if _, err := OpenWorker(intake, id); !errors.Is(err, ErrLink) && name == "worker/<id> as a link" {
				t.Errorf("OpenWorker of a link: %v", err)
			}
			for _, p := range []string{"files/files/upload.mkv", "merged/1/film.mkv"} {
				if data, err := os.ReadFile(filepath.Join(intake, Jobs, other, p)); err != nil || string(data) != "theirs" {
					t.Errorf("the other job's %s: %q %v", p, data, err)
				}
			}
			if left, _ := os.ReadDir(filepath.Join(intake, Worker, Spill)); len(left) != 0 {
				t.Errorf("set aside: %v", left)
			}
		})
	}
}

func mkdirT(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
