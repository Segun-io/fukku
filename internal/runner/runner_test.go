package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/extract"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

func wantState(t *testing.T, j store.Job, state store.State, code string) {
	t.Helper()
	if j.State != state || j.ErrorCode != code {
		t.Fatalf("job is %s (%q, %q), want %s (%q)", j.State, j.ErrorCode, j.ErrorText, state, code)
	}
}

func wantFile(t *testing.T, f store.File, status store.FileStatus, reason string) {
	t.Helper()
	if f.Status != status || f.Reason != reason {
		t.Errorf("%s is %s (%q), want %s (%q)", f.Path, f.Status, f.Reason, status, reason)
	}
}

func wantClean(t *testing.T, e *env, id string) {
	t.Helper()
	if l := e.leftovers(id); len(l) > 0 {
		t.Errorf("left in the spool: %v", l)
	}
}

func wantDisk(t *testing.T, e *env, id string, paths ...string) {
	t.Helper()
	var got []string
	for p := range e.onDisk(id) {
		if !strings.HasSuffix(p, "/") {
			got = append(got, p)
		}
	}
	slices.Sort(got)
	slices.Sort(paths)
	if !slices.Equal(got, paths) {
		t.Errorf("on disk: %v, want %v", got, paths)
	}
}

func TestOneVideo(t *testing.T) {
	e := newEnv(t)
	j := e.newJob(video("Film (2001).mkv"))
	j = e.run(j.ID, store.Probed)

	if want := t0.Add(e.cfg.Upload.JobRetention.D()); !j.ExpiresAt.Equal(want) {
		t.Errorf("expires %s, want %s", j.ExpiresAt, want)
	}
	f := e.file(j.ID, "files/Film (2001).mkv")
	wantFile(t, f, store.FileOK, "")
	if string(f.Report) != probeJSON(t, "Film (2001).mkv") {
		t.Errorf("report %s", f.Report)
	}
	if want := []string{j.ID + "-1-p"}; !slices.Equal(e.w.ran, want) {
		t.Errorf("tasks %v, want %v", e.w.ran, want)
	}
	wantClean(t, e, j.ID)
	hist, _ := e.st.History(e.ctx, j.ID)
	var path []string
	for _, ev := range hist {
		path = append(path, string(ev.To))
	}
	if want := "uploaded probing probed"; strings.Join(path, " ") != want {
		t.Errorf("history %v, want %s", path, want)
	}
	for _, msg := range []string{"job_state", "task_submitted", "task_finished"} {
		if e.logs.count(msg) == 0 {
			t.Errorf("no %s in the log:\n%s", msg, e.logs)
		}
	}
	if !strings.Contains(e.logs.String(), "msg=task_finished job="+j.ID+" kind=probe") {
		t.Errorf("task_finished without job and kind:\n%s", e.logs)
	}
}

func TestVideoAndSubtitle(t *testing.T) {
	e := newEnv(t)
	var args media.ProbeArgs
	e.w.hold = func(task spool.Task) bool {
		_ = json.Unmarshal(task.Args, &args)
		return false
	}
	j := e.newJob(video("Film.mkv"), subtitle("Film.en.srt"))
	j = e.run(j.ID, store.Probed)
	if want := []string{"files/Film.en.srt", "files/Film.mkv"}; !slices.Equal(args.Paths, want) {
		t.Errorf("probe paths %v, want %v", args.Paths, want)
	}
	wantFile(t, e.file(j.ID, "files/Film.mkv"), store.FileOK, "")
	sub := e.file(j.ID, "files/Film.en.srt")
	wantFile(t, sub, store.FileOK, "")
	if sub.Kind != store.KindSubtitle || sub.Report != nil {
		t.Errorf("subtitle %+v", sub)
	}
	wantClean(t, e, j.ID)
}

