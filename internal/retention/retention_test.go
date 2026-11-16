package retention_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/retention"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/spool/spooltest"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

const (
	idleExpiry   = 48 * time.Hour
	jobRetention = 14 * 24 * time.Hour
	finishedKeep = 30 * 24 * time.Hour
	second       = time.Second
)

// clock is a fake clock safe for concurrent use.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// lockedBuffer collects log output.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type env struct {
	t     *testing.T
	clk   *clock
	st    *storetest.Fake
	cfg   config.Config
	root  string // intake root
	base  string // parent of the intake root: stands for the rest of the filesystem
	log   *lockedBuffer
	sweep *retention.Sweeper
	queue *spooltest.Queue
}

func newEnv(t *testing.T) *env {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "intake")
	for _, d := range []string{".incoming", "jobs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := filepath.Join(root, ".mounted")
	write(t, sentinel, "x", time.Time{})

	cfg := config.Defaults()
	cfg.Paths.IntakeRoot = root
	cfg.Paths.Sentinels = []string{sentinel}

	e := &env{t: t, clk: &clock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}, cfg: cfg, root: root, base: base, log: &lockedBuffer{}}
	e.st = storetest.NewFake(e.clk.Now)
	e.queue = &spooltest.Queue{}
	log := slog.New(slog.NewTextHandler(e.log, nil))
	e.sweep = retention.New(cfg, e.st, e.queue, log, e.clk.Now)
	return e
}

func write(t *testing.T, p, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *env) path(parts ...string) string {
	return filepath.Join(append([]string{e.root}, parts...)...)
}

func (e *env) exists(parts ...string) bool {
	_, err := os.Lstat(e.path(parts...))
	return err == nil
}

func (e *env) run() { e.sweep.Sweep(context.Background()) }

func randID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (e *env) transition(id string, from, to store.State, c store.Change) {
	e.t.Helper()
	if _, err := e.st.Transition(context.Background(), id, from, to, c); err != nil {
		e.t.Fatalf("transition %s -> %s: %v", from, to, err)
	}
}

// jobIn creates a job and walks it to the given state at the current fake time.
func (e *env) jobIn(state store.State, expires time.Time) store.Job {
	e.t.Helper()
	ctx := context.Background()
	j, err := e.st.CreateJob(ctx, "alice")
	if err != nil {
		e.t.Fatal(err)
	}
	path := map[store.State][]store.State{
		store.Uploading:            {},
		store.Uploaded:             {store.Uploaded},
		store.Probing:              {store.Uploaded, store.Probing},
		store.Probed:               {store.Uploaded, store.Probing, store.Probed},
		store.Matched:              {store.Uploaded, store.Probing, store.Probed, store.Matched},
		store.AwaitingConfirmation: {store.Uploaded, store.Probing, store.Probed, store.Matched, store.AwaitingConfirmation},
		store.Importing:            {store.Uploaded, store.Probing, store.Probed, store.Matched, store.AwaitingConfirmation, store.Importing},
		store.Imported:             {store.Uploaded, store.Probing, store.Probed, store.Matched, store.AwaitingConfirmation, store.Importing, store.Imported},
		store.PostProcessing:       {store.Uploaded, store.Probing, store.Probed, store.Matched, store.AwaitingConfirmation, store.Importing, store.Imported, store.PostProcessing},
		store.Done:                 {store.Uploaded, store.Probing, store.Probed, store.Matched, store.AwaitingConfirmation, store.Importing, store.Imported, store.PostProcessing, store.Done},
		store.Cancelled:            {store.Cancelled},
		store.Failed:               {store.Failed},
		store.Stalled:              {store.Uploaded, store.Probing, store.Stalled},
	}[state]
	from := store.Uploading
	for _, to := range path {
		c := store.Change{Actor: "alice"}
		if to == store.Failed || to == store.Stalled {
			c.ErrorCode = "test"
		}
		if to == store.Probed && !expires.IsZero() {
			c.ExpiresAt = &expires
		}
		e.transition(j.ID, from, to, c)
		from = to
	}
	j, _ = e.st.Job(ctx, j.ID)
	return j
}

