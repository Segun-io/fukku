package flows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// hostile answers re-checks with the path of another job's file.
type hostile struct {
	*arrtest.FakeSeries
	path    string
	noPaths bool // answer without any path instead
}

func (h hostile) Recheck(ctx context.Context, rows []arr.Row) ([]arr.Row, error) {
	out, err := h.FakeSeries.Recheck(ctx, rows)
	for i := range out {
		if h.noPaths {
			out[i].Path = ""
		} else {
			out[i].Path = h.path
		}
		out[i].Raw = json.RawMessage(`{"path":` + fmt.Sprintf("%q", out[i].Path) + `}`)
	}
	return out, err
}

func newClockStore(t *testing.T, now *time.Time) store.Store {
	t.Helper()
	return storetest.NewFake(func() time.Time { return *now })
}

// The path that reaches the app and the store is always the one Fukku derives from the
// row's file. A re-check answer naming another file fails the step, and an edit, with
// nothing stored; an answer with no paths at all is paired by position.
func TestRecheckAnswerCannotMoveARowToAnotherFile(t *testing.T) {
	e := newEnv(t)
	other := appIntake + "/jobs/" + strings.Repeat("f", 32) + "/files/secret.mkv"
	j := e.job("sonarr", showID, vid("S01E01.mkv", epRuntime))
	it := e.item(j, "S01E01.mkv", webdl1080)
	it.Path = appIntake + "/jobs/./" + j.ID + "/files/S01E01.mkv" // the app's own spelling
	e.series.Proposed[e.folder(j)] = []arr.Row{it}
	e.p = NewProposals(e.cfg, nil, e.st, nil, hostile{FakeSeries: e.series, path: other}, nil)
	j = e.matched(j)
	if j.State != store.Failed || j.ErrorCode != CodeProposalFailed || !strings.Contains(j.ErrorText, "not sent") {
		t.Fatalf("hostile answer: job %s %s %s", j.State, j.ErrorCode, j.ErrorText)
	}
	if rows := e.rows(j); len(rows) != 0 {
		t.Fatalf("rows stored: %+v", rows)
	}
	if sent := e.series.RecheckCalls()[0][0]; sent.Path != e.appPath(j, "S01E01.mkv") {
		t.Errorf("sent path %q, want the derived one", sent.Path)
	}

	// Without paths the answer is paired by position, and the derived path is kept.
	e2 := newEnv(t)
	j2 := e2.job("sonarr", showID, vid("S01E01.mkv", epRuntime))
	e2.series.Proposed[e2.folder(j2)] = []arr.Row{e2.item(j2, "S01E01.mkv", webdl1080)}
	e2.p = NewProposals(e2.cfg, nil, e2.st, nil, hostile{FakeSeries: e2.series, noPaths: true}, nil)
	p := e2.ready(j2)
	var ar arr.Row
	if err := json.Unmarshal(e2.rows(j2)[0].Proposal, &ar); err != nil {
		t.Fatal(err)
	}
	want := e2.appPath(j2, "S01E01.mkv")
	if ar.Path != want || !strings.Contains(string(ar.Raw), want) || !p.Rows[0].Importable {
		t.Errorf("stored %q %s", ar.Path, ar.Raw)
	}

	// An edit whose answer names another file is refused and stores nothing.
	before := string(e2.rows(j2)[0].Proposal)
	e2.p = NewProposals(e2.cfg, nil, e2.st, nil, hostile{FakeSeries: e2.series, path: other}, nil)
	if _, err := e2.p.EditRow(e2.ctx, j2, p.Rows[0].ID, Edit{EpisodeIDs: &[]int{102}}); Code(err) != CodeArrUnavailable {
		t.Errorf("hostile edit answer: %v", err)
	}
	if after := string(e2.rows(j2)[0].Proposal); after != before || strings.Contains(after, "secret") {
		t.Errorf("stored after a hostile answer: %s", after)
	}
	// Even if a stored row named another path, the next re-check sends the derived one.
	rows := e2.rows(j2)
	_ = json.Unmarshal(rows[0].Proposal, &ar)
	ar.Path = other
	rows[0].Proposal, _ = json.Marshal(ar)
	if err := e2.st.UpdateRow(e2.ctx, rows[0]); err != nil {
		t.Fatal(err)
	}
	e2.p = NewProposals(e2.cfg, nil, e2.st, nil, e2.series, nil)
	e2.edit(j2, p.Rows[0].ID, Edit{EpisodeIDs: &[]int{102}})
	calls := e2.series.RecheckCalls()
	if got := calls[len(calls)-1][0].Path; got != want {
		t.Errorf("re-checked %q, want %q", got, want)
	}
}

