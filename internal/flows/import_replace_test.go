package flows

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/store"
)

// Each submission checks again that the import replaces no file the title got after the
// confirmation (#116, review of #141): the check at the confirmation is not enough, since a
// retry, a resume after a stall or a crash submits later, when the app may have a file.

// retry moves a failed job back to importing, as POST /api/jobs/{id}/retry does.
func (x *ienv) retry(j store.Job) store.Job {
	x.t.Helper()
	j, err := x.st.Transition(x.ctx, j.ID, store.Failed, store.Importing, store.Change{Actor: "user1"})
	if err != nil {
		x.t.Fatal(err)
	}
	return j
}

// A new film's import fails title_busy while Radarr downloads it; the download lands; the
// retry does not submit: the job goes back to probed, to be matched again (flow 2 now).
func TestRetryDoesNotReplaceAFilmThatGotAFile(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.filmJob())
	x.movies.Queue = map[int]bool{filmID: true}
	if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeTitleBusy {
		t.Fatalf("busy: %s %s", j.State, j.ErrorCode)
	}
	x.movies.Queue = nil
	x.movies.Library[0].File = &arr.MovieFile{ID: 90, Size: 5000}
	x.movies.Library[0].HasFile = true
	if j, _ = x.step(x.eng, x.retry(j)); j.State != store.Probed || len(x.movies.Imports) != 0 {
		t.Fatalf("the retry: job %s (%s), %d imports", j.State, j.ErrorText, len(x.movies.Imports))
	}
	if _, err := os.Stat(x.src(j, "files/Some Film (2001).mkv")); err != nil {
		t.Errorf("the upload is gone: %v", err)
	}
}

// The same for an episode that got a file, and the episodes without one are not sent either:
// the job is matched again, and its proposal asks what to do with that episode.
func TestRetryDoesNotReplaceAnEpisodeThatGotAFile(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.seasonJob(2))
	x.series.Queue = map[int]bool{showID: true}
	if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeTitleBusy {
		t.Fatalf("busy: %s %s", j.State, j.ErrorCode)
	}
	x.series.Queue = nil
	x.series.Eps[showID][1].FileID = 702
	if j, _ = x.step(x.eng, x.retry(j)); j.State != store.Probed || len(x.series.Imports) != 0 {
		t.Fatalf("the retry: job %s (%s), %d imports", j.State, j.ErrorText, len(x.series.Imports))
	}
}

// Resumed after a stall during the submission (the app did not answer the import call): the
// import is recorded as submitted, so the job fails with library_has_file when the film has
// a file by then, and nothing is sent again.
func TestResumeDoesNotReplaceAFilmThatGotAFile(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.filmJob())
	x.movies.Err = map[string]error{"Import": errRefused}
	if j, _ = x.step(x.eng, j); j.State != store.Stalled || len(x.movies.Imports) != 0 {
		t.Fatalf("stall: %s (%s)", j.State, j.ErrorText)
	}
	x.movies.Err = nil
	x.movies.Library[0].File = &arr.MovieFile{ID: 90, Size: 5000}
	x.movies.Library[0].HasFile = true
	x.clock.advance(time.Minute)
	if j, _ = x.step(x.eng, j); j.State != store.Importing { // the app answers again
		t.Fatalf("resumed: %s", j.State)
	}
	if j, _ = x.step(x.eng, x.jobNow(j)); j.State != store.Failed || j.ErrorCode != CodeLibraryHasFile || len(x.movies.Imports) != 0 {
		t.Fatalf("the resume: job %s (%s: %s), %d imports", j.State, j.ErrorCode, j.ErrorText, len(x.movies.Imports))
	}
}

// The same for episodes; a row whose episode had a file at the confirmation and is covered
// by replace still goes, which the season-dub tests show.
func TestResumeDoesNotReplaceAnEpisodeThatGotAFile(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.seasonJob(2))
	x.series.Err = map[string]error{"Import": errRefused}
	if j, _ = x.step(x.eng, j); j.State != store.Stalled {
		t.Fatalf("stall: %s (%s)", j.State, j.ErrorText)
	}
	x.series.Err = nil
	x.series.Eps[showID][0].FileID = 701
	x.clock.advance(time.Minute)
	if j, _ = x.step(x.eng, j); j.State != store.Importing { // the app answers again
		t.Fatalf("resumed: %s", j.State)
	}
	if j, _ = x.step(x.eng, x.jobNow(j)); j.State != store.Failed || j.ErrorCode != CodeLibraryHasFile || len(x.series.Imports) != 0 {
		t.Fatalf("the resume: job %s (%s: %s), %d imports", j.State, j.ErrorCode, j.ErrorText, len(x.series.Imports))
	}
	// The text names the episode and the file, not the row's id.
	if !strings.Contains(j.ErrorText, "S01E01 (Show.S01E01.mkv) has a file in Sonarr now") || strings.Contains(j.ErrorText, "r1") {
		t.Errorf("error text %q", j.ErrorText)
	}
}