func TestZipWithSeveralFiles(t *testing.T) {
	e := newEnv(t)
	e.w.extracts = map[string]extractPlan{"archives/Show S01.zip": {
		files: map[string]string{
			"Season 01/Show - S01E01.mkv":    "e1",
			"Season 01/Show - S01E02.mkv":    "e2",
			"Season 01/Show - S01E01.en.srt": "s1",
		},
		skipped: []extract.Skipped{{Name: "../evil.mkv", Reason: extract.ReasonTraversal}},
	}}
	var extractArgs extract.Args
	e.w.hold = func(task spool.Task) bool {
		if task.Kind == spool.KindExtract {
			_ = json.Unmarshal(task.Args, &extractArgs)
		}
		return false
	}
	j := e.newJob(archive("Show S01.zip"))
	j = e.run(j.ID, store.Probed)

	zip := e.file(j.ID, "archives/Show S01.zip")
	wantFile(t, zip, store.FileOK, "")
	if want := (extract.Args{Archive: "archives/Show S01.zip", Dest: "files/Show S01", Scratch: "work/extract-1-x" + strconv.FormatInt(zip.ID, 10)}); extractArgs != want {
		t.Errorf("extract args %+v, want %+v", extractArgs, want)
	}
	var report extract.Output
	if err := json.Unmarshal(zip.Report, &report); err != nil || len(report.Files) != 3 || len(report.Skipped) != 1 {
		t.Errorf("archive report %s (%v)", zip.Report, err)
	}
	for _, p := range []string{"Show - S01E01.mkv", "Show - S01E02.mkv", "Show - S01E01.en.srt"} {
		f := e.file(j.ID, "files/Show S01/Season 01/"+p)
		wantFile(t, f, store.FileOK, "")
		if f.From != zip.Path || f.Size != 2 {
			t.Errorf("%s: from %q, size %d", f.Path, f.From, f.Size)
		}
	}
	if f := e.file(j.ID, "files/Show S01/Season 01/Show - S01E01.en.srt"); f.Kind != store.KindSubtitle {
		t.Errorf("srt kind %s", f.Kind)
	}
	if want := []string{extractID(j, 1, zip.ID), probeID(j, 1)}; !slices.Equal(e.w.ran, want) {
		t.Errorf("tasks %v, want %v", e.w.ran, want)
	}
	if len(e.files(j.ID)) != 4 {
		t.Errorf("files: %v", e.files(j.ID))
	}
	wantClean(t, e, j.ID)
}

func TestRefusedArchive(t *testing.T) {
	e := newEnv(t)
	e.w.extracts = map[string]extractPlan{"archives/big.zip": {refused: extract.RefusedTooLarge}}
	j := e.newJob(archive("big.zip"), video("Film.mkv"))
	j = e.run(j.ID, store.Probed)
	a := e.file(j.ID, "archives/big.zip")
	wantFile(t, a, store.FileRefused, extract.RefusedTooLarge)
	if !strings.Contains(string(a.Report), `"refused":"too_large"`) {
		t.Errorf("report %s", a.Report)
	}
	wantDisk(t, e, j.ID, "files/Film.mkv")
	wantClean(t, e, j.ID)

	// Only the refused archive: nothing usable.
	j = e.newJob(archive("big.zip"))
	j = e.run(j.ID, store.Failed)
	wantState(t, j, store.Failed, CodeNoUsableFiles)
	wantFile(t, e.file(j.ID, "archives/big.zip"), store.FileRefused, extract.RefusedTooLarge)
}

func TestRefusedFileAmongGoodOnes(t *testing.T) {
	e := newEnv(t)
	e.w.refuse = map[string]string{"files/broken.mkv": media.RefusalNoVideoStream}
	j := e.newJob(video("good.mkv"), video("broken.mkv"), subtitle("good.srt"))
	j = e.run(j.ID, store.Probed)
	wantFile(t, e.file(j.ID, "files/good.mkv"), store.FileOK, "")
	wantFile(t, e.file(j.ID, "files/good.srt"), store.FileOK, "")
	bad := e.file(j.ID, "files/broken.mkv")
	wantFile(t, bad, store.FileRefused, media.RefusalNoVideoStream)
	if bad.Report != nil {
		t.Errorf("refused file has a report: %s", bad.Report)
	}
	wantDisk(t, e, j.ID, "files/good.mkv", "files/good.srt")
	wantClean(t, e, j.ID)
}

