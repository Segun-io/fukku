package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/store"
)

// Run is the conformance suite of store.Store. open must return a new, empty store whose
// clock does not go backwards; Run closes it. Fake and the SQLite store both pass it.
func Run(t *testing.T, open func(t *testing.T) store.Store) {
	t.Helper()
	tests := []struct {
		name string
		fn   func(t *testing.T, s store.Store)
	}{
		{"CreateJob", testCreateJob},
		{"TransitionTable", testTransitionTable},
		{"Conflict", testConflict},
		{"Race", testRace},
		{"RetryRace", testRetryRace},
		{"FailAndRetry", testFailAndRetry},
		{"StallAndResume", testStallAndResume},
		{"ErrorCodeRequired", testErrorCodeRequired},
		{"FailedLeavesOnlyForResumeOrCancel", testFailedExits},
		{"Events", testEvents},
		{"ExpiresAt", testExpiresAt},
		{"Jobs", testJobs},
		{"JobsDefaultLimit", testJobsDefaultLimit},
		{"Files", testFiles},
		{"Uploads", testUploads},
		{"DeleteJob", testDeleteJob},
		{"Audit", testAudit},
		{"Ping", testPing},
	}
	tests = append(tests, m2Tests...)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := open(t)
			t.Cleanup(func() { _ = s.Close() })
			tc.fn(t, s)
		})
	}
}

var hexID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// must(f())(t) is f's value, or the test fails on f's error.
func must[T any](v T, err error) func(t *testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func newJob(t *testing.T, s store.Store, owner string) store.Job {
	t.Helper()
	return must(s.CreateJob(t.Context(), owner))(t)
}

func drive(t *testing.T, s store.Store, id string, to store.State) store.Job {
	t.Helper()
	return must(Drive(t.Context(), s, id, to))(t)
}

// sameJob compares jobs field by field, times with Equal.
func sameJob(a, b store.Job) bool {
	return a.ID == b.ID && a.Owner == b.Owner && a.State == b.State && a.Resume == b.Resume &&
		a.Attempt == b.Attempt && a.ErrorCode == b.ErrorCode && a.ErrorText == b.ErrorText &&
		a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt) && a.ExpiresAt.Equal(b.ExpiresAt)
}

func checkStored(t *testing.T, s store.Store, want store.Job) {
	t.Helper()
	got := must(s.Job(t.Context(), want.ID))(t)
	if !sameJob(got, want) {
		t.Fatalf("stored job\n got %+v\nwant %+v", got, want)
	}
}

func history(t *testing.T, s store.Store, id string) []store.Event {
	t.Helper()
	return must(s.History(t.Context(), id))(t)
}

// allowed is what Transition must accept: the table, plus leaving failed or stalled for
// the job's Resume state.
func allowed(j store.Job, to store.State) bool {
	if store.CanTransition(j.State, to) {
		return true
	}
	return (j.State == store.Failed || j.State == store.Stalled) && j.Resume != "" && to == j.Resume
}

// ---- jobs -----------------------------------------------------------------------------

func testCreateJob(t *testing.T, s store.Store) {
	a := newJob(t, s, "user1")
	b := newJob(t, s, "user1")
	if !hexID.MatchString(a.ID) || a.ID == b.ID {
		t.Fatalf("ids %q, %q: want two different 32-character lower-case hex ids", a.ID, b.ID)
	}
	if a.Owner != "user1" || a.State != store.Uploading || a.Attempt != 1 || a.Resume != "" ||
		a.ErrorCode != "" || a.ErrorText != "" {
		t.Fatalf("new job %+v", a)
	}
	if a.CreatedAt.IsZero() || !a.UpdatedAt.Equal(a.CreatedAt) || !a.ExpiresAt.IsZero() {
		t.Fatalf("new job times %+v", a)
	}
	checkStored(t, s, a)
	if h := history(t, s, a.ID); len(h) != 0 {
		t.Fatalf("history of a new job: %+v", h)
	}
	if _, err := s.Job(t.Context(), "0123456789abcdef0123456789abcdef"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown job: %v, want ErrNotFound", err)
	}
}