// A row the confirmation chose to replace replaces the file its episode had then, and no
// other: when Sonarr got a new file for that episode since (a grab), the retry refuses it.
func TestRetryUnderReplaceRefusesAFileThatChanged(t *testing.T) {
	x := dubEnv(t, nil, 101)
	j := x.dubJob()
	if _, err := x.confirmDub(j, PolicyReplace); err != nil {
		t.Fatal(err)
	}
	j = x.jobNow(j)
	tg, _ := x.st.Target(x.ctx, j.ID)
	if c, _ := seasonDubOf(tg); c.Files["101"] != 601 {
		t.Fatalf("stored files %v", c.Files)
	}
	x.series.Queue = map[int]bool{showID: true}
	if j, _ = x.step(x.eng, j); j.State != store.Failed {
		t.Fatalf("busy: %s", j.State)
	}
	x.series.Queue = nil
	x.series.Eps[showID][0].FileID = 999 // a new release of S01E01
	if j, _ = x.step(x.eng, x.retry(j)); j.State != store.Probed || len(x.series.Imports) != 0 {
		t.Fatalf("the retry: job %s (%s), %d imports", j.State, j.ErrorText, len(x.series.Imports))
	}
	h, _ := x.st.History(x.ctx, j.ID)
	if last := h[len(h)-1]; !strings.Contains(last.Note, "S01E01 (Show.S01E01.mkv) has a file in Sonarr now") {
		t.Errorf("note %q", last.Note)
	}
}

// A choice stored before the files were kept (an upgrade) is not compared.
func TestReplaceChoiceWithoutFilesIsNotCompared(t *testing.T) {
	t1 := store.Target{App: AppSonarr}
	var err error
	if t1.Options, err = withOption(nil, "seasonDub", seasonDubChoice{Policy: PolicyReplace, Rows: []string{"r1"}}); err != nil {
		t.Fatal(err)
	}
	plan := []planned{{row: store.Row{ID: "r1"}, ar: arr.Row{EpisodeIDs: []int{101}}}}
	now := tracked{Episodes: map[string]int{"101": 999}}
	if got := replacesUnchosen(t1, plan, []bool{false}, now); len(got) != 0 {
		t.Errorf("refused %v", got)
	}
	if t1.Options, err = withOption(nil, "seasonDub", seasonDubChoice{Policy: PolicyReplace, Rows: []string{"r1"}, Files: map[string]int{"101": 601}}); err != nil {
		t.Fatal(err)
	}
	if got := replacesUnchosen(t1, plan, []bool{false}, now); len(got) != 1 {
		t.Errorf("not refused: %v", got)
	}
}

// Under replace, the rows the confirmation chose to replace are submitted on a retry, and
// a row whose episode got a file after the confirmation is not.
func TestRetryUnderReplaceSendsOnlyTheChosenRows(t *testing.T) {
	x := dubEnv(t, nil, 101)
	j := x.dubJob()
	if _, err := x.confirmDub(j, PolicyReplace); err != nil {
		t.Fatal(err)
	}
	j = x.jobNow(j)
	x.series.Queue = map[int]bool{showID: true}
	if j, _ = x.step(x.eng, j); j.State != store.Failed || j.ErrorCode != CodeTitleBusy {
		t.Fatalf("busy: %s %s", j.State, j.ErrorCode)
	}
	x.series.Queue = nil
	if j, _ = x.step(x.eng, x.retry(j)); j.State != store.Importing || len(x.series.Imports) != 1 {
		t.Fatalf("the retry with only the chosen file: job %s (%s), %d imports", j.State, j.ErrorText, len(x.series.Imports))
	}

	y := dubEnv(t, nil, 101)
	k := y.dubJob()
	if _, err := y.confirmDub(k, PolicyReplace); err != nil {
		t.Fatal(err)
	}
	k = y.jobNow(k)
	y.series.Queue = map[int]bool{showID: true}
	if k, _ = y.step(y.eng, k); k.State != store.Failed {
		t.Fatalf("busy: %s", k.State)
	}
	y.series.Queue = nil
	y.series.Eps[showID][2].FileID = 703 // S01E03 had no file at the confirmation
	if k, _ = y.step(y.eng, y.retry(k)); k.State != store.Probed || len(y.series.Imports) != 0 {
		t.Fatalf("the retry: job %s (%s), %d imports", k.State, k.ErrorText, len(y.series.Imports))
	}
}