func TestNothingUsable(t *testing.T) {
	e := newEnv(t)
	e.w.refuse = map[string]string{"files/a.mkv": media.RefusalNoDuration}
	e.w.extracts = map[string]extractPlan{"archives/s.zip": {files: map[string]string{"notes.srt": "x"}}}
	j := e.newJob(video("a.mkv"), subtitle("b.srt"), archive("s.zip"))
	j = e.run(j.ID, store.Failed)
	wantState(t, j, store.Failed, CodeNoUsableFiles)
	if j.Resume != store.Uploaded {
		t.Errorf("resume %s", j.Resume)
	}
	wantFile(t, e.file(j.ID, "files/a.mkv"), store.FileRefused, media.RefusalNoDuration)
	wantFile(t, e.file(j.ID, "files/b.srt"), store.FileOK, "")
	wantFile(t, e.file(j.ID, "files/s/notes.srt"), store.FileOK, "")
	wantDisk(t, e, j.ID, "files/b.srt", "files/s/notes.srt", "archives/s.zip")
	wantClean(t, e, j.ID)
}

func TestTaskError(t *testing.T) {
	e := newEnv(t)
	// Two archives: the first fails, the second is still running then.
	e.w.fail = func(task spool.Task) *spool.Error {
		if strings.Contains(string(task.Args), "a.zip") {
			return &spool.Error{Code: spool.CodeInternal, Message: "disk on fire"}
		}
		return nil
	}
	e.w.hold = func(task spool.Task) bool { return strings.Contains(string(task.Args), "b.zip") }
	j := e.newJob(archive("a.zip"), archive("b.zip"))
	j = e.run(j.ID, store.Failed)
	wantState(t, j, store.Failed, CodeTaskFailed)
	if !strings.Contains(j.ErrorText, "disk on fire") || !strings.Contains(j.ErrorText, "internal") {
		t.Errorf("error text %q", j.ErrorText)
	}
	// The result is acknowledged, and the other task is cancelled.
	wantClean(t, e, j.ID)
	wantFile(t, e.file(j.ID, "archives/a.zip"), store.FilePending, "")
}

func TestTimeout(t *testing.T) {
	e := newEnv(t)
	e.w.fail = func(task spool.Task) *spool.Error {
		return &spool.Error{Code: spool.CodeTimeout, Message: "ran past worker.taskTimeout (6h)"}
	}
	j := e.newJob(video("a.mkv"))
	j = e.run(j.ID, store.Failed)
	wantState(t, j, store.Failed, CodeTaskTimeout)
	wantClean(t, e, j.ID)
	if e.logs.count("job_state") != 2 {
		t.Errorf("job_state lines: %d\n%s", e.logs.count("job_state"), e.logs)
	}
}

// A result the worker should never write: a path outside the destination.
func TestUntrustedExtractOutput(t *testing.T) {
	e := newEnv(t)
	e.w.extracts = map[string]extractPlan{"archives/a.zip": {files: map[string]string{"x.mkv": "x"}}}
	j := e.newJob(archive("a.zip"))
	e.r.pass(e.ctx) // → probing
	e.r.pass(e.ctx) // submits
	tasks := e.q.Tasks()
	if len(tasks) != 1 {
		t.Fatalf("tasks %v", tasks)
	}
	e.q.Complete(tasks[0].ID, extract.Output{Files: []extract.Entry{{Path: "files/../../../etc/passwd", Size: 1}}, Skipped: []extract.Skipped{}})
	j = e.run(j.ID, store.Failed)
	wantState(t, j, store.Failed, CodeTaskFailed)
	if len(e.files(j.ID)) != 1 {
		t.Errorf("files %v", e.files(j.ID))
	}
	wantClean(t, e, j.ID)
}

func TestStalled(t *testing.T) {
	e := newEnv(t)
	e.beat(3 * time.Minute) // older than worker.heartbeatMaxAge (2m)
	e.w.hold = func(spool.Task) bool { return true }
	j := e.newJob(video("a.mkv"))
	j = e.run(j.ID, store.Stalled)
	wantState(t, j, store.Stalled, CodeWorkerUnavailable)
	if j.Resume != store.Probing || !strings.Contains(j.ErrorText, "heartbeat") {
		t.Errorf("resume %s, text %q", j.Resume, j.ErrorText)
	}
	// A restart while stalled changes nothing.
	e.restart()
	j = e.run(j.ID, store.Stalled)
	wantState(t, j, store.Stalled, CodeWorkerUnavailable)

	// Back as soon as the heartbeat is fresh, with the same task.
	e.beat(0)
	e.w.hold = nil
	j = e.run(j.ID, store.Probed)
	if j.Attempt != 1 || j.ErrorCode != "" {
		t.Errorf("after resume: %+v", j)
	}
	if want := []string{j.ID + "-1-p"}; !slices.Equal(e.w.ran, want) {
		t.Errorf("tasks %v, want %v", e.w.ran, want)
	}

	// No heartbeat at all is a stalled worker too.
	e2 := newEnv(t)
	if err := removeHeartbeat(e2); err != nil {
		t.Fatal(err)
	}
	e2.w.hold = func(spool.Task) bool { return true }
	j2 := e2.newJob(video("a.mkv"))
	wantState(t, e2.run(j2.ID, store.Stalled), store.Stalled, CodeWorkerUnavailable)
}

