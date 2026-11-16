package flows

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/spool/spooltest"
	"github.com/segun-io/fukku/internal/store"
)

const (
	libRoot     = "/data/media/movies"
	appLibRoot  = "/movies" // the library root as Radarr sees it
	filmMinutes = 90 * minute
)

// libFile is the film's tracked file, as Radarr reports it.
func libFile() *arr.MovieFile {
	return &arr.MovieFile{
		ID: 12, Path: appLibRoot + "/Some Film (2001)/Some Film (2001) WEBDL-1080p.mkv", Size: 4000,
		Quality: webdl1080, Languages: []arr.Language{english}, RuntimeMs: filmMinutes,
	}
}

// flow2Env is env with the spool, the library root and a live worker.
type flow2Env struct {
	*env
	q *spooltest.Queue
}

func newFlow2Env(t *testing.T) *flow2Env {
	t.Helper()
	e := newEnv(t)
	e.cfg.Paths.LibraryRoots = []string{libRoot}
	e.cfg.Paths.Mappings.Radarr = append(e.cfg.Paths.Mappings.Radarr, config.PathMapping{From: libRoot, To: appLibRoot})
	e.p = NewProposals(e.cfg, nil, e.st, e.movies, e.series, nil)
	x := &flow2Env{env: e, q: &spooltest.Queue{}}
	x.p.SetQueue(x.q)
	x.p.SetWorkerDown(func() (string, bool) { return "", false })
	return x
}

// flow2Job is a matched flow 2 job for the film, with an upload of the duration.
func (x *flow2Env) flow2Job(uploadMs int64) store.Job {
	x.t.Helper()
	j := x.job(AppRadarr, filmID, vid("Some Film (2001).es.mkv", uploadMs))
	x.setRecord(j, newFlow2(libFile(), x.cfg))
	return x.jobNow(j)
}

// setRecord replaces the job target's flow 2 record (keeping its MatchedAt).
func (x *flow2Env) setRecord(j store.Job, rec *Flow2) {
	x.t.Helper()
	t, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	if t.Options, err = withOption(t.Options, "flow2", rec); err != nil {
		x.t.Fatal(err)
	}
	if err := x.st.SetTarget(x.ctx, j.ID, t); err != nil {
		x.t.Fatal(err)
	}
}

func (x *flow2Env) record(j store.Job) Flow2 {
	x.t.Helper()
	t, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	rec, ok := Flow2Of(t)
	if !ok {
		x.t.Fatalf("job %s is not flow 2: %s", j.ID, t.Options)
	}
	return rec
}

// libProbe is a probe of the library file of duration ms.
func libProbe(ms int64) *media.Probe {
	return &media.Probe{Container: "matroska,webm", DurationMs: ms, Size: 4000,
		Video: []media.VideoTrack{{Codec: "h264", Width: 1920, Height: 1080, DurationMs: ms}},
		Audio: []media.AudioTrack{{Index: 1, Codec: "aac", Language: "eng", Default: true}}}
}

// worker answers the waiting library probe task with output.
func (x *flow2Env) worker(output any) spool.Task {
	x.t.Helper()
	tasks := x.q.Tasks()
	if len(tasks) != 1 || tasks[0].Kind != spool.KindLibraryProbe {
		x.t.Fatalf("tasks %+v, want one libraryProbe task", tasks)
	}
	x.q.Complete(tasks[0].ID, output)
	return tasks[0]
}

func (x *flow2Env) noLeftovers(j store.Job) {
	x.t.Helper()
	if tasks := x.q.Tasks(); len(tasks) != 0 {
		x.t.Errorf("tasks left: %+v", tasks)
	}
	for a := 1; a <= j.Attempt+1; a++ {
		if _, ok, _ := x.q.Result(x.ctx, runner.LibraryProbeID(j.ID, a)); ok {
			x.t.Errorf("result of attempt %d left", a)
		}
	}
}

