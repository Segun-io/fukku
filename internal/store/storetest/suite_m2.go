package storetest

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/store"
)

// m2Tests are the conformance tests of store.M2; Run includes them.
var m2Tests = []struct {
	name string
	fn   func(t *testing.T, s store.Store)
}{
	{"Target", testTarget},
	{"ReplaceTarget", testReplaceTarget},
	{"PutProposal", testPutProposal},
	{"TransitionFor", testTransitionFor},
	{"Rows", testRows},
	{"Import", testImport},
	{"CancelUnsubmitted", testCancelUnsubmitted},
	{"BackToConfirmation", testBackToConfirmation},
	{"ConfirmImport", testConfirmImport},
	{"PostSteps", testPostSteps},
	{"ActiveJobsFor", testActiveJobsFor},
	{"DeleteJobM2", testDeleteJobM2},
}

const unknownJob = "0123456789abcdef0123456789abcdef"

func addVideo(t *testing.T, s store.Store, jobID, name string) store.File {
	t.Helper()
	return must(s.AddFile(t.Context(), store.File{JobID: jobID, Path: "files/" + name, Kind: store.KindVideo, Size: 1}))(t)
}

func testTarget(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.Probed)
	if _, err := s.Target(ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("target of a job without one: %v, want ErrNotFound", err)
	}
	if err := s.ClearTarget(ctx, j.ID); err != nil {
		t.Fatalf("clearing a missing target: %v, want nil", err)
	}

	opts := json.RawMessage(`{"qualityProfileId":4,"monitored":true}`)
	at := time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC)
	want := store.Target{App: "radarr", TitleID: 603, Title: "Some Film", Year: 1999, Options: opts, MatchedBy: "user1", MatchedAt: at}
	if err := s.SetTarget(ctx, j.ID, want); err != nil {
		t.Fatal(err)
	}
	opts[2] = 'X' // the caller's slice is not the stored one
	got := must(s.Target(ctx, j.ID))(t)
	if got.App != "radarr" || got.TitleID != 603 || got.Title != "Some Film" || got.Year != 1999 || got.MatchedBy != "user1" ||
		!got.MatchedAt.Equal(at) || !sameJSON(got.Options, `{"qualityProfileId":4,"monitored":true}`) {
		t.Fatalf("stored target %+v (options %s)", got, got.Options)
	}

	// Setting again replaces; a zero MatchedAt is now, and no options stay nil.
	if err := s.SetTarget(ctx, j.ID, store.Target{App: "sonarr", TitleID: 12, Title: "Some Show"}); err != nil {
		t.Fatal(err)
	}
	got = must(s.Target(ctx, j.ID))(t)
	if got.App != "sonarr" || got.TitleID != 12 || got.Year != 0 || got.Options != nil || got.MatchedAt.IsZero() {
		t.Fatalf("replaced target %+v", got)
	}

	if err := s.SetTarget(ctx, j.ID, store.Target{TitleID: 1}); err == nil {
		t.Fatal("a target without an app was accepted")
	}
	if err := s.SetTarget(ctx, unknownJob, store.Target{App: "radarr", TitleID: 1}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("target of an unknown job: %v, want ErrNotFound", err)
	}
	if err := s.ClearTarget(ctx, unknownJob); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("clearing the target of an unknown job: %v, want ErrNotFound", err)
	}

	// Clearing removes the target and its rows, and only for this job.
	other := drive(t, s, newJob(t, s, "user1").ID, store.Probed)
	for _, id := range []string{j.ID, other.ID} {
		if err := s.SetTarget(ctx, id, store.Target{App: "radarr", TitleID: 1, Title: "x"}); err != nil {
			t.Fatal(err)
		}
		f := addVideo(t, s, id, "a.mkv")
		if err := s.PutRows(ctx, id, []store.Row{{ID: "r1", FileID: f.ID}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ClearTarget(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Target(ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("target after ClearTarget: %v, want ErrNotFound", err)
	}
	if rs := must(s.Rows(ctx, j.ID))(t); len(rs) != 0 {
		t.Fatalf("rows after ClearTarget: %+v", rs)
	}
	must(s.Target(ctx, other.ID))(t)
	if rs := must(s.Rows(ctx, other.ID))(t); len(rs) != 1 {
		t.Fatalf("ClearTarget touched another job's rows: %+v", rs)
	}
}

func testReplaceTarget(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.Probed)
	first := store.Target{App: "radarr", TitleID: 1, Title: "One", MatchedBy: "user1"}

	// probed → matched with a target, one event.
	got := must(s.ReplaceTarget(ctx, j.ID, store.Probed, store.Matched, &first, store.Change{Actor: "user1"}))(t)
	if got.State != store.Matched {
		t.Fatalf("job after: %+v", got)
	}
	checkStored(t, s, got)
	if tg := must(s.Target(ctx, j.ID))(t); tg.TitleID != 1 || tg.MatchedAt.IsZero() {
		t.Fatalf("target %+v", tg)
	}
	h := history(t, s, j.ID)
	if last := h[len(h)-1]; last.From != store.Probed || last.To != store.Matched || last.Actor != "user1" {
		t.Fatalf("last event %+v", last)
	}

	// A stale from is ErrConflict and changes nothing.
	f := addVideo(t, s, j.ID, "a.mkv")
	if err := s.PutRows(ctx, j.ID, []store.Row{{ID: "r1", FileID: f.ID}}); err != nil {
		t.Fatal(err)
	}
	second := store.Target{App: "sonarr", TitleID: 2, Title: "Two"}
	if _, err := s.ReplaceTarget(ctx, j.ID, store.Probed, store.Matched, &second, store.Change{}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale from: %v, want ErrConflict", err)
	}
	if tg := must(s.Target(ctx, j.ID))(t); tg.TitleID != 1 || len(must(s.Rows(ctx, j.ID))(t)) != 1 {
		t.Fatalf("a refused call changed the job: %+v", tg)
	}

	// matched → matched replaces target and rows, records no event, state unchanged.
	n := len(history(t, s, j.ID))
	got = must(s.ReplaceTarget(ctx, j.ID, store.Matched, store.Matched, &second, store.Change{}))(t)
	if got.State != store.Matched || len(history(t, s, j.ID)) != n {
		t.Fatalf("re-match: %+v, events %d → %d", got, n, len(history(t, s, j.ID)))
	}
	if tg := must(s.Target(ctx, j.ID))(t); tg.TitleID != 2 || len(must(s.Rows(ctx, j.ID))(t)) != 0 {
		t.Fatalf("re-match left %+v", tg)
	}

	// awaiting-confirmation → matched, which cannot be the target state, and back to probed.
	drive(t, s, j.ID, store.AwaitingConfirmation)
	if _, err := s.ReplaceTarget(ctx, j.ID, store.AwaitingConfirmation, store.AwaitingConfirmation, &first, store.Change{}); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("awaiting-confirmation → awaiting-confirmation: %v", err)
	}
	must(s.ReplaceTarget(ctx, j.ID, store.AwaitingConfirmation, store.Matched, &first, store.Change{}))(t)
	must(s.ReplaceTarget(ctx, j.ID, store.Matched, store.Probed, nil, store.Change{}))(t)
	if _, err := s.Target(ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("target after unmatch: %v", err)
	}

	// Other states are refused, as are a target without an app and an unknown job.
	if _, err := s.ReplaceTarget(ctx, j.ID, store.Probed, store.Cancelled, nil, store.Change{}); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("probed → cancelled: %v", err)
	}
	if _, err := s.ReplaceTarget(ctx, j.ID, store.Probed, store.AwaitingConfirmation, &first, store.Change{}); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("probed → awaiting-confirmation: %v", err)
	}
	soon := time.Now().Add(time.Hour)
	if _, err := s.ReplaceTarget(ctx, j.ID, store.Probed, store.Matched, &first, store.Change{ExpiresAt: &soon}); err == nil {
		t.Fatal("a retention date was accepted and lost")
	}
	if _, err := s.Target(ctx, j.ID); !errors.Is(err, store.ErrNotFound) || must(s.Job(ctx, j.ID))(t).State != store.Probed {
		t.Fatal("a refused call changed the job")
	}
	imp := drive(t, s, newJob(t, s, "user1").ID, store.Importing)
	if _, err := s.ReplaceTarget(ctx, imp.ID, store.Importing, store.Matched, &first, store.Change{}); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("importing: %v", err)
	}
	if _, err := s.ReplaceTarget(ctx, j.ID, store.Probed, store.Matched, &store.Target{}, store.Change{}); err == nil {
		t.Fatal("a target without an app was accepted")
	}
	if _, err := s.ReplaceTarget(ctx, unknownJob, store.Probed, store.Matched, &first, store.Change{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown job: %v", err)
	}
}