// testTransitionTable tries every pair of states (13 × 13) and checks Transition against
// CanTransition and the Resume rule.
func testTransitionTable(t *testing.T, s store.Store) {
	ctx := t.Context()
	for _, from := range States {
		for _, to := range States {
			t.Run(fmt.Sprintf("%s→%s", from, to), func(t *testing.T) {
				j := drive(t, s, newJob(t, s, "user1").ID, from)
				before := len(history(t, s, j.ID))
				c := store.Change{Actor: "user1", Note: "table", ErrorCode: "test_error", ErrorText: "detail"}
				got, err := s.Transition(ctx, j.ID, from, to, c)
				if !allowed(j, to) {
					if !errors.Is(err, store.ErrInvalidTransition) {
						t.Fatalf("got %v, want ErrInvalidTransition", err)
					}
					checkStored(t, s, j)
					if n := len(history(t, s, j.ID)); n != before {
						t.Fatalf("a refused transition wrote an event (%d → %d)", before, n)
					}
					return
				}
				if err != nil {
					t.Fatalf("allowed transition refused: %v", err)
				}
				if got.State != to || got.UpdatedAt.Before(j.UpdatedAt) {
					t.Fatalf("after the transition: %+v", got)
				}
				checkStored(t, s, got)
				h := history(t, s, j.ID)
				if len(h) != before+1 {
					t.Fatalf("history has %d events, want %d", len(h), before+1)
				}
				if e := h[len(h)-1]; e.From != from || e.To != to || e.Actor != "user1" || e.Note != "table" || !e.At.Equal(got.UpdatedAt) {
					t.Fatalf("event %+v", e)
				}
			})
		}
	}
}

func testConflict(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := newJob(t, s, "user1")
	// The job is uploading: a transition from another state conflicts, valid or not.
	if _, err := s.Transition(ctx, j.ID, store.Uploaded, store.Probing, store.Change{}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
	j = drive(t, s, j.ID, store.Uploaded)
	if _, err := s.Transition(ctx, j.ID, store.Uploading, store.Uploaded, store.Change{}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a repeated transition: %v, want ErrConflict", err)
	}
	checkStored(t, s, j)
	if n := len(history(t, s, j.ID)); n != 1 {
		t.Fatalf("history has %d events, want 1", n)
	}
	if _, err := s.Transition(ctx, "0123456789abcdef0123456789abcdef", store.Uploading, store.Uploaded, store.Change{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown job: %v, want ErrNotFound", err)
	}
}

// race runs n copies of the same transition at once and returns how many succeeded; the
// others must have got ErrConflict.
func race(t *testing.T, s store.Store, id string, from, to store.State, c store.Change) int {
	t.Helper()
	const n = 8
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		errs  = make([]error, n)
	)
	for i := range n {
		wg.Go(func() {
			<-start
			_, errs[i] = s.Transition(context.Background(), id, from, to, c)
		})
	}
	close(start)
	wg.Wait()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, store.ErrConflict):
			t.Fatalf("a losing racer got %v, want ErrConflict", err)
		}
	}
	return won
}

func testRace(t *testing.T, s store.Store) {
	j := newJob(t, s, "user1")
	if won := race(t, s, j.ID, store.Uploading, store.Uploaded, store.Change{}); won != 1 {
		t.Fatalf("%d racers won, want exactly 1", won)
	}
	if h := history(t, s, j.ID); len(h) != 1 {
		t.Fatalf("history %+v, want one event", h)
	}
	if got := must(s.Job(t.Context(), j.ID))(t); got.State != store.Uploaded {
		t.Fatalf("state %s", got.State)
	}
}