func (e *env) state(id string) store.State {
	e.t.Helper()
	j, err := e.st.Job(context.Background(), id)
	if err != nil {
		e.t.Fatalf("job %s: %v", id, err)
	}
	return j.State
}

func (e *env) auditActions(jobID string) []string {
	e.t.Helper()
	log, _ := e.st.AuditLog(context.Background(), jobID)
	var out []string
	for _, a := range log {
		if a.Actor != "system" {
			e.t.Errorf("audit actor %q, want system", a.Actor)
		}
		out = append(out, a.Action)
	}
	return out
}

func (e *env) hasAudit(jobID, action string) bool {
	for _, a := range e.auditActions(jobID) {
		if a == action {
			return true
		}
	}
	return false
}

// upload registers an unfinished upload with its data and info files.
func (e *env) upload(jobID string, progress time.Time) store.Upload {
	e.t.Helper()
	u := store.Upload{ID: randID(), JobID: jobID, Owner: "alice", Name: "film.mkv", Size: 10, Offset: 5, CreatedAt: progress, ProgressAt: progress}
	if err := e.st.PutUpload(context.Background(), u); err != nil {
		e.t.Fatal(err)
	}
	write(e.t, e.path(".incoming", u.ID), "data", progress)
	write(e.t, e.path(".incoming", u.ID+".info"), "{}", progress)
	return u
}

func (e *env) uploadRowExists(id string) bool {
	_, err := e.st.Upload(context.Background(), id)
	return err == nil
}

func TestIdleUploadExpiry(t *testing.T) {
	e := newEnv(t)
	job := e.jobIn(store.Uploading, time.Time{})
	now := e.clk.Now()
	justBefore := e.upload(job.ID, now.Add(-idleExpiry+second))
	justAfter := e.upload(job.ID, now.Add(-idleExpiry-second))

	e.run()

	if !e.uploadRowExists(justBefore.ID) || !e.exists(".incoming", justBefore.ID) || !e.exists(".incoming", justBefore.ID+".info") {
		t.Error("an upload just inside the limit must be kept")
	}
	if e.uploadRowExists(justAfter.ID) || e.exists(".incoming", justAfter.ID) || e.exists(".incoming", justAfter.ID+".info") {
		t.Error("an upload just past the limit must lose its row, data and info file")
	}
	if !e.hasAudit(job.ID, "upload.expired") {
		t.Errorf("no upload.expired audit entry: %v", e.auditActions(job.ID))
	}
	if got := e.state(job.ID); got != store.Uploading {
		t.Errorf("job with a live upload is %s, want uploading", got)
	}
}

func TestUploadingJobWithoutUploadIsCancelledWhenIdle(t *testing.T) {
	e := newEnv(t)
	before := e.jobIn(store.Uploading, time.Time{})
	e.clk.Advance(idleExpiry - second)
	e.run()
	if got := e.state(before.ID); got != store.Uploading {
		t.Fatalf("just before the limit: %s", got)
	}
	e.clk.Advance(2 * second)
	e.run()
	if got := e.state(before.ID); got != store.Cancelled {
		t.Fatalf("just after the limit: %s", got)
	}
	if !e.hasAudit(before.ID, "job.expired") {
		t.Error("no job.expired audit entry")
	}
	hist, _ := e.st.History(context.Background(), before.ID)
	if last := hist[len(hist)-1]; last.Actor != "system" || last.To != store.Cancelled {
		t.Errorf("last event %+v", last)
	}
}

func TestUploadingJobWithUnfinishedUploadIsKept(t *testing.T) {
	e := newEnv(t)
	job := e.jobIn(store.Uploading, time.Time{})
	e.clk.Advance(idleExpiry * 2)
	e.upload(job.ID, e.clk.Now().Add(-time.Minute))
	e.run()
	if got := e.state(job.ID); got != store.Uploading {
		t.Fatalf("%s", got)
	}
}