// A job stalled with a code registered through ResumeWhen goes back to its Resume state
// once the check says so; a code nobody registered leaves it stalled.
func TestResumeWhen(t *testing.T) {
	e := newEnv(t)
	e.beat(0)
	answers := false
	var asked []string
	e.r.ResumeWhen("arr_unavailable", "the app answers again", func(_ context.Context, j store.Job) (bool, error) {
		asked = append(asked, j.ID)
		return answers, nil
	})
	stall := func(code string) store.Job {
		j, err := e.st.CreateJob(e.ctx, "user1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := storetest.Drive(e.ctx, e.st, j.ID, store.Matched); err != nil {
			t.Fatal(err)
		}
		j, err = e.st.Transition(e.ctx, j.ID, store.Matched, store.Stalled, store.Change{Actor: "system", ErrorCode: code, ErrorText: "down"})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	j := stall("arr_unavailable")
	other := stall("something_else")
	e.r.pass(e.ctx)
	wantState(t, e.job(j.ID), store.Stalled, "arr_unavailable")
	answers = true
	e.r.pass(e.ctx)
	wantState(t, e.job(j.ID), store.Matched, "")
	wantState(t, e.job(other.ID), store.Stalled, "something_else")
	if !slices.Contains(asked, j.ID) || slices.Contains(asked, other.ID) {
		t.Errorf("asked about %v", asked)
	}
	h, err := e.st.History(e.ctx, j.ID)
	if err != nil || h[len(h)-1].Note != "the app answers again" {
		t.Errorf("history %+v, %v", h, err)
	}
}

// A result that is there while the heartbeat is old is used: only waiting stalls.
func TestOldHeartbeatWithResult(t *testing.T) {
	e := newEnv(t)
	e.beat(time.Hour)
	j := e.newJob(video("a.mkv"))
	e.r.pass(e.ctx) // → probing
	e.r.pass(e.ctx) // submits, then stalls
	wantState(t, e.job(j.ID), store.Stalled, CodeWorkerUnavailable)
	e.w.work() // the worker finished just before it went away
	e.beat(0)
	j = e.run(j.ID, store.Probed)
	wantClean(t, e, j.ID)
}

func TestCancelledMidTask(t *testing.T) {
	for _, tc := range []struct {
		name   string
		finish bool // the worker finished before the cancel
	}{{"running", false}, {"finished", true}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.w.extracts = map[string]extractPlan{"archives/s.zip": {files: map[string]string{"e1.mkv": "1"}}}
			e.w.hold = func(task spool.Task) bool { return task.Kind == spool.KindProbe && !tc.finish }
			j := e.newJob(archive("s.zip"), video("a.mkv"))
			up := e.addUnfinishedUpload(j)
			for range 3 {
				e.r.pass(e.ctx)
				e.w.work()
			}
			if tc.finish {
				if l := e.leftovers(j.ID); len(l) != 1 || !strings.HasPrefix(l[0], "result ") {
					t.Fatalf("leftovers %v", l)
				}
			} else if len(e.q.Tasks()) != 1 {
				t.Fatalf("tasks %v", e.q.Tasks())
			}
			// The job API (#11) cancels it.
			if _, err := e.st.Transition(e.ctx, j.ID, store.Probing, store.Cancelled, store.Change{Actor: "user1"}); err != nil {
				t.Fatal(err)
			}
			j = e.run(j.ID, store.Cancelled)
			if e.onDisk(j.ID) != nil {
				t.Errorf("job directory left: %v", e.onDisk(j.ID))
			}
			if ups, _ := e.st.Uploads(e.ctx, "", j.ID); len(ups) != 0 {
				t.Errorf("uploads left: %v", ups)
			}
			if s := e.snapshot(j.ID); strings.Contains(s, "incoming "+up) {
				t.Errorf("upload data left:\n%s", s)
			}
			wantClean(t, e, j.ID)
			// Nothing more to do: a pass makes no change and logs nothing new.
			n := e.logs.count("job_cleaned")
			e.r.pass(e.ctx)
			if e.logs.count("job_cleaned") != n || n != 1 {
				t.Errorf("job_cleaned logged %d times", e.logs.count("job_cleaned"))
			}
		})
	}
}

