package flows

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/store"
)

// Findings of the independent review of #40, each with its test.

func (s fusedStore) ConfirmImport(ctx context.Context, id string, t store.Target, rows []store.Row, options json.RawMessage, audit []store.AuditEntry, c store.Change) (store.Job, error) {
	if err := s.f.tick(); err != nil {
		return store.Job{}, err
	}
	return s.Store.ConfirmImport(ctx, id, t, rows, options, audit, c)
}

func copyMode(x *ienv) {
	x.cfg.Radarr.ImportMode, x.cfg.Sonarr.ImportMode = "copy", "copy"
	x.rebuild(x.movies, x.series)
}

// 1. With importMode copy, a file the app had before the submission (here: one it grabbed
// by itself after the match) is not taken for the import's. Since #116 the import is not
// even submitted: it would replace that file, which the confirmation did not choose, so the
// job goes back to probed, to be matched again (the film is flow 2 then).
func TestCopyModeAFileTheAppHadIsNotTheImports(t *testing.T) {
	x := newImportEnv(t)
	copyMode(x)
	j := x.confirm(x.filmJob())
	// Even of the same size as the job's file: it was there before the submission.
	x.movies.Library[0].File = &arr.MovieFile{ID: 77, Path: "/movies/Some Film (2001)/Some.Film.2001.mkv", Size: 1000}
	x.movies.Library[0].HasFile = true

	if j, _ = x.step(x.eng, j); j.State != store.Probed || len(x.movies.Imports) != 0 {
		t.Fatalf("the film's own file: job %s, %d imports", j.State, len(x.movies.Imports))
	}
}