func testRetryRace(t *testing.T, s store.Store) {
	j := drive(t, s, newJob(t, s, "user1").ID, store.Probing)
	j = must(s.Transition(t.Context(), j.ID, store.Probing, store.Failed, store.Change{ErrorCode: "worker_failed"}))(t)
	if won := race(t, s, j.ID, store.Failed, store.Uploaded, store.Change{Actor: "user1"}); won != 1 {
		t.Fatalf("%d retries won, want exactly 1", won)
	}
	if got := must(s.Job(t.Context(), j.ID))(t); got.State != store.Uploaded || got.Attempt != 2 {
		t.Fatalf("after racing retries: %+v, want uploaded, attempt 2", got)
	}
}

func testFailAndRetry(t *testing.T, s store.Store) {
	ctx := t.Context()
	for _, from := range States {
		if !store.CanTransition(from, store.Failed) || from == store.Stalled { // stalled: testStallAndResume
			continue
		}
		t.Run(string(from), func(t *testing.T) {
			j := drive(t, s, newJob(t, s, "user1").ID, from)
			want := store.ResumeState(from)
			for attempt := 1; attempt <= 2; attempt++ {
				failed := must(s.Transition(ctx, j.ID, j.State, store.Failed, store.Change{ErrorCode: "boom", ErrorText: "it broke"}))(t)
				if failed.Resume != want || failed.Attempt != attempt || failed.ErrorCode != "boom" || failed.ErrorText != "it broke" {
					t.Fatalf("failed job %+v, want resume %s, attempt %d and the error", failed, want, attempt)
				}
				checkStored(t, s, failed)
				j = must(s.Transition(ctx, j.ID, store.Failed, want, store.Change{Actor: "user1"}))(t)
				if j.State != want || j.Attempt != attempt+1 || j.Resume != "" || j.ErrorCode != "" || j.ErrorText != "" {
					t.Fatalf("retried job %+v, want %s, attempt %d, no resume, no error", j, want, attempt+1)
				}
				checkStored(t, s, j)
				// Back to where it failed, so it can fail there again.
				j = drive(t, s, j.ID, from)
			}
		})
	}
}

func testStallAndResume(t *testing.T, s store.Store) {
	ctx := t.Context()
	for _, from := range States {
		if !store.CanTransition(from, store.Stalled) {
			continue
		}
		t.Run(string(from), func(t *testing.T) {
			j := drive(t, s, newJob(t, s, "user1").ID, from)
			stalled := must(s.Transition(ctx, j.ID, from, store.Stalled, store.Change{ErrorCode: "worker_unavailable", ErrorText: "heartbeat old"}))(t)
			if stalled.Resume != from || stalled.Attempt != 1 || stalled.ErrorCode != "worker_unavailable" || stalled.ErrorText != "heartbeat old" {
				t.Fatalf("stalled job %+v", stalled)
			}
			checkStored(t, s, stalled)
			// It resumes by itself: same attempt, error cleared.
			j = must(s.Transition(ctx, j.ID, store.Stalled, from, store.Change{}))(t)
			if j.State != from || j.Attempt != 1 || j.Resume != "" || j.ErrorCode != "" || j.ErrorText != "" {
				t.Fatalf("resumed job %+v", j)
			}
			checkStored(t, s, j)

			// Stalled for good: it fails as if it had failed where it stalled.
			must(s.Transition(ctx, j.ID, from, store.Stalled, store.Change{ErrorCode: "worker_unavailable"}))(t)
			failed := must(s.Transition(ctx, j.ID, store.Stalled, store.Failed, store.Change{ErrorCode: "gave_up", ErrorText: "too long"}))(t)
			if failed.Resume != store.ResumeState(from) || failed.Attempt != 1 || failed.ErrorCode != "gave_up" || failed.ErrorText != "too long" {
				t.Fatalf("stalled then failed %+v, want resume %s", failed, store.ResumeState(from))
			}
			j = must(s.Transition(ctx, j.ID, store.Failed, store.ResumeState(from), store.Change{}))(t)
			if j.Attempt != 2 || j.ErrorCode != "" || j.Resume != "" {
				t.Fatalf("retried job %+v", j)
			}

			// Cancelling a stalled job clears its error too.
			j = drive(t, s, j.ID, from)
			must(s.Transition(ctx, j.ID, from, store.Stalled, store.Change{ErrorCode: "worker_unavailable"}))(t)
			j = must(s.Transition(ctx, j.ID, store.Stalled, store.Cancelled, store.Change{Actor: "user1"}))(t)
			if j.Resume != "" || j.ErrorCode != "" || j.ErrorText != "" {
				t.Fatalf("cancelled job %+v", j)
			}
		})
	}
}

