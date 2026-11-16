package flows

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/fscheck"
	"github.com/segun-io/fukku/internal/store"
)

// A film: confirm, one ManualImport (move) with the path rebuilt from the job's file and the
// film's id, poll while it runs, then the app is asked: the film has its file and the source
// is gone, so imported, then monitoring as chosen, then done.
func TestImportFilm(t *testing.T) {
	x := newImportEnv(t)
	j := x.filmJob()
	if err := x.st.SetTarget(x.ctx, j.ID, x.target(j, `{"added":true,"monitored":false}`)); err != nil {
		t.Fatal(err)
	}
	x.movies.Library[0].Monitored = true
	j = x.confirm(j)

	j, err := x.step(x.eng, j)
	if err != nil || j.State != store.Importing {
		t.Fatalf("after the submission: %s %v", j.State, err)
	}
	if len(x.movies.Imports) != 1 {
		t.Fatalf("%d imports", len(x.movies.Imports))
	}
	call := x.movies.Imports[0]
	if call.Mode != "move" || len(call.Rows) != 1 || call.Rows[0].Path != x.appPath(j, "Some Film (2001).mkv") || call.Rows[0].MovieID != filmID {
		t.Fatalf("import %+v", call)
	}
	if imp := x.importRecord(j); imp.CommandID != call.CommandID || imp.SubmittedAt.IsZero() || !imp.FinishedAt.IsZero() {
		t.Fatalf("import record %+v", imp)
	}
	iv, rv := x.detail(j)
	if iv.Status != ImportRunning || rv != nil {
		t.Errorf("detail while running: %+v %+v", iv, rv)
	}

	// Still running: nothing changes.
	if j, err = x.step(x.eng, j); err != nil || j.State != store.Importing || len(x.movies.Imports) != 1 {
		t.Fatalf("while running: %s %v, %d imports", j.State, err, len(x.movies.Imports))
	}
	x.movies.Complete(call.CommandID)
	if _, err := os.Lstat(x.src(j, "files/Some Film (2001).mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the fake did not move the file")
	}
	if j, err = x.step(x.eng, j); err != nil || j.State != store.Imported {
		t.Fatalf("after the command: %s (%s) %v", j.State, j.ErrorText, err)
	}
	if got := results(x.rows(j)); !slices.Equal(got, []string{store.ResultImported}) {
		t.Errorf("results %v", got)
	}
	if j, err = x.step(x.eng, j); err != nil || j.State != store.PostProcessing {
		t.Fatalf("imported step: %s %v", j.State, err)
	}
	if x.movies.Library[0].Monitored {
		t.Error("the film was not unmonitored as chosen")
	}
	// No step registered: straight to done.
	if j, err = x.step(x.eng, j); err != nil || j.State != store.Done {
		t.Fatalf("post-processing: %s %v", j.State, err)
	}
	if len(x.movies.Imports) != 1 {
		t.Errorf("%d imports", len(x.movies.Imports))
	}
	iv, rv = x.detail(j)
	if iv.Status != ImportDone || len(iv.Rows) != 1 || iv.Rows[0].Result != store.ResultImported || rv == nil || rv.TitleLink != nil || len(rv.PostProcessing) != 0 {
		t.Errorf("detail when done: %+v %+v", iv, rv)
	}
}

func (x *ienv) target(j store.Job, options string) store.Target {
	x.t.Helper()
	tg, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	tg.Options = json.RawMessage(options)
	return tg
}

func (x *ienv) detail(j store.Job) (*ImportView, *ResultView) {
	x.t.Helper()
	iv, rv, err := x.eng.Detail(x.ctx, x.jobNow(j), true)
	if err != nil {
		x.t.Fatal(err)
	}
	return iv, rv
}