func TestUnavailable(t *testing.T) {
	for _, tc := range []struct {
		err  error
		down bool
	}{
		{errRefused, true},
		{fmt.Errorf("sonarr: GET /x: timed out after 30s: %w", context.DeadlineExceeded), true},
		{&arr.Error{Status: 503}, true},
		{&arr.Error{Status: 401}, true},
		{&arr.Error{Status: 403}, true},
		{&arr.Error{Status: 429}, true},
		{&arr.Error{Status: 400}, false},
		{&arr.Error{Status: 404}, false},
		{&arr.Error{Status: 408}, false},
		{errors.New("sonarr: GET /x: decode answer: invalid character"), false},
	} {
		if got := Unavailable(tc.err); got != tc.down {
			t.Errorf("%v: unavailable %v, want %v", tc.err, got, tc.down)
		}
	}
}

// An app that answers its status but keeps timing out on the proposal stalls the job with
// a growing wait, and fails it after server.arrStallLimit stalls; it does not cycle.
func TestStallCyclesBackOffAndEnd(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	e.st = newClockStore(t, &now)
	e.p = NewProposals(e.cfg, nil, e.st, nil, e.series, nil)
	e.p.now = func() time.Time { return now }
	j := e.job("sonarr", showID, vid("S01E01.mkv", epRuntime))
	e.series.Err = map[string]error{"Proposal": fmt.Errorf("sonarr: GET /manualimport: timed out after 30s: %w", context.DeadlineExceeded)}
	base := e.cfg.Server.ArrStallBackoff.D()
	limit := e.cfg.Server.ArrStallLimit
	for n := 1; n <= limit; n++ {
		j = e.matched(j)
		if j.State != store.Stalled || j.ErrorCode != CodeArrUnavailable {
			t.Fatalf("stall %d: job %s %s", n, j.State, j.ErrorCode)
		}
		wait := base << min(n-1, 4)
		now = now.Add(wait - time.Second)
		if ok, err := e.p.AppAnswers(e.ctx, j); ok || err != nil {
			t.Fatalf("stall %d: resumed before %s: %v %v", n, wait, ok, err)
		}
		now = now.Add(time.Second)
		if ok, err := e.p.AppAnswers(e.ctx, j); !ok || err != nil {
			t.Fatalf("stall %d: not resumed after %s: %v %v", n, wait, ok, err)
		}
		if j, _ = e.st.Transition(e.ctx, j.ID, store.Stalled, store.Matched, store.Change{Actor: "system"}); j.State != store.Matched {
			t.Fatal("resume")
		}
	}
	j = e.matched(j)
	if j.State != store.Failed || j.ErrorCode != CodeArrUnavailable || !strings.Contains(j.ErrorText, "times in a row") {
		t.Fatalf("after %d stalls: job %s %s %s", limit, j.State, j.ErrorCode, j.ErrorText)
	}
	// A retry starts counting again.
	if j, _ = e.st.Transition(e.ctx, j.ID, store.Failed, store.Matched, store.Change{Actor: "user1"}); j.State != store.Matched {
		t.Fatal("retry")
	}
	if j = e.matched(j); j.State != store.Stalled {
		t.Errorf("after a retry: %s", j.State)
	}

	// An answer the client cannot use is a failure at once.
	e2 := newEnv(t)
	j2 := e2.job("sonarr", showID, vid("S01E01.mkv", epRuntime))
	e2.series.Err = map[string]error{"Episodes": errors.New("sonarr: GET /episode: decode answer: invalid character")}
	if j2 = e2.matched(j2); j2.State != store.Failed || j2.ErrorCode != CodeProposalFailed {
		t.Errorf("an unreadable answer: job %s %s", j2.State, j2.ErrorCode)
	}
}