func TestRetryAfterFailure(t *testing.T) {
	e := newEnv(t)
	e.w.extracts = map[string]extractPlan{"archives/s.zip": {files: map[string]string{"e1.mkv": "1", "e2.mkv": "2"}}}
	e.w.refuse = map[string]string{"files/s/e2.mkv": media.RefusalNoVideoStream}
	failProbe := true
	e.w.fail = func(task spool.Task) *spool.Error {
		if task.Kind == spool.KindProbe && failProbe {
			return &spool.Error{Code: spool.CodeInternal, Message: "ffprobe crashed"}
		}
		return nil
	}
	j := e.newJob(archive("s.zip"), video("a.mkv"), video("bad.mkv"))
	j = e.run(j.ID, store.Failed)
	wantState(t, j, store.Failed, CodeTaskFailed)
	wantClean(t, e, j.ID)

	// The job API (#11) retries it: back to Resume, attempt 2.
	failProbe = false
	e.w.refuse["files/bad.mkv"] = media.RefusalUnreadable
	if j, _ = e.st.Transition(e.ctx, j.ID, store.Failed, j.Resume, store.Change{Actor: "user1"}); j.Attempt != 2 || j.State != store.Uploaded {
		t.Fatalf("retry: %+v", j)
	}
	e.restart()
	e.w.ran = nil
	j = e.run(j.ID, store.Probed)
	zip := e.file(j.ID, "archives/s.zip")
	if want := []string{extractID(j, 2, zip.ID), probeID(j, 2)}; !slices.Equal(e.w.ran, want) {
		t.Errorf("tasks of the retry %v, want %v", e.w.ran, want)
	}
	// Each attempt unpacks into its own scratch directory, so the two cannot collide.
	scratch := []string{e.w.args[0].Scratch, e.w.args[1].Scratch}
	if want := []string{"work/extract-1-x" + strconv.FormatInt(zip.ID, 10), "work/extract-2-x" + strconv.FormatInt(zip.ID, 10)}; !slices.Equal(scratch, want) {
		t.Errorf("scratch directories %v, want %v", scratch, want)
	}
	wantFile(t, zip, store.FileOK, "")
	wantFile(t, e.file(j.ID, "files/a.mkv"), store.FileOK, "")
	wantFile(t, e.file(j.ID, "files/bad.mkv"), store.FileRefused, media.RefusalUnreadable)
	wantFile(t, e.file(j.ID, "files/s/e1.mkv"), store.FileOK, "")
	wantFile(t, e.file(j.ID, "files/s/e2.mkv"), store.FileRefused, media.RefusalNoVideoStream)
	if len(e.files(j.ID)) != 5 {
		t.Errorf("files %v", e.files(j.ID))
	}
	wantDisk(t, e, j.ID, "archives/s.zip", "files/a.mkv", "files/s/e1.mkv")
	wantClean(t, e, j.ID)
}