func TestUploadingJobWithRecentFileActivityIsKept(t *testing.T) {
	e := newEnv(t)
	job := e.jobIn(store.Uploading, time.Time{})
	e.clk.Advance(idleExpiry * 2)
	// A file finished uploading a minute ago: the upload row is gone, the directory is new.
	recent := e.clk.Now().Add(-time.Minute)
	write(t, e.path("jobs", job.ID, "files", "film.mkv"), "x", recent)
	for _, d := range []string{"files", ""} {
		if err := os.Chtimes(e.path("jobs", job.ID, d), recent, recent); err != nil {
			t.Fatal(err)
		}
	}
	e.run()
	if got := e.state(job.ID); got != store.Uploading {
		t.Fatalf("%s", got)
	}
	// Once the activity is old too, the job goes.
	e.clk.Advance(idleExpiry + second)
	e.run()
	if got := e.state(job.ID); got != store.Cancelled {
		t.Fatalf("%s", got)
	}
}

func TestWaitingJobsExpireAtExpiresAt(t *testing.T) {
	for _, state := range []store.State{store.Probed, store.Matched, store.AwaitingConfirmation} {
		t.Run(string(state), func(t *testing.T) {
			e := newEnv(t)
			expires := e.clk.Now().Add(jobRetention)
			j := e.jobIn(state, expires)
			never := e.jobIn(state, time.Time{})

			e.clk.Advance(jobRetention - second)
			e.run()
			if got := e.state(j.ID); got != state {
				t.Fatalf("just before ExpiresAt: %s", got)
			}
			e.clk.Advance(2 * second)
			e.run()
			if got := e.state(j.ID); got != store.Cancelled {
				t.Fatalf("just after ExpiresAt: %s", got)
			}
			if !e.hasAudit(j.ID, "job.expired") {
				t.Error("no job.expired audit entry")
			}
			if got := e.state(never.ID); got != state {
				t.Errorf("a job without ExpiresAt is %s, want %s", got, state)
			}
		})
	}
}

func TestFailedJobCancelledAfterJobRetention(t *testing.T) {
	e := newEnv(t)
	j := e.jobIn(store.Failed, time.Time{})
	e.clk.Advance(jobRetention - second)
	e.run()
	if got := e.state(j.ID); got != store.Failed {
		t.Fatalf("just before the limit: %s", got)
	}
	e.clk.Advance(2 * second)
	e.run()
	if got := e.state(j.ID); got != store.Cancelled {
		t.Fatalf("just after the limit: %s", got)
	}
	if !e.hasAudit(j.ID, "job.expired") {
		t.Error("no job.expired audit entry")
	}
}

func TestFinishedJobsDeletedAfterFinishedJobRetention(t *testing.T) {
	for _, state := range []store.State{store.Done, store.Cancelled} {
		t.Run(string(state), func(t *testing.T) {
			e := newEnv(t)
			j := e.jobIn(state, time.Time{})
			if _, err := e.st.AddFile(context.Background(), store.File{JobID: j.ID, Path: "files/a.mkv", Kind: store.KindVideo, Status: store.FileOK}); err != nil {
				t.Fatal(err)
			}
			if err := e.st.Audit(context.Background(), store.AuditEntry{Actor: "alice", Action: "job.created", JobID: j.ID}); err != nil {
				t.Fatal(err)
			}
			write(t, e.path("jobs", j.ID, "files", "a.mkv"), "x", time.Time{})

			e.clk.Advance(finishedKeep - second)
			e.run()
			if _, err := e.st.Job(context.Background(), j.ID); err != nil {
				t.Fatalf("just before the limit: %v", err)
			}
			if !e.exists("jobs", j.ID) {
				t.Fatal("directory removed too early")
			}
			e.clk.Advance(2 * second)
			e.run()
			if _, err := e.st.Job(context.Background(), j.ID); err == nil {
				t.Fatal("job row kept past the limit")
			}
			if e.exists("jobs", j.ID) {
				t.Error("the directory that was left must be removed")
			}
			if files, _ := e.st.Files(context.Background(), j.ID); len(files) != 0 {
				t.Errorf("files kept: %v", files)
			}
			log, _ := e.st.AuditLog(context.Background(), j.ID)
			var created, deleted bool
			for _, a := range log {
				created = created || a.Action == "job.created"
				deleted = deleted || (a.Action == "job.deleted" && a.Actor == "system")
			}
			if !created || !deleted {
				t.Errorf("audit entries must stay and include job.deleted: %+v", log)
			}
		})
	}
}

