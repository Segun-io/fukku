package flows

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/store"
)

// The store calls the import adds to the fused store of crash_test.go.

func (s fusedStore) PutImport(ctx context.Context, i store.Import) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.PutImport(ctx, i)
}

func (s fusedStore) Import(ctx context.Context, id string) (store.Import, error) {
	if err := s.f.tick(); err != nil {
		return store.Import{}, err
	}
	return s.Store.Import(ctx, id)
}

func (s fusedStore) PutPostStep(ctx context.Context, p store.PostStep) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.PutPostStep(ctx, p)
}

func (s fusedStore) PostSteps(ctx context.Context, id string) ([]store.PostStep, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.Store.PostSteps(ctx, id)
}

// The app calls of the import, for the fused show of crash_test.go and a fused film app.

func (s fusedSeries) Busy(ctx context.Context, id int) (bool, error) {
	if err := s.f.tick(); err != nil {
		return false, err
	}
	return s.FakeSeries.Busy(ctx, id)
}

func (s fusedSeries) Import(ctx context.Context, rows []arr.Row, mode string) (int, error) {
	if err := s.f.tick(); err != nil {
		return 0, err
	}
	return s.FakeSeries.Import(ctx, rows, mode)
}

func (s fusedSeries) Command(ctx context.Context, id int) (arr.Command, error) {
	if err := s.f.tick(); err != nil {
		return arr.Command{}, err
	}
	return s.FakeSeries.Command(ctx, id)
}

func (s fusedSeries) Commands(ctx context.Context) ([]arr.Command, error) {
	if err := s.f.tick(); err != nil {
		return nil, err
	}
	return s.FakeSeries.Commands(ctx)
}

func (s fusedSeries) Series(ctx context.Context, id int) (arr.Series, error) {
	if err := s.f.tick(); err != nil {
		return arr.Series{}, err
	}
	return s.FakeSeries.Series(ctx, id)
}

func (s fusedSeries) SetEpisodesMonitored(ctx context.Context, ids []int, monitored bool) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.FakeSeries.SetEpisodesMonitored(ctx, ids, monitored)
}

type fusedMovies struct {
	*arrtest.FakeMovies
	f *fuse
}

func (m fusedMovies) Recheck(ctx context.Context, rows []arr.Row) ([]arr.Row, error) {
	if err := m.f.tick(); err != nil {
		return nil, err
	}
	return m.FakeMovies.Recheck(ctx, rows)
}

func (m fusedMovies) Movie(ctx context.Context, id int) (arr.Movie, error) {
	if err := m.f.tick(); err != nil {
		return arr.Movie{}, err
	}
	return m.FakeMovies.Movie(ctx, id)
}

func (m fusedMovies) Busy(ctx context.Context, id int) (bool, error) {
	if err := m.f.tick(); err != nil {
		return false, err
	}
	return m.FakeMovies.Busy(ctx, id)
}

func (m fusedMovies) Import(ctx context.Context, rows []arr.Row, mode string) (int, error) {
	if err := m.f.tick(); err != nil {
		return 0, err
	}
	return m.FakeMovies.Import(ctx, rows, mode)
}

func (m fusedMovies) Command(ctx context.Context, id int) (arr.Command, error) {
	if err := m.f.tick(); err != nil {
		return arr.Command{}, err
	}
	return m.FakeMovies.Command(ctx, id)
}

func (m fusedMovies) Commands(ctx context.Context) ([]arr.Command, error) {
	if err := m.f.tick(); err != nil {
		return nil, err
	}
	return m.FakeMovies.Commands(ctx)
}

func (m fusedMovies) SetMonitored(ctx context.Context, id int, monitored bool) error {
	if err := m.f.tick(); err != nil {
		return err
	}
	return m.FakeMovies.SetMonitored(ctx, id, monitored)
}

// importCase is a job to import: a film, or a season with a skipped row and a double episode.
type importCase struct {
	name  string
	setup func(x *ienv) store.Job
	sent  func(x *ienv) [][]arr.Row // the ManualImport calls the app received
	files int                       // rows imported
}

var importCases = []importCase{
	{"film", func(x *ienv) store.Job {
		j := x.filmJob()
		_ = x.st.SetTarget(x.ctx, j.ID, x.target(j, `{"added":true,"monitored":false}`))
		return j
	}, (*ienv).movieImports, 1},
	{"season", func(x *ienv) store.Job {
		// S01E03.mkv is a double (twice the runtime) of 103 and 104; S01E04.mkv is really 105.
		j := x.seasonJobOf(epRuntime, epRuntime, 2*epRuntime, epRuntime)
		rows := x.rows(j)
		x.edit(j, rows[1].ID, Edit{Skip: ptr(true)})
		x.edit(j, rows[2].ID, Edit{EpisodeIDs: &[]int{103, 104}})
		x.edit(j, rows[3].ID, Edit{EpisodeIDs: &[]int{105}})
		return x.jobNow(j)
	}, (*ienv).seriesImports, 3},
}

