package flows

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/store"
)

var errDied = errors.New("the process died")

// fuse counts the calls into the store and the app. After `left` calls every call fails
// without doing anything: for the store and the app the process died after call N.
type fuse struct {
	mu   sync.Mutex
	left int
}

func (f *fuse) tick() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.left == 0 {
		return errDied
	}
	f.left--
	return nil
}

func (f *fuse) blown() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.left == 0
}

type fusedStore struct {
	store.Store
	f *fuse
}

func (s fusedStore) Job(ctx context.Context, id string) (store.Job, error) {
	if err := s.f.tick(); err != nil {
		return store.Job{}, err
	}
	return s.Store.Job(ctx, id)
}

func (s fusedStore) Transition(ctx context.Context, id string, from, to store.State, c store.Change) (store.Job, error) {
	if err := s.f.tick(); err != nil {
		return store.Job{}, err
	}
	return s.Store.Transition(ctx, id, from, to, c)
}

func (s fusedStore) TransitionFor(ctx context.Context, id string, t store.Target, from, to store.State, c store.Change) (store.Job, error) {
	if err := s.f.tick(); err != nil {
		return store.Job{}, err
	}
	return s.Store.TransitionFor(ctx, id, t, from, to, c)
}

func (s fusedStore) PutProposal(ctx context.Context, id string, from, to store.State, t store.Target, rows []store.Row, c store.Change) (store.Job, error) {
	if err := s.f.tick(); err != nil {
		return store.Job{}, err
	}
	return s.Store.PutProposal(ctx, id, from, to, t, rows, c)
}

func (s fusedStore) History(ctx context.Context, id string) ([]store.Event, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.Store.History(ctx, id)
}

func (s fusedStore) Files(ctx context.Context, id string) ([]store.File, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.Store.Files(ctx, id)
}

func (s fusedStore) Target(ctx context.Context, id string) (store.Target, error) {
	if err := s.f.tick(); err != nil {
		return store.Target{}, err
	}
	return s.Store.Target(ctx, id)
}

func (s fusedStore) PutRows(ctx context.Context, id string, rows []store.Row) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.PutRows(ctx, id, rows)
}

func (s fusedStore) Rows(ctx context.Context, id string) ([]store.Row, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.Store.Rows(ctx, id)
}

func (s fusedStore) UpdateRow(ctx context.Context, r store.Row) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.UpdateRow(ctx, r)
}

type fusedSeries struct {
	*arrtest.FakeSeries
	f *fuse
}

func (s fusedSeries) Proposal(ctx context.Context, folder string) ([]arr.Row, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.FakeSeries.Proposal(ctx, folder)
}

func (s fusedSeries) Recheck(ctx context.Context, rows []arr.Row) ([]arr.Row, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.FakeSeries.Recheck(ctx, rows)
}

func (s fusedSeries) Episodes(ctx context.Context, id int) ([]arr.Episode, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.FakeSeries.Episodes(ctx, id)
}