func TestCancelledByRetentionIsNotDeletedInTheSameSweep(t *testing.T) {
	e := newEnv(t)
	j := e.jobIn(store.Failed, time.Time{})
	e.clk.Advance(finishedKeep + jobRetention) // old enough for both rules
	e.run()
	if got := e.state(j.ID); got != store.Cancelled {
		t.Fatalf("%s", got)
	}
}

func TestJobsInOtherStatesAreNeverTouched(t *testing.T) {
	e := newEnv(t)
	states := []store.State{store.Uploaded, store.Probing, store.Importing, store.Imported, store.PostProcessing, store.Stalled}
	ids := map[store.State]string{}
	for _, s := range states {
		j := e.jobIn(s, e.clk.Now().Add(time.Hour)) // an ExpiresAt in the past must not matter either
		ids[s] = j.ID
		write(t, e.path("jobs", j.ID, "files", "a.mkv"), "x", e.clk.Now().Add(-1000*24*time.Hour))
	}
	e.clk.Advance(1000 * 24 * time.Hour)
	e.run()
	for _, s := range states {
		if got := e.state(ids[s]); got != s {
			t.Errorf("%s job is now %s", s, got)
		}
		if !e.exists("jobs", ids[s], "files", "a.mkv") {
			t.Errorf("%s job lost its files", s)
		}
		if acts := e.auditActions(ids[s]); len(acts) != 0 {
			t.Errorf("%s job has audit entries %v", s, acts)
		}
	}
}

func TestOrphanFilesInIncoming(t *testing.T) {
	e := newEnv(t)
	now := e.clk.Now()
	job := e.jobIn(store.Uploading, time.Time{})
	known := e.upload(job.ID, now.Add(-time.Hour)) // old data, but its row is current
	write(t, e.path(".incoming", "old.bin"), "x", now.Add(-time.Hour-second))
	write(t, e.path(".incoming", randID()), "x", now.Add(-time.Hour-second))
	young := randID()
	write(t, e.path(".incoming", young), "x", now.Add(-time.Hour+second))
	write(t, e.path(".incoming", "odd-dir", "inner"), "x", now.Add(-72*time.Hour))
	if err := os.Chtimes(e.path(".incoming", "odd-dir"), now.Add(-72*time.Hour), now.Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}

	e.run()

	if !e.exists(".incoming", known.ID) || !e.exists(".incoming", known.ID+".info") {
		t.Error("files of a known upload were deleted")
	}
	if e.exists(".incoming", "old.bin") {
		t.Error("an unknown file older than an hour must be deleted")
	}
	if !e.exists(".incoming", young) {
		t.Error("an unknown file younger than an hour must stay")
	}
	if !e.exists(".incoming", "odd-dir", "inner") {
		t.Error("a directory in .incoming must be left alone")
	}
	logs := e.log.String()
	if strings.Count(logs, "orphan_upload") != 2 {
		t.Errorf("want two orphan_upload log lines:\n%s", logs)
	}
	if n := strings.Count(logs, "retention_ignored"); n != 1 {
		t.Errorf("odd-dir must be logged once, got %d", n)
	}
	e.run()
	if n := strings.Count(e.log.String(), "retention_ignored"); n != 1 {
		t.Errorf("a second sweep logged it again: %d", n)
	}
	// Each deletion has its audit entry.
	log, _ := e.st.AuditLog(context.Background(), "")
	var n int
	for _, a := range log {
		if a.Action == "upload.orphan_deleted" && a.Actor == "system" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d audit entries for 2 deleted orphans", n)
	}
}