// The runner dies after call number N into the store or the app, for every N from the
// confirmation to done; a new process (the user confirming again if the job still waits for
// it) always ends in done, the app has received exactly one ManualImport, and each row is
// imported once. The app finishes a command at once (slow 1), or only after it has been
// looked at for a few passes (slow 3), so that the new process may find it still running.
func TestImportCrashAtEveryCall(t *testing.T) {
	for _, c := range importCases {
		for _, slow := range []int{1, 3} {
			t.Run(fmt.Sprintf("%s slow=%d", c.name, slow), func(t *testing.T) { testImportCrash(t, c, slow) })
		}
	}
}

func testImportCrash(t *testing.T, c importCase, slow int) {
	{
		{
			for n := 0; ; n++ {
				x := newImportEnv(t)
				x.slow = slow
				step := &recordingStep{name: "test"}
				x.eng.AddPostStep(step)
				j := c.setup(x)

				f := &fuse{left: n}
				dying := x.engineOn(fusedStore{x.st, f}, fusedMovies{x.movies, f}, fusedSeries{x.series, f})
				if _, err := dying.Confirm(x.ctx, "user1", j, ConfirmRequest{}); err != nil && !f.blown() {
					t.Fatalf("after %d calls: confirm: %v", n, err)
				}
				for range 50 {
					if f.blown() || !slices.Contains([]store.State{store.Importing, store.Imported, store.PostProcessing}, x.jobNow(j).State) {
						break
					}
					if _, err := x.step(dying, j); err != nil && !f.blown() {
						t.Fatalf("after %d calls: an uninterrupted step failed: %v", n, err)
					}
					x.appWorks()
					x.clock.advance(1)
				}
				finished := !f.blown()
				if s := x.jobNow(j).State; s == store.Failed || s == store.Stalled {
					t.Fatalf("after %d calls: the dying process left the job %s (%s)", n, s, x.jobNow(j).ErrorText)
				}

				// A new process takes over; the app has finished its command meanwhile.
				x.appWorks()
				if x.jobNow(j).State == store.AwaitingConfirmation {
					x.confirm(j)
				}
				j = x.drive(x.eng, j)
				if j.State != store.Done {
					t.Fatalf("after %d calls: job %s (%s %s)", n, j.State, j.ErrorCode, j.ErrorText)
				}
				sent := c.sent(x)
				if len(sent) != 1 {
					t.Fatalf("after %d calls: %d ManualImport commands", n, len(sent))
				}
				for p, k := range importedPaths(sent) {
					if k != 1 {
						t.Fatalf("after %d calls: %s sent %d times", n, p, k)
					}
				}
				imported := 0
				for _, r := range x.rows(j) {
					if r.Result == store.ResultImported {
						imported++
					}
				}
				if imported != c.files || len(sent[0]) != c.files {
					t.Fatalf("after %d calls: %d rows imported, %d sent", n, imported, len(sent[0]))
				}
				steps, _ := x.st.PostSteps(x.ctx, j.ID)
				if len(steps) != 1 || steps[0].Status != store.StepOK || step.count() < 1 {
					t.Fatalf("after %d calls: post steps %+v, %d runs", n, steps, step.count())
				}
				if finished {
					t.Logf("an uninterrupted run takes %d calls", n)
					if n < 15 {
						t.Fatalf("the import finished after %d calls: the fuse does not see it", n)
					}
					return
				}
			}
		}
	}
}

// outage makes call number at into the app fail as an app that does not answer. With lost,
// a ManualImport reaches the app and only its answer is lost (a timeout).
type outage struct {
	mu        sync.Mutex
	calls, at int
	lost      bool
	hit       string // the method that failed
}

func (o *outage) down(method string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	if o.calls == o.at {
		o.hit = method
		return true
	}
	return false
}

func (o *outage) err(method string) error {
	return fmt.Errorf("%s: %w: connection refused", method, arr.ErrUnreachable)
}

type downMovies struct {
	*arrtest.FakeMovies
	o *outage
}

func (m downMovies) Movie(ctx context.Context, id int) (arr.Movie, error) {
	if m.o.down("Movie") {
		return arr.Movie{}, m.o.err("Movie")
	}
	return m.FakeMovies.Movie(ctx, id)
}

func (m downMovies) Busy(ctx context.Context, id int) (bool, error) {
	if m.o.down("Busy") {
		return false, m.o.err("Busy")
	}
	return m.FakeMovies.Busy(ctx, id)
}

func (m downMovies) Import(ctx context.Context, rows []arr.Row, mode string) (int, error) {
	if m.o.down("Import") {
		if m.o.lost {
			_, _ = m.FakeMovies.Import(ctx, rows, mode)
			return 0, fmt.Errorf("Import: timed out: %w", context.DeadlineExceeded)
		}
		return 0, m.o.err("Import")
	}
	return m.FakeMovies.Import(ctx, rows, mode)
}

