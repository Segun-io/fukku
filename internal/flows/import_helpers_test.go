package flows

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/pathmap"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// ienv is env for the import: the intake root is a real directory, so the job's files are
// on disk, and the fake apps "move" a file away when they import it.
type ienv struct {
	*env
	root  string
	eng   *Engine
	clock *testClock
	// slow: appWorks finishes a command only once it has seen it this many times (0 or 1:
	// at once), so that a pass, or a new process, finds it still running.
	slow int
	seen map[string]int
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var testStart = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// newImportEnv uses the fake store, on the test's clock.
func newImportEnv(t *testing.T) *ienv {
	t.Helper()
	clock := &testClock{t: testStart}
	return newImportEnvWith(t, storetest.NewFake(clock.now), clock)
}

func newImportEnvWith(t *testing.T, st store.Store, clock *testClock) *ienv {
	t.Helper()
	e := newEnvWith(t, st)
	root := t.TempDir()
	cfg := e.cfg
	cfg.Paths.IntakeRoot = root
	cfg.Paths.Mappings.Radarr = []config.PathMapping{{From: root, To: appIntake}}
	cfg.Paths.Mappings.Sonarr = []config.PathMapping{{From: root, To: appIntake}}
	cfg.Server.ImportPollInterval = 0
	cfg.Server.ArrStallBackoff = config.Duration(time.Nanosecond)
	e.cfg = cfg
	x := &ienv{env: e, root: root, clock: clock}
	x.rebuild(e.movies, e.series)
	moved := func(r arr.Row) { _ = os.Remove(pathmap.From(cfg.Paths.Mappings.Radarr, r.Path)) }
	e.movies.OnImport, e.series.OnImport = moved, moved
	return x
}

// rebuild makes a new Proposals and Engine (a new process) on the apps given.
func (x *ienv) rebuild(movies arr.MovieManager, series arr.SeriesManager) {
	x.p = NewProposals(x.cfg, nil, x.st, movies, series, nil)
	x.p.now = x.clock.now
	x.eng = NewEngine(x.p)
	x.eng.checkStorage = func() error { return nil }
}

// engineOn returns a new process on st and the apps given, with x's settings.
func (x *ienv) engineOn(st store.Store, movies arr.MovieManager, series arr.SeriesManager) *Engine {
	p := NewProposals(x.cfg, nil, st, movies, series, nil)
	p.now = x.clock.now
	eng := NewEngine(p)
	eng.checkStorage = func() error { return nil }
	for _, s := range x.eng.steps {
		eng.AddPostStep(s)
	}
	return eng
}

// onDisk writes the job's videos into its files directory.
func (x *ienv) onDisk(j store.Job) {
	x.t.Helper()
	files, err := x.st.Files(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	for _, f := range files {
		if f.Kind != store.KindVideo {
			continue
		}
		p := x.src(j, f.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			x.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("video"), 0o644); err != nil {
			x.t.Fatal(err)
		}
	}
}

func (x *ienv) src(j store.Job, rel string) string {
	return filepath.Join(x.root, "jobs", j.ID, filepath.FromSlash(rel))
}

// filmJob is a film job awaiting confirmation with one video.
func (x *ienv) filmJob() store.Job {
	x.t.Helper()
	j := x.job("radarr", filmID, vid("Some Film (2001).mkv", 100*minute))
	x.onDisk(j)
	x.movies.Proposed[x.folder(j)] = []arr.Row{x.item(j, "Some Film (2001).mkv", webdl1080)}
	x.ready(j)
	return x.jobNow(j)
}

// seasonJob is a season job awaiting confirmation with n episodes (S01E01…).
func (x *ienv) seasonJob(n int) store.Job {
	x.t.Helper()
	d := make([]int64, n)
	for i := range d {
		d[i] = epRuntime
	}
	return x.seasonJobOf(d...)
}

// seasonJobOf is seasonJob with a file of each duration.
func (x *ienv) seasonJobOf(durations ...int64) store.Job {
	x.t.Helper()
	var fs []file
	var items []arr.Row
	j0 := []string{}
	for i, d := range durations {
		name := "Show.S01E0" + string(rune('1'+i)) + ".mkv"
		fs = append(fs, vid(name, d))
		j0 = append(j0, name)
	}
	j := x.job("sonarr", showID, fs...)
	x.onDisk(j)
	for _, name := range j0 {
		items = append(items, x.item(j, name, webdl1080))
	}
	x.series.Proposed[x.folder(j)] = items
	x.ready(j)
	return x.jobNow(j)
}

func (x *ienv) confirm(j store.Job, overrides ...string) store.Job {
	x.t.Helper()
	c, err := x.eng.Confirm(x.ctx, "user1", j, ConfirmRequest{Overrides: overrides})
	if err != nil {
		x.t.Fatalf("confirm: %v", err)
	}
	if c.Job.State != store.Importing {
		x.t.Fatalf("confirmed job is %s", c.Job.State)
	}
	return c.Job
}

// step runs the job's step once with eng and returns the job afterwards.
func (x *ienv) step(eng *Engine, j store.Job) (store.Job, error) {
	j = x.jobNow(j)
	var err error
	switch j.State {
	case store.Importing:
		err = eng.Importing(x.ctx, j)
	case store.Imported:
		err = eng.Imported(x.ctx, j)
	case store.PostProcessing:
		err = eng.postProcess(x.ctx, j)
	case store.Stalled:
		err = x.resume(eng, j)
	}
	return x.jobNow(j), err
}

// resume is the runner's stalled step for arr_unavailable.
func (x *ienv) resume(eng *Engine, j store.Job) error {
	ok, err := eng.p.AppAnswers(x.ctx, j)
	if err != nil || !ok {
		return err
	}
	_, err = eng.st.Transition(x.ctx, j.ID, store.Stalled, j.Resume, store.Change{Actor: "system", Note: "the app answers again"})
	return err
}

// appWorks finishes every running import command of the fake apps.
func (x *ienv) appWorks() {
	if x.seen == nil {
		x.seen = map[string]int{}
	}
	due := func(key string) bool {
		x.seen[key]++
		return x.seen[key] >= x.slow
	}
	for _, id := range x.movies.Started() {
		if due(fmt.Sprintf("radarr %d", id)) {
			x.movies.Complete(id)
		}
	}
	for _, id := range x.series.Started() {
		if due(fmt.Sprintf("sonarr %d", id)) {
			x.series.Complete(id)
		}
	}
}

// drive runs steps with eng, the apps finishing their commands between passes, until the
// job is in a state without a step (done, failed, awaiting-confirmation…).
func (x *ienv) drive(eng *Engine, j store.Job) store.Job {
	x.t.Helper()
	for range 50 {
		j = x.jobNow(j)
		if !slices.Contains([]store.State{store.Importing, store.Imported, store.PostProcessing, store.Stalled}, j.State) {
			return j
		}
		if _, err := x.step(eng, j); err != nil {
			x.t.Fatalf("%s step: %v", j.State, err)
		}
		x.appWorks()
		x.clock.advance(time.Hour) // past any backoff
	}
	x.t.Fatalf("job still %s", x.jobNow(j).State)
	return j
}

func (x *ienv) importRecord(j store.Job) store.Import {
	x.t.Helper()
	i, err := x.st.Import(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	return i
}

func results(rows []store.Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Result)
	}
	return out
}

// importedPaths counts, over every ManualImport the app received, how often each path was sent.
func importedPaths(calls [][]arr.Row) map[string]int {
	out := map[string]int{}
	for _, rows := range calls {
		for _, r := range rows {
			out[r.Path]++
		}
	}
	return out
}

func (x *ienv) movieImports() [][]arr.Row {
	var out [][]arr.Row
	for _, c := range x.movies.Imports {
		out = append(out, c.Rows)
	}
	return out
}

func (x *ienv) seriesImports() [][]arr.Row {
	var out [][]arr.Row
	for _, c := range x.series.Imports {
		out = append(out, c.Rows)
	}
	return out
}

// recordingStep is a PostStep that records its runs and answers from a script.
type recordingStep struct {
	name string
	mu   sync.Mutex
	runs int
	// answer returns the outcome of run n (1-based); nil: done, "ok".
	answer func(n int) (bool, string, error)
}

func (s *recordingStep) Name() string { return s.name }

func (s *recordingStep) Run(context.Context, store.Job, store.Target, []store.Row) (bool, string, error) {
	s.mu.Lock()
	s.runs++
	n := s.runs
	s.mu.Unlock()
	if s.answer == nil {
		return true, "ok", nil
	}
	return s.answer(n)
}

func (s *recordingStep) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs
}
