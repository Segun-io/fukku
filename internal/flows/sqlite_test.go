package flows

import (
	"slices"
	"testing"

	"github.com/segun-io/fukku/internal/store"
)

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
