package flows

import (
	"net/http"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/store"
)

// Follow-ups of the second review of the import engine (#74), each with its test.

// A. With importMode copy, a file the app tracks with a size of 0 is not taken for the
// import's: the job's file stays where it was, so the size is the only sign that the file
// is the job's. (With move, the job's file being gone is.)
func TestCopyModeASizeOfZeroIsNotArrived(t *testing.T) {
	t.Run("film", func(t *testing.T) {
		x := newImportEnv(t)
		copyMode(x)
		j := x.confirm(x.filmJob())
		j, _ = x.step(x.eng, j)
		// A file the app got by itself while the import ran, whose size it does not report.
		x.movies.Library[0].File = &arr.MovieFile{ID: 79, Size: 0}
		x.movies.Library[0].HasFile = true
		x.movies.Fail(x.movies.Imports[0].CommandID, "Not an upgrade")
		if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeImportFailed {
			t.Fatalf("a file of size 0 was taken for the import's: job %s (%s)", j.State, j.ErrorText)
		}
		if got := results(x.rows(j)); got[0] != store.ResultFailed {
			t.Errorf("results %v", got)
		}
	})
	t.Run("show", func(t *testing.T) {
		x := newImportEnv(t)
		copyMode(x)
		j := x.confirm(x.seasonJob(1))
		j, _ = x.step(x.eng, j)
		x.series.Eps[showID][0].FileID = 4243
		x.series.Files = map[int][]arr.EpisodeFile{showID: {{ID: 4243, Path: "/tv/Show/S01E01.mkv", Size: 0}}}
		x.series.Fail(x.series.Imports[0].CommandID, "Disk full")
		if j, _ = x.step(x.eng, j); j.State != store.Failed {
			t.Fatalf("an episode file of size 0 was taken for the import's: job %s", j.State)
		}
		if got := results(x.rows(j)); got[0] != store.ResultFailed {
			t.Errorf("results %v", got)
		}
	})
	// With move, a size the app does not report is not held against a file once the job's
	// file is gone.
	t.Run("move", func(t *testing.T) {
		x := newImportEnv(t)
		j := x.confirm(x.filmJob())
		j, _ = x.step(x.eng, j)
		x.movies.Complete(x.movies.Imports[0].CommandID)
		x.movies.Library[0].File.Size = 0
		if j, _ = x.step(x.eng, j); j.State != store.Imported {
			t.Fatalf("move: job %s (%s)", j.State, j.ErrorText)
		}
	})
}

// B. With importMode copy, an import without a recorded baseline (an earlier version
// submitted it) counts nothing as arrived: the file the app tracks may be one it had
// before. The job fails, saying why, and a retry submits again with a baseline.
func TestCopyModeWithoutABaselineNothingArrives(t *testing.T) {
	x := newImportEnv(t)
	copyMode(x)
	j := x.confirm(x.filmJob())
	j, _ = x.step(x.eng, j)
	imp := x.importRecord(j)
	imp.Baseline = nil
	if err := x.st.PutImport(x.ctx, imp); err != nil {
		t.Fatal(err)
	}
	// The app's own file, of the same size as the job's, and the command failed.
	x.movies.Library[0].File = &arr.MovieFile{ID: 77, Size: 1000}
	x.movies.Library[0].HasFile = true
	x.movies.Fail(imp.CommandID, "Not an upgrade")
	if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeImportFailed || !strings.Contains(j.ErrorText, "no baseline") {
		t.Fatalf("the app's own file was taken for the import's: job %s (%s)", j.State, j.ErrorText)
	}
	if got := results(x.rows(j)); got[0] != store.ResultFailed {
		t.Errorf("results %v", got)
	}

	if _, err := x.st.Transition(x.ctx, j.ID, store.Failed, store.Importing, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	if j, _ = x.step(x.eng, j); j.State != store.Importing || len(x.movies.Imports) != 2 {
		t.Fatalf("the retry: job %s, %d imports", j.State, len(x.movies.Imports))
	}
	if b := baselineOf(x.importRecord(j)); !b.recorded || b.Movie == nil || b.Movie.ID != 77 {
		t.Fatalf("the retry's baseline %+v", b)
	}
	x.movies.Complete(x.movies.Imports[1].CommandID)
	if j = x.drive(x.eng, j); j.State != store.Done || len(x.movies.Imports) != 2 {
		t.Fatalf("job %s (%s), %d imports", j.State, j.ErrorText, len(x.movies.Imports))
	}
}

// With importMode move, the job's file being gone still tells the import's file apart: an
// import without a baseline is imported once the app has it.
func TestMoveModeWithoutABaseline(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.filmJob())
	j, _ = x.step(x.eng, j)
	imp := x.importRecord(j)
	imp.Baseline = nil
	if err := x.st.PutImport(x.ctx, imp); err != nil {
		t.Fatal(err)
	}
	x.movies.Complete(imp.CommandID)
	if j = x.drive(x.eng, j); j.State != store.Done || len(x.movies.Imports) != 1 {
		t.Fatalf("job %s (%s), %d imports", j.State, j.ErrorText, len(x.movies.Imports))
	}
}