func TestSeveralFilesOfAFilm(t *testing.T) {
	e := newEnv(t)
	j := e.job("radarr", filmID, vid("a sample.mkv", 2*minute), vid("b Film.mkv", 100*minute), vid("c extra.mkv", 10*minute))
	e.movies.Proposed[e.folder(j)] = []arr.Row{
		e.item(j, "a sample.mkv", webdl1080), e.item(j, "b Film.mkv", webdl1080), e.item(j, "c extra.mkv", webdl1080),
	}
	p := e.ready(j)
	for _, r := range p.Rows {
		longest := r.Name == "b Film.mkv"
		if r.Importable != longest || r.Skip == longest || len(r.Warnings) != 0 {
			t.Errorf("row %s: importable %v, skip %v, warnings %v", r.Name, r.Importable, r.Skip, codes(r.Warnings))
		}
	}
	// Taking a second file as well: both are warned.
	e.edit(j, byName(p, "a sample.mkv").ID, Edit{Skip: ptr(false)})
	p = e.view(j)
	wantCodes(t, "the film", byName(p, "b Film.mkv").Warnings, WarnDuplicateFilm)
	wantCodes(t, "the sample", byName(p, "a sample.mkv").Warnings, WarnDuplicateFilm)
	wantCodes(t, "the skipped one", byName(p, "c extra.mkv").Warnings)
}

// The step and the edits never hold a job's lock while the app works, and one job's lock
// does not hold up another's.
func TestLocks(t *testing.T) {
	e := newEnv(t)
	j := e.job("sonarr", showID, vid("S01E01.mkv", epRuntime))
	e.series.Proposed[e.folder(j)] = []arr.Row{e.item(j, "S01E01.mkv", webdl1080)}
	probe := &lockProbe{FakeSeries: e.series, p: nil, job: j.ID}
	e.p = NewProposals(e.cfg, nil, e.st, nil, probe, nil)
	probe.p = e.p
	p := e.ready(j)
	e.edit(j, p.Rows[0].ID, Edit{EpisodeIDs: &[]int{102}})
	if probe.calls < 2 || probe.blocked {
		t.Errorf("re-checks %d; the lock was held during one: %v", probe.calls, probe.blocked)
	}

	unlock := e.p.Lock("a")
	got := make(chan struct{})
	go func() { e.p.Lock("b")(); close(got) }()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Error("job b waited for job a's lock")
	}
	unlock()
	if len(e.p.locks.m) != 0 {
		t.Errorf("locks kept: %d", len(e.p.locks.m))
	}
}

// lockProbe checks, during each re-check, that the job's lock is free.
type lockProbe struct {
	*arrtest.FakeSeries
	p       *Proposals
	job     string
	mu      sync.Mutex
	calls   int
	blocked bool
}

func (l *lockProbe) Recheck(ctx context.Context, rows []arr.Row) ([]arr.Row, error) {
	free := make(chan struct{})
	go func() { l.p.Lock(l.job)(); close(free) }()
	select {
	case <-free:
	case <-time.After(5 * time.Second):
		l.mu.Lock()
		l.blocked = true
		l.mu.Unlock()
	}
	l.mu.Lock()
	l.calls++
	l.mu.Unlock()
	return l.FakeSeries.Recheck(ctx, rows)
}