func testErrorCodeRequired(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.Probing)
	for _, to := range []store.State{store.Failed, store.Stalled} {
		if _, err := s.Transition(ctx, j.ID, store.Probing, to, store.Change{ErrorText: "no code"}); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("to %s without an error code: %v, want ErrInvalidTransition", to, err)
		}
	}
	checkStored(t, s, j)
}

func testFailedExits(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.Probing)
	j = must(s.Transition(ctx, j.ID, store.Probing, store.Failed, store.Change{ErrorCode: "worker_failed"}))(t)
	// A failure in probing retries from uploaded, not from probing.
	if _, err := s.Transition(ctx, j.ID, store.Failed, store.Probing, store.Change{}); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("failed → probing: %v, want ErrInvalidTransition", err)
	}
	j = must(s.Transition(ctx, j.ID, store.Failed, store.Cancelled, store.Change{Actor: "user1"}))(t)
	if j.Attempt != 1 || j.Resume != "" || j.ErrorCode != "" {
		t.Fatalf("cancelled job %+v", j)
	}
	for _, to := range States {
		if _, err := s.Transition(ctx, j.ID, store.Cancelled, to, store.Change{ErrorCode: "x"}); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("cancelled → %s: %v, want ErrInvalidTransition", to, err)
		}
	}
}

func testEvents(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := newJob(t, s, "user1")
	steps := []struct {
		to store.State
		c  store.Change
	}{
		{store.Uploaded, store.Change{Actor: "user1", Note: "submitted"}},
		{store.Probing, store.Change{}},
		{store.Stalled, store.Change{ErrorCode: "worker_unavailable"}},
		{store.Probing, store.Change{Note: "worker back"}},
	}
	from := j.State
	var last store.Job
	for _, st := range steps {
		next := must(s.Transition(ctx, j.ID, from, st.to, st.c))(t)
		if next.UpdatedAt.Before(last.UpdatedAt) || !next.CreatedAt.Equal(j.CreatedAt) {
			t.Fatalf("times after %s: %+v", st.to, next)
		}
		last, from = next, st.to
	}
	h := history(t, s, j.ID)
	want := []store.Event{
		{JobID: j.ID, From: store.Uploading, To: store.Uploaded, Actor: "user1", Note: "submitted"},
		{JobID: j.ID, From: store.Uploaded, To: store.Probing, Actor: "system"},
		{JobID: j.ID, From: store.Probing, To: store.Stalled, Actor: "system"},
		{JobID: j.ID, From: store.Stalled, To: store.Probing, Actor: "system", Note: "worker back"},
	}
	if len(h) != len(want) {
		t.Fatalf("history %+v", h)
	}
	for i, e := range h {
		if e.At.IsZero() || (i > 0 && e.At.Before(h[i-1].At)) {
			t.Fatalf("event %d time %v", i, e.At)
		}
		e.At = time.Time{}
		if e != want[i] {
			t.Fatalf("event %d\n got %+v\nwant %+v", i, e, want[i])
		}
	}
	if !h[len(h)-1].At.Equal(last.UpdatedAt) {
		t.Fatalf("last event at %v, job updated at %v", h[len(h)-1].At, last.UpdatedAt)
	}
	other := newJob(t, s, "user2")
	if h := history(t, s, other.ID); len(h) != 0 {
		t.Fatalf("another job's history %+v", h)
	}
}