func (m downMovies) Command(ctx context.Context, id int) (arr.Command, error) {
	if m.o.down("Command") {
		return arr.Command{}, m.o.err("Command")
	}
	return m.FakeMovies.Command(ctx, id)
}

func (m downMovies) Commands(ctx context.Context) ([]arr.Command, error) {
	if m.o.down("Commands") {
		return nil, m.o.err("Commands")
	}
	return m.FakeMovies.Commands(ctx)
}

func (m downMovies) SetMonitored(ctx context.Context, id int, monitored bool) error {
	if m.o.down("SetMonitored") {
		return m.o.err("SetMonitored")
	}
	return m.FakeMovies.SetMonitored(ctx, id, monitored)
}

type downSeries struct {
	*arrtest.FakeSeries
	o *outage
}

func (s downSeries) Episodes(ctx context.Context, id int) ([]arr.Episode, error) {
	if s.o.down("Episodes") {
		return nil, s.o.err("Episodes")
	}
	return s.FakeSeries.Episodes(ctx, id)
}

func (s downSeries) Series(ctx context.Context, id int) (arr.Series, error) {
	if s.o.down("Series") {
		return arr.Series{}, s.o.err("Series")
	}
	return s.FakeSeries.Series(ctx, id)
}

func (s downSeries) Busy(ctx context.Context, id int) (bool, error) {
	if s.o.down("Busy") {
		return false, s.o.err("Busy")
	}
	return s.FakeSeries.Busy(ctx, id)
}

func (s downSeries) Import(ctx context.Context, rows []arr.Row, mode string) (int, error) {
	if s.o.down("Import") {
		if s.o.lost {
			_, _ = s.FakeSeries.Import(ctx, rows, mode)
			return 0, fmt.Errorf("Import: timed out: %w", context.DeadlineExceeded)
		}
		return 0, s.o.err("Import")
	}
	return s.FakeSeries.Import(ctx, rows, mode)
}

func (s downSeries) Command(ctx context.Context, id int) (arr.Command, error) {
	if s.o.down("Command") {
		return arr.Command{}, s.o.err("Command")
	}
	return s.FakeSeries.Command(ctx, id)
}

func (s downSeries) Commands(ctx context.Context) ([]arr.Command, error) {
	if s.o.down("Commands") {
		return nil, s.o.err("Commands")
	}
	return s.FakeSeries.Commands(ctx)
}

func (s downSeries) SetEpisodesMonitored(ctx context.Context, ids []int, monitored bool) error {
	if s.o.down("SetEpisodesMonitored") {
		return s.o.err("SetEpisodesMonitored")
	}
	return s.FakeSeries.SetEpisodesMonitored(ctx, ids, monitored)
}

// The app does not answer at one call, for every call from the confirmation to done (and a
// ManualImport whose answer is lost after it reached the app): the job stalls with
// arr_unavailable, never fails, and finishes once the app is back, with one ManualImport.
func TestAppAwayAtEveryCall(t *testing.T) {
	for _, c := range importCases {
		for _, lost := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s lost=%v", c.name, lost), func(t *testing.T) {
				for at := 1; ; at++ {
					x := newImportEnv(t)
					x.slow = 3
					x.cfg.Merge.UnmonitorAfterDubOnlyImport = true
					x.movies.Library[0].OriginalLanguage = "English"
					x.series.Library[0].OriginalLanguage = "English"
					j := c.setup(x)
					o := &outage{at: at, lost: lost}
					x.rebuild(downMovies{x.movies, o}, downSeries{x.series, o})

					// The confirmation reads a show's episodes: refused while the app is away,
					// so the user tries again.
					for range 2 {
						if _, err := x.eng.Confirm(x.ctx, "user1", j, ConfirmRequest{}); err == nil {
							break
						} else if Code(err) != CodeArrUnavailable {
							t.Fatalf("at %d: confirm: %v", at, err)
						}
					}
					o.mu.Lock()
					inConfirm := o.hit != ""
					o.mu.Unlock()
					j = x.drive(x.eng, j)
					if j.State != store.Done {
						t.Fatalf("at %d (%s): job %s (%s %s)", at, o.hit, j.State, j.ErrorCode, j.ErrorText)
					}
					if sent := c.sent(x); len(sent) != 1 {
						t.Fatalf("at %d (%s): %d ManualImport commands", at, o.hit, len(sent))
					}
					h, _ := x.st.History(x.ctx, j.ID)
					stalled := slices.ContainsFunc(h, func(e store.Event) bool { return e.To == store.Stalled })
					if failed := slices.ContainsFunc(h, func(e store.Event) bool { return e.To == store.Failed }); failed {
						t.Fatalf("at %d (%s): the job failed on the way", at, o.hit)
					}
					switch {
					case o.hit == "":
						if at < 6 {
							t.Fatalf("only %d app calls", at)
						}
						return
					case inConfirm: // refused, and confirmed again
					case !stalled:
						t.Fatalf("at %d (%s): the job did not stall", at, o.hit)
					}
				}
			})
		}
	}
}
