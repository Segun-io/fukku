package flows

import (
	"slices"
	"testing"

	"github.com/segun-io/fukku/internal/store"
)

// The import on the real store, with a restart while its command runs: the new process
// polls the stored command and does not submit again.
func TestImportSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{t: testStart}
	st, err := store.Open(t.Context(), store.Options{Dir: dir, Now: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	x := newImportEnvWith(t, st, clock)
	x.eng.AddPostStep(&recordingStep{name: "test"})
	j := x.confirm(x.seasonJob(2))
	if j, _ = x.step(x.eng, j); j.State != store.Importing || len(x.series.Imports) != 1 {
		t.Fatalf("job %s, %d imports", j.State, len(x.series.Imports))
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := store.Open(t.Context(), store.Options{Dir: dir, Now: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	x.st = st2
	steps := x.eng.steps
	x.rebuild(x.movies, x.series)
	x.eng.steps = steps
	if imp := x.importRecord(j); imp.CommandID != x.series.Imports[0].CommandID {
		t.Fatalf("import record %+v", imp)
	}
	if j = x.drive(x.eng, j); j.State != store.Done || len(x.series.Imports) != 1 {
		t.Fatalf("job %s, %d imports", j.State, len(x.series.Imports))
	}
	if got := results(x.rows(j)); !slices.Equal(got, []string{store.ResultImported, store.ResultImported}) {
		t.Errorf("results %v", got)
	}
	if ps, _ := st2.PostSteps(x.ctx, j.ID); len(ps) != 1 || ps[0].Status != store.StepOK {
		t.Errorf("post steps %+v", ps)
	}
}

// Rows, their ids and the user's edits survive a restart on the real store; edits after it
// keep the ids, and running the matched step again (a crash before its transition) writes
// the same rows.
func TestRowsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(t.Context(), store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	e := newEnvWith(t, st)
	j, p := seasonJob(e, 3)
	before := ids(p.Rows)
	e.edit(j, before[2], Edit{EpisodeIDs: &[]int{104, 105}})
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := store.Open(t.Context(), store.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	e2 := newEnvWith(t, st2)
	p = e2.view(j)
	if !slices.Equal(ids(p.Rows), before) {
		t.Errorf("ids after a restart %v, were %v", ids(p.Rows), before)
	}
	if got := episodeIDs(p.Rows[2]); !slices.Equal(got, []int{104, 105}) {
		t.Errorf("the edit after a restart: %v", got)
	}
	got := e2.edit(j, before[0], Edit{QualityID: ptr(7)})
	if got.ID != before[0] || !slices.Equal(ids(e2.view(j).Rows), before) {
		t.Errorf("ids after another edit %v", ids(e2.view(j).Rows))
	}

	// The matched step once more, as after a crash before its transition: the same ids.
	if _, err := st2.Transition(t.Context(), j.ID, store.AwaitingConfirmation, store.Matched, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	e2.series.Proposed[e2.folder(j)] = e.series.Proposed[e.folder(j)]
	if p := e2.ready(j); !slices.Equal(ids(p.Rows), before) {
		t.Errorf("ids after the step ran again %v, were %v", ids(p.Rows), before)
	}
}