func testExpiresAt(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := newJob(t, s, "user1")
	at := time.Date(2030, 1, 2, 3, 4, 5, 6, time.FixedZone("x", 3600))
	j = must(s.Transition(ctx, j.ID, store.Uploading, store.Uploaded, store.Change{ExpiresAt: &at}))(t)
	if !j.ExpiresAt.Equal(at) {
		t.Fatalf("expires %v, want %v", j.ExpiresAt, at)
	}
	checkStored(t, s, j)
	// Without ExpiresAt the date stays.
	j = must(s.Transition(ctx, j.ID, store.Uploaded, store.Probing, store.Change{}))(t)
	if !j.ExpiresAt.Equal(at) {
		t.Fatalf("expires %v after a transition without a date", j.ExpiresAt)
	}
	// A zero date means never.
	never := time.Time{}
	j = must(s.Transition(ctx, j.ID, store.Probing, store.Probed, store.Change{ExpiresAt: &never}))(t)
	if !j.ExpiresAt.IsZero() {
		t.Fatalf("expires %v, want never", j.ExpiresAt)
	}
	checkStored(t, s, j)
}

func ids(js []store.Job) []string {
	out := make([]string, len(js))
	for i, j := range js {
		out[i] = j.ID
	}
	return out
}

func sameIDs(t *testing.T, what string, got []store.Job, want ...store.Job) {
	t.Helper()
	g, w := ids(got), ids(want)
	if fmt.Sprint(g) != fmt.Sprint(w) {
		t.Fatalf("%s: got %v, want %v", what, g, w)
	}
}

func testJobs(t *testing.T, s store.Store) {
	ctx := t.Context()
	a1 := newJob(t, s, "user1")
	b1 := newJob(t, s, "user2")
	a2 := drive(t, s, newJob(t, s, "user1").ID, store.Probing)
	a3 := drive(t, s, newJob(t, s, "user1").ID, store.Cancelled)
	b2 := drive(t, s, newJob(t, s, "user2").ID, store.Probing)

	jobs := func(f store.JobFilter) []store.Job { return must(s.Jobs(ctx, f))(t) }
	sameIDs(t, "all, newest first", jobs(store.JobFilter{}), b2, a3, a2, b1, a1)
	sameIDs(t, "owner", jobs(store.JobFilter{Owner: "user1"}), a3, a2, a1)
	sameIDs(t, "state", jobs(store.JobFilter{States: []store.State{store.Probing}}), b2, a2)
	sameIDs(t, "states", jobs(store.JobFilter{States: []store.State{store.Uploading, store.Cancelled}}), a3, b1, a1)
	sameIDs(t, "owner and state", jobs(store.JobFilter{Owner: "user2", States: []store.State{store.Probing}}), b2)
	sameIDs(t, "nobody", jobs(store.JobFilter{Owner: "user3"}))
	sameIDs(t, "limit", jobs(store.JobFilter{Limit: 2}), b2, a3)
	sameIDs(t, "offset", jobs(store.JobFilter{Limit: 2, Offset: 2}), a2, b1)
	sameIDs(t, "past the end", jobs(store.JobFilter{Offset: 10}))
	for _, j := range jobs(store.JobFilter{}) {
		checkStored(t, s, j) // Jobs returns the full rows
	}
}

func testJobsDefaultLimit(t *testing.T, s store.Store) {
	for range 101 {
		newJob(t, s, "user1")
	}
	if n := len(must(s.Jobs(t.Context(), store.JobFilter{}))(t)); n != 100 {
		t.Fatalf("%d jobs without a limit, want 100", n)
	}
}

// ---- files ----------------------------------------------------------------------------