// A season with a row the user skipped: one ManualImport with the other rows, each with the
// series and its episodes; the skipped row is recorded as skipped, never sent.
func TestImportSeasonWithASkippedRow(t *testing.T) {
	x := newImportEnv(t)
	j := x.seasonJob(3)
	rows := x.rows(j)
	x.edit(j, rows[1].ID, Edit{Skip: ptr(true)})
	j = x.confirm(j)
	if iv, _ := x.detail(j); iv.Status != ImportPending || len(iv.Rows) != 3 || iv.Rows[1].Result != store.ResultSkipped {
		t.Fatalf("detail before the submission: %+v", iv)
	}
	j = x.drive(x.eng, j)
	if j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if len(x.series.Imports) != 1 {
		t.Fatalf("%d imports", len(x.series.Imports))
	}
	sent := x.series.Imports[0].Rows
	if len(sent) != 2 || sent[0].Path != x.appPath(j, "Show.S01E01.mkv") || sent[1].Path != x.appPath(j, "Show.S01E03.mkv") {
		t.Fatalf("sent %+v", sent)
	}
	for _, r := range sent {
		if r.SeriesID != showID || r.MovieID != 0 || len(r.EpisodeIDs) != 1 {
			t.Errorf("row %+v", r)
		}
	}
	if got := results(x.rows(j)); !slices.Equal(got, []string{store.ResultImported, store.ResultSkipped, store.ResultImported}) {
		t.Errorf("results %v", got)
	}
	if _, err := os.Lstat(x.src(j, "files/Show.S01E02.mkv")); err != nil {
		t.Errorf("the skipped file: %v", err)
	}
	eps, _ := x.series.Episodes(x.ctx, showID)
	if eps[0].FileID == 0 || eps[1].FileID != 0 || eps[2].FileID == 0 {
		t.Errorf("episodes %+v", eps[:3])
	}
}

// A row with a rejection is imported only when the user lists it in overrides; then it is
// flagged as overridden, and the confirmation says which reasons were overridden.
func TestOverriddenRejection(t *testing.T) {
	x := newImportEnv(t)
	j := x.job("sonarr", showID, vid("Show.S01E01.mkv", epRuntime), vid("Show.S01E02.mkv", epRuntime))
	x.onDisk(j)
	x.series.Proposed[x.folder(j)] = []arr.Row{x.item(j, "Show.S01E01.mkv", webdl1080), x.item(j, "Show.S01E02.mkv", webdl1080)}
	x.series.Verdicts = map[string][]arr.Rejection{x.appPath(j, "Show.S01E02.mkv"): {{Reason: "Not an upgrade for existing episode file(s)", Permanent: true}}}
	x.ready(j)
	rows := x.rows(j)

	_, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{})
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Code != CodeNeedsOverride || !slices.Equal(refusal.Rows, []string{rows[1].ID}) {
		t.Fatalf("without the override: %v (%+v)", err, refusal)
	}
	if x.jobNow(j).State != store.AwaitingConfirmation {
		t.Fatal("a refused confirmation moved the job")
	}
	if _, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{Overrides: []string{"r999"}}); Code(err) != CodeBadRequest {
		t.Errorf("an unknown row: %v", err)
	}
	c, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{Overrides: []string{rows[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Overridden) != 1 || c.Overridden[0].RowID != rows[1].ID || c.Overridden[0].File != "Show.S01E02.mkv" ||
		!slices.Equal(c.Overridden[0].Rejections, []string{"Not an upgrade for existing episode file(s)"}) || !slices.Equal(c.Rows, []string{rows[0].ID, rows[1].ID}) {
		t.Fatalf("confirmation %+v", c)
	}
	if got := x.rows(j); got[0].Override || !got[1].Override {
		t.Errorf("override flags %v %v", got[0].Override, got[1].Override)
	}
	j = x.drive(x.eng, j)
	if j.State != store.Done || len(x.series.Imports) != 1 || len(x.series.Imports[0].Rows) != 2 {
		t.Fatalf("job %s, imports %+v", j.State, x.series.Imports)
	}
}

// The other refusals of the confirmation.
func TestConfirmRefusals(t *testing.T) {
	x := newImportEnv(t)
	j := x.seasonJob(2)
	rows := x.rows(j)

	// A row without episodes cannot be imported, even overridden.
	x.edit(j, rows[0].ID, Edit{EpisodeIDs: &[]int{}})
	_, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{Overrides: []string{rows[0].ID}})
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Code != CodeIncompleteRows || !slices.Equal(refusal.Rows, []string{rows[0].ID}) {
		t.Fatalf("incomplete: %v", err)
	}
	x.edit(j, rows[0].ID, Edit{Skip: ptr(true)})
	x.edit(j, rows[1].ID, Edit{Skip: ptr(true)})
	if _, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{}); Code(err) != CodeNothingToImport {
		t.Fatalf("all skipped: %v", err)
	}
	x.edit(j, rows[1].ID, Edit{Skip: ptr(false)})

	// The storage checks.
	x.eng.checkStorage = func() error { return errors.New("/mnt/media/.sentinel is missing") }
	if _, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{}); Code(err) != CodeStorageUnavailable {
		t.Fatalf("storage: %v", err)
	}
	x.eng.checkStorage = func() error { return nil }

	// The app does not answer: a show's rows are judged with its episodes.
	x.series.Err = map[string]error{"Episodes": errRefused}
	if _, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{}); Code(err) != CodeArrUnavailable {
		t.Fatalf("app down: %v", err)
	}
	x.series.Err = nil

	x.confirm(j)
	if _, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{}); Code(err) != CodeWrongState {
		t.Fatalf("twice: %v", err)
	}
}