// A retry probes again what is still on disk; a file refused (and deleted) before stays
// refused, and its path is not probed again.
func TestRetryKeepsRefusedFiles(t *testing.T) {
	e := newEnv(t)
	e.w.refuse = map[string]string{"files/a.mkv": media.RefusalNoDuration}
	j := e.newJob(video("a.mkv"), subtitle("a.srt"))
	j = e.run(j.ID, store.Failed)
	wantState(t, j, store.Failed, CodeNoUsableFiles)

	var probed []string
	e.w.hold = func(task spool.Task) bool {
		var a media.ProbeArgs
		_ = json.Unmarshal(task.Args, &a)
		probed = append(probed, a.Paths...)
		return false
	}
	if _, err := e.st.Transition(e.ctx, j.ID, store.Failed, j.Resume, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	j = e.run(j.ID, store.Failed)
	wantState(t, j, store.Failed, CodeNoUsableFiles)
	if j.Attempt != 2 || !slices.Equal(probed, []string{"files/a.srt"}) {
		t.Errorf("attempt %d probed %v", j.Attempt, probed)
	}
	wantFile(t, e.file(j.ID, "files/a.mkv"), store.FileRefused, media.RefusalNoDuration)
	wantFile(t, e.file(j.ID, "files/a.srt"), store.FileOK, "")
	wantClean(t, e, j.ID)
}

func TestJobStuck(t *testing.T) {
	e := newEnv(t)
	e.w.fail = func(spool.Task) *spool.Error { return &spool.Error{Code: spool.CodeInternal, Message: "x"} }
	stuck := e.newJob(video("a.mkv"))
	stuck = e.run(stuck.ID, store.Failed)
	failedAt := e.clk.Now()

	after := e.cfg.Server.JobStuckAfter.D()
	e.clk.Advance(after - 30*time.Second)
	e.beat(0)
	fresh := e.newJob(video("b.mkv"))
	e.run(fresh.ID, store.Failed)
	e.restart() // its first pass sweeps
	before := e.logs.count("job_stuck")
	if before != 0 {
		t.Fatalf("job_stuck before %s:\n%s", after, e.logs)
	}

	// Passes every second for five minutes: one line a minute once the job is stuck.
	var at []time.Duration
	for range 300 {
		e.clk.Advance(time.Second)
		n := e.logs.count("job_stuck")
		e.r.pass(e.ctx)
		if e.logs.count("job_stuck") > n {
			at = append(at, e.clk.Now().Sub(failedAt))
		}
	}
	if len(at) != 4 {
		t.Fatalf("job_stuck at %v, want 4 lines", at)
	}
	for i := 1; i < len(at); i++ {
		if at[i]-at[i-1] != time.Minute {
			t.Errorf("job_stuck at %v: not once a minute", at)
		}
	}
	if at[0] <= after {
		t.Errorf("job_stuck at %v, before %s", at[0], after)
	}
	for _, line := range strings.Split(e.logs.String(), "\n") {
		if strings.Contains(line, "msg=job_stuck") && !strings.Contains(line, "job="+stuck.ID) {
			t.Errorf("job_stuck for another job: %s", line)
		}
	}
}

func TestHandle(t *testing.T) {
	e := newEnv(t)
	var seen []string
	e.r.Handle(store.Probed, func(_ context.Context, j store.Job) error {
		seen = append(seen, j.ID)
		return nil
	})
	j := e.newJob(video("a.mkv"))
	e.run(j.ID, store.Probed)
	if len(seen) < 1 || seen[0] != j.ID {
		t.Errorf("probed step saw %v", seen)
	}
}

func TestExtractDests(t *testing.T) {
	files := []store.File{
		{ID: 1, Path: "archives/Show.zip", Kind: store.KindArchive},
		{ID: 2, Path: "archives/show.ZIP", Kind: store.KindArchive},
		{ID: 3, Path: "files/Film.mkv", Kind: store.KindVideo},
		{ID: 4, Path: "archives/Film.mkv.zip", Kind: store.KindArchive},
		{ID: 5, Path: "archives/.zip", Kind: store.KindArchive},
		{ID: 6, Path: "files/Show/e1.mkv", Kind: store.KindVideo, From: "archives/Show.zip"},
	}
	got := extractDests(files)
	want := map[int64]string{1: "files/Show", 2: "files/show (2)", 4: "files/Film.mkv (4)", 5: "files/archive"}
	for id, d := range want {
		if got[id] != d {
			t.Errorf("dest of %d: %q, want %q", id, got[id], d)
		}
	}
	if len(got) != len(want) {
		t.Errorf("dests %v", got)
	}

	// x.zip's default scratch directory is files/x.partial: x.partial.zip must not get it,
	// whichever archive comes first.
	for _, order := range [][2]string{{"archives/x.zip", "archives/x.partial.zip"}, {"archives/x.partial.zip", "archives/X.zip"}} {
		got := extractDests([]store.File{
			{ID: 1, Path: order[0], Kind: store.KindArchive},
			{ID: 2, Path: order[1], Kind: store.KindArchive},
		})
		for id, d := range got {
			for other, o := range got {
				if id != other && (strings.EqualFold(d, o) || strings.EqualFold(d, o+".partial")) {
					t.Errorf("%v: dests %q and %q collide", order, d, o)
				}
			}
		}
		if p := got[1]; strings.HasSuffix(order[0], "x.partial.zip") && p != "files/x.partial (1)" {
			t.Errorf("%v: dest %q", order, p)
		}
	}
}

// A retry after the archive itself went missing (an ok archive is kept on disk, but not by
// force): it is not unpacked again, so its files are kept and checked again, not deleted.
func TestRetryWithArchiveGone(t *testing.T) {
	e := newEnv(t)
	e.w.extracts = map[string]extractPlan{"archives/s.zip": {files: map[string]string{"e1.mkv": "1"}}}
	e.w.fail = func(task spool.Task) *spool.Error {
		if task.ID == task.Job+"-1-p" {
			return &spool.Error{Code: spool.CodeInternal, Message: "x"}
		}
		return nil
	}
	j := e.newJob(archive("s.zip"))
	j = e.run(j.ID, store.Failed)
	if err := os.Remove(filepath.Join(jobdir.Dir(e.cfg.Paths.IntakeRoot, j.ID), "archives/s.zip")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Transition(e.ctx, j.ID, store.Failed, j.Resume, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	e.w.ran = nil
	j = e.run(j.ID, store.Probed)
	if want := []string{probeID(j, 2)}; !slices.Equal(e.w.ran, want) {
		t.Errorf("tasks %v, want %v", e.w.ran, want)
	}
	wantFile(t, e.file(j.ID, "files/s/e1.mkv"), store.FileOK, "")
	wantFile(t, e.file(j.ID, "archives/s.zip"), store.FileOK, "")
	wantClean(t, e, j.ID)
}

// Too little free space to extract: the archive is kept, the job stalls (free_space) and
// resumes on its own once there is room, running the same task again.
func TestFreeSpaceStalls(t *testing.T) {
	e := newEnv(t)
	e.cfg.Upload.FreeSpaceMargin = 1000
	e.restart()
	e.w.extracts = map[string]extractPlan{"archives/s.zip": {files: map[string]string{"e1.mkv": "1"}}}
	e.w.noRoom = map[string]int{"archives/s.zip": 1}
	j := e.newJob(archive("s.zip"))
	a := e.file(j.ID, "archives/s.zip")
	e.free = 0
	j = e.run(j.ID, store.Stalled)
	wantState(t, j, store.Stalled, CodeFreeSpace)
	if j.Resume != store.Probing || !strings.Contains(j.ErrorText, "free space") {
		t.Errorf("resume %s, text %q", j.Resume, j.ErrorText)
	}
	wantFile(t, e.file(j.ID, a.Path), store.FilePending, "")
	wantDisk(t, e, j.ID, a.Path)
	wantClean(t, e, j.ID)

	// Not yet enough: the margin plus the archive's size is needed.
	e.free = 1000 + a.Size - 1
	e.restart()
	j = e.run(j.ID, store.Stalled)
	wantState(t, j, store.Stalled, CodeFreeSpace)

	e.free = 1000 + a.Size
	j = e.run(j.ID, store.Probed)
	if want := []string{extractID(j, 1, a.ID), extractID(j, 1, a.ID), probeID(j, 1)}; !slices.Equal(e.w.ran, want) {
		t.Errorf("tasks %v, want %v", e.w.ran, want)
	}
	wantFile(t, e.file(j.ID, a.Path), store.FileOK, "")
	wantFile(t, e.file(j.ID, "files/s/e1.mkv"), store.FileOK, "")
	if !strings.Contains(e.logs.String(), "msg=job_state job="+j.ID+" from=stalled to=probing") {
		t.Errorf("no resume in the log:\n%s", e.logs)
	}
}

// A real statfs of a directory.
func TestFreeSpaceMeasured(t *testing.T) {
	n, err := freeSpace(t.TempDir())
	if err != nil || n <= 0 {
		t.Errorf("freeSpace: %d, %v", n, err)
	}
	if _, err := freeSpace(filepath.Join(t.TempDir(), "missing")); err == nil && runtime.GOOS == "linux" {
		t.Error("no error for a missing directory")
	}
}

func TestRun(t *testing.T) {
	e := newEnv(t)
	e.cfg.Worker.PollInterval = config.Duration(5 * time.Millisecond)
	e.restart()
	j := e.newJob(video("a.mkv"))
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- e.r.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for e.job(j.ID).State != store.Probing || len(e.q.Tasks()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the runner did not submit the probe")
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.w.work()
	for e.job(j.ID).State != store.Probed {
		if time.Now().After(deadline) {
			t.Fatal("the runner did not finish the job")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e.logs.count("runner_started") != 1 || e.logs.count("runner_stopped") != 1 {
		t.Errorf("log:\n%s", e.logs)
	}
}

// Discard (for retention) removes every task and result the job left, in all attempts.
func TestDiscard(t *testing.T) {
	e := newEnv(t)
	e.w.extracts = map[string]extractPlan{"archives/s.zip": {files: map[string]string{"e1.mkv": "1"}}}
	e.w.fail = func(task spool.Task) *spool.Error {
		if task.ID == task.Job+"-1-p" {
			return &spool.Error{Code: spool.CodeInternal, Message: "x"}
		}
		return nil
	}
	j := e.newJob(archive("s.zip"), video("a.mkv"))
	j = e.run(j.ID, store.Failed)
	if _, err := e.st.Transition(e.ctx, j.ID, store.Failed, j.Resume, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	// Attempt 2: one task waiting, and a stale result of attempt 1 put back by hand.
	e.w.hold = func(spool.Task) bool { return true }
	for range 3 {
		e.r.pass(e.ctx)
	}
	j = e.job(j.ID)
	stale := spool.Task{ID: probeID(j, 1), Kind: spool.KindProbe, Job: j.ID}
	if err := e.q.Submit(e.ctx, stale); err != nil {
		t.Fatal(err)
	}
	e.q.Complete(stale.ID, nil)
	// And a flow 2 library probe (#52) of each attempt: a result, and a waiting task.
	lib1 := spool.Task{ID: LibraryProbeID(j.ID, 1), Kind: spool.KindLibraryProbe, Job: j.ID}
	lib2 := spool.Task{ID: LibraryProbeID(j.ID, 2), Kind: spool.KindLibraryProbe, Job: j.ID}
	for _, task := range []spool.Task{lib1, lib2} {
		if err := e.q.Submit(e.ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	e.q.Complete(lib1.ID, nil)
	// And a flow 2a merge (#55) of each attempt: a result, and a waiting task.
	m1 := spool.Task{ID: MergeID(j.ID, 1), Kind: spool.KindMerge, Job: j.ID}
	m2 := spool.Task{ID: MergeID(j.ID, 2), Kind: spool.KindMerge, Job: j.ID}
	for _, task := range []spool.Task{m1, m2} {
		if err := e.q.Submit(e.ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	e.q.Complete(m1.ID, nil)
	if l := e.leftovers(j.ID); len(l) != 6 {
		t.Fatalf("leftovers %v", l)
	}
	for range 2 { // safe to repeat
		if err := Discard(e.ctx, e.q, j, e.files(j.ID)); err != nil {
			t.Fatal(err)
		}
	}
	wantClean(t, e, j.ID)
}

// A result file that cannot be read (here: larger than the spool reads) fails its job once
// and is removed, and a job in that state can still be cancelled.
func TestUnreadableResult(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancel), func(t *testing.T) {
			e := newEnv(t)
			dir, err := spool.Open(e.cfg.Worker.SpoolDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = dir.Close() })
			e.rq = dir
			e.restart()
			j := e.newJob(video("a.mkv"))
			e.r.pass(e.ctx) // probing
			e.r.pass(e.ctx) // submits the probe
			id := probeID(e.job(j.ID), 1)
			big := `{"v":1,"id":"` + id + `","status":"ok","output":"` + strings.Repeat("x", 17<<20) + `"}`
			path := filepath.Join(e.cfg.Worker.SpoolDir, "results", id+".json")
			if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
				t.Fatal(err)
			}
			want := store.Failed
			if cancel {
				want = store.Cancelled
				if _, err := e.st.Transition(e.ctx, j.ID, store.Probing, store.Cancelled, store.Change{Actor: "user1"}); err != nil {
					t.Fatal(err)
				}
			}
			j = e.run(j.ID, want)
			if !cancel {
				wantState(t, j, store.Failed, CodeTaskFailed)
				if !strings.Contains(j.ErrorText, spool.CodeBadResult) {
					t.Errorf("error text %q", j.ErrorText)
				}
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("the result file is still there: %v", err)
			}
			if e.logs.count("level=ERROR") != 0 {
				t.Errorf("errors logged:\n%s", e.logs)
			}
		})
	}
}