func testRows(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.Probed)
	other := drive(t, s, newJob(t, s, "user1").ID, store.Probed)
	fa, fb, fc := addVideo(t, s, j.ID, "a.mkv"), addVideo(t, s, j.ID, "b.mkv"), addVideo(t, s, j.ID, "c.mkv")
	foreign := addVideo(t, s, other.ID, "a.mkv")

	if rs := must(s.Rows(ctx, j.ID))(t); len(rs) != 0 {
		t.Fatalf("rows of a job without any: %+v", rs)
	}
	edit := json.RawMessage(`{"seasonNumber":1,"episodeIds":[503,504]}`)
	prop := json.RawMessage(`{"rejections":[{"reason":"x"}]}`)
	// Not in path order, to show that the order of the call is kept.
	rows := []store.Row{
		{ID: "r3", FileID: fc.ID},
		{ID: "r1", FileID: fa.ID, Skip: true, Edit: edit, Proposal: prop, Override: true, Result: store.ResultSkipped},
		{ID: "r2", FileID: fb.ID},
	}
	if err := s.PutRows(ctx, j.ID, rows); err != nil {
		t.Fatal(err)
	}
	edit[2] = 'X'
	got := must(s.Rows(ctx, j.ID))(t)
	if len(got) != 3 || got[0].ID != "r3" || got[1].ID != "r1" || got[2].ID != "r2" {
		t.Fatalf("rows %+v: want r3, r1, r2", got)
	}
	r1 := got[1]
	if r1.JobID != j.ID || r1.FileID != fa.ID || !r1.Skip || !r1.Override || r1.Result != store.ResultSkipped ||
		!sameJSON(r1.Edit, `{"seasonNumber":1,"episodeIds":[503,504]}`) || !sameJSON(r1.Proposal, `{"rejections":[{"reason":"x"}]}`) {
		t.Fatalf("stored row %+v", r1)
	}
	if got[0].Edit != nil || got[0].Proposal != nil || got[0].Skip || got[0].Override || got[0].Result != "" {
		t.Fatalf("row with defaults %+v", got[0])
	}

	// UpdateRow changes the editable fields, keeps the file and the place.
	upd := r1
	upd.Skip, upd.Override, upd.Result = false, false, store.ResultImported
	upd.Edit, upd.Proposal = nil, json.RawMessage(`{"ok":true}`)
	upd.FileID = fb.ID // ignored
	if err := s.UpdateRow(ctx, upd); err != nil {
		t.Fatal(err)
	}
	got = must(s.Rows(ctx, j.ID))(t)
	if got[1].ID != "r1" || got[1].FileID != fa.ID || got[1].Skip || got[1].Override || got[1].Result != store.ResultImported ||
		got[1].Edit != nil || !sameJSON(got[1].Proposal, `{"ok":true}`) {
		t.Fatalf("updated row %+v", got[1])
	}
	if err := s.UpdateRow(ctx, store.Row{JobID: j.ID, ID: "nope"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update of an unknown row: %v, want ErrNotFound", err)
	}
	if err := s.UpdateRow(ctx, store.Row{JobID: other.ID, ID: "r1"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update of another job's row: %v, want ErrNotFound", err)
	}

	// The same row id can live in two jobs.
	if err := s.PutRows(ctx, other.ID, []store.Row{{ID: "r1", FileID: foreign.ID}}); err != nil {
		t.Fatal(err)
	}

	// A bad call changes nothing.
	for name, bad := range map[string]struct {
		rows []store.Row
		want error
	}{
		"duplicate id": {[]store.Row{{ID: "x", FileID: fa.ID}, {ID: "x", FileID: fb.ID}}, store.ErrExists},
		"unknown file": {[]store.Row{{ID: "x", FileID: 1 << 40}}, store.ErrNotFound},
		"foreign file": {[]store.Row{{ID: "x", FileID: foreign.ID}}, store.ErrNotFound},
		"missing id":   {[]store.Row{{FileID: fa.ID}}, nil},
	} {
		err := s.PutRows(ctx, j.ID, bad.rows)
		if err == nil || (bad.want != nil && !errors.Is(err, bad.want)) {
			t.Fatalf("%s: %v, want %v", name, err, bad.want)
		}
		if n := len(must(s.Rows(ctx, j.ID))(t)); n != 3 {
			t.Fatalf("%s: %d rows left, want the 3 from before", name, n)
		}
	}
	if err := s.PutRows(ctx, unknownJob, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rows of an unknown job: %v, want ErrNotFound", err)
	}

	// PutRows replaces; an empty slice removes all. The other job is untouched.
	if err := s.PutRows(ctx, j.ID, []store.Row{{ID: "r9", FileID: fb.ID}}); err != nil {
		t.Fatal(err)
	}
	if got = must(s.Rows(ctx, j.ID))(t); len(got) != 1 || got[0].ID != "r9" {
		t.Fatalf("rows after a replace: %+v", got)
	}
	if n := len(must(s.Rows(ctx, other.ID))(t)); n != 1 {
		t.Fatalf("PutRows touched another job: %d rows", n)
	}
	if err := s.PutRows(ctx, j.ID, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(must(s.Rows(ctx, j.ID))(t)); n != 0 {
		t.Fatalf("%d rows after an empty PutRows", n)
	}

	// A row goes when its file does.
	if err := s.PutRows(ctx, j.ID, []store.Row{{ID: "a", FileID: fa.ID}, {ID: "b", FileID: fb.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFile(ctx, fa.ID); err != nil {
		t.Fatal(err)
	}
	if got = must(s.Rows(ctx, j.ID))(t); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("rows after deleting a file: %+v", got)
	}
}

func testImport(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.AwaitingConfirmation)
	if _, err := s.Import(ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("import of a job without one: %v, want ErrNotFound", err)
	}
	// "Submitting": stored before the call, with no command id yet.
	sub := time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC)
	if err := s.PutImport(ctx, store.Import{JobID: j.ID, SubmittedAt: sub}); err != nil {
		t.Fatal(err)
	}
	got := must(s.Import(ctx, j.ID))(t)
	if got.JobID != j.ID || got.CommandID != 0 || !got.SubmittedAt.Equal(sub) || !got.FinishedAt.IsZero() {
		t.Fatalf("import %+v", got)
	}
	fin := sub.Add(time.Minute)
	if err := s.PutImport(ctx, store.Import{JobID: j.ID, CommandID: 77, SubmittedAt: sub, FinishedAt: fin}); err != nil {
		t.Fatal(err)
	}
	got = must(s.Import(ctx, j.ID))(t)
	if got.CommandID != 77 || !got.SubmittedAt.Equal(sub) || !got.FinishedAt.Equal(fin) || got.Baseline != nil {
		t.Fatalf("replaced import %+v", got)
	}
	// The baseline is stored as given and comes back as a copy.
	base := json.RawMessage(`{"movieFileId":12}`)
	if err := s.PutImport(ctx, store.Import{JobID: j.ID, SubmittedAt: sub, Baseline: base}); err != nil {
		t.Fatal(err)
	}
	base[2] = 'X'
	if got = must(s.Import(ctx, j.ID))(t); !sameJSON(got.Baseline, `{"movieFileId":12}`) {
		t.Fatalf("baseline %s", got.Baseline)
	}
	// Where the refusals are counted from is stored and replaced like the rest.
	if err := s.PutImport(ctx, store.Import{JobID: j.ID, CommandID: 77, SubmittedAt: sub, Baseline: json.RawMessage(`{"movieFileId":12}`), RefusalsFrom: 9}); err != nil {
		t.Fatal(err)
	}
	if got = must(s.Import(ctx, j.ID))(t); got.RefusalsFrom != 9 || got.CommandID != 77 || !sameJSON(got.Baseline, `{"movieFileId":12}`) {
		t.Fatalf("import with RefusalsFrom %+v", got)
	}
	if err := s.PutImport(ctx, store.Import{JobID: j.ID, CommandID: 77, SubmittedAt: sub}); err != nil {
		t.Fatal(err)
	}
	if got = must(s.Import(ctx, j.ID))(t); got.RefusalsFrom != 0 {
		t.Fatalf("RefusalsFrom not replaced: %+v", got)
	}
	if err := s.PutImport(ctx, store.Import{JobID: unknownJob}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("import of an unknown job: %v, want ErrNotFound", err)
	}
}

func testPostSteps(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.PostProcessing)
	other := drive(t, s, newJob(t, s, "user1").ID, store.PostProcessing)
	if ps := must(s.PostSteps(ctx, j.ID))(t); len(ps) != 0 {
		t.Fatalf("steps of a job without any: %+v", ps)
	}
	at := time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC)
	puts := []store.PostStep{
		{JobID: j.ID, Name: "subtitles"},
		{JobID: j.ID, Name: "mediaServerRefresh", Status: store.StepOK, Attempts: 1, Detail: "refreshed", UpdatedAt: at},
		{JobID: other.ID, Name: "subtitles", Status: store.StepFailed},
	}
	for _, p := range puts {
		if err := s.PutPostStep(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	got := must(s.PostSteps(ctx, j.ID))(t)
	if len(got) != 2 || got[0].Name != "subtitles" || got[1].Name != "mediaServerRefresh" {
		t.Fatalf("steps %+v: want the order they were first put", got)
	}
	if got[0].Status != store.StepPending || got[0].Attempts != 0 || got[0].UpdatedAt.IsZero() {
		t.Fatalf("defaults %+v", got[0])
	}
	if got[1].JobID != j.ID || got[1].Status != store.StepOK || got[1].Attempts != 1 || got[1].Detail != "refreshed" || !got[1].UpdatedAt.Equal(at) {
		t.Fatalf("step %+v", got[1])
	}

	// Putting again replaces in place.
	if err := s.PutPostStep(ctx, store.PostStep{JobID: j.ID, Name: "subtitles", Status: store.StepFailed, Attempts: 5, Detail: "gave up"}); err != nil {
		t.Fatal(err)
	}
	got = must(s.PostSteps(ctx, j.ID))(t)
	if len(got) != 2 || got[0].Name != "subtitles" || got[0].Status != store.StepFailed || got[0].Attempts != 5 || got[0].Detail != "gave up" {
		t.Fatalf("replaced steps %+v", got)
	}
	if o := must(s.PostSteps(ctx, other.ID))(t); len(o) != 1 || o[0].Status != store.StepFailed {
		t.Fatalf("other job's steps %+v", o)
	}
	if err := s.PutPostStep(ctx, store.PostStep{JobID: j.ID}); err == nil {
		t.Fatal("a step without a name was accepted")
	}
	if err := s.PutPostStep(ctx, store.PostStep{JobID: unknownJob, Name: "x"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("step of an unknown job: %v, want ErrNotFound", err)
	}
}

func testActiveJobsFor(t *testing.T, s store.Store) {
	ctx := t.Context()
	target := func(j store.Job, app string, id int) {
		t.Helper()
		if err := s.SetTarget(ctx, j.ID, store.Target{App: app, TitleID: id, Title: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if js := must(s.ActiveJobsFor(ctx, "radarr", 1))(t); len(js) != 0 {
		t.Fatalf("active jobs of nothing: %+v", js)
	}
	a := drive(t, s, newJob(t, s, "user1").ID, store.Matched)
	b := drive(t, s, newJob(t, s, "user2").ID, store.Importing)
	done := drive(t, s, newJob(t, s, "user1").ID, store.Done)
	cancelled := must(s.Transition(ctx, newJob(t, s, "user1").ID, store.Uploading, store.Cancelled, store.Change{}))(t)
	failed := must(s.Transition(ctx, newJob(t, s, "user1").ID, store.Uploading, store.Failed, store.Change{ErrorCode: "x"}))(t)
	otherTitle := drive(t, s, newJob(t, s, "user1").ID, store.Matched)
	otherApp := drive(t, s, newJob(t, s, "user1").ID, store.Matched)
	drive(t, s, newJob(t, s, "user1").ID, store.Probed) // no target
	for _, j := range []store.Job{a, b, done, cancelled, failed} {
		target(j, "radarr", 1)
	}
	target(otherTitle, "radarr", 2)
	target(otherApp, "sonarr", 1)

	got := must(s.ActiveJobsFor(ctx, "radarr", 1))(t)
	// Newest first; done and cancelled are not active, a failed job still holds the title.
	sameIDs(t, "active jobs", got, failed, b, a)
	if got[1].State != store.Importing || got[1].Owner != "user2" {
		t.Fatalf("job %+v", got[1])
	}

	// Moving the job on or clearing its target frees the title.
	must(s.Transition(ctx, a.ID, store.Matched, store.Cancelled, store.Change{}))(t)
	if err := s.ClearTarget(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "active jobs after a cancel and a clear", must(s.ActiveJobsFor(ctx, "radarr", 1))(t), failed)
	sameIDs(t, "sonarr title 1", must(s.ActiveJobsFor(ctx, "sonarr", 1))(t), otherApp)
}

func testDeleteJobM2(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.PostProcessing)
	keep := drive(t, s, newJob(t, s, "user1").ID, store.PostProcessing)
	for _, id := range []string{j.ID, keep.ID} {
		f := addVideo(t, s, id, "a.mkv")
		if err := s.SetTarget(ctx, id, store.Target{App: "radarr", TitleID: 5, Title: "x"}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutRows(ctx, id, []store.Row{{ID: "r1", FileID: f.ID}}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutImport(ctx, store.Import{JobID: id, CommandID: 3}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutPostStep(ctx, store.PostStep{JobID: id, Name: "subtitles"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Target(ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("target of a deleted job: %v", err)
	}
	if _, err := s.Import(ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("import of a deleted job: %v", err)
	}
	if n := len(must(s.Rows(ctx, j.ID))(t)) + len(must(s.PostSteps(ctx, j.ID))(t)); n != 0 {
		t.Fatalf("%d rows and steps of a deleted job", n)
	}
	sameIDs(t, "active jobs", must(s.ActiveJobsFor(ctx, "radarr", 5))(t), keep)

	must(s.Target(ctx, keep.ID))(t)
	must(s.Import(ctx, keep.ID))(t)
	if len(must(s.Rows(ctx, keep.ID))(t)) != 1 || len(must(s.PostSteps(ctx, keep.ID))(t)) != 1 {
		t.Fatal("deleting one job touched another")
	}
}

func testPutProposal(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.Probed)
	a, b := addVideo(t, s, j.ID, "a.mkv"), addVideo(t, s, j.ID, "b.mkv")
	if _, err := s.PutProposal(ctx, j.ID, store.Matched, store.AwaitingConfirmation, store.Target{App: "radarr", TitleID: 1}, nil, store.Change{}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a probed job: %v, want ErrConflict", err)
	}
	must(s.ReplaceTarget(ctx, j.ID, store.Probed, store.Matched, &store.Target{App: "radarr", TitleID: 1, Title: "One"}, store.Change{Actor: "user1"}))(t)
	tg := must(s.Target(ctx, j.ID))(t)
	rows := []store.Row{{ID: "r1", FileID: a.ID, Proposal: json.RawMessage(`{"MovieID":1}`)}, {ID: "r2", FileID: b.ID, Skip: true}}

	// Refused calls change nothing.
	for name, call := range map[string]func() error{
		"another title": func() error {
			_, err := s.PutProposal(ctx, j.ID, store.Matched, store.AwaitingConfirmation, store.Target{App: "radarr", TitleID: 2, MatchedAt: tg.MatchedAt}, rows, store.Change{})
			return want(err, store.ErrConflict)
		},
		"another match of the same title": func() error {
			_, err := s.PutProposal(ctx, j.ID, store.Matched, store.AwaitingConfirmation, store.Target{App: "radarr", TitleID: 1, MatchedAt: tg.MatchedAt.Add(time.Second)}, rows, store.Change{})
			return want(err, store.ErrConflict)
		},
		"another app": func() error {
			_, err := s.PutProposal(ctx, j.ID, store.Matched, store.AwaitingConfirmation, store.Target{App: "sonarr", TitleID: 1, MatchedAt: tg.MatchedAt}, rows, store.Change{})
			return want(err, store.ErrConflict)
		},
		"a stale from": func() error {
			_, err := s.PutProposal(ctx, j.ID, store.AwaitingConfirmation, store.AwaitingConfirmation, tg, rows, store.Change{})
			return want(err, store.ErrConflict)
		},
		"to matched": func() error {
			_, err := s.PutProposal(ctx, j.ID, store.Matched, store.Matched, tg, rows, store.Change{})
			return want(err, store.ErrInvalidTransition)
		},
		"a file of no job": func() error {
			_, err := s.PutProposal(ctx, j.ID, store.Matched, store.AwaitingConfirmation, tg, []store.Row{{ID: "r9", FileID: 99999}}, store.Change{})
			return want(err, store.ErrNotFound)
		},
		"a row twice": func() error {
			_, err := s.PutProposal(ctx, j.ID, store.Matched, store.AwaitingConfirmation, tg, []store.Row{rows[0], rows[0]}, store.Change{})
			return want(err, store.ErrExists)
		},
		"a retention date": func() error {
			soon := time.Now().Add(time.Hour)
			if _, err := s.PutProposal(ctx, j.ID, store.Matched, store.AwaitingConfirmation, tg, rows, store.Change{ExpiresAt: &soon}); err == nil {
				return errors.New("accepted")
			}
			return nil
		},
	} {
		if err := call(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if got := must(s.Job(ctx, j.ID))(t); got.State != store.Matched || len(must(s.Rows(ctx, j.ID))(t)) != 0 {
			t.Fatalf("%s changed the job: %s, rows %v", name, got.State, must(s.Rows(ctx, j.ID))(t))
		}
	}

	// matched → awaiting-confirmation with the rows, one event.
	events := len(history(t, s, j.ID))
	got := must(s.PutProposal(ctx, j.ID, store.Matched, store.AwaitingConfirmation, tg, rows, store.Change{Note: "2 rows"}))(t)
	if got.State != store.AwaitingConfirmation {
		t.Fatalf("job after: %+v", got)
	}
	checkStored(t, s, got)
	h := history(t, s, j.ID)
	if len(h) != events+1 || h[len(h)-1].From != store.Matched || h[len(h)-1].To != store.AwaitingConfirmation || h[len(h)-1].Note != "2 rows" {
		t.Fatalf("history %+v", h)
	}
	if rs := must(s.Rows(ctx, j.ID))(t); len(rs) != 2 || rs[0].ID != "r1" || !sameJSON(rs[0].Proposal, `{"MovieID":1}`) || !rs[1].Skip {
		t.Fatalf("rows %+v", rs)
	}

	// awaiting-confirmation to itself replaces the rows and records nothing.
	must(s.PutProposal(ctx, j.ID, store.AwaitingConfirmation, store.AwaitingConfirmation, tg, rows[1:], store.Change{}))(t)
	if rs := must(s.Rows(ctx, j.ID))(t); len(rs) != 1 || rs[0].ID != "r2" || len(history(t, s, j.ID)) != events+1 {
		t.Fatalf("rows %+v", rs)
	}

	// A re-match between the read of the target and the write: refused, no old rows.
	must(s.ReplaceTarget(ctx, j.ID, store.AwaitingConfirmation, store.Matched, &store.Target{App: "radarr", TitleID: 3}, store.Change{}))(t)
	if _, err := s.PutProposal(ctx, j.ID, store.Matched, store.AwaitingConfirmation, tg, rows, store.Change{}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("after a re-match: %v, want ErrConflict", err)
	}
	if len(must(s.Rows(ctx, j.ID))(t)) != 0 || must(s.Job(ctx, j.ID))(t).State != store.Matched {
		t.Fatal("the old title's rows were stored")
	}
	if _, err := s.PutProposal(ctx, unknownJob, store.Matched, store.AwaitingConfirmation, tg, nil, store.Change{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown job: %v", err)
	}
}

func want(err, target error) error {
	if !errors.Is(err, target) {
		return fmt.Errorf("got %w, want %w", err, target)
	}
	return nil
}

func testTransitionFor(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.Probed)
	fail := store.Change{Actor: "system", ErrorCode: "proposal_failed", ErrorText: "refused"}
	if _, err := s.TransitionFor(ctx, j.ID, store.Target{App: "radarr", TitleID: 1}, store.Probed, store.Failed, fail); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a job without a target: %v, want ErrConflict", err)
	}
	must(s.ReplaceTarget(ctx, j.ID, store.Probed, store.Matched, &store.Target{App: "radarr", TitleID: 1, Title: "One"}, store.Change{Actor: "user1"}))(t)
	tg := must(s.Target(ctx, j.ID))(t)
	events := len(history(t, s, j.ID))

	// Another title, app or match: refused, nothing changes.
	for name, other := range map[string]store.Target{
		"another title":                   {App: "radarr", TitleID: 2, MatchedAt: tg.MatchedAt},
		"another app":                     {App: "sonarr", TitleID: 1, MatchedAt: tg.MatchedAt},
		"another match of the same title": {App: "radarr", TitleID: 1, MatchedAt: tg.MatchedAt.Add(time.Second)},
	} {
		if _, err := s.TransitionFor(ctx, j.ID, other, store.Matched, store.Failed, fail); !errors.Is(err, store.ErrConflict) {
			t.Errorf("%s: %v, want ErrConflict", name, err)
		}
	}
	if got := must(s.Job(ctx, j.ID))(t); got.State != store.Matched || got.ErrorCode != "" || len(history(t, s, j.ID)) != events {
		t.Fatalf("a refused call changed the job: %+v", got)
	}

	// Transition's own rules still hold.
	if _, err := s.TransitionFor(ctx, j.ID, tg, store.Probed, store.Matched, store.Change{}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("a stale from: %v, want ErrConflict", err)
	}
	if _, err := s.TransitionFor(ctx, j.ID, tg, store.Matched, store.Done, store.Change{}); !errors.Is(err, store.ErrInvalidTransition) {
		t.Errorf("matched → done: %v, want ErrInvalidTransition", err)
	}
	if _, err := s.TransitionFor(ctx, j.ID, tg, store.Matched, store.Stalled, store.Change{}); !errors.Is(err, store.ErrInvalidTransition) {
		t.Errorf("stalled without a code: %v, want ErrInvalidTransition", err)
	}

	// The same title: the move happens, with Transition's bookkeeping.
	got := must(s.TransitionFor(ctx, j.ID, tg, store.Matched, store.Stalled, store.Change{Actor: "system", ErrorCode: "arr_unavailable", ErrorText: "down"}))(t)
	if got.State != store.Stalled || got.Resume != store.Matched || got.ErrorCode != "arr_unavailable" {
		t.Fatalf("job after: %+v", got)
	}
	checkStored(t, s, got)
	if h := history(t, s, j.ID); len(h) != events+1 || h[len(h)-1].To != store.Stalled {
		t.Fatalf("history %+v", h)
	}
	got = must(s.TransitionFor(ctx, j.ID, tg, store.Stalled, store.Matched, store.Change{Actor: "system"}))(t)
	if got.State != store.Matched || got.ErrorCode != "" {
		t.Fatalf("resumed: %+v", got)
	}

	// After a re-match the step's failure for the old title is refused.
	must(s.ReplaceTarget(ctx, j.ID, store.Matched, store.Matched, &store.Target{App: "radarr", TitleID: 2}, store.Change{Actor: "user1"}))(t)
	if _, err := s.TransitionFor(ctx, j.ID, tg, store.Matched, store.Failed, fail); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("after a re-match: %v, want ErrConflict", err)
	}
	if got := must(s.Job(ctx, j.ID))(t); got.State != store.Matched || got.ErrorCode != "" {
		t.Fatalf("the new title's job failed: %+v", got)
	}
	if _, err := s.TransitionFor(ctx, unknownJob, tg, store.Matched, store.Failed, fail); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown job: %v", err)
	}
}

// testConfirmImport: the confirmation's one write. The title is checked before anything is
// written; the rows' flags, the options, the transition and the audit entries all happen,
// or nothing does.
func testConfirmImport(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.Probed)
	fa, fb := addVideo(t, s, j.ID, "a.mkv"), addVideo(t, s, j.ID, "b.mkv")
	at := time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC)
	tg := store.Target{App: "sonarr", TitleID: 12, Title: "Some Show", Options: json.RawMessage(`{"added":true}`), MatchedAt: at}
	if err := s.SetTarget(ctx, j.ID, tg); err != nil {
		t.Fatal(err)
	}
	rows := []store.Row{
		{ID: "r1", FileID: fa.ID, Edit: json.RawMessage(`{"seasonNumber":1}`), Proposal: json.RawMessage(`{"a":1}`)},
		{ID: "r2", FileID: fb.ID},
	}
	j = must(s.Transition(ctx, j.ID, store.Probed, store.Matched, store.Change{Actor: "user1"}))(t)
	j = must(s.PutProposal(ctx, j.ID, store.Matched, store.AwaitingConfirmation, tg, rows, store.Change{}))(t)
	flags := []store.Row{{ID: "r1", Override: true}, {ID: "r2", Skip: true, Result: store.ResultSkipped}}
	opts := json.RawMessage(`{"added":true,"subtitles":{"3":{"language":"es"}}}`)
	audit := []store.AuditEntry{
		{Actor: "user1", Email: "user1@example.com", Action: "import.confirmed", Detail: map[string]string{"rows": "r1"}},
		{Actor: "user1", Action: "rejection.overridden", JobID: unknownJob, Detail: map[string]string{"row": "r1"}},
	}
	before := len(must(s.AuditLog(ctx, j.ID))(t))

	// Refusals change nothing: another title (even one with the same id matched later), a
	// row the job does not have, a job in another state.
	other := tg
	other.MatchedAt = at.Add(time.Second)
	if _, err := s.ConfirmImport(ctx, j.ID, other, flags, opts, audit, store.Change{Actor: "user1"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("another title: %v, want ErrConflict", err)
	}
	if _, err := s.ConfirmImport(ctx, j.ID, tg, []store.Row{{ID: "r9"}}, opts, audit, store.Change{Actor: "user1"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown row: %v, want ErrNotFound", err)
	}
	if _, err := s.ConfirmImport(ctx, j.ID, tg, flags, opts, []store.AuditEntry{{Actor: "user1"}}, store.Change{Actor: "user1"}); err == nil {
		t.Fatal("an audit entry without an action was accepted")
	}
	if _, err := s.ConfirmImport(ctx, unknownJob, tg, nil, nil, nil, store.Change{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown job: %v, want ErrNotFound", err)
	}
	if got := must(s.Job(ctx, j.ID))(t); got.State != store.AwaitingConfirmation {
		t.Fatalf("a refusal moved the job to %s", got.State)
	}
	if got := must(s.Rows(ctx, j.ID))(t); got[0].Override || got[1].Skip {
		t.Fatalf("a refusal changed the rows %+v", got)
	}
	if got := must(s.Target(ctx, j.ID))(t); !sameJSON(got.Options, `{"added":true}`) {
		t.Fatalf("a refusal changed the options %s", got.Options)
	}
	if n := len(must(s.AuditLog(ctx, j.ID))(t)); n != before {
		t.Fatalf("a refusal wrote %d audit entries", n-before)
	}

	next, err := s.ConfirmImport(ctx, j.ID, tg, flags, opts, audit, store.Change{Actor: "user1", Note: "2 confirmed"})
	if err != nil || next.State != store.Importing {
		t.Fatalf("ConfirmImport = %+v, %v", next, err)
	}
	got := must(s.Rows(ctx, j.ID))(t)
	if !got[0].Override || got[0].Skip || !sameJSON(got[0].Edit, `{"seasonNumber":1}`) || !sameJSON(got[0].Proposal, `{"a":1}`) ||
		!got[1].Skip || got[1].Result != store.ResultSkipped {
		t.Fatalf("rows %+v", got)
	}
	if tgt := must(s.Target(ctx, j.ID))(t); !sameJSON(tgt.Options, string(opts)) || !tgt.MatchedAt.Equal(at) {
		t.Fatalf("target %+v (%s)", tgt, tgt.Options)
	}
	h := must(s.History(ctx, j.ID))(t)
	if last := h[len(h)-1]; last.From != store.AwaitingConfirmation || last.To != store.Importing || last.Actor != "user1" || last.Note != "2 confirmed" {
		t.Fatalf("event %+v", last)
	}
	log := must(s.AuditLog(ctx, j.ID))(t)
	if len(log) != before+2 || log[before].Action != "import.confirmed" || log[before].Email != "user1@example.com" ||
		log[before+1].Action != "rejection.overridden" || log[before+1].JobID != j.ID || log[before+1].Detail["row"] != "r1" {
		t.Fatalf("audit %+v", log)
	}
	if _, err := s.ConfirmImport(ctx, j.ID, tg, nil, nil, nil, store.Change{}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("twice: %v, want ErrConflict", err)
	}
}
