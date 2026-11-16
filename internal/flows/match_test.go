package flows_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// hookMovies runs hooks inside the fake's calls, to hold a match while the test acts.
type hookMovies struct {
	*arrtest.FakeMovies
	onMovie func(id int)
	onAdd   func()
}

func (h *hookMovies) Movie(ctx context.Context, id int) (arr.Movie, error) {
	m, err := h.FakeMovies.Movie(ctx, id)
	if h.onMovie != nil {
		h.onMovie(id)
	}
	return m, err
}

func (h *hookMovies) Add(ctx context.Context, a arr.AddMovie) (arr.Movie, error) {
	m, err := h.FakeMovies.Add(ctx, a)
	if h.onAdd != nil {
		h.onAdd()
	}
	return m, err
}

type fixture struct {
	t      *testing.T
	st     *storetest.Fake
	movies *hookMovies
	series *arrtest.FakeSeries
	locks  *flows.JobLocks
	m      *flows.Matcher
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	cfg := config.Defaults()
	cfg.Radarr.URL = "http://radarr.example.com:7878"
	cfg.Radarr.QualityProfile = "HD"
	cfg.Sonarr.URL = "http://sonarr.example.com:8989"
	f := &fixture{t: t, st: storetest.NewFake(nil), locks: flows.NewJobLocks()}
	f.movies = &hookMovies{FakeMovies: &arrtest.FakeMovies{
		Profile: []arr.Profile{{ID: 4, Name: "HD"}},
		Roots:   []arr.RootFolder{{Path: "/data/media/movies"}},
		Results: []arr.Movie{{TMDBID: 603, Title: "The Matrix", Year: 1999}, {ID: 7, TMDBID: 604, Title: "Other", Year: 2003}},
		Library: []arr.Movie{{ID: 7, TMDBID: 604, Title: "Other", Year: 2003}},
	}}
	f.series = &arrtest.FakeSeries{
		Profile: []arr.Profile{{ID: 9, Name: "Any"}},
		Roots:   []arr.RootFolder{{Path: "/data/media/tv"}},
		Results: []arr.Series{{ID: 20, TVDBID: 2, Title: "Known Show", Year: 2012}},
		Library: []arr.Series{{ID: 20, TVDBID: 2, Title: "Known Show", Year: 2012}},
	}
	f.m = flows.NewMatcher(cfg, f.st, f.movies, f.series, f.locks, nil)
	return f
}

func (f *fixture) job(state store.State) store.Job {
	f.t.Helper()
	j, err := f.st.CreateJob(f.t.Context(), "user1")
	if err != nil {
		f.t.Fatal(err)
	}
	j, err = storetest.Drive(f.t.Context(), f.st, j.ID, state)
	if err != nil {
		f.t.Fatal(err)
	}
	return j
}