// C. Only refusals with no successful poll between them are counted toward
// server.arrStallLimit: refusals now and then, while the command keeps running and the app
// answers its polls in between, never fail the job.
func TestRefusalsBetweenSuccessfulPollsAreNotARun(t *testing.T) {
	x := newImportEnv(t)
	x.cfg.Server.ArrStallLimit = 3
	x.rebuild(x.movies, x.series)
	j := x.confirm(x.filmJob())
	j, _ = x.step(x.eng, j)
	refused := map[string]error{"Command": &arr.Error{App: "radarr", Method: "GET", Path: "/command/1", Status: http.StatusBadRequest, Message: "bad"}}
	for i := 1; i <= 5; i++ {
		x.movies.Err = refused
		if j, _ = x.step(x.eng, j); j.State != store.Stalled {
			t.Fatalf("refusal %d: %s (%s)", i, j.State, j.ErrorText)
		}
		x.clock.advance(1e12)
		if j, _ = x.step(x.eng, j); j.State != store.Importing {
			t.Fatalf("resume %d: %s", i, j.State)
		}
		x.movies.Err = nil
		if j, _ = x.step(x.eng, j); j.State != store.Importing {
			t.Fatalf("poll %d: %s", i, j.State)
		}
	}
	// A run without a successful poll still fails at the limit.
	x.movies.Err = refused
	for i := 1; i < 3; i++ {
		if j, _ = x.step(x.eng, j); j.State != store.Stalled {
			t.Fatalf("refusal in a row %d: %s", i, j.State)
		}
		x.clock.advance(1e12)
		if j, _ = x.step(x.eng, j); j.State != store.Importing {
			t.Fatalf("resume in a row %d: %s", i, j.State)
		}
	}
	if j, _ = x.step(x.eng, j); j.State != store.Failed || !strings.Contains(j.ErrorText, "refused 3 times in a row") {
		t.Fatalf("at the limit: %s %q", j.State, j.ErrorText)
	}
}

// The same with a lost command id: a pass that finds the command again (the app answered
// every call) breaks the run too.
func TestFindingTheCommandBreaksARunOfRefusals(t *testing.T) {
	x := newImportEnv(t)
	x.cfg.Server.ArrStallLimit = 2
	x.rebuild(x.movies, x.series)
	j := x.confirm(x.filmJob())
	j, _ = x.step(x.eng, j)
	lost := x.importRecord(j)
	cmd := lost.CommandID
	refused := &arr.Error{App: "radarr", Method: "GET", Path: "/command", Status: http.StatusConflict, Message: "busy"}
	for i := 1; i <= 3; i++ {
		lost.CommandID = 0 // a crash lost the id
		if err := x.st.PutImport(x.ctx, lost); err != nil {
			t.Fatal(err)
		}
		x.movies.Err = map[string]error{"Commands": refused}
		if j, _ = x.step(x.eng, j); j.State != store.Stalled {
			t.Fatalf("refusal %d: %s (%s)", i, j.State, j.ErrorText)
		}
		x.clock.advance(1e12)
		if j, _ = x.step(x.eng, j); j.State != store.Importing {
			t.Fatalf("resume %d: %s", i, j.State)
		}
		x.movies.Err = nil
		if j, _ = x.step(x.eng, j); j.State != store.Importing || x.importRecord(j).CommandID != cmd {
			t.Fatalf("found %d: job %s, command %d", i, j.State, x.importRecord(j).CommandID)
		}
		lost = x.importRecord(j)
	}
	if len(x.movies.Imports) != 1 {
		t.Fatalf("%d imports", len(x.movies.Imports))
	}
}