// The matched step interrupted after any call, then run again by a new process, ends in
// awaiting-confirmation with one set of rows, whose ids are those of an uninterrupted run.
func TestMatchedStepCrash(t *testing.T) {
	for n := 0; ; n++ {
		e := newEnv(t)
		e.series.Eps[showID][2].FileID = 9001 // S01E03 has a file
		j := e.job("sonarr", showID, vid("S01E01.mkv", epRuntime), vid("S01E03.mkv", epRuntime), vid("sample.mkv", minute))
		e.series.Proposed[e.folder(j)] = []arr.Row{e.item(j, "S01E01.mkv", webdl1080), e.item(j, "S01E03.mkv", webdl1080)}
		files, err := e.st.Files(e.ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for _, f := range offerable(files) {
			want = append(want, RowID(f.ID))
		}

		f := &fuse{left: n}
		dying := NewProposals(e.cfg, nil, fusedStore{e.st, f}, nil, fusedSeries{e.series, f}, nil)
		err = dying.Matched(e.ctx, e.jobNow(j))
		finished := !f.blown()
		if finished && err != nil {
			t.Fatalf("an uninterrupted run failed: %v", err)
		}
		if j := e.jobNow(j); j.State != store.Matched && j.State != store.AwaitingConfirmation {
			t.Fatalf("after %d calls: job %s (%s %s)", n, j.State, j.ErrorCode, j.ErrorText)
		}
		// A new process takes over.
		for i := 0; i < 3 && e.jobNow(j).State == store.Matched; i++ {
			if err := e.p.Matched(e.ctx, e.jobNow(j)); err != nil {
				t.Fatalf("after %d calls: the next run: %v", n, err)
			}
		}
		if j := e.jobNow(j); j.State != store.AwaitingConfirmation {
			t.Fatalf("after %d calls: job %s", n, j.State)
		}
		rows := e.rows(j)
		if got := storedIDs(rows); !slices.Equal(got, want) {
			t.Fatalf("after %d calls: rows %v, want %v", n, got, want)
		}
		p := e.view(j)
		wantCodes(t, "S01E03", byName(p, "S01E03.mkv").Warnings, WarnExistingFile)
		wantCodes(t, "sample", byName(p, "sample.mkv").Warnings, WarnNotOffered)
		if !byName(p, "S01E01.mkv").Importable {
			t.Fatalf("after %d calls: %+v", n, p.Rows)
		}
		if finished {
			if n < 5 {
				t.Fatalf("the step finished after %d calls: the fuse does not see it", n)
			}
			return
		}
	}
}

// Edits of one job at the same time, on different rows and on the same row, all survive:
// the job's lock serialises them.
func TestConcurrentEdits(t *testing.T) {
	e := newEnv(t)
	j, p := seasonJob(e, 4)
	r := ids(p.Rows)
	calls := []func() error{
		func() error { _, err := e.p.EditRow(e.ctx, j, r[0], Edit{QualityID: ptr(7)}); return err },
		func() error { _, err := e.p.EditRow(e.ctx, j, r[0], Edit{LanguageIDs: &[]int{spanish.ID}}); return err },
		func() error { _, err := e.p.EditRow(e.ctx, j, r[1], Edit{QualityID: ptr(4)}); return err },
		func() error {
			_, err := e.p.Bulk(e.ctx, j, BulkEdit{RowIDs: []string{r[1]}, SeasonNumber: ptr(2)})
			return err
		},
		func() error { _, err := e.p.EditRow(e.ctx, j, r[2], Edit{EpisodeIDs: &[]int{105}}); return err },
		func() error { _, err := e.p.EditRow(e.ctx, j, r[3], Edit{Skip: ptr(true)}); return err },
		func() error { _, err := e.p.View(e.ctx, j); return err },
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, len(calls))
	for i, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = c()
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
	p = e.view(j)
	if !slices.Equal(ids(p.Rows), r) {
		t.Errorf("ids %v, were %v", ids(p.Rows), r)
	}
	r0, r1, r2, r3 := p.Rows[0], p.Rows[1], p.Rows[2], p.Rows[3]
	if r0.Quality.ID != 7 || len(r0.Languages) != 1 || r0.Languages[0].ID != spanish.ID {
		t.Errorf("row 1 lost an edit: %+v", r0)
	}
	if r1.Quality.ID != 4 || !slices.Equal(episodeIDs(r1), []int{202}) {
		t.Errorf("row 2 lost an edit: %+v", r1)
	}
	if !slices.Equal(episodeIDs(r2), []int{105}) || !r3.Skip {
		t.Errorf("rows 3 and 4: %+v %+v", r2, r3)
	}
	// One re-check for the proposal, one per edit.
	if n := len(e.series.RecheckCalls()); n < 1+6 {
		t.Errorf("%d re-checks, want at least 7 (retries re-check again)", n)
	}
}

// A re-check that ends after the user chose another title stores nothing.
func TestEditAfterTheTitleChanged(t *testing.T) {
	e := newEnv(t)
	j, p := seasonJob(e, 2)
	e.p = NewProposals(e.cfg, nil, e.st, nil, rematching{e.series, e, j}, nil)
	if _, err := e.p.EditRow(e.ctx, j, p.Rows[0].ID, Edit{EpisodeIDs: &[]int{105}}); Code(err) != CodeWrongState {
		t.Errorf("edit across a new match: %v", err)
	}
	if rows := e.rows(j); len(rows) != 0 {
		t.Errorf("rows %+v", rows)
	}
}

// rematching answers a re-check after the user has gone back to choose another title.
type rematching struct {
	*arrtest.FakeSeries
	e *env
	j store.Job
}

func (r rematching) Recheck(ctx context.Context, rows []arr.Row) ([]arr.Row, error) {
	if _, err := r.e.st.Transition(ctx, r.j.ID, store.AwaitingConfirmation, store.Probed, store.Change{Actor: "user1"}); err != nil {
		return nil, err
	}
	if err := r.e.st.ClearTarget(ctx, r.j.ID); err != nil {
		return nil, err
	}
	return r.FakeSeries.Recheck(ctx, rows)
}