// The same when another file arrives while a failing import runs: the file the app has is
// its own, and the job fails rather than being taken as imported. A retry does not submit
// again, since it would replace that file (#116): it fails with library_has_file.
func TestCopyModeAFileTheAppHadIsNotTheImportsWhenItFails(t *testing.T) {
	x := newImportEnv(t)
	copyMode(x)
	j := x.confirm(x.filmJob())
	j, _ = x.step(x.eng, j)
	x.movies.Library[0].File = &arr.MovieFile{ID: 77, Size: 999} // another release, grabbed meanwhile
	x.movies.Fail(x.movies.Imports[0].CommandID, "Not an upgrade")
	if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeImportFailed {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if _, err := x.st.Transition(x.ctx, j.ID, store.Failed, store.Importing, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeLibraryHasFile || len(x.movies.Imports) != 1 {
		t.Fatalf("the retry: job %s (%s), %d imports", j.State, j.ErrorCode, len(x.movies.Imports))
	}
}

// A file the app gets by itself while the import runs (another release: another size) does
// not count either: the command failed, so the job fails.
func TestAFileTheAppGotMeanwhileIsNotTheImports(t *testing.T) {
	t.Run("film", func(t *testing.T) {
		x := newImportEnv(t)
		copyMode(x)
		j := x.confirm(x.filmJob())
		j, _ = x.step(x.eng, j)
		x.movies.Library[0].File = &arr.MovieFile{ID: 78, Size: 4_321}
		x.movies.Fail(x.movies.Imports[0].CommandID, "Not an upgrade")
		if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeImportFailed {
			t.Fatalf("job %s (%s)", j.State, j.ErrorText)
		}
		if got := results(x.rows(j)); got[0] != store.ResultFailed {
			t.Errorf("results %v", got)
		}
	})
	t.Run("show", func(t *testing.T) {
		x := newImportEnv(t)
		copyMode(x)
		j := x.confirm(x.seasonJob(2))
		j, _ = x.step(x.eng, j)
		// Sonarr gets S01E01 from a download of its own.
		x.series.Eps[showID][0].FileID = 4242
		x.series.Files = map[int][]arr.EpisodeFile{showID: {{ID: 4242, Path: "/tv/Show/S01E01.mkv", Size: 777}}}
		x.series.Fail(x.series.Imports[0].CommandID, "Disk full")
		if j, _ = x.step(x.eng, j); j.State != store.Failed {
			t.Fatalf("job %s", j.State)
		}
		if got := results(x.rows(j)); got[0] != store.ResultFailed || got[1] != store.ResultFailed {
			t.Errorf("results %v", got)
		}
	})
}

// finishingMovies completes a running import command when it is polled, just before it
// answers: the import finishes between two calls of one pass.
type finishingMovies struct{ *arrtest.FakeMovies }

func (m finishingMovies) Command(ctx context.Context, id int) (arr.Command, error) {
	c, err := m.FakeMovies.Command(ctx, id)
	if err == nil && c.Status == "started" {
		m.Complete(id)
		return m.FakeMovies.Command(ctx, id)
	}
	return c, err
}

// 2. An import that finishes during the pass is imported, not reported failed: once the
// poll says the command has ended, the app is asked what arrived.
func TestImportFinishingDuringThePass(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.filmJob())
	j, _ = x.step(x.eng, j)
	x.rebuild(finishingMovies{x.movies}, x.series)
	if j, _ = x.step(x.eng, j); j.State != store.Imported {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
}

// refusing answers every call of a method with a 404 of the app.
var notFound = &arr.Error{App: "radarr", Method: "GET", Path: "/movie/7", Status: http.StatusNotFound, Message: "NotFound"}

// 3. The film deleted from the app during the import: once the command has ended, the 404
// fails the job at once, with the app's answer.
func TestTitleDeletedDuringTheImport(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.filmJob())
	j, _ = x.step(x.eng, j)
	x.movies.Err = map[string]error{"Movie": notFound}
	if j, _ = x.step(x.eng, j); j.State != store.Importing {
		t.Fatalf("while the command runs nothing is decided: %s", j.State)
	}
	x.movies.Fail(x.movies.Imports[0].CommandID, "Movie not found")
	if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeImportFailed || !strings.Contains(j.ErrorText, "404") {
		t.Fatalf("job %s %q", j.State, j.ErrorText)
	}
}

// A permanent refusal while an import may be running stalls the job, but only
// server.arrStallLimit times in a row: then it fails, saying why. An app that does not
// answer still only stalls.
func TestRefusalsWhileImportingAreCounted(t *testing.T) {
	x := newImportEnv(t)
	x.cfg.Server.ArrStallLimit = 3
	x.rebuild(x.movies, x.series)
	j := x.confirm(x.filmJob())
	j, _ = x.step(x.eng, j)
	x.movies.Err = map[string]error{"Command": &arr.Error{App: "radarr", Method: "GET", Path: "/command/1", Status: http.StatusBadRequest, Message: "bad"}}
	for i := 1; i < 3; i++ {
		if j, _ = x.step(x.eng, j); j.State != store.Stalled {
			t.Fatalf("refusal %d: %s", i, j.State)
		}
		x.clock.advance(1e12)
		if j, _ = x.step(x.eng, j); j.State != store.Importing {
			t.Fatalf("resume %d: %s", i, j.State)
		}
	}
	if j, _ = x.step(x.eng, j); j.State != store.Failed || !strings.Contains(j.ErrorText, "refused 3 times in a row") {
		t.Fatalf("at the limit: %s %q", j.State, j.ErrorText)
	}
	h, _ := x.st.History(x.ctx, j.ID)
	if n := len(h); n > 20 {
		t.Errorf("%d events", n)
	}

	// The app away does not count: stalled, however often.
	if _, err := x.st.Transition(x.ctx, j.ID, store.Failed, store.Importing, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	x.movies.Err = map[string]error{"Command": errRefused}
	j = x.jobNow(j)
	for i := 0; i < 5; i++ {
		if j, _ = x.step(x.eng, j); j.State != store.Stalled {
			t.Fatalf("outage %d: %s", i, j.State)
		}
		if _, err := x.st.Transition(x.ctx, j.ID, store.Stalled, store.Importing, store.Change{Actor: "system"}); err != nil {
			t.Fatal(err)
		}
		j = x.jobNow(j)
	}
	x.movies.Err = nil
	x.movies.Complete(x.movies.Imports[0].CommandID)
	if j = x.drive(x.eng, j); j.State != store.Done || len(x.movies.Imports) != 1 {
		t.Fatalf("job %s, %d imports", j.State, len(x.movies.Imports))
	}
}

func TestRefusals(t *testing.T) {
	ev := func(from, to store.State, note string) store.Event {
		return store.Event{From: from, To: to, Note: note}
	}
	h := []store.Event{
		ev(store.AwaitingConfirmation, store.Importing, ""),
		ev(store.Importing, store.Stalled, refusalNote+"command"),
		ev(store.Stalled, store.Importing, ""),
		ev(store.Importing, store.Stalled, ""), // the app away: the run starts again
		ev(store.Stalled, store.Importing, ""),
		ev(store.Importing, store.Stalled, refusalNote+"command"),
		ev(store.Stalled, store.Importing, ""),
		ev(store.Importing, store.Stalled, refusalNote+"files"),
		ev(store.Stalled, store.Importing, ""),
	}
	if n := refusals(h, store.Importing); n != 2 {
		t.Errorf("refusals %d, want 2", n)
	}
}

// 4. The confirmation is one write: when it fails, nothing of it is stored (the rows'
// flags, the subtitle choices, the transition, the audit entries).
func TestConfirmIsOneWrite(t *testing.T) {
	x := newImportEnv(t)
	j := x.filmJob()
	sub := x.subtitleFile(j)
	before, _ := x.st.Target(x.ctx, j.ID)
	f := &fuse{left: 0}
	dying := x.engineOn(fusedOnlyConfirm{x.st, f}, x.movies, x.series)
	if _, err := dying.Confirm(x.ctx, "user1", "", true, j, ConfirmRequest{Subtitles: []SubtitleChoice{{FileID: sub.ID, Language: "es"}}}); err == nil {
		t.Fatal("the write did not fail")
	}
	after, _ := x.st.Target(x.ctx, j.ID)
	log, _ := x.st.AuditLog(x.ctx, j.ID)
	if x.jobNow(j).State != store.AwaitingConfirmation || string(after.Options) != string(before.Options) || len(log) != 0 || x.rows(j)[0].Result != "" {
		t.Fatalf("a failed confirmation left %s, options %s, %d audit entries", x.jobNow(j).State, after.Options, len(log))
	}
}

// fusedOnlyConfirm fails the confirmation's write only.
type fusedOnlyConfirm struct {
	store.Store
	f *fuse
}

func (s fusedOnlyConfirm) ConfirmImport(ctx context.Context, id string, t store.Target, rows []store.Row, options json.RawMessage, audit []store.AuditEntry, c store.Change) (store.Job, error) {
	if err := s.f.tick(); err != nil {
		return store.Job{}, err
	}
	return s.Store.ConfirmImport(ctx, id, t, rows, options, audit, c)
}
