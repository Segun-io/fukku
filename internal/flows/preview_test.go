package flows

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/preview"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

// readyFlow2Job is a flow 2 job awaiting confirmation whose upload is the same cut as the
// library file and has two audio tracks, the second the default.
func (x *flow2Env) readyFlow2Job() store.Job {
	x.t.Helper()
	j := x.matched(x.flow2Job(filmMinutes + 200))
	x.worker(media.LibraryProbeOutput{Path: x.record(j).Library.Path, OK: true, Media: libProbe(filmMinutes)})
	if j = x.matched(j); j.State != store.AwaitingConfirmation {
		x.t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	x.setUploadAudio(j, []media.AudioTrack{{Index: 1, Codec: "aac", Language: "eng"}, {Index: 2, Codec: "aac", Language: "spa", Default: true}})
	return j
}

func (x *flow2Env) setUploadAudio(j store.Job, audio []media.AudioTrack) {
	x.t.Helper()
	files, err := x.st.Files(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	f, pr, ok := Flow2Upload(files)
	if !ok {
		x.t.Fatal("no upload")
	}
	pr.Audio = audio
	f.Report, _ = json.Marshal(pr)
	if err := x.st.UpdateFile(x.ctx, f); err != nil {
		x.t.Fatal(err)
	}
}

func (x *flow2Env) previewRecord(j store.Job) (Preview, bool) {
	x.t.Helper()
	t, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	return PreviewOf(t)
}

func (x *flow2Env) previewStep(j store.Job) {
	x.t.Helper()
	if err := x.p.Previews(x.ctx, x.jobNow(j)); err != nil {
		x.t.Fatalf("preview step: %v", err)
	}
}

// previewTask is the one waiting task, which must be the job's preview.
func (x *flow2Env) previewTask(j store.Job) (spool.Task, preview.Args) {
	x.t.Helper()
	tasks := x.q.Tasks()
	if len(tasks) != 1 || tasks[0].Kind != spool.KindPreview || tasks[0].ID != x.previewID(j) || tasks[0].Job != j.ID {
		x.t.Fatalf("tasks %+v, want the preview task", tasks)
	}
	var a preview.Args
	if err := json.Unmarshal(tasks[0].Args, &a); err != nil {
		x.t.Fatal(err)
	}
	return tasks[0], a
}

func rendered(a preview.Args) preview.Output {
	out := preview.Output{Request: a.Request, OK: true}
	for i, c := range a.Clips {
		out.Clips = append(out.Clips, preview.ClipFile{N: i + 1, StartMs: c.StartMs, LengthMs: c.LengthMs, Size: 1000 + int64(i)})
	}
	return out
}

func offset(ms int64) PreviewRequest { return PreviewRequest{OffsetMs: &ms} }

func TestPreviewRequestAndResult(t *testing.T) {
	x := newFlow2Env(t)
	j := x.readyFlow2Job()
	if _, ok := x.previewRecord(j); ok {
		t.Fatal("a preview before any request")
	}
	pv, created, err := x.p.RequestPreview(x.ctx, "user1", j, offset(-800))
	if err != nil || !created {
		t.Fatalf("request: %+v %t %v", pv, created, err)
	}
	if !preview.ValidRequest(pv.Request) || pv.State != PreviewPending || pv.OffsetMs != -800 || pv.AudioTrack != 2 || pv.RequestedBy != "user1" {
		t.Errorf("record %+v (the default track is the upload's default)", pv)
	}
	if want := preview.Plan(filmMinutes, 3, 20*time.Second); !slices.Equal(pv.Planned, want) {
		t.Errorf("planned %v, want %v", pv.Planned, want)
	}
	stored, _ := x.previewRecord(j)
	if stored.Request != pv.Request || stored.State != PreviewPending {
		t.Errorf("stored %+v", stored)
	}
	// The request submits the task at once; a pass of the step submits nothing more.
	x.previewStep(j)
	_, a := x.previewTask(j)
	files, _ := x.st.Files(x.ctx, j.ID)
	up, _, _ := Flow2Upload(files)
	want := preview.Args{Request: pv.Request, Library: x.record(j).Library.Path, Upload: up.Path, AudioIndex: 2, OffsetMs: -800, Clips: pv.Planned}
	if a.Request != want.Request || a.Library != want.Library || a.Upload != want.Upload || a.AudioIndex != 2 || a.OffsetMs != -800 || !slices.Equal(a.Clips, want.Clips) {
		t.Errorf("args %+v, want %+v", a, want)
	}
	if err := preview.Check(a); err != nil {
		t.Errorf("args the worker refuses: %v", err)
	}

	// The worker renders the clips; the step stores them and acknowledges the result.
	x.q.Complete(x.previewID(j), rendered(a))
	x.previewStep(j)
	got, _ := x.previewRecord(j)
	if got.State != PreviewReady || len(got.Clips) != 3 || got.Clips[2].Size != 1002 || got.Clips[1].StartMs != pv.Planned[1].StartMs {
		t.Fatalf("record %+v", got)
	}
	if _, ok, _ := x.q.Result(x.ctx, runner.PreviewID(j.ID, 1)); ok {
		t.Error("the result was not acknowledged")
	}
	x.previewStep(j)
	if tasks := x.q.Tasks(); len(tasks) != 0 {
		t.Errorf("a ready preview submitted %+v", tasks)
	}
	if j = x.jobNow(j); j.State != store.AwaitingConfirmation {
		t.Errorf("the preview moved the job to %s", j.State)
	}

	// The same offset and track again changes nothing.
	again, created, err := x.p.RequestPreview(x.ctx, "user1", j, PreviewRequest{OffsetMs: ptr(int64(-800)), AudioTrack: ptr(2)})
	if err != nil || created || again.Request != pv.Request || again.State != PreviewReady {
		t.Errorf("same request again: %+v %t %v", again, created, err)
	}

	// A new offset replaces the clips: a new request, pending, with its own task.
	next, created, err := x.p.RequestPreview(x.ctx, "user1", j, PreviewRequest{OffsetMs: ptr(int64(120)), AudioTrack: ptr(1)})
	if err != nil || !created || next.Request == pv.Request || next.State != PreviewPending || len(next.Clips) != 0 || next.AudioTrack != 1 {
		t.Fatalf("new offset: %+v %t %v", next, created, err)
	}
	if _, a = x.previewTask(j); a.Request != next.Request || a.OffsetMs != 120 || a.AudioIndex != 1 {
		t.Errorf("args %+v", a)
	}
	// The offset and track the merge will use are the last request's.
	cur, _ := x.previewRecord(j)
	if cur.OffsetMs != 120 || cur.AudioTrack != 1 {
		t.Errorf("record %+v", cur)
	}
}

// previewID is the task id of the job's current request.
func (x *flow2Env) previewID(j store.Job) string {
	x.t.Helper()
	pv, ok := x.previewRecord(j)
	if !ok {
		x.t.Fatal("no preview record")
	}
	return runner.PreviewID(j.ID, previewSlot(pv.Seq))
}

// Each request has its own task id, so every result, task errors included, is the
// request's of its slot. Results of an earlier request are dropped; a cancelled task or
// one that names another request is submitted again.
func TestPreviewEarlierResults(t *testing.T) {
	x := newFlow2Env(t)
	j := x.readyFlow2Job()
	first, _, _ := x.p.RequestPreview(x.ctx, "user1", j, offset(0))
	firstID := x.previewID(j)
	second, _, _ := x.p.RequestPreview(x.ctx, "user1", j, offset(-800))
	id := x.previewID(j)
	if id == firstID || first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("ids %s %s, seq %d %d", firstID, id, first.Seq, second.Seq)
	}
	// The first request's waiting task is gone; its slot is watched for a late result.
	if _, a := x.previewTask(j); a.Request != second.Request {
		t.Fatalf("the waiting task is %s, want the second request", a.Request)
	}
	if pv, _ := x.previewRecord(j); len(pv.Earlier) != 1 || pv.Earlier[0].Slot != 1 {
		t.Fatalf("earlier %+v", pv.Earlier)
	}
	// It was running after all: its result (a task error) comes late, and is dropped.
	if err := x.q.Submit(x.ctx, spool.Task{ID: firstID, Kind: spool.KindPreview, Job: j.ID}); err != nil {
		t.Fatal(err)
	}
	x.q.Fail(firstID, spool.CodeInternal, "boom")
	x.previewStep(j)
	pv, _ := x.previewRecord(j)
	if _, ok, _ := x.q.Result(x.ctx, firstID); ok || len(pv.Earlier) != 0 || pv.State != PreviewPending {
		t.Fatalf("after the first request's late result: %+v (result left: %t)", pv, ok)
	}

	// The second request's task is cancelled (the job's end, or a re-submission after a
	// crash), or a result names another request: it is submitted again.
	for _, finish := range []func(a preview.Args){
		func(preview.Args) { x.q.Fail(id, spool.CodeCancelled, "cancelled") },
		func(a preview.Args) { a.Request = first.Request; x.q.Complete(id, rendered(a)) },
	} {
		_, a := x.previewTask(j)
		finish(a)
		x.previewStep(j)
		if pv, _ := x.previewRecord(j); pv.State != PreviewPending || pv.Request != second.Request {
			t.Fatalf("record %+v", pv)
		}
		x.previewStep(j)
		if _, a = x.previewTask(j); a.Request != second.Request {
			t.Fatalf("resubmitted %s", a.Request)
		}
	}

	// Its own task error fails it.
	x.q.Fail(id, spool.CodeTimeout, "ran past worker.taskTimeout")
	x.previewStep(j)
	pv, _ = x.previewRecord(j)
	if pv.State != PreviewFailed || pv.ErrorCode != runner.CodeTaskTimeout || !strings.Contains(pv.ErrorText, "taskTimeout") {
		t.Fatalf("record %+v", pv)
	}
	if tasks := x.q.Tasks(); len(tasks) != 0 {
		t.Errorf("a failed preview submitted %+v", tasks)
	}
	// The same offset again tries again, in the next slot.
	again, created, err := x.p.RequestPreview(x.ctx, "user1", j, offset(-800))
	if err != nil || !created || again.Request == second.Request || x.previewID(j) == id {
		t.Fatalf("retry: %+v %t %v", again, created, err)
	}
	task, a := x.previewTask(j)
	x.q.Complete(task.ID, preview.Output{Request: a.Request, Refusal: preview.RefusalRenderFailed, Detail: "clip 1: Invalid data found when processing input"})
	x.previewStep(j)
	if pv, _ = x.previewRecord(j); pv.State != PreviewFailed || pv.ErrorCode != preview.RefusalRenderFailed || !strings.Contains(pv.ErrorText, "Invalid data") {
		t.Errorf("refused: %+v", pv)
	}
	if _, ok, _ := x.q.Result(x.ctx, task.ID); ok {
		t.Error("result left")
	}
}

// The slots come round again after runner.PreviewSlots requests; a slot is cleared before
// it is used, and an earlier slot without a result is forgotten after previewGrace.
func TestPreviewSlots(t *testing.T) {
	x := newFlow2Env(t)
	j := x.readyFlow2Job()
	for n := 1; n <= runner.PreviewSlots; n++ {
		if _, _, err := x.p.RequestPreview(x.ctx, "user1", j, offset(int64(n))); err != nil {
			t.Fatal(err)
		}
	}
	pv, _ := x.previewRecord(j)
	if pv.Seq != runner.PreviewSlots || x.previewID(j) != runner.PreviewID(j.ID, runner.PreviewSlots) || len(pv.Earlier) != runner.PreviewSlots-1 {
		t.Fatalf("record seq %d, earlier %d", pv.Seq, len(pv.Earlier))
	}
	// Slot 1 holds a result nobody claimed.
	if err := x.q.Submit(x.ctx, spool.Task{ID: runner.PreviewID(j.ID, 1), Kind: spool.KindPreview, Job: j.ID}); err != nil {
		t.Fatal(err)
	}
	x.q.Complete(runner.PreviewID(j.ID, 1), preview.Output{Request: "0000000000000001", OK: true})
	next, _, err := x.p.RequestPreview(x.ctx, "user1", j, offset(100))
	if err != nil {
		t.Fatal(err)
	}
	if task, a := x.previewTask(j); task.ID != runner.PreviewID(j.ID, 1) || a.Request != next.Request {
		t.Fatalf("task %s for %s", task.ID, a.Request)
	}
	pv, _ = x.previewRecord(j)
	for _, e := range pv.Earlier {
		if e.Slot == 1 {
			t.Errorf("slot 1 is both current and earlier: %+v", pv.Earlier)
		}
	}
	x.p.now = func() time.Time { return time.Now().Add(previewGrace + time.Minute) }
	x.previewStep(j)
	if pv, _ = x.previewRecord(j); len(pv.Earlier) != 0 || !pv.Submitted {
		t.Errorf("after the grace: %+v", pv)
	}
}

func TestPreviewRefusals(t *testing.T) {
	check := func(t *testing.T, err error, code string) {
		t.Helper()
		if Code(err) != code {
			t.Errorf("error %v, want %s", err, code)
		}
	}
	t.Run("not flow 2", func(t *testing.T) {
		x := newFlow2Env(t)
		j := x.job(AppRadarr, filmID, vid("Film.mkv", filmMinutes))
		_, _, err := x.p.RequestPreview(x.ctx, "user1", j, offset(0))
		check(t, err, CodeNotFlow2)
	})
	t.Run("not awaiting confirmation", func(t *testing.T) {
		x := newFlow2Env(t)
		j := x.matched(x.flow2Job(filmMinutes))
		_, _, err := x.p.RequestPreview(x.ctx, "user1", j, offset(0))
		check(t, err, CodeWrongState)
	})
	t.Run("a different cut", func(t *testing.T) {
		x := newFlow2Env(t)
		j := x.matched(x.flow2Job(filmMinutes + 60_000))
		x.worker(media.LibraryProbeOutput{Path: x.record(j).Library.Path, OK: true, Media: libProbe(filmMinutes)})
		j = x.matched(j)
		_, _, err := x.p.RequestPreview(x.ctx, "user1", j, offset(0))
		check(t, err, CodeMergeNotOffered)
	})
	t.Run("merge disabled", func(t *testing.T) {
		x := newFlow2Env(t)
		j := x.readyFlow2Job()
		x.cfg.Merge.Enabled = false
		x.p = NewProposals(x.cfg, nil, x.st, x.movies, x.series, nil)
		x.p.SetQueue(x.q)
		_, _, err := x.p.RequestPreview(x.ctx, "user1", j, offset(0))
		check(t, err, CodeMergeNotOffered)
	})
	t.Run("bad requests", func(t *testing.T) {
		x := newFlow2Env(t)
		j := x.readyFlow2Job()
		for _, req := range []PreviewRequest{
			{OffsetMs: ptr(int64(preview.MaxOffsetMs + 1))},
			{OffsetMs: ptr(int64(-preview.MaxOffsetMs - 1))},
			{AudioTrack: ptr(0)}, // the video stream
			{AudioTrack: ptr(7)},
		} {
			_, _, err := x.p.RequestPreview(x.ctx, "user1", j, req)
			check(t, err, CodeBadRequest)
		}
		x.setUploadAudio(j, nil)
		_, _, err := x.p.RequestPreview(x.ctx, "user1", j, offset(0))
		check(t, err, CodeNoAudio)
		if tasks := x.q.Tasks(); len(tasks) != 0 {
			t.Errorf("submitted %+v", tasks)
		}
		if _, ok := x.previewRecord(j); ok {
			t.Error("a refused request was stored")
		}
	})
	// A library file that is no longer under paths.libraryRoots fails the preview without
	// a task.
	t.Run("outside the roots", func(t *testing.T) {
		x := newFlow2Env(t)
		j := x.readyFlow2Job()
		x.cfg.Paths.LibraryRoots = []string{"/data/media/tv"}
		x.p = NewProposals(x.cfg, nil, x.st, x.movies, x.series, nil)
		x.p.SetQueue(x.q)
		if _, _, err := x.p.RequestPreview(x.ctx, "user1", j, offset(0)); err != nil {
			t.Fatal(err)
		}
		pv, _ := x.previewRecord(j)
		if pv.State != PreviewFailed || pv.ErrorCode != preview.RefusalOutsideLibrary {
			t.Errorf("record %+v", pv)
		}
		if tasks := x.q.Tasks(); len(tasks) != 0 {
			t.Errorf("submitted %+v", tasks)
		}
	})
}

// The step leaves jobs that are not flow 2 alone, makes no record, and drops a result
// whose record went with an earlier target; a job that is no longer awaiting
// confirmation is not touched.
func TestPreviewStepOtherJobs(t *testing.T) {
	x := newFlow2Env(t)
	flow1 := x.job(AppRadarr, filmID, vid("Film.mkv", filmMinutes))
	x.previewStep(flow1)
	j := x.readyFlow2Job()
	x.previewStep(j)
	if _, ok := x.previewRecord(j); ok {
		t.Error("the step made a record")
	}
	if tasks := x.q.Tasks(); len(tasks) != 0 {
		t.Errorf("submitted %+v", tasks)
	}

	// The record goes (the user matched the job again) while its task runs.
	pv, _, _ := x.p.RequestPreview(x.ctx, "user1", j, offset(0))
	task, a := x.previewTask(j)
	tg, _ := x.st.Target(x.ctx, j.ID)
	tg.Options, _ = withOption(tg.Options, "preview", nil)
	if err := x.st.SetTarget(x.ctx, j.ID, tg); err != nil {
		t.Fatal(err)
	}
	x.q.Complete(task.ID, rendered(a))
	x.p.orphans = orphanSweeps{} // as after a restart: the slots are looked at now
	x.previewStep(j)
	if _, ok, _ := x.q.Result(x.ctx, task.ID); ok {
		t.Error("an orphaned result was kept")
	}
	if _, ok := x.previewRecord(j); ok || pv.Request == "" {
		t.Error("the step made a record")
	}

	// A job that left awaiting confirmation is not the step's any more.
	k := x.readyFlow2Job()
	if _, _, err := x.p.RequestPreview(x.ctx, "user1", k, offset(0)); err != nil {
		t.Fatal(err)
	}
	waiting, ka := x.previewTask(k)
	if _, err := x.st.Transition(x.ctx, k.ID, store.AwaitingConfirmation, store.Cancelled, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	x.q.Complete(waiting.ID, rendered(ka))
	if err := x.p.advancePreview(x.ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if pv, _ := x.previewRecord(k); pv.State != PreviewPending {
		t.Errorf("a cancelled job's preview was stored: %+v", pv)
	}
}

// A rematch that drops the record, or an unmatch, cancels its tasks, so they do not hold
// the worker; a rematch of the same title and file keeps the record and its task.
func TestPreviewDroppedByRematch(t *testing.T) {
	x := newFlow2Env(t)
	x.movies.Library[0].TMDBID = 604
	x.movies.Results = []arr.Movie{{TMDBID: 604, Title: "Some Film", Year: 2001}}
	x.movies.Library[0].HasFile = true
	x.movies.Library[0].File = libFile()
	cfg := x.cfg
	cfg.Radarr.URL = "http://radarr.example.com:7878"
	m := NewMatcher(cfg, x.st, x.movies, x.series, x.p.locks, nil)
	m.SetQueue(x.q)

	j := x.readyFlow2Job()
	if _, _, err := x.p.RequestPreview(x.ctx, "user1", j, offset(0)); err != nil {
		t.Fatal(err)
	}
	// The same title and file: the record and its task stay.
	if _, err := m.Match(x.ctx, "user1", x.jobNow(j), MatchRequest{App: AppRadarr, TMDBID: 604}); err != nil {
		t.Fatal(err)
	}
	if _, ok := x.previewRecord(j); !ok || len(x.q.Tasks()) != 1 {
		t.Fatalf("same title: record %t, tasks %+v", ok, x.q.Tasks())
	}
	// Radarr has another file now: the record goes, and so does its task.
	nf := libFile()
	nf.Size = 4001
	x.movies.Library[0].File = nf
	if _, err := m.Match(x.ctx, "user1", x.jobNow(j), MatchRequest{App: AppRadarr, TMDBID: 604}); err != nil {
		t.Fatal(err)
	}
	if _, ok := x.previewRecord(j); ok {
		t.Error("the record survived another file")
	}
	if tasks := x.q.Tasks(); len(tasks) != 0 {
		t.Errorf("rematch: tasks left %+v", tasks)
	}

	// An unmatch does the same, for the current slot and the earlier ones.
	k := x.readyFlow2Job()
	for _, ms := range []int64{0, 40} {
		if _, _, err := x.p.RequestPreview(x.ctx, "user1", k, offset(ms)); err != nil {
			t.Fatal(err)
		}
	}
	tasks := x.q.Tasks()
	if len(tasks) != 1 {
		t.Fatalf("tasks %+v", tasks)
	}
	// The first request's task was still running: put it back, as the worker has it.
	pv, _ := x.previewRecord(k)
	early := spool.Task{ID: runner.PreviewID(k.ID, pv.Earlier[0].Slot), Kind: spool.KindPreview, Job: k.ID}
	if err := x.q.Submit(x.ctx, early); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Unmatch(x.ctx, "user1", x.jobNow(k)); err != nil {
		t.Fatal(err)
	}
	if tasks := x.q.Tasks(); len(tasks) != 0 {
		t.Errorf("unmatch: tasks left %+v", tasks)
	}
}