func TestFlow2StepProbesTheLibraryFile(t *testing.T) {
	x := newFlow2Env(t)
	j := x.flow2Job(filmMinutes + 200)
	rec := x.record(j)
	if want := libRoot + "/Some Film (2001)/Some Film (2001) WEBDL-1080p.mkv"; rec.Library.Path != want {
		t.Fatalf("library path %q, want %q (paths.mappings.radarr)", rec.Library.Path, want)
	}

	// First pass: the task, and the job waits in matched. Nothing is asked of Radarr.
	if j = x.matched(j); j.State != store.Matched {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	for range 2 { // a repeated pass submits nothing new
		x.matched(j)
	}
	task := x.worker(media.LibraryProbeOutput{Path: rec.Library.Path, OK: true, Media: libProbe(filmMinutes)})
	if task.ID != runner.LibraryProbeID(j.ID, 1) || task.Job != j.ID {
		t.Errorf("task %+v", task)
	}
	var args media.LibraryProbeArgs
	if err := json.Unmarshal(task.Args, &args); err != nil || args.Path != rec.Library.Path {
		t.Errorf("args %s (%v)", task.Args, err)
	}
	if calls := x.movies.ProposalCalls(); len(calls) != 0 {
		t.Errorf("a flow 2 job asked for a proposal: %v", calls)
	}

	// Second pass: stored, acknowledged, and the job waits for the user.
	if j = x.matched(j); j.State != store.AwaitingConfirmation {
		t.Fatalf("job %s (%s: %s)", j.State, j.ErrorCode, j.ErrorText)
	}
	if got := x.record(j).Library.Probe; got == nil || got.DurationMs != filmMinutes {
		t.Fatalf("stored probe %+v", got)
	}
	x.noLeftovers(j)
	h, _ := x.st.History(x.ctx, j.ID)
	if note := h[len(h)-1].Note; !strings.Contains(note, "the same cut") || !strings.Contains(note, "+200 ms") {
		t.Errorf("note %q", note)
	}
	if rows := x.rows(j); len(rows) != 0 {
		t.Errorf("rows %+v", rows)
	}
}

func TestFlow2StepFailures(t *testing.T) {
	timeout := &spool.Error{Code: spool.CodeTimeout, Message: "ran past worker.taskTimeout"}
	for name, tc := range map[string]struct {
		output any
		err    *spool.Error
		code   string
		text   string
	}{
		"outside the roots": {output: media.LibraryProbeOutput{Refusal: media.RefusalOutsideLibrary}, code: CodeLibraryUnreadable, text: "outside_library"},
		"unreadable":        {output: media.LibraryProbeOutput{Refusal: media.RefusalUnreadable}, code: CodeLibraryUnreadable, text: "unreadable"},
		"no refusal given":  {output: media.LibraryProbeOutput{}, code: CodeLibraryUnreadable, text: "unreadable"},
		"timeout":           {err: timeout, code: runner.CodeTaskTimeout, text: "timeout"},
		"internal":          {err: &spool.Error{Code: spool.CodeInternal, Message: "boom"}, code: runner.CodeTaskFailed, text: "boom"},
		"unreadable output": {output: "not an object", code: runner.CodeTaskFailed, text: "unreadable output"},
	} {
		t.Run(name, func(t *testing.T) {
			x := newFlow2Env(t)
			j := x.matched(x.flow2Job(filmMinutes))
			id := runner.LibraryProbeID(j.ID, 1)
			switch out := tc.output.(type) {
			case media.LibraryProbeOutput:
				out.Path = x.record(j).Library.Path
				x.worker(out)
			case nil:
				x.q.Fail(id, tc.err.Code, tc.err.Message)
			default:
				x.worker(out)
			}
			j = x.matched(j)
			if j.State != store.Failed || j.ErrorCode != tc.code || !strings.Contains(j.ErrorText, tc.text) {
				t.Fatalf("job %s %s %q, want failed %s", j.State, j.ErrorCode, j.ErrorText, tc.code)
			}
			if j.Resume != store.Matched {
				t.Errorf("resume %s", j.Resume)
			}
			x.noLeftovers(j)

			// A retry probes again, with the next attempt's task.
			if j, _ = x.st.Transition(x.ctx, j.ID, store.Failed, store.Matched, store.Change{Actor: "user1"}); j.Attempt != 2 {
				t.Fatalf("retry: %+v", j)
			}
			x.matched(j)
			if tasks := x.q.Tasks(); len(tasks) != 1 || tasks[0].ID != runner.LibraryProbeID(j.ID, 2) {
				t.Fatalf("tasks after the retry: %+v", tasks)
			}
		})
	}
}

func TestFlow2LibraryFileOutsideTheRoots(t *testing.T) {
	for name, roots := range map[string][]string{
		"no roots":        nil,
		"another root":    {"/data/media/tv"},
		"a sibling":       {"/data/media/mov"},
		"the file itself": {libRoot + "/Some Film (2001)/Some Film (2001) WEBDL-1080p.mkv"},
	} {
		t.Run(name, func(t *testing.T) {
			x := newFlow2Env(t)
			x.cfg.Paths.LibraryRoots = roots
			x.p = NewProposals(x.cfg, nil, x.st, x.movies, x.series, nil)
			x.p.SetQueue(x.q)
			x.p.SetWorkerDown(func() (string, bool) { return "", false })
			j := x.matched(x.flow2Job(filmMinutes))
			if j.State != store.Failed || j.ErrorCode != CodeLibraryUnreadable || !strings.Contains(j.ErrorText, "paths.libraryRoots") {
				t.Fatalf("job %s %s %q", j.State, j.ErrorCode, j.ErrorText)
			}
			if tasks := x.q.Tasks(); len(tasks) != 0 {
				t.Errorf("submitted %+v", tasks)
			}
		})
	}
	// A file Radarr gave no path for.
	x := newFlow2Env(t)
	j := x.job(AppRadarr, filmID, vid("a.mkv", filmMinutes))
	f := libFile()
	f.Path = ""
	x.setRecord(j, newFlow2(f, x.cfg))
	if j = x.matched(j); j.State != store.Failed || j.ErrorCode != CodeLibraryUnreadable {
		t.Fatalf("no path: job %s %s", j.State, j.ErrorCode)
	}
}

// A result for another library file (the job was matched again in the same attempt) is
// dropped, and the file the job names now is probed.
func TestFlow2StaleResult(t *testing.T) {
	x := newFlow2Env(t)
	j := x.matched(x.flow2Job(filmMinutes))
	x.worker(media.LibraryProbeOutput{Path: libRoot + "/Other (1999)/Other.mkv", OK: true, Media: libProbe(filmMinutes)})
	if j = x.matched(j); j.State != store.Matched || x.record(j).Library.Probe != nil {
		t.Fatalf("job %s, probe %+v", j.State, x.record(j).Library.Probe)
	}
	if _, ok, _ := x.q.Result(x.ctx, runner.LibraryProbeID(j.ID, 1)); ok {
		t.Fatal("the stale result was not acknowledged")
	}
	x.matched(j)
	x.worker(media.LibraryProbeOutput{Path: x.record(j).Library.Path, OK: true, Media: libProbe(filmMinutes)})
	if j = x.matched(j); j.State != store.AwaitingConfirmation {
		t.Fatalf("job %s", j.State)
	}
}

// A probe that arrives after the user chose another title is not stored on the new one,
// and its result stays for the pass that works for the new title.
func TestFlow2ProbeAfterARematch(t *testing.T) {
	x := newFlow2Env(t)
	j := x.matched(x.flow2Job(filmMinutes))
	rec := x.record(j)
	x.worker(media.LibraryProbeOutput{Path: rec.Library.Path, OK: true, Media: libProbe(filmMinutes)})
	old, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The user matches the same film again: a new MatchedAt.
	again := old
	again.MatchedAt = old.MatchedAt.Add(1e9)
	if _, err := x.st.ReplaceTarget(x.ctx, j.ID, store.Matched, store.Matched, &again, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	if err := x.p.useLibraryProbe(x.ctx, j, old, rec, mustResult(t, x, j)); err != nil {
		t.Fatal(err)
	}
	if got := x.record(j); got.Library.Probe != nil {
		t.Fatalf("stored on the new match: %+v", got)
	}
	if _, ok, _ := x.q.Result(x.ctx, runner.LibraryProbeID(j.ID, 1)); !ok {
		t.Fatal("the result is gone; the new match's pass needs it")
	}
	if j = x.matched(j); j.State != store.AwaitingConfirmation || x.record(j).Library.Probe == nil {
		t.Fatalf("job %s", j.State)
	}
}

func mustResult(t *testing.T, x *flow2Env, j store.Job) spool.Result {
	t.Helper()
	res, ok, err := x.q.Result(x.ctx, runner.LibraryProbeID(j.ID, j.Attempt))
	if err != nil || !ok {
		t.Fatalf("no result: %v", err)
	}
	return res
}

func TestFlow2WorkerDownStalls(t *testing.T) {
	x := newFlow2Env(t)
	x.p.SetWorkerDown(func() (string, bool) { return "no heartbeat", true })
	j := x.matched(x.flow2Job(filmMinutes))
	if j.State != store.Stalled || j.ErrorCode != runner.CodeWorkerUnavailable || j.Resume != store.Matched {
		t.Fatalf("job %+v", j)
	}
	if len(x.q.Tasks()) != 1 {
		t.Errorf("tasks %+v", x.q.Tasks())
	}
}

func TestFlow2StepWithoutQueue(t *testing.T) {
	x := newFlow2Env(t)
	x.p = NewProposals(x.cfg, nil, x.st, x.movies, x.series, nil)
	j := x.flow2Job(filmMinutes)
	if err := x.p.Matched(x.ctx, j); err == nil {
		t.Fatal("no error without a queue")
	}
	if j = x.jobNow(j); j.State != store.Matched {
		t.Errorf("job %s", j.State)
	}
}

// The flow 1 confirmation refuses a flow 2 job: it would import the upload over the film's
// file.
func TestConfirmRefusesFlow2(t *testing.T) {
	x := newFlow2Env(t)
	j := x.flow2Job(filmMinutes)
	x.matched(j)
	x.worker(media.LibraryProbeOutput{Path: x.record(j).Library.Path, OK: true, Media: libProbe(filmMinutes)})
	j = x.matched(j)
	eng := NewEngine(x.p)
	eng.checkStorage = func() error { return nil }
	_, err := eng.Confirm(x.ctx, "user1", "user1@example.com", j, ConfirmRequest{})
	var fe *Error
	if !errors.As(err, &fe) || fe.Code != CodeFlow2 {
		t.Fatalf("confirm: %v, want %s", err, CodeFlow2)
	}
	if j = x.jobNow(j); j.State != store.AwaitingConfirmation {
		t.Errorf("job %s", j.State)
	}
}

// A probe of a file whose size is not the tracked file's (Radarr replaced the file under
// the same name between the match and the probe) is not used: the job goes back to probed
// without its target, so that matching it again asks Radarr again, and then it works.
func TestFlow2StaleProbeSendsTheJobBackToProbed(t *testing.T) {
	stale := libProbe(filmMinutes)
	stale.Size = 4001 // the tracked file has 4000 bytes
	x := newFlow2Env(t)
	j := x.matched(x.flow2Job(filmMinutes))
	x.worker(media.LibraryProbeOutput{Path: x.record(j).Library.Path, FileID: 12, TrackedSize: 4000, OK: true, Media: stale})
	j = x.matched(j)
	if j.State != store.Probed || j.ErrorCode != "" {
		t.Fatalf("job %s %s %q", j.State, j.ErrorCode, j.ErrorText)
	}
	if _, err := x.st.Target(x.ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the target (and the stale record) is still there: %v", err)
	}
	x.noLeftovers(j)

	// Radarr tracks the new file now; the user matches the film again.
	x.movies.Library[0].TMDBID = 604
	x.movies.Results = []arr.Movie{{TMDBID: 604, Title: "Some Film", Year: 2001}}
	x.movies.Library[0].HasFile = true
	nf := libFile()
	nf.Size = 4001
	x.movies.Library[0].File = nf
	cfg := x.cfg
	cfg.Radarr.URL = "http://radarr.example.com:7878"
	m := NewMatcher(cfg, x.st, x.movies, x.series, x.p.locks, nil)
	if _, err := m.Match(x.ctx, "user1", j, MatchRequest{App: AppRadarr, TMDBID: 604}); err != nil {
		t.Fatal(err)
	}
	j = x.matched(x.jobNow(j))
	if rec := x.record(j); rec.Library.Size != 4001 {
		t.Fatalf("record %+v", rec.Library)
	}
	x.worker(media.LibraryProbeOutput{Path: x.record(j).Library.Path, FileID: 12, TrackedSize: 4001, OK: true, Media: stale})
	if j = x.matched(j); j.State != store.AwaitingConfirmation {
		t.Fatalf("after matching again: job %s %s %q", j.State, j.ErrorCode, j.ErrorText)
	}

	// A probe stored in the record for an older file does the same.
	x = newFlow2Env(t)
	j = x.flow2Job(filmMinutes)
	rec := x.record(j)
	rec.Library.Probe = stale
	x.setRecord(j, &rec)
	if j = x.matched(j); j.State != store.Probed {
		t.Fatalf("stored: job %s %s", j.State, j.ErrorCode)
	}
	if _, err := x.st.Target(x.ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stored: the target is still there: %v", err)
	}
}

// The result of a probe asked for an earlier match, for the same path but another tracked
// file (the film was matched again after Radarr replaced its file), is dropped and the
// file is probed again; it must not fail or advance the job.
func TestFlow2ResultOfAnotherTrackedFileIsDropped(t *testing.T) {
	x := newFlow2Env(t)
	j := x.matched(x.flow2Job(filmMinutes))
	rec := x.record(j)
	x.worker(media.LibraryProbeOutput{Path: rec.Library.Path, FileID: 11, TrackedSize: 3000, OK: true, Media: libProbe(filmMinutes)})
	if j = x.matched(j); j.State != store.Matched || x.record(j).Library.Probe != nil {
		t.Fatalf("job %s (%s)", j.State, j.ErrorCode)
	}
	if _, ok, _ := x.q.Result(x.ctx, runner.LibraryProbeID(j.ID, 1)); ok {
		t.Fatal("the result was not acknowledged")
	}
	x.matched(j)
	var args media.LibraryProbeArgs
	tasks := x.q.Tasks()
	if len(tasks) != 1 {
		t.Fatalf("tasks %+v", tasks)
	}
	if err := json.Unmarshal(tasks[0].Args, &args); err != nil || args.FileID != 12 || args.TrackedSize != 4000 {
		t.Fatalf("args %+v %v", args, err)
	}
}