// The confirmation takes the job's lock (shared with the match and the proposal edits) for
// its write: it waits while another writer holds it.
func TestConfirmTakesTheJobLock(t *testing.T) {
	x := newImportEnv(t)
	j := x.filmJob()
	unlock := x.p.locks.Lock(j.ID)
	done := make(chan error, 1)
	go func() {
		_, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("confirmed while the lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if x.jobNow(j).State != store.AwaitingConfirmation {
		t.Fatal("the job moved while the lock was held")
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if x.jobNow(j).State != store.Importing {
		t.Fatal("not importing")
	}
}

// An edit stored between the judgement of the rows and the write makes the confirmation
// judge them again: a row skipped meanwhile is not imported.
func TestConfirmSeesAnEditMadeMeanwhile(t *testing.T) {
	x := newImportEnv(t)
	j := x.seasonJob(2)
	rows := x.rows(j)
	edited := false
	x.rebuild(x.movies, editingSeries{x.series, func() {
		if !edited {
			edited = true
			r := x.rows(j)[1]
			r.Skip, r.Edit = true, json.RawMessage(`{"skip":true}`)
			if err := x.st.UpdateRow(x.ctx, r); err != nil {
				t.Error(err)
			}
		}
	}})
	c, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Rows, []string{rows[0].ID}) {
		t.Fatalf("rows %v", c.Rows)
	}
}

// editingSeries runs edit after each Episodes call: another writer at work while the app is asked.
type editingSeries struct {
	*arrtest.FakeSeries
	edit func()
}

func (s editingSeries) Episodes(ctx context.Context, id int) ([]arr.Episode, error) {
	eps, err := s.FakeSeries.Episodes(ctx, id)
	s.edit()
	return eps, err
}

// A command that fails with nothing imported: the rows fail, the job fails with
// import_failed and the app's message. A retry submits again, and the import then finishes.
func TestImportCommandFails(t *testing.T) {
	x := newImportEnv(t)
	j := x.seasonJob(2)
	j = x.confirm(j)
	if j, _ = x.step(x.eng, j); len(x.series.Imports) != 1 {
		t.Fatalf("%d imports", len(x.series.Imports))
	}
	x.series.Fail(x.series.Imports[0].CommandID, "Disk full")
	j, err := x.step(x.eng, j)
	if err != nil || j.State != store.Failed || j.ErrorCode != CodeImportFailed || !strings.Contains(j.ErrorText, "Disk full") ||
		!strings.Contains(j.ErrorText, "Show.S01E01.mkv") {
		t.Fatalf("job %s %s %q %v", j.State, j.ErrorCode, j.ErrorText, err)
	}
	if got := results(x.rows(j)); !slices.Equal(got, []string{store.ResultFailed, store.ResultFailed}) {
		t.Errorf("results %v", got)
	}
	if iv, _ := x.detail(j); iv.Status != ImportFailed {
		t.Errorf("detail %+v", iv)
	}
	if imp := x.importRecord(j); imp.FinishedAt.IsZero() {
		t.Errorf("import record %+v", imp)
	}

	// The user retries: the failed command is not reported again; the rows are submitted again.
	if _, err := x.st.Transition(x.ctx, j.ID, store.Failed, store.Importing, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	j = x.drive(x.eng, j)
	if j.State != store.Done || len(x.series.Imports) != 2 || len(x.series.Imports[1].Rows) != 2 {
		t.Fatalf("job %s (%s), %d imports", j.State, j.ErrorText, len(x.series.Imports))
	}
	if got := results(x.rows(j)); !slices.Equal(got, []string{store.ResultImported, store.ResultImported}) {
		t.Errorf("results %v", got)
	}
}

// A command that fails with part imported: the rows that arrived are imported, the others
// failed. A retry submits only the missing rows, so each row is imported once.
func TestImportCommandFailsPartway(t *testing.T) {
	x := newImportEnv(t)
	j := x.seasonJob(3)
	j = x.confirm(j)
	j, _ = x.step(x.eng, j)
	x.series.FailPartway(x.series.Imports[0].CommandID, 1, "Destination already exists")
	j, err := x.step(x.eng, j)
	if err != nil || j.State != store.Failed || j.ErrorCode != CodeImportFailed || strings.Contains(j.ErrorText, "S01E01") {
		t.Fatalf("job %s %q %v", j.State, j.ErrorText, err)
	}
	if got := results(x.rows(j)); !slices.Equal(got, []string{store.ResultImported, store.ResultFailed, store.ResultFailed}) {
		t.Errorf("results %v", got)
	}
	if _, err := x.st.Transition(x.ctx, j.ID, store.Failed, store.Importing, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	j = x.drive(x.eng, j)
	if j.State != store.Done || len(x.series.Imports) != 2 {
		t.Fatalf("job %s, %d imports", j.State, len(x.series.Imports))
	}
	if second := x.series.Imports[1].Rows; len(second) != 2 || second[0].Path != x.appPath(j, "Show.S01E02.mkv") {
		t.Fatalf("second import %+v", second)
	}
	// The imported row is never sent again; the two that failed are sent once more.
	sent := importedPaths(x.seriesImports())
	if sent[x.appPath(j, "Show.S01E01.mkv")] != 1 || sent[x.appPath(j, "Show.S01E02.mkv")] != 2 || sent[x.appPath(j, "Show.S01E03.mkv")] != 2 {
		t.Errorf("sent %v", sent)
	}
	// Each episode got one file.
	files := map[int]bool{}
	for _, ep := range x.series.Eps[showID][:3] {
		if ep.FileID == 0 || files[ep.FileID] {
			t.Errorf("episode %+v", ep)
		}
		files[ep.FileID] = true
	}
	if got := results(x.rows(j)); !slices.Equal(got, []string{store.ResultImported, store.ResultImported, store.ResultImported}) {
		t.Errorf("results %v", got)
	}
}

// A command the app ends "completed" without the files is a failure too: imported is
// decided by asking the app, never assumed.
func TestCompletedWithoutTheFilesFails(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.filmJob())
	j, _ = x.step(x.eng, j)
	x.movies.OnImport = nil // the file stays where it is: not moved
	x.movies.Complete(x.movies.Imports[0].CommandID)
	j, _ = x.step(x.eng, j)
	if j.State != store.Failed || j.ErrorCode != CodeImportFailed || !strings.Contains(j.ErrorText, "not every file arrived") {
		t.Fatalf("job %s %q", j.State, j.ErrorText)
	}
}

// After a crash between the submission and the storing of its id, the command is found
// among the app's (it carries the job folder's paths) and adopted: no second import. One
// for another job's folder is not.
func TestLostCommandIDIsFound(t *testing.T) {
	x := newImportEnv(t)
	other := x.filmJobFor(filmID + 1)
	j := x.confirm(x.filmJob())
	// Another job's command, newer than this one's.
	j, _ = x.step(x.eng, j)
	mine := x.movies.Imports[0].CommandID
	if _, err := x.movies.Import(x.ctx, []arr.Row{{Path: x.appPath(other, "Other.mkv"), MovieID: filmID + 1}}, "move"); err != nil {
		t.Fatal(err)
	}
	imp := x.importRecord(j)
	imp.CommandID = 0 // the id was lost
	if err := x.st.PutImport(x.ctx, imp); err != nil {
		t.Fatal(err)
	}
	x.rebuild(x.movies, x.series)
	if j, _ = x.step(x.eng, j); j.State != store.Importing || x.importRecord(j).CommandID != mine || len(x.movies.Imports) != 2 {
		t.Fatalf("job %s, record %+v, %d imports", j.State, x.importRecord(j), len(x.movies.Imports))
	}
	x.movies.Complete(mine)
	if j = x.drive(x.eng, j); j.State != store.Done {
		t.Fatalf("job %s", j.State)
	}
}

func (x *ienv) filmJobFor(id int) store.Job {
	x.t.Helper()
	x.movies.Library = append(x.movies.Library, arr.Movie{ID: id, Title: "Other Film", Year: 2002})
	j := x.job("radarr", id, vid("Other.mkv", 100*minute))
	return j
}

// Each row's path comes from Fukku's own file, whatever the stored item says, and a row
// whose item names another title is refused before the app is asked.
func TestRowsArePlannedFromTheJobsFiles(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.filmJob())
	rows := x.rows(j)
	ar, _, _ := decodeRow(rows[0])
	ar.Path = "/data/media/movies/Elsewhere/x.mkv"
	ar.Raw = overlay(ar.Raw, json.RawMessage(`{"path":"/data/media/movies/Elsewhere/x.mkv"}`))
	rows[0].Proposal, _ = json.Marshal(ar)
	if err := x.st.UpdateRow(x.ctx, rows[0]); err != nil {
		t.Fatal(err)
	}
	if j, _ = x.step(x.eng, j); len(x.movies.Imports) != 1 {
		t.Fatalf("job %s %q", j.State, j.ErrorText)
	}
	sent := x.movies.Imports[0].Rows[0]
	if sent.Path != x.appPath(j, "Some Film (2001).mkv") {
		t.Errorf("sent %q", sent.Path)
	}

	for name, tamper := range map[string]func(*arr.Row){
		"movieId":   func(ar *arr.Row) { ar.MovieID = filmID + 1 },
		"seriesId":  func(ar *arr.Row) { ar.SeriesID = showID },
		"movie.id":  func(ar *arr.Row) { ar.Raw = overlay(ar.Raw, json.RawMessage(`{"movie":{"id":99}}`)) },
		"no movie":  func(ar *arr.Row) { ar.MovieID = 0 },
		"not ours?": func(ar *arr.Row) { ar.MovieID, ar.SeriesID = 0, filmID },
	} {
		t.Run(name, func(t *testing.T) {
			x := newImportEnv(t)
			j := x.confirm(x.filmJob())
			rows := x.rows(j)
			ar, _, _ := decodeRow(rows[0])
			tamper(&ar)
			rows[0].Proposal, _ = json.Marshal(ar)
			if err := x.st.UpdateRow(x.ctx, rows[0]); err != nil {
				t.Fatal(err)
			}
			j, _ = x.step(x.eng, j)
			if j.State != store.Failed || j.ErrorCode != CodeImportFailed || len(x.movies.Imports) != 0 {
				t.Fatalf("job %s %q, %d imports", j.State, j.ErrorText, len(x.movies.Imports))
			}
		})
	}
}

// A missing sentinel, or a library root on another filesystem with importMode move, refuses
// the import before the app is called. With importMode copy the filesystems may differ.
func TestStorageChecksRefuseTheImport(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc, a filesystem of its own")
	}
	x := newImportEnv(t)
	sentinel := filepath.Join(x.root, ".sentinel")
	if err := os.WriteFile(sentinel, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	x.cfg.Paths.Sentinels = []string{sentinel}
	x.rebuild(x.movies, x.series)
	x.eng.checkStorage = NewEngine(x.p).checkStorage // the real checks
	j := x.confirm(x.filmJob())
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}
	if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeImportFailed || !strings.Contains(j.ErrorText, ".sentinel is missing") {
		t.Fatalf("missing sentinel: %s %q", j.State, j.ErrorText)
	}
	if len(x.movies.Imports) != 0 {
		t.Fatal("the app was asked to import")
	}

	// Another filesystem.
	if err := os.WriteFile(sentinel, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fscheck.SameFilesystem(x.cfg); err != nil {
		t.Fatalf("before: %v", err)
	}
	x.cfg.Paths.LibraryRoots = []string{"/proc"}
	x.cfg.Radarr.URL = "http://radarr.example.com:7878"
	x.cfg.Radarr.ImportMode = "move"
	x.rebuild(x.movies, x.series)
	x.eng.checkStorage = NewEngine(x.p).checkStorage
	if _, err := x.st.Transition(x.ctx, j.ID, store.Failed, store.Importing, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	if j, _ = x.step(x.eng, j); j.State != store.Failed || !strings.Contains(j.ErrorText, "different filesystem") || len(x.movies.Imports) != 0 {
		t.Fatalf("another filesystem: %s %q", j.State, j.ErrorText)
	}

	// Copy: the check does not apply.
	x.cfg.Radarr.ImportMode = "copy"
	x.rebuild(x.movies, x.series)
	x.eng.checkStorage = NewEngine(x.p).checkStorage
	if _, err := x.st.Transition(x.ctx, j.ID, store.Failed, store.Importing, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	if j, _ = x.step(x.eng, j); j.State != store.Importing || len(x.movies.Imports) != 1 || x.movies.Imports[0].Mode != "copy" {
		t.Fatalf("copy: %s %q, %+v", j.State, j.ErrorText, x.movies.Imports)
	}
	// In copy mode the source stays: the film's file is enough.
	x.movies.OnImport = nil
	x.movies.Complete(x.movies.Imports[0].CommandID)
	if j = x.drive(x.eng, j); j.State != store.Done {
		t.Fatalf("copy: %s %q", j.State, j.ErrorText)
	}
}

// The title being downloaded by the app refuses the import (title_busy) before it is
// submitted; the sources must still be there.
func TestBusyTitleAndMissingSource(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.filmJob())
	x.movies.Queue = map[int]bool{filmID: true}
	if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeTitleBusy || len(x.movies.Imports) != 0 {
		t.Fatalf("busy: %s %s", j.State, j.ErrorCode)
	}
	x.movies.Queue = nil
	if err := os.Remove(x.src(j, "files/Some Film (2001).mkv")); err != nil {
		t.Fatal(err)
	}
	if _, err := x.st.Transition(x.ctx, j.ID, store.Failed, store.Importing, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeImportFailed || !strings.Contains(j.ErrorText, "is gone") || len(x.movies.Imports) != 0 {
		t.Fatalf("missing source: %s %q", j.State, j.ErrorText)
	}
}

// The app refusing the import outright (a 400) fails the job; there is no command.
func TestImportRefusedByTheApp(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.filmJob())
	x.movies.Err = map[string]error{"Import": &arr.Error{App: "radarr", Method: "POST", Path: "/command", Status: 400, Message: "Invalid path"}}
	if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeImportFailed || !strings.Contains(j.ErrorText, "Invalid path") {
		t.Fatalf("job %s %q", j.State, j.ErrorText)
	}
}

// Monitoring after the import: a film is set as chosen at the match; with
// merge.unmonitorAfterDubOnlyImport a file with only a dub unmonitors the film or the
// episodes (B6); a show's monitoring is otherwise unchanged.
func TestMonitoringAfterTheImport(t *testing.T) {
	t.Run("film as chosen", func(t *testing.T) {
		x := newImportEnv(t)
		j := x.filmJob()
		_ = x.st.SetTarget(x.ctx, j.ID, x.target(j, `{"added":true,"monitored":true}`))
		x.movies.Library[0].OriginalLanguage = "English"
		if j = x.drive(x.eng, x.confirm(j)); j.State != store.Done || !x.movies.Library[0].Monitored {
			t.Fatalf("job %s, monitored %v", j.State, x.movies.Library[0].Monitored)
		}
	})
	t.Run("film in the library already", func(t *testing.T) {
		x := newImportEnv(t)
		x.movies.Library[0].Monitored = true
		if j := x.drive(x.eng, x.confirm(x.filmJob())); j.State != store.Done || !x.movies.Library[0].Monitored {
			t.Fatalf("job %s, monitored %v", j.State, x.movies.Library[0].Monitored)
		}
	})
	t.Run("film with only a dub", func(t *testing.T) {
		x := newImportEnv(t)
		j := x.filmJob()
		_ = x.st.SetTarget(x.ctx, j.ID, x.target(j, `{"added":true,"monitored":true}`))
		x.movies.Library[0].OriginalLanguage = "English"
		rows := x.rows(j)
		x.edit(j, rows[0].ID, Edit{LanguageIDs: &[]int{spanish.ID}})
		j = x.drive(x.eng, x.confirm(j))
		if j.State != store.Done || x.movies.Library[0].Monitored {
			t.Fatalf("job %s, monitored %v", j.State, x.movies.Library[0].Monitored)
		}
		h, _ := x.st.History(x.ctx, j.ID)
		if !slices.ContainsFunc(h, func(e store.Event) bool { return strings.Contains(e.Note, "only a dub") }) {
			t.Error("the history does not say why")
		}
	})
	t.Run("episodes with only a dub", func(t *testing.T) {
		x := newImportEnv(t)
		x.series.Library[0].OriginalLanguage = "English"
		for i := range x.series.Eps[showID] {
			x.series.Eps[showID][i].Monitored = true
		}
		j := x.seasonJob(2)
		rows := x.rows(j)
		x.edit(j, rows[1].ID, Edit{LanguageIDs: &[]int{spanish.ID}})
		if j = x.drive(x.eng, x.confirm(j)); j.State != store.Done {
			t.Fatalf("job %s", j.State)
		}
		eps := x.series.Eps[showID]
		if !eps[0].Monitored || eps[1].Monitored || !eps[2].Monitored {
			t.Errorf("monitored %v %v %v", eps[0].Monitored, eps[1].Monitored, eps[2].Monitored)
		}
	})
	t.Run("dub rule off", func(t *testing.T) {
		x := newImportEnv(t)
		x.cfg.Merge.UnmonitorAfterDubOnlyImport = false
		x.rebuild(x.movies, x.series)
		x.movies.Library[0].Monitored, x.movies.Library[0].OriginalLanguage = true, "English"
		j := x.filmJob()
		x.edit(j, x.rows(j)[0].ID, Edit{LanguageIDs: &[]int{spanish.ID}})
		if j = x.drive(x.eng, x.confirm(j)); j.State != store.Done || !x.movies.Library[0].Monitored {
			t.Fatalf("job %s, monitored %v", j.State, x.movies.Library[0].Monitored)
		}
	})
}

func TestDubOnly(t *testing.T) {
	for _, c := range []struct {
		orig  string
		langs []string
		want  bool
	}{
		{"English", []string{"Spanish"}, true},
		{"English", []string{"Spanish", "English"}, false},
		{"English", []string{"english"}, false},
		{"English", []string{"Unknown"}, false},
		{"English", nil, false},
		{"", []string{"Spanish"}, false},
	} {
		var ls []arr.Language
		for _, n := range c.langs {
			ls = append(ls, arr.Language{Name: n})
		}
		if got := dubOnly(c.orig, ls); got != c.want {
			t.Errorf("dubOnly(%q, %v) = %v", c.orig, c.langs, got)
		}
	}
}

func TestReportedCommands(t *testing.T) {
	h := []store.Event{
		{From: store.Importing, To: store.Failed, Note: failNote(12, 2)},
		{From: store.Importing, To: store.Failed, Note: "something else"},
		{From: store.Importing, To: store.Stalled, Note: failNote(13, 1)},
		{From: store.Failed, To: store.Importing},
	}
	if got := reportedCommands(h); len(got) != 1 || !got[12] {
		t.Errorf("reported %v", got)
	}
}
