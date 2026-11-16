package store_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

func stepClock() func() time.Time {
	var mu sync.Mutex
	t := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t = t.Add(time.Millisecond)
		return t
	}
}

func open(t *testing.T, dir string, log *slog.Logger) store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), store.Options{Dir: dir, Log: log, Now: stepClock()})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSQLite(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store { return open(t, t.TempDir(), nil) })
}

// Every reading the same instant: ordering must not rely on the clock.
func TestSQLiteFixedClock(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := store.Open(t.Context(), store.Options{Dir: t.TempDir(), Now: func() time.Time { return at }})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestSQLiteRealClock(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		s, err := store.Open(t.Context(), store.Options{Dir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// A job in each of the 13 states, with every field set, survives closing and re-opening.
func TestReopenEachState(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	s := open(t, dir, nil)
	expires := time.Date(2030, 1, 2, 3, 4, 5, 6, time.UTC)
	want := map[store.State]store.Job{}
	for _, st := range storetest.States {
		j, err := s.CreateJob(ctx, "user1")
		if err != nil {
			t.Fatal(err)
		}
		if st != store.Uploading {
			if j, err = s.Transition(ctx, j.ID, store.Uploading, store.Uploaded, store.Change{ExpiresAt: &expires}); err != nil {
				t.Fatal(err)
			}
			if j, err = storetest.Drive(ctx, s, j.ID, st); err != nil {
				t.Fatal(err)
			}
		}
		want[st] = j
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = open(t, dir, nil)
	defer func() { _ = s.Close() }()
	for st, w := range want {
		got, err := s.Job(ctx, w.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Fatalf("%s after re-open\n got %+v\nwant %+v", st, got, w)
		}
	}
	if n := len(want); n != 13 {
		t.Fatalf("%d states", n)
	}
}

func TestDatabaseFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh", "data")
	s := open(t, dir, nil)
	defer func() { _ = s.Close() }()
	st, err := os.Stat(filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("database mode %v, want 0600", st.Mode().Perm())
	}
	if d, err := os.Stat(dir); err != nil || d.Mode().Perm() != 0o700 {
		t.Fatalf("created data directory: %v, %v", d.Mode(), err)
	}
}

func TestAuditLogLine(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	s := open(t, t.TempDir(), log)
	defer func() { _ = s.Close() }()
	buf.Reset()
	err := s.Audit(t.Context(), store.AuditEntry{
		Actor: "user1", Email: "user1@example.com", Action: "job.created", JobID: "0123456789abcdef0123456789abcdef",
		Detail: map[string]string{"files": "2", "job": "shadow", "email": "user1@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "example.com") {
		t.Fatalf("the audit log line contains the email address: %s", buf.String())
	}
	var lines []map[string]any
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 1 {
		t.Fatalf("want one log line, got %v", lines)
	}
	l := lines[0]
	if l["msg"] != "audit" || l["action"] != "job.created" || l["actor"] != "user1" ||
		l["job"] != "0123456789abcdef0123456789abcdef" || l["files"] != "2" || l["detail_job"] != "shadow" {
		t.Fatalf("audit line %v", l)
	}
}

// Writes from many goroutines on different jobs never fail with a busy database.
func TestConcurrentWriters(t *testing.T) {
	ctx := t.Context()
	s := open(t, t.TempDir(), nil)
	defer func() { _ = s.Close() }()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			j, err := s.CreateJob(ctx, "user1")
			if err == nil {
				_, err = storetest.Drive(ctx, s, j.ID, store.Done)
			}
			if err == nil {
				_, err = s.Jobs(ctx, store.JobFilter{Owner: "user1"})
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	done, err := s.Jobs(ctx, store.JobFilter{States: []store.State{store.Done}})
	if err != nil || len(done) != 16 {
		t.Fatalf("%d done jobs, %v", len(done), err)
	}
}

// backupLine is the backup command in docs/backup.md.
var backupLine = regexp.MustCompile(`(?m)^sqlite3 (\S+) "\.backup '([^']+)'"$`)

// The documented backup command copies a database that is being written to.
func TestBackupCommandWhileInUse(t *testing.T) {
	sqlite3, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 is not installed (the dev image has it)")
	}
	doc, err := os.ReadFile("../../docs/backup.md")
	if err != nil {
		t.Fatal(err)
	}
	m := backupLine.FindSubmatch(doc)
	if m == nil {
		t.Fatalf("docs/backup.md has no line matching %s", backupLine)
	}
	if string(m[1]) != "/var/lib/fukku/fukku.db" {
		t.Fatalf("the documented command backs up %s, not the default database", m[1])
	}

	ctx := t.Context()
	dir := t.TempDir()
	s := open(t, dir, nil)
	defer func() { _ = s.Close() }()
	for range 20 {
		if _, err := s.CreateJob(ctx, "user1"); err != nil {
			t.Fatal(err)
		}
	}

	// Keep writing while the backup runs.
	writeCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var writeErr error
	wg.Go(func() {
		for writeCtx.Err() == nil {
			j, err := s.CreateJob(writeCtx, "user2")
			if err == nil {
				_, err = storetest.Drive(writeCtx, s, j.ID, store.Probed)
			}
			if err != nil && writeCtx.Err() == nil {
				writeErr = err
				return
			}
		}
	})
	time.Sleep(50 * time.Millisecond)

	dest := filepath.Join(t.TempDir(), "restore", store.FileName)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(ctx, sqlite3, filepath.Join(dir, store.FileName), ".backup '"+dest+"'").CombinedOutput()
	stop()
	wg.Wait()
	if err != nil {
		t.Fatalf("backup: %v: %s", err, out)
	}
	if writeErr != nil {
		t.Fatalf("a write failed during the backup: %v", writeErr)
	}

	check, err := exec.CommandContext(ctx, sqlite3, dest, "PRAGMA integrity_check").CombinedOutput()
	if err != nil || strings.TrimSpace(string(check)) != "ok" {
		t.Fatalf("integrity check of the copy: %v: %s", err, check)
	}
	restored := open(t, filepath.Dir(dest), nil)
	defer func() { _ = restored.Close() }()
	jobs, err := restored.Jobs(ctx, store.JobFilter{Owner: "user1"})
	if err != nil || len(jobs) != 20 {
		t.Fatalf("restored copy has %d of 20 jobs: %v", len(jobs), err)
	}
}

func TestOpenRefusesRelativeDir(t *testing.T) {
	if _, err := store.Open(t.Context(), store.Options{Dir: "data"}); err == nil {
		t.Fatal("a relative data directory was accepted")
	}
	if _, err := store.Open(t.Context(), store.Options{}); err == nil {
		t.Fatal("an empty data directory was accepted")
	}
}

func TestOpenRefusesUnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err := store.Open(t.Context(), store.Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("got %v, want a not-writable error", err)
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	all := []error{store.ErrNotFound, store.ErrConflict, store.ErrInvalidTransition, store.ErrExists}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("%v is %v", a, b)
			}
		}
	}
}

// The M2 data survives closing and re-opening the database.
func TestReopenM2(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	s := open(t, dir, nil)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	job, err := s.CreateJob(ctx, "user1")
	check(err)
	job, err = storetest.Drive(ctx, s, job.ID, store.PostProcessing)
	check(err)
	f, err := s.AddFile(ctx, store.File{JobID: job.ID, Path: "files/a.mkv", Kind: store.KindVideo, Size: 1})
	check(err)
	at := time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC)
	check(s.SetTarget(ctx, job.ID, store.Target{App: "sonarr", TitleID: 12, Title: "Some Show", Year: 2010,
		Options: json.RawMessage(`{"seriesType":"standard"}`), MatchedBy: "user1", MatchedAt: at}))
	check(s.PutRows(ctx, job.ID, []store.Row{{ID: "r1", FileID: f.ID, Skip: true, Override: true, Result: store.ResultImported,
		Edit: json.RawMessage(`{"seasonNumber":1}`), Proposal: json.RawMessage(`{"a":1}`)}}))
	check(s.PutImport(ctx, store.Import{JobID: job.ID, CommandID: 9, SubmittedAt: at, FinishedAt: at.Add(time.Second)}))
	check(s.PutPostStep(ctx, store.PostStep{JobID: job.ID, Name: "subtitles", Status: store.StepFailed, Attempts: 5, Detail: "x", UpdatedAt: at}))
	check(s.PutPostStep(ctx, store.PostStep{JobID: job.ID, Name: "mediaServerRefresh", Status: store.StepOK, UpdatedAt: at}))
	check(s.Close())

	s = open(t, dir, nil)
	defer func() { _ = s.Close() }()
	tg, err := s.Target(ctx, job.ID)
	check(err)
	if tg.App != "sonarr" || tg.TitleID != 12 || tg.Title != "Some Show" || tg.Year != 2010 || tg.MatchedBy != "user1" ||
		!tg.MatchedAt.Equal(at) || string(tg.Options) != `{"seriesType":"standard"}` {
		t.Fatalf("target after re-open %+v", tg)
	}
	rows, err := s.Rows(ctx, job.ID)
	check(err)
	if len(rows) != 1 || rows[0].ID != "r1" || rows[0].FileID != f.ID || !rows[0].Skip || !rows[0].Override ||
		rows[0].Result != store.ResultImported || string(rows[0].Edit) != `{"seasonNumber":1}` || string(rows[0].Proposal) != `{"a":1}` {
		t.Fatalf("rows after re-open %+v", rows)
	}
	im, err := s.Import(ctx, job.ID)
	check(err)
	if im.CommandID != 9 || !im.SubmittedAt.Equal(at) || !im.FinishedAt.Equal(at.Add(time.Second)) {
		t.Fatalf("import after re-open %+v", im)
	}
	steps, err := s.PostSteps(ctx, job.ID)
	check(err)
	if len(steps) != 2 || steps[0].Name != "subtitles" || steps[0].Status != store.StepFailed || steps[0].Attempts != 5 ||
		steps[1].Name != "mediaServerRefresh" || !steps[1].UpdatedAt.Equal(at) {
		t.Fatalf("steps after re-open %+v", steps)
	}
	active, err := s.ActiveJobsFor(ctx, "sonarr", 12)
	check(err)
	if len(active) != 1 || active[0] != job {
		t.Fatalf("active jobs after re-open %+v, want %+v", active, job)
	}
}