func (f *fixture) state(j store.Job) store.State {
	f.t.Helper()
	got, err := f.st.Job(f.t.Context(), j.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return got.State
}

// A second match on a matched job, while the proposal step stores rows and moves the job
// to awaiting-confirmation: the match must not leave the job half changed.
func TestRematchWhileTheProposalStepWrites(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	j := f.job(store.Matched)
	file, err := f.st.AddFile(ctx, store.File{JobID: j.ID, Path: "files/a.mkv", Kind: store.KindVideo, Size: 1, Status: store.FileOK})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetTarget(ctx, j.ID, store.Target{App: "radarr", TitleID: 99, Title: "Old"}); err != nil {
		t.Fatal(err)
	}

	inApp, proceed := make(chan struct{}), make(chan struct{})
	f.movies.onMovie = func(int) { close(inApp); <-proceed }
	done := make(chan error, 1)
	go func() {
		_, err := f.m.Match(ctx, "user1", j, flows.MatchRequest{App: "radarr", TMDBID: 604})
		done <- err
	}()
	<-inApp
	// The proposal step: under the job's lock, rows and a new state.
	func() {
		defer f.locks.Lock(j.ID)()
		if err := f.st.PutRows(ctx, j.ID, []store.Row{{ID: "r1", FileID: file.ID}}); err != nil {
			t.Error(err)
		}
		if _, err := f.st.Transition(ctx, j.ID, store.Matched, store.AwaitingConfirmation, store.Change{Actor: "system"}); err != nil {
			t.Error(err)
		}
	}()
	close(proceed)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The result is consistent: matched, the new title, no rows of the old one.
	got, _ := f.st.Job(ctx, j.ID)
	tg, _ := f.st.Target(ctx, j.ID)
	rows, _ := f.st.Rows(ctx, j.ID)
	if got.State != store.Matched || tg.TitleID != 7 || len(rows) != 0 {
		t.Errorf("state=%s target=%+v rows=%d", got.State, tg, len(rows))
	}
}

// A job that moved on to importing while the app was asked is refused, and keeps its
// target and rows.
func TestMatchRefusedWhenTheJobMovedOn(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	j := f.job(store.AwaitingConfirmation)
	file, _ := f.st.AddFile(ctx, store.File{JobID: j.ID, Path: "files/a.mkv", Kind: store.KindVideo, Size: 1, Status: store.FileOK})
	_ = f.st.SetTarget(ctx, j.ID, store.Target{App: "radarr", TitleID: 99, Title: "Old"})
	_ = f.st.PutRows(ctx, j.ID, []store.Row{{ID: "r1", FileID: file.ID}})

	inApp, proceed := make(chan struct{}), make(chan struct{})
	f.movies.onMovie = func(int) { close(inApp); <-proceed }
	done := make(chan error, 1)
	go func() {
		_, err := f.m.Match(ctx, "user1", j, flows.MatchRequest{App: "radarr", TMDBID: 604})
		done <- err
	}()
	<-inApp
	if _, err := f.st.Transition(ctx, j.ID, store.AwaitingConfirmation, store.Importing, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	if code := flows.Code(<-done); code != flows.CodeWrongState {
		t.Errorf("code = %q, want wrong_state", code)
	}
	tg, _ := f.st.Target(ctx, j.ID)
	rows, _ := f.st.Rows(ctx, j.ID)
	if f.state(j) != store.Importing || tg.TitleID != 99 || len(rows) != 1 {
		t.Errorf("a refused match changed the job: target %+v, rows %d", tg, len(rows))
	}
	// And a job already importing is refused at once.
	if _, err := f.m.Match(ctx, "user1", f.job(store.Importing), flows.MatchRequest{App: "radarr", TMDBID: 604}); flows.Code(err) != flows.CodeWrongState {
		t.Errorf("importing: %v", err)
	}
}

// The title was added; the client went away; the target must still be recorded.
func TestTheAddedTitleIsRecordedEvenIfTheRequestIsCancelled(t *testing.T) {
	f := newFixture(t)
	j := f.job(store.Probed)
	ctx, cancel := context.WithCancel(t.Context())
	f.movies.onAdd = cancel
	got, err := f.m.Match(ctx, "user1", j, flows.MatchRequest{App: "radarr", TMDBID: 603})
	if err != nil || got.State != store.Matched {
		t.Fatalf("Match = %+v, %v", got, err)
	}
	tg, err := f.st.Target(t.Context(), j.ID)
	if err != nil || tg.Title != "The Matrix" {
		t.Errorf("target = %+v, %v", tg, err)
	}
}

// One slow title does not hold up other titles, other jobs or the other app.
func TestASlowTitleBlocksOnlyItself(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	slow, other := f.job(store.Probed), f.job(store.Probed)
	inApp, proceed := make(chan struct{}), make(chan struct{})
	f.movies.onMovie = func(int) { close(inApp); <-proceed }
	done := make(chan error, 1)
	go func() {
		_, err := f.m.Match(ctx, "user1", slow, flows.MatchRequest{App: "radarr", TMDBID: 604})
		done <- err
	}()
	<-inApp

	finished := make(chan error, 3)
	go func() {
		_, err := f.m.Match(ctx, "user2", other, flows.MatchRequest{App: "sonarr", TVDBID: 2})
		finished <- err
	}()
	go func() {
		_, err := f.m.Match(ctx, "user2", f.job(store.Probed), flows.MatchRequest{App: "radarr", TMDBID: 603, QualityProfileID: 4})
		finished <- err
	}()
	go func() {
		m := f.job(store.Matched)
		_, err := f.m.Unmatch(ctx, "user2", m)
		finished <- err
	}()
	for range 3 {
		select {
		case err := <-finished:
			if err != nil {
				t.Errorf("another match: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a match waits for an unrelated slow title")
		}
	}
	close(proceed)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Two jobs choosing the same new title at once: it is added once and only one job gets it.
func TestTheSameNewTitleIsAddedOnce(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	var ok, inUse atomic.Int32
	for range 4 {
		j := f.job(store.Probed)
		wg.Go(func() {
			_, err := f.m.Match(t.Context(), "user1", j, flows.MatchRequest{App: "radarr", TMDBID: 603})
			switch flows.Code(err) {
			case "":
				ok.Add(1)
			case flows.CodeTitleInUse:
				inUse.Add(1)
			default:
				t.Errorf("err = %v", err)
			}
		})
	}
	wg.Wait()
	if len(f.movies.Added) != 1 || ok.Load() != 1 || inUse.Load() != 3 {
		t.Errorf("added %d, ok %d, in use %d", len(f.movies.Added), ok.Load(), inUse.Load())
	}
}

func TestJobLocks(t *testing.T) {
	l := flows.NewJobLocks()
	unlockA := l.Lock("a")
	got := make(chan string, 2)
	go func() { defer l.Lock("b")(); got <- "b" }() // another key: not blocked
	if g := <-got; g != "b" {
		t.Fatal(g)
	}
	go func() { defer l.Lock("a")(); got <- "a" }() // the same key: waits
	select {
	case <-got:
		t.Fatal("the same key was not held")
	case <-time.After(50 * time.Millisecond):
	}
	unlockA()
	unlockA() // releasing twice is harmless
	if g := <-got; g != "a" {
		t.Fatal(g)
	}
}

// The limit of the final write starts when the job's lock is taken, not before the wait
// for it: a long hold by another step must not make the write fail after the title was
// added.
func TestTheWriteLimitStartsAfterTheJobLock(t *testing.T) {
	f := newFixture(t)
	f.m.SetWriteTimeout(150 * time.Millisecond)
	j := f.job(store.Probed)
	unlock := f.locks.Lock(j.ID)
	done := make(chan error, 1)
	go func() {
		_, err := f.m.Match(t.Context(), "user1", j, flows.MatchRequest{App: "radarr", TMDBID: 603})
		done <- err
	}()
	time.Sleep(400 * time.Millisecond) // longer than the limit
	unlock()
	if err := <-done; err != nil {
		t.Fatalf("Match after a long wait: %v", err)
	}
	if _, err := f.st.Target(t.Context(), j.ID); err != nil || len(f.movies.Added) != 1 {
		t.Errorf("target: %v, added %d", err, len(f.movies.Added))
	}
}