func TestOrphanJobDirectories(t *testing.T) {
	e := newEnv(t)
	now := e.clk.Now()
	known := e.jobIn(store.Probed, now.Add(jobRetention))
	write(t, e.path("jobs", known.ID, "files", "a.mkv"), "x", now.Add(-100*24*time.Hour))
	if err := os.Chtimes(e.path("jobs", known.ID), now.Add(-100*24*time.Hour), now.Add(-100*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	young, old := randID(), randID()
	write(t, e.path("jobs", young, "files", "a.mkv"), "x", time.Time{})
	write(t, e.path("jobs", old, "files", "a.mkv"), "x", time.Time{})
	chtimes := func(id string, mt time.Time) {
		if err := os.Chtimes(e.path("jobs", id), mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	chtimes(young, now.Add(-jobRetention+second))
	chtimes(old, now.Add(-jobRetention-second))
	write(t, e.path("jobs", "not-a-job-id", "x"), "x", now.Add(-100*24*time.Hour))
	write(t, e.path("jobs", "stray.txt"), "x", now.Add(-100*24*time.Hour))

	e.run()

	if !e.exists("jobs", known.ID, "files", "a.mkv") {
		t.Error("the directory of a known job was deleted")
	}
	if !e.exists("jobs", young, "files", "a.mkv") {
		t.Error("an orphan directory younger than upload.jobRetention must survive")
	}
	if e.exists("jobs", old) {
		t.Error("an orphan directory older than upload.jobRetention must be deleted")
	}
	if !e.exists("jobs", "not-a-job-id", "x") || !e.exists("jobs", "stray.txt") {
		t.Error("unknown entries in jobs/ must be left alone")
	}
	logs := e.log.String()
	if !strings.Contains(logs, "orphan_job_dir") {
		t.Errorf("orphan_job_dir not logged:\n%s", logs)
	}
	if !e.hasAudit(old, "job.orphan_deleted") {
		t.Error("no audit entry for the deleted orphan directory")
	}
	e.run()
	if n := strings.Count(e.log.String(), "retention_ignored"); n != 2 {
		t.Errorf("unknown entries logged %d times, want once each (2)", n)
	}
	if n := strings.Count(e.log.String(), "action=kept"); n != 1 {
		t.Errorf("the kept orphan directory is logged %d times, want once", n)
	}
	// When the young one ages past the limit it goes.
	e.clk.Advance(2 * second)
	e.run()
	if e.exists("jobs", young) {
		t.Error("orphan directory kept past the limit")
	}
}

func TestSymlinksAreNeverFollowedOrRemoved(t *testing.T) {
	e := newEnv(t)
	now := e.clk.Now()
	old := now.Add(-1000 * 24 * time.Hour)

	outsideDir := filepath.Join(e.base, "library")
	outsideFile := filepath.Join(e.base, "precious.txt")
	write(t, filepath.Join(outsideDir, "film.mkv"), "keep", old)
	write(t, outsideFile, "keep", old)

	link := func(target, name string) {
		if err := os.Symlink(target, name); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	link(outsideFile, e.path(".incoming", "link-to-file"))
	link(outsideDir, e.path(".incoming", randID()))
	jobLink := randID()
	link(outsideDir, e.path("jobs", jobLink))
	link(outsideFile, e.path("jobs", "link-to-file"))
	// A symlink inside an orphan job directory, and one inside a finished job's directory.
	orphan := randID()
	write(t, e.path("jobs", orphan, "files", "a.mkv"), "x", old)
	link(outsideDir, e.path("jobs", orphan, "files", "escape"))
	if err := os.Chtimes(e.path("jobs", orphan), old, old); err != nil {
		t.Fatal(err)
	}
	finished := e.jobIn(store.Cancelled, time.Time{})
	write(t, e.path("jobs", finished.ID, "files", "a.mkv"), "x", old)
	link(outsideDir, e.path("jobs", finished.ID, "files", "escape"))
	e.clk.Advance(finishedKeep + second)

	e.run()

	for _, p := range []string{filepath.Join(outsideDir, "film.mkv"), outsideFile} {
		b, err := os.ReadFile(p)
		if err != nil || string(b) != "keep" {
			t.Errorf("%s was touched: %q %v", p, b, err)
		}
	}
	if !e.exists(".incoming", "link-to-file") || !e.exists("jobs", jobLink) || !e.exists("jobs", "link-to-file") {
		t.Error("symlinks in .incoming/ and jobs/ must be left in place")
	}
	if e.exists("jobs", orphan) || e.exists("jobs", finished.ID) {
		t.Error("directories holding symlinks should still be deleted (as links, not targets)")
	}
}

func TestSymlinkedTopLevelDirectoriesAreIgnored(t *testing.T) {
	e := newEnv(t)
	old := e.clk.Now().Add(-1000 * 24 * time.Hour)
	outside := filepath.Join(e.base, "elsewhere")
	write(t, filepath.Join(outside, "a"), "keep", old)
	write(t, filepath.Join(outside, randID(), "b"), "keep", old)
	for _, d := range []string{".incoming", "jobs"} {
		if err := os.RemoveAll(e.path(d)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, e.path(d)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	before := snapshot(t, outside)
	e.run()
	if after := snapshot(t, outside); after != before {
		t.Errorf("the target of a symlinked .incoming or jobs was touched:\n%s\n%s", before, after)
	}
}

func TestMissingSentinelMeansNothingIsDone(t *testing.T) {
	e := newEnv(t)
	now := e.clk.Now()
	job := e.jobIn(store.Uploading, time.Time{})
	u := e.upload(job.ID, now.Add(-10*idleExpiry))
	failed := e.jobIn(store.Failed, time.Time{})
	orphanFile := e.path(".incoming", "junk")
	write(t, orphanFile, "x", now.Add(-48*time.Hour))
	orphanDir := randID()
	write(t, e.path("jobs", orphanDir, "f"), "x", time.Time{})
	if err := os.Chtimes(e.path("jobs", orphanDir), now.Add(-1000*24*time.Hour), now.Add(-1000*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(1000 * 24 * time.Hour)

	if err := os.Remove(e.cfg.Paths.Sentinels[0]); err != nil {
		t.Fatal(err)
	}
	e.run()

	if !e.uploadRowExists(u.ID) || !e.exists(".incoming", u.ID) || !e.exists(".incoming", "junk") || !e.exists("jobs", orphanDir, "f") {
		t.Error("something was deleted although the sentinel is missing")
	}
	if e.state(job.ID) != store.Uploading || e.state(failed.ID) != store.Failed {
		t.Error("a job changed although the sentinel is missing")
	}
	if log, _ := e.st.AuditLog(context.Background(), ""); len(log) != 0 {
		t.Errorf("audit entries written: %v", log)
	}
	if !strings.Contains(e.log.String(), "retention_skipped") || !strings.Contains(e.log.String(), ".mounted") {
		t.Errorf("the skip is not logged:\n%s", e.log.String())
	}

	// With the sentinel back, the same sweep acts.
	write(t, e.cfg.Paths.Sentinels[0], "x", time.Time{})
	e.run()
	if e.uploadRowExists(u.ID) || e.exists(".incoming", "junk") {
		t.Error("the sweep did not resume once the sentinel was back")
	}
}

// TestNothingOutsideIncomingAndJobsIsTouched sweeps a tree full of expired material and
// compares everything else byte for byte.
func TestNothingOutsideIncomingAndJobsIsTouched(t *testing.T) {
	e := newEnv(t)
	now := e.clk.Now()
	old := now.Add(-1000 * 24 * time.Hour)
	write(t, e.path("stray.mkv"), "x", old)
	write(t, e.path("library", "Film (2020)", "Film.mkv"), "x", old)
	write(t, e.path(".incoming-other", "x"), "x", old)
	write(t, e.path("jobsx", "x"), "x", old)
	write(t, filepath.Join(e.base, "sibling", "x"), "x", old)

	e.upload(e.jobIn(store.Uploading, time.Time{}).ID, old)
	write(t, e.path(".incoming", "junk"), "x", old)
	write(t, e.path("jobs", randID(), "f"), "x", old)
	e.jobIn(store.Failed, time.Time{})
	e.clk.Advance(1000 * 24 * time.Hour)

	outside := func() string {
		var out []string
		for _, p := range []string{e.path("stray.mkv"), e.path("library"), e.path(".incoming-other"), e.path("jobsx"), e.path(".mounted"), filepath.Join(e.base, "sibling")} {
			out = append(out, snapshot(t, p))
		}
		return strings.Join(out, "|")
	}
	before := outside()
	e.run()
	if after := outside(); after != before {
		t.Errorf("files outside .incoming/ and jobs/ changed:\n%s\n%s", before, after)
	}
	if e.exists(".incoming", "junk") {
		t.Error("the sweep did not run")
	}
}

func snapshot(t *testing.T, root string) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(root, p)
			sb.WriteString(rel + "=" + string(b) + ";")
		}
		return nil
	})
	if err != nil {
		sb.WriteString("error:" + err.Error())
	}
	return sb.String()
}

func TestRunSweepsAtStartAndEveryInterval(t *testing.T) {
	e := newEnv(t)
	e.cfg.Upload.SweepInterval = config.Duration(20 * time.Millisecond)
	log := slog.New(slog.NewTextHandler(e.log, nil))
	e.sweep = retention.New(e.cfg, e.st, e.queue, log, e.clk.Now)

	job := e.jobIn(store.Uploading, time.Time{})
	first := e.upload(job.ID, e.clk.Now().Add(-10*idleExpiry))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.sweep.Run(ctx) }()

	waitFor(t, "the start-up sweep", func() bool { return !e.uploadRowExists(first.ID) })
	second := e.upload(job.ID, e.clk.Now().Add(-10*idleExpiry))
	waitFor(t, "a later sweep", func() bool { return !e.uploadRowExists(second.ID) })

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSettingsDefaults(t *testing.T) {
	d := config.Defaults().Upload
	if d.SweepInterval.D() != 15*time.Minute || d.FinishedJobRetention.D() != 30*24*time.Hour {
		t.Errorf("defaults: sweepInterval %v, finishedJobRetention %v", d.SweepInterval, d.FinishedJobRetention)
	}
}

func TestFinishedJobTasksAreCancelledAndAcknowledgedBeforeDeletion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	j := e.jobIn(store.Cancelled, time.Time{})
	f, err := e.st.AddFile(ctx, store.File{JobID: j.ID, Path: "archives/s.zip", Kind: store.KindArchive, Status: store.FileOK})
	if err != nil {
		t.Fatal(err)
	}
	probe := j.ID + "-1-p"
	extract := j.ID + "-1-x" + strconv.FormatInt(f.ID, 10)
	for _, id := range []string{probe, extract} {
		task := spool.Task{V: spool.Version, ID: id, Kind: spool.KindProbe, Job: j.ID, Created: e.clk.Now(), Args: []byte("{}")}
		if err := e.queue.Submit(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	e.queue.Complete(probe, map[string]string{}) // a late result
	// extract stays waiting.

	e.clk.Advance(finishedKeep + second)
	e.run()

	if _, ok, _ := e.queue.Result(ctx, probe); ok {
		t.Error("the late result was not acknowledged")
	}
	for _, task := range e.queue.Tasks() {
		t.Errorf("task %s was left behind", task.ID)
	}
	if _, err := e.st.Job(ctx, j.ID); err == nil {
		t.Error("job not deleted")
	}
}
