package flows

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/merge"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

// errRefused400 is an app's answer that refuses the call.
var errRefused400 error = &arr.Error{App: "radarr", Method: "POST", Path: "/command", Status: 400}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A confirmation that read the job before another one moved it on (a double click, a second
// tab) is refused without touching the running import's record: no second ManualImport.
func TestMergeConfirmRaceKeepsTheRunningImport(t *testing.T) {
	x := newMergeEnv(t)
	j := x.readyJob()
	s, err := x.p.edit(x.ctx, j.ID) // the slower confirmation's session, read first
	if err != nil {
		t.Fatal(err)
	}
	rec := x.flow2Record(j)
	x.confirmMerge(j, nil) // the faster one
	running := store.Import{JobID: j.ID, CommandID: 77, SubmittedAt: x.clock.now()}
	if err := x.st.PutImport(x.ctx, running); err != nil {
		t.Fatal(err)
	}
	if _, err := x.eng.confirmMerge(x.ctx, "user1", "", s, rec, mergeReq(nil)); Code(err) != CodeWrongState {
		t.Fatalf("err %v, want wrong_state", err)
	}
	if got := x.importRecord(j); got.CommandID != 77 || !got.SubmittedAt.Equal(running.SubmittedAt) {
		t.Fatalf("the running import's record was changed: %+v", got)
	}
}

// What an earlier attempt's merge left goes once its task has ended: a result that comes
// late (the task was still running) is acknowledged and its directory removed. A merged file
// in use by a retried import stays.
func TestMergeEarlierAttemptsAreRemoved(t *testing.T) {
	x := newMergeEnv(t)
	j := x.confirmMerge(x.readyJob(), nil)
	if j = x.run(x.eng, j, func(o *merge.Output) { *o = merge.Output{Refusal: merge.RefusalFreeSpace} }); j.State != store.Failed {
		t.Fatalf("job %s", j.State)
	}
	// Attempt 1's task left a file and a result after all (a crash before they were cleared).
	x.leave(j, "merged/1/Some Film (2001) WEBDL-1080p.mkv")
	stale := spool.Task{ID: runner.MergeID(j.ID, 1), Kind: spool.KindMerge, Job: j.ID}
	if err := x.q.Submit(x.ctx, stale); err != nil {
		t.Fatal(err)
	}
	x.q.Complete(stale.ID, merge.Output{Refusal: merge.RefusalFreeSpace})
	var err error
	if j, err = x.st.Transition(x.ctx, j.ID, store.Failed, j.Resume, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	x.pass(j)
	if left := x.mergedOnDisk(j); len(left) != 0 {
		t.Fatalf("attempt 1's file is still there: %v", left)
	}
	if _, ok, _ := x.q.Result(x.ctx, stale.ID); ok {
		t.Fatal("attempt 1's result was not acknowledged")
	}
	x.propose(j, 2)
	if j = x.run(x.eng, j, nil); j.State != store.Done || len(x.movies.Imports) != 1 {
		t.Fatalf("job %s (%s), %d imports", j.State, j.ErrorText, len(x.movies.Imports))
	}
	x.noMergeLeftovers(j)
}

// A retried import keeps the merged file of the attempt that made it, even when an earlier
// attempt's late result comes in.
func TestMergeRetriedImportKeepsItsFile(t *testing.T) {
	x := newMergeEnv(t)
	x.movies.Err = map[string]error{"Import": errRefused400}
	j := x.confirmMerge(x.readyJob(), nil)
	x.propose(j, 1)
	if j = x.run(x.eng, j, nil); j.State != store.Failed || j.ErrorCode != CodeImportFailed {
		t.Fatalf("job %s %s (%s)", j.State, j.ErrorCode, j.ErrorText)
	}
	x.movies.Err = nil
	var err error
	if j, err = x.st.Transition(x.ctx, j.ID, store.Failed, j.Resume, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	stale := spool.Task{ID: runner.MergeID(j.ID, 1), Kind: spool.KindMerge, Job: j.ID}
	if err := x.q.Submit(x.ctx, stale); err != nil {
		t.Fatal(err)
	}
	x.q.Complete(stale.ID, merge.Output{Refusal: merge.RefusalBusy})
	if j = x.run(x.eng, j, nil); j.State != store.Done || len(x.movies.Imports) != 1 || x.merges != 1 {
		t.Fatalf("job %s (%s), %d imports, %d merges", j.State, j.ErrorText, len(x.movies.Imports), x.merges)
	}
}

// With importMode copy Radarr leaves the merged file in the job: it goes once the job is
// done. The upload stays.
func TestMergeDoneRemovesTheMergedFile(t *testing.T) {
	x := newMergeEnvWith(t, func(c *config.Config) { c.Radarr.ImportMode = "copy" })
	x.movies.OnImport = nil // a copy: the merged file stays where it is
	j := x.confirmMerge(x.readyJob(), nil)
	x.propose(j, 1)
	for i := 0; i < 50 && x.jobNow(j).State != store.PostProcessing; i++ {
		x.pass(x.jobNow(j))
		x.worker(nil)
		x.appWorks()
		x.clock.advance(time.Hour)
	}
	if left := x.mergedOnDisk(j); len(left) != 1 {
		t.Fatalf("before done: %v", left)
	}
	if j = x.run(x.eng, j, nil); j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if left := x.mergedOnDisk(j); len(left) != 0 {
		t.Errorf("after done: %v", left)
	}
	if _, err := os.Stat(x.src(j, "files/Some Film (2001).es.mkv")); err != nil {
		t.Errorf("the upload: %v", err)
	}
}

// A merge imports its merged file only: a row of the upload under files/ is refused before
// the import, so the dub alone never replaces the film's file.
func TestMergePlanTakesOnlyTheMergedFile(t *testing.T) {
	x := newMergeEnv(t)
	j := x.confirmMerge(x.readyJob(), nil)
	x.propose(j, 1)
	for i := 0; i < 5 && x.choice(j).Output == nil; i++ {
		x.pass(j)
		x.worker(nil)
	}
	c := x.choice(j)
	files, _ := x.st.Files(x.ctx, j.ID)
	ar := arr.Row{Path: x.appPath(j, "Some Film (2001).es.mkv"), Size: 1000, MovieID: filmID, Quality: webdl1080}
	data := mustJSON(t, ar)
	up := store.Row{ID: RowID(c.UploadFileID), JobID: j.ID, FileID: c.UploadFileID, Proposal: data}
	if _, err := x.eng.plan(j, x.tgt(j), []store.Row{up}, files); err == nil || !strings.Contains(err.Error(), "not one of the job's videos") {
		t.Fatalf("the upload's row: %v", err)
	}
	// Before the merged file is in, nothing at all can be planned.
	tg := x.tgt(j)
	c.Output = nil
	tg.Options, _ = withOption(tg.Options, "merge", c)
	if _, err := x.eng.plan(j, tg, []store.Row{up}, files); err == nil {
		t.Fatal("a merge without its merged file planned a row")
	}
}

// A tag Radarr has no language for: the import carries the existing languages only, and the
// result says so.
func TestMergeLanguageUnknownToRadarr(t *testing.T) {
	x := newMergeEnv(t)
	tag := "eu"
	j := x.confirmMerge(x.readyJob(), &MergeRequest{LanguageTag: &tag})
	if c := x.choice(j); c.ArrLanguage != nil {
		t.Fatalf("arrLanguage %+v", c.ArrLanguage)
	}
	x.propose(j, 1)
	if j = x.run(x.eng, j, nil); j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if r := x.movies.Imports[0].Rows[0]; len(r.Languages) != 1 || r.Languages[0] != english {
		t.Errorf("languages %+v", r.Languages)
	}
	_, rv, err := x.eng.Detail(x.ctx, j, false)
	if err != nil {
		t.Fatal(err)
	}
	codes := []string{}
	for _, w := range rv.Merge.Warnings {
		codes = append(codes, w.Code)
	}
	if strings.Join(codes, ",") != WarnSearchCanReplace+","+WarnLanguageUnknown {
		t.Errorf("warnings %v", codes)
	}
}