func testFiles(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := newJob(t, s, "user1")
	other := newJob(t, s, "user1")
	report := json.RawMessage(`{"streams":[{"type":"video"}],"duration":12.5}`)

	zip := must(s.AddFile(ctx, store.File{JobID: j.ID, Path: "archives/season.zip", Kind: store.KindArchive, Size: 300}))(t)
	if zip.ID == 0 || zip.Status != store.FilePending {
		t.Fatalf("added file %+v: want an id and status pending", zip)
	}
	video := must(s.AddFile(ctx, store.File{
		JobID: j.ID, Path: "files/S01E01.mkv", Kind: store.KindVideo, Size: 100,
		Status: store.FileOK, From: "archives/season.zip", Report: report,
	}))(t)
	sub := must(s.AddFile(ctx, store.File{JobID: j.ID, Path: "files/S01E01.srt", Kind: store.KindSubtitle, Size: 2}))(t)
	if video.ID == zip.ID || sub.ID == video.ID {
		t.Fatal("file ids are not unique")
	}
	// Changing the caller's slice must not change the stored report.
	report[2] = 'X'

	if _, err := s.AddFile(ctx, store.File{JobID: j.ID, Path: "files/S01E01.mkv", Kind: store.KindVideo}); !errors.Is(err, store.ErrExists) {
		t.Fatalf("duplicate path: %v, want ErrExists", err)
	}
	must(s.AddFile(ctx, store.File{JobID: other.ID, Path: "files/S01E01.mkv", Kind: store.KindVideo}))(t)
	if _, err := s.AddFile(ctx, store.File{JobID: "0123456789abcdef0123456789abcdef", Path: "files/a.mkv"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("file of an unknown job: %v, want ErrNotFound", err)
	}

	files := must(s.Files(ctx, j.ID))(t)
	if len(files) != 3 || files[0].Path != "archives/season.zip" || files[1].Path != "files/S01E01.mkv" || files[2].Path != "files/S01E01.srt" {
		t.Fatalf("files not ordered by path: %+v", files)
	}
	got := files[1]
	if got.ID != video.ID || got.JobID != j.ID || got.Kind != store.KindVideo || got.Size != 100 ||
		got.Status != store.FileOK || got.From != "archives/season.zip" || !sameJSON(got.Report, `{"streams":[{"type":"video"}],"duration":12.5}`) {
		t.Fatalf("stored file %+v (report %s)", got, got.Report)
	}
	if files[0].Report != nil {
		t.Fatalf("file without a report has %q", files[0].Report)
	}

	got.Status, got.Reason, got.Report = store.FileRefused, "no_video_stream", json.RawMessage(`{"error":"x"}`)
	if err := s.UpdateFile(ctx, got); err != nil {
		t.Fatal(err)
	}
	upd := must(s.Files(ctx, j.ID))(t)[1]
	if upd.Status != store.FileRefused || upd.Reason != "no_video_stream" || !sameJSON(upd.Report, `{"error":"x"}`) || upd.Path != got.Path {
		t.Fatalf("updated file %+v", upd)
	}
	taken := upd
	taken.Path = "files/S01E01.srt"
	if err := s.UpdateFile(ctx, taken); !errors.Is(err, store.ErrExists) {
		t.Fatalf("update to a taken path: %v, want ErrExists", err)
	}
	if err := s.UpdateFile(ctx, store.File{ID: 1 << 40, JobID: j.ID, Path: "files/x"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update of an unknown file: %v, want ErrNotFound", err)
	}

	if err := s.DeleteFile(ctx, sub.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFile(ctx, sub.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v, want ErrNotFound", err)
	}
	if n := len(must(s.Files(ctx, j.ID))(t)); n != 2 {
		t.Fatalf("%d files after a delete, want 2", n)
	}
	// The path is free again.
	must(s.AddFile(ctx, store.File{JobID: j.ID, Path: "files/S01E01.srt", Kind: store.KindSubtitle}))(t)
	if fs := must(s.Files(ctx, "0123456789abcdef0123456789abcdef"))(t); len(fs) != 0 {
		t.Fatalf("files of an unknown job: %+v", fs)
	}
}

func sameJSON(got json.RawMessage, want string) bool {
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(want), &b) != nil {
		return false
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// ---- uploads --------------------------------------------------------------------------

func testUploads(t *testing.T, s store.Store) {
	ctx := t.Context()
	j1 := newJob(t, s, "user1")
	j2 := newJob(t, s, "user2")
	t0 := time.Date(2030, 5, 1, 10, 0, 0, 0, time.UTC)
	put := func(u store.Upload) {
		t.Helper()
		if err := s.PutUpload(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	put(store.Upload{ID: "u1", JobID: j1.ID, Owner: "user1", Name: "film.mkv", Size: 1000, CreatedAt: t0, ProgressAt: t0})
	put(store.Upload{ID: "u2", JobID: j1.ID, Owner: "user1", Name: "film.srt", Size: 10, CreatedAt: t0.Add(time.Second)})
	put(store.Upload{ID: "u3", JobID: j2.ID, Owner: "user2", Name: "season.zip", Size: 5000, CreatedAt: t0.Add(2 * time.Second)})

	u1 := must(s.Upload(ctx, "u1"))(t)
	if u1.JobID != j1.ID || u1.Owner != "user1" || u1.Name != "film.mkv" || u1.Size != 1000 || u1.Offset != 0 ||
		!u1.CreatedAt.Equal(t0) || !u1.ProgressAt.Equal(t0) {
		t.Fatalf("stored upload %+v", u1)
	}
	if u2 := must(s.Upload(ctx, "u2"))(t); !u2.ProgressAt.Equal(u2.CreatedAt) {
		t.Fatalf("progress of a new upload defaults to its creation: %+v", u2)
	}

	// Update by id: the offset grows; a zero CreatedAt keeps the stored one.
	t1 := t0.Add(time.Minute)
	put(store.Upload{ID: "u1", JobID: j1.ID, Owner: "user1", Name: "film.mkv", Size: 1000, Offset: 512, ProgressAt: t1})
	u1 = must(s.Upload(ctx, "u1"))(t)
	if u1.Offset != 512 || !u1.ProgressAt.Equal(t1) || !u1.CreatedAt.Equal(t0) {
		t.Fatalf("updated upload %+v", u1)
	}

	uploads := func(owner, job string) []string {
		var out []string
		for _, u := range must(s.Uploads(ctx, owner, job))(t) {
			out = append(out, u.ID)
		}
		return out
	}
	for _, c := range []struct {
		owner, job string
		want       string
	}{
		{"", "", "[u1 u2 u3]"},
		{"user1", "", "[u1 u2]"},
		{"", j2.ID, "[u3]"},
		{"user1", j1.ID, "[u1 u2]"},
		{"user2", j1.ID, "[]"},
	} {
		if got := fmt.Sprint(uploads(c.owner, c.job)); got != c.want {
			t.Fatalf("Uploads(%q, %q) = %s, want %s", c.owner, c.job, got, c.want)
		}
	}

	if err := s.PutUpload(ctx, store.Upload{ID: "u4", JobID: "0123456789abcdef0123456789abcdef", Owner: "user1"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("upload into an unknown job: %v, want ErrNotFound", err)
	}
	if err := s.DeleteUpload(ctx, "u2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(ctx, "u2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted upload: %v, want ErrNotFound", err)
	}
	if err := s.DeleteUpload(ctx, "u2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v, want ErrNotFound", err)
	}
}

// ---- deletion and audit ---------------------------------------------------------------

func testDeleteJob(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.Probed)
	keep := drive(t, s, newJob(t, s, "user1").ID, store.Uploaded)
	for _, id := range []string{j.ID, keep.ID} {
		must(s.AddFile(ctx, store.File{JobID: id, Path: "files/a.mkv", Kind: store.KindVideo}))(t)
		if err := s.PutUpload(ctx, store.Upload{ID: "up-" + id, JobID: id, Owner: "user1", Name: "b.mkv"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Audit(ctx, store.AuditEntry{Actor: "user1", Action: "job.created", JobID: id}); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.DeleteJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Job(ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted job: %v, want ErrNotFound", err)
	}
	if fs := must(s.Files(ctx, j.ID))(t); len(fs) != 0 {
		t.Fatalf("files of a deleted job: %+v", fs)
	}
	if us := must(s.Uploads(ctx, "", j.ID))(t); len(us) != 0 {
		t.Fatalf("uploads of a deleted job: %+v", us)
	}
	if _, err := s.Upload(ctx, "up-"+j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("upload of a deleted job: %v, want ErrNotFound", err)
	}
	if h := history(t, s, j.ID); len(h) != 0 {
		t.Fatalf("history of a deleted job: %+v", h)
	}
	if a := must(s.AuditLog(ctx, j.ID))(t); len(a) != 1 {
		t.Fatalf("audit of a deleted job: %+v, want the entry kept", a)
	}
	if err := s.DeleteJob(ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v, want ErrNotFound", err)
	}

	// The other job is untouched.
	checkStored(t, s, keep)
	if len(must(s.Files(ctx, keep.ID))(t)) != 1 || len(must(s.Uploads(ctx, "", keep.ID))(t)) != 1 || len(history(t, s, keep.ID)) != 1 {
		t.Fatal("deleting one job touched another")
	}
}

func testAudit(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := newJob(t, s, "user1")
	at := time.Date(2030, 6, 1, 12, 0, 0, 0, time.UTC)
	entries := []store.AuditEntry{
		{Actor: "user1", Email: "user1@example.com", Action: "job.created", JobID: j.ID, Detail: map[string]string{"files": "2"}},
		{Action: "upload.expired", JobID: "", Detail: map[string]string{"upload": "u1"}},
		{At: at, Actor: "admin1", Email: "admin1@example.com", Action: "job.cancelled", JobID: j.ID},
	}
	for _, e := range entries {
		if err := s.Audit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	entries[0].Detail["files"] = "changed" // the stored entry is a copy
	if err := s.Audit(ctx, store.AuditEntry{Actor: "user1"}); err == nil {
		t.Fatal("an entry without an action was stored")
	}

	log := must(s.AuditLog(ctx, j.ID))(t)
	if len(log) != 2 {
		t.Fatalf("audit log of the job: %+v", log)
	}
	first, second := log[0], log[1]
	if first.ID == 0 || first.At.IsZero() || first.Actor != "user1" || first.Email != "user1@example.com" ||
		first.Action != "job.created" || first.JobID != j.ID || !maps.Equal(first.Detail, map[string]string{"files": "2"}) {
		t.Fatalf("first entry %+v", first)
	}
	if !second.At.Equal(at) || second.Actor != "admin1" || second.Action != "job.cancelled" || second.Detail != nil || second.ID == first.ID {
		t.Fatalf("second entry %+v", second)
	}

	all := must(s.AuditLog(ctx, ""))(t)
	if len(all) != 3 {
		t.Fatalf("whole audit log: %+v", all)
	}
	for i := 1; i < len(all); i++ {
		if all[i].At.Before(all[i-1].At) {
			t.Fatalf("audit log not oldest first: %+v", all)
		}
	}
	var expired store.AuditEntry
	for _, e := range all {
		if e.Action == "upload.expired" {
			expired = e
		}
	}
	if expired.Actor != "system" || expired.JobID != "" || expired.Detail["upload"] != "u1" {
		t.Fatalf("entry without an actor or job: %+v", expired)
	}
}

func testPing(t *testing.T, s store.Store) {
	if err := s.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
}
