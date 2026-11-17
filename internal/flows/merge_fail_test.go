package flows

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/merge"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

// merge.unmonitorAfterMerge decides whether the film is unmonitored after the import, and
// the result's warning says what can still replace the merged file.
func TestMergeUnmonitorFollowsTheSetting(t *testing.T) {
	for _, on := range []bool{true, false} {
		x := newMergeEnvWith(t, func(c *config.Config) { c.Merge.UnmonitorAfterMerge = on })
		j := x.confirmMerge(x.readyJob(), nil)
		x.propose(j, 1)
		if j = x.run(x.eng, j, nil); j.State != store.Done {
			t.Fatalf("on=%t: job %s (%s)", on, j.State, j.ErrorText)
		}
		_, rv, err := x.eng.Detail(x.ctx, j, false)
		if err != nil {
			t.Fatal(err)
		}
		m := x.movies.Library[0]
		wantReason, wantWarn := UnmonitorMerged, WarnSearchCanReplace
		if !on {
			wantReason, wantWarn = "", WarnUpgradeCanReplace
		}
		if m.Monitored == on || *rv.Monitored != !on || rv.UnmonitorReason != wantReason ||
			len(rv.Merge.Warnings) != 1 || rv.Merge.Warnings[0].Code != wantWarn {
			t.Errorf("on=%t: monitored %t, result %+v, merge %+v", on, m.Monitored, rv, rv.Merge)
		}
		h, _ := x.st.History(x.ctx, j.ID)
		found := slices.ContainsFunc(h, func(e store.Event) bool {
			return e.From == store.Imported && e.To == store.PostProcessing && len(e.Note) > 0 && (on == (e.Note[:11] == "unmonitored"))
		})
		if !found {
			t.Errorf("on=%t: no note saying why: %+v", on, h)
		}
	}
}

// Radarr's refusal to replace the file it has ("not an upgrade") is what the merge's
// confirmation answers: the import goes ahead.
func TestMergeAcceptsNotAnUpgrade(t *testing.T) {
	x := newMergeEnv(t)
	j := x.confirmMerge(x.readyJob(), nil)
	p := x.propose(j, 1)
	x.movies.Verdicts = map[string][]arr.Rejection{p: {{Reason: "Not an upgrade for existing movie file. Existing quality: WEBDL-1080p. New Quality WEBDL-1080p."}}}
	if j = x.run(x.eng, j, nil); j.State != store.Done || len(x.movies.Imports) != 1 {
		t.Fatalf("job %s (%s), %d import(s)", j.State, j.ErrorText, len(x.movies.Imports))
	}
}

// The confirmation's checks, each refusing with nothing written and nothing submitted.
func TestMergeConfirmRefusals(t *testing.T) {
	i := func(v ...int) []int { return v }
	str := func(s string) *string { return &s }
	cases := []struct {
		name  string
		setup func(x *mergeEnv, j store.Job)
		cfg   func(*config.Config)
		req   ConfirmRequest
		code  string
	}{
		{"no preview", func(x *mergeEnv, j store.Job) { x.dropPreview(j) }, nil, mergeReq(nil), CodePreviewRequired},
		{"another offset", nil, nil, mergeReq(&MergeRequest{OffsetMs: ptr(int64(-700))}), CodePreviewRequired},
		{"pending preview", func(x *mergeEnv, j store.Job) {
			if _, _, err := x.p.RequestPreview(x.ctx, "user1", j, offset(-900)); err != nil {
				x.t.Fatal(err)
			}
		}, nil, mergeReq(&MergeRequest{OffsetMs: ptr(int64(-900))}), CodePreviewRequired},
		{"track not previewed", nil, nil, mergeReq(&MergeRequest{AudioTracks: i(1)}), CodePreviewRequired},
		{"no audio", nil, nil, mergeReq(&MergeRequest{AudioTracks: []int{}}), CodeBadRequest},
		{"unknown audio", nil, nil, mergeReq(&MergeRequest{AudioTracks: i(2, 9)}), CodeBadRequest},
		{"audio twice", nil, nil, mergeReq(&MergeRequest{AudioTracks: i(2, 2)}), CodeBadRequest},
		{"audio as subtitle", nil, nil, mergeReq(&MergeRequest{SubtitleTracks: i(2)}), CodeBadRequest},
		{"unknown subtitle", nil, nil, mergeReq(&MergeRequest{SubtitleTracks: i(4)}), CodeBadRequest},
		{"tag not canonical", nil, nil, mergeReq(&MergeRequest{LanguageTag: str("ES-419")}), CodeBadRequest},
		{"tag not a tag", nil, nil, mergeReq(&MergeRequest{LanguageTag: str("en_US")}), CodeBadRequest},
		{"empty name", nil, nil, mergeReq(&MergeRequest{TrackName: str("")}), CodeBadRequest},
		{"control in name", nil, nil, mergeReq(&MergeRequest{TrackName: str("a\nb")}), CodeBadRequest},
		{"overrides", nil, nil, ConfirmRequest{Flow2: OfferMerge, Overrides: []string{"r1"}}, CodeBadRequest},
		{"season dub", nil, nil, ConfirmRequest{Flow2: OfferMerge, SeasonDub: PolicyReplace}, CodeBadRequest},
		{"merge with a version", nil, nil, ConfirmRequest{Flow2: OfferMerge, Version: &VersionRequest{Label: "Español"}}, CodeBadRequest},
		{"version with a merge", nil, nil, ConfirmRequest{Flow2: OfferVersion, Merge: &MergeRequest{}}, CodeBadRequest},
		{"merge disabled", nil, func(c *config.Config) { c.Merge.Enabled = false }, mergeReq(nil), CodeMergeNotOffered},
		{"flow 1 import", nil, nil, ConfirmRequest{}, CodeFlow2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := newMergeEnv(t)
			j := x.readyJob()
			if c.setup != nil {
				c.setup(x, j)
			}
			if c.cfg != nil { // the settings change after the preview, as on a restart
				c.cfg(&x.cfg)
				x.eng = x.process(x.st, x.movies, x.q)
			}
			before := len(x.q.Tasks())
			_, err := x.eng.Confirm(x.ctx, "user1", "", true, x.jobNow(j), c.req)
			if Code(err) != c.code {
				t.Fatalf("err %v, want %s", err, c.code)
			}
			if j = x.jobNow(j); j.State != store.AwaitingConfirmation || IsMerge(x.tgt(j)) || len(x.q.Tasks()) != before {
				t.Errorf("job %s, merge %t, tasks %+v", j.State, IsMerge(x.tgt(j)), x.q.Tasks())
			}
		})
	}
}

func (x *mergeEnv) dropPreview(j store.Job) {
	x.t.Helper()
	t := x.tgt(j)
	var err error
	if t.Options, err = withOption(t.Options, "preview", nil); err != nil {
		x.t.Fatal(err)
	}
	if err := x.st.SetTarget(x.ctx, j.ID, t); err != nil {
		x.t.Fatal(err)
	}
}

// flow2 and merge on a job that is not flow 2 are a bad request.
func TestMergeRefusedForFlow1(t *testing.T) {
	x := newImportEnv(t)
	j := x.filmJob()
	if _, err := x.eng.Confirm(x.ctx, "user1", "", true, j, mergeReq(nil)); Code(err) != CodeBadRequest {
		t.Fatalf("err %v", err)
	}
	if j = x.jobNow(j); j.State != store.AwaitingConfirmation {
		t.Errorf("job %s", j.State)
	}
}

// Radarr's tracked file changed since the match: the confirmation is refused, and the job
// goes back to probed without its target, to be matched again.
func TestMergeConfirmLibraryChanged(t *testing.T) {
	for name, change := range map[string]func(f *arr.MovieFile) *arr.MovieFile{
		"replaced": func(f *arr.MovieFile) *arr.MovieFile { f.ID = 13; return f },
		"resized":  func(f *arr.MovieFile) *arr.MovieFile { f.Size = 4001; return f },
		"deleted":  func(*arr.MovieFile) *arr.MovieFile { return nil },
		"renamed": func(f *arr.MovieFile) *arr.MovieFile {
			f.Path = appLibRoot + "/Some Film (2001)/Some Film (2001) - WEBDL-1080p.mkv"
			return f
		},
	} {
		t.Run(name, func(t *testing.T) {
			x := newMergeEnv(t)
			j := x.readyJob()
			x.movies.Library[0].File = change(libFile())
			if _, err := x.eng.Confirm(x.ctx, "user1", "", true, j, mergeReq(nil)); Code(err) != CodeLibraryChanged {
				t.Fatalf("err %v", err)
			}
			if j = x.jobNow(j); j.State != store.Probed {
				t.Errorf("job %s", j.State)
			}
			if _, err := x.st.Target(x.ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("target %v", err)
			}
			if len(x.q.Tasks()) != 0 {
				t.Errorf("tasks %+v", x.q.Tasks())
			}
		})
	}
}

// previewing calls back once, from the confirmation's last call to Radarr before its write.
type previewing struct {
	*arrtest.FakeMovies
	then func()
}

func (m previewing) Languages(ctx context.Context) ([]arr.Language, error) {
	if m.then != nil {
		m.then()
	}
	return m.FakeMovies.Languages(ctx)
}

// A preview requested while the merge is being confirmed is not written over: the
// confirmation reads the target again under the job's lock and refuses.
func TestMergeConfirmPreviewRequestedMeanwhile(t *testing.T) {
	x := newMergeEnv(t)
	j := x.readyJob()
	var newer Preview
	eng := x.process(x.st, previewing{x.movies, func() {
		pv, _, err := x.p.RequestPreview(x.ctx, "user2", x.jobNow(j), offset(-600))
		if err != nil {
			t.Error(err)
		}
		newer = pv
	}}, x.q)
	if _, err := eng.Confirm(x.ctx, "user1", "", true, j, mergeReq(nil)); Code(err) != CodePreviewRequired {
		t.Fatalf("err %v", err)
	}
	got, _ := PreviewOf(x.tgt(j))
	if j = x.jobNow(j); j.State != store.AwaitingConfirmation || IsMerge(x.tgt(j)) || got.Request != newer.Request || got.OffsetMs != -600 {
		t.Errorf("job %s, preview %+v", j.State, got)
	}
}

// Every failure before the import leaves the library as it was: the film keeps its file, no
// import is sent, and it stays monitored.
func TestMergeFailuresLeaveTheLibraryUntouched(t *testing.T) {
	cases := []struct {
		name   string
		before func(x *mergeEnv, j store.Job) // after the confirmation
		out    func(*merge.Output)            // the worker's output
		fail   string                         // instead, the task fails with this spool error code
		after  func(x *mergeEnv, j store.Job) // once the merged file is in
		state  store.State
		code   string
		refuse string // the merge view's refusal; for a job back at its confirmation (#95), the flow 2 view's
		kept   bool   // the verified merged file stays for a retry (it was in before the failure)
	}{
		{name: "refused: truncated dub", out: func(o *merge.Output) {
			*o = merge.Output{Refusal: merge.RefusalVerifyFailed, Detail: "audio ends early"}
		},
			state: store.AwaitingConfirmation, refuse: merge.RefusalVerifyFailed},
		{name: "refused: mkvmerge warning", out: func(o *merge.Output) { *o = merge.Output{Refusal: merge.RefusalMuxFailed, Detail: "exit 1"} },
			state: store.AwaitingConfirmation, refuse: merge.RefusalMuxFailed},
		{name: "refused: library changed while read", out: func(o *merge.Output) { *o = merge.Output{Refusal: merge.RefusalLibraryChanged} },
			state: store.Probed},
		{name: "task error", fail: spool.CodeInternal, state: store.Failed, code: runner.CodeTaskFailed},
		{name: "task timeout", fail: spool.CodeTimeout, state: store.Failed, code: runner.CodeTaskTimeout},
		{name: "merged from another size", out: func(o *merge.Output) { o.Library.Size = 3999 }, state: store.Probed},
		{name: "another track name", out: func(o *merge.Output) { o.TrackName = "Latino" }, state: store.Failed, code: runner.CodeTaskFailed},
		{name: "elsewhere", out: func(o *merge.Output) { o.Path = "merged/2/x.mkv" }, state: store.Failed, code: runner.CodeTaskFailed},
		{name: "merged file not on disk", out: func(o *merge.Output) { o.Detail = noFile },
			state: store.Failed, code: CodeMergeFailed},
		// What a compromised worker may report as its merge (#113): never offered to Radarr.
		{name: "merged file is a link", out: func(o *merge.Output) { o.Detail = linkFile },
			state: store.Failed, code: CodeMergeFailed},
		{name: "merged folder is a link", out: func(o *merge.Output) { o.Detail = linkDir },
			state: store.Failed, code: CodeMergeFailed},
		{name: "outside the library roots", before: func(x *mergeEnv, j store.Job) {
			x.cfg.Paths.LibraryRoots = []string{"/data/other"}
			x.eng = x.process(x.st, x.movies, x.q)
		}, state: store.Failed, code: CodeLibraryUnreadable},
		{name: "sentinel missing before the merge", before: func(x *mergeEnv, j store.Job) {
			x.eng.checkStorage = func() error { return errors.New("sentinel /data/media/.fukku-sentinel is missing") }
		}, state: store.Failed, code: CodeMergeFailed},
		{name: "film replaced before the merge", before: func(x *mergeEnv, j store.Job) { x.movies.Library[0].File.ID = 13 },
			state: store.Probed},
		{name: "rejected proposal", after: func(x *mergeEnv, j store.Job) {
			x.movies.Verdicts = map[string][]arr.Rejection{x.propose(j, 1): {{Reason: "Sample", Permanent: true}}}
		}, state: store.Failed, code: CodeMergeRejected, kept: true},
		{name: "not in the proposal", after: func(x *mergeEnv, j store.Job) { delete(x.movies.Proposed, x.mergedFolder(j, 1)) },
			state: store.Failed, code: CodeMergeRejected, kept: true},
		{name: "proposal refused", after: func(x *mergeEnv, j store.Job) {
			x.movies.Err = map[string]error{"Proposal": errors.New("radarr: 400 bad request")}
		}, state: store.Failed, code: CodeMergeFailed, kept: true},
		{name: "sentinel missing before the import", after: func(x *mergeEnv, j store.Job) {
			x.eng.checkStorage = func() error { return errors.New("sentinel /data/media/.fukku-sentinel is missing") }
		}, state: store.Failed, code: CodeImportFailed, kept: true},
		{name: "film replaced before the import", after: func(x *mergeEnv, j store.Job) { x.movies.Library[0].File.ID = 13 },
			state: store.Probed},
		{name: "merged file gone before the import", after: func(x *mergeEnv, j store.Job) {
			if err := os.Remove(x.src(j, "merged/1/Some Film (2001) WEBDL-1080p.mkv")); err != nil {
				x.t.Fatal(err)
			}
		}, state: store.Failed, code: CodeMergeFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := newMergeEnv(t)
			j := x.confirmMerge(x.readyJob(), nil)
			x.propose(j, 1)
			if c.before != nil {
				c.before(x, j)
			}
			applied := false
			for range 30 {
				j = x.jobNow(j)
				if j.State != store.Importing && j.State != store.Stalled {
					break
				}
				if c.after != nil && !applied {
					if ch, ok := mergeOf(x.tgt(j)); ok && ch.Output != nil {
						c.after(x, j)
						applied = true
					}
				}
				if _, err := x.step(x.eng, j); err != nil {
					t.Fatalf("step: %v", err)
				}
				if c.fail != "" {
					for _, task := range x.q.Tasks() {
						if task.Kind == spool.KindMerge {
							x.leave(j, "merged/1/half.mkv") // what a task that dies may leave
							x.q.Fail(task.ID, c.fail, "boom")
						}
					}
				} else {
					x.worker(c.out)
				}
				x.appWorks()
			}
			if c.after != nil && !applied {
				t.Fatal("the merged file never came in")
			}
			if j = x.jobNow(j); j.State != c.state || j.ErrorCode != c.code {
				t.Fatalf("job %s %s (%s), want %s %s", j.State, j.ErrorCode, j.ErrorText, c.state, c.code)
			}
			x.untouched()
			if c.state == store.Failed {
				iv := x.importView(j)
				if c.refuse != "" && (iv.Merge == nil || iv.Merge.State != MergeFailed || iv.Merge.Refusal != c.refuse) {
					t.Errorf("merge view %+v", iv.Merge)
				}
			}
			if c.state == store.AwaitingConfirmation {
				v := ViewFlow2(x.cfg, x.tgt(j), nil)
				if v == nil || v.MergeRefusal == nil || v.MergeRefusal.Code != c.refuse {
					t.Errorf("flow 2 view %+v", v)
				}
				if pv, ok := PreviewOf(x.tgt(j)); !ok || pv.State != PreviewReady || IsMerge(x.tgt(j)) {
					t.Errorf("preview %+v, merge choice kept %t", pv, IsMerge(x.tgt(j)))
				}
			}
			if c.state == store.Probed {
				if _, err := x.st.Target(x.ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
					t.Errorf("target after going back: %v", err)
				}
			}
			if tasks := x.q.Tasks(); len(tasks) != 0 {
				t.Errorf("tasks left %+v", tasks)
			}
			// No copy of the film is left behind, but the one a retry imports.
			left, rows := x.mergedOnDisk(j), x.mergedRows(j)
			switch {
			case c.kept && (len(left) != 1 || len(rows) != 1):
				t.Errorf("kept for the retry: on disk %v, rows %+v", left, rows)
			case !c.kept && len(left) != 0:
				t.Errorf("left behind on disk: %v", left)
			case c.state == store.Probed && len(rows) != 0:
				t.Errorf("merged file rows after going back: %+v", rows)
			}
		})
	}
}

// leave writes a file into the worker's directory of the job, as a merge task may leave one.
func (x *mergeEnv) leave(j store.Job, rel string) {
	x.t.Helper()
	p := filepath.Join(x.root, "worker", j.ID, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		x.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("half"), 0o644); err != nil {
		x.t.Fatal(err)
	}
}

// mergedOnDisk lists the files under the job's merged/, and under the worker's.
func (x *mergeEnv) mergedOnDisk(j store.Job) []string {
	x.t.Helper()
	var out []string
	for _, dir := range []string{x.src(j, "merged"), filepath.Join(x.root, "worker", j.ID, "merged")} {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

// mergedRows are the job's files under merged/.
func (x *mergeEnv) mergedRows(j store.Job) []store.File {
	x.t.Helper()
	files, err := x.st.Files(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	var out []store.File
	for _, f := range files {
		if strings.HasPrefix(f.Path, "merged/") {
			out = append(out, f)
		}
	}
	return out
}

// After a refused merge the user retries: the next attempt merges with its own task and
// into its own directory, and the job ends done with one import.
func TestMergeRetryAfterARefusal(t *testing.T) {
	x := newMergeEnv(t)
	j := x.confirmMerge(x.readyJob(), nil)
	refuse := func(o *merge.Output) { *o = merge.Output{Refusal: merge.RefusalFreeSpace, Detail: "1 GiB free"} }
	if j = x.run(x.eng, j, refuse); j.State != store.Failed || j.ErrorCode != CodeMergeFailed {
		t.Fatalf("job %s %s", j.State, j.ErrorCode)
	}
	var err error
	if j, err = x.st.Transition(x.ctx, j.ID, store.Failed, j.Resume, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	if v := x.importView(j); v.Merge.State != MergePending || v.Merge.Refusal != merge.RefusalFreeSpace {
		t.Errorf("merge view before the retry's first pass %+v", v.Merge)
	}
	x.pass(j)
	tasks := x.q.Tasks()
	if len(tasks) != 1 || tasks[0].ID != runner.MergeID(j.ID, 2) {
		t.Fatalf("tasks %+v", tasks)
	}
	if v := x.importView(j); v.Merge.State != MergeRunning || v.Merge.Refusal != "" {
		t.Errorf("merge view of the retry %+v", v.Merge)
	}
	p := x.propose(j, 2)
	if j = x.run(x.eng, j, nil); j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if len(x.movies.Imports) != 1 || x.movies.Imports[0].Rows[0].Path != p {
		t.Fatalf("imports %+v", x.movies.Imports)
	}
	x.noMergeLeftovers(j)
}

// A merge that went back to probed before its import, matched and confirmed again, merges
// again in the same attempt and imports once.
func TestMergeAgainAfterGoingBack(t *testing.T) {
	x := newMergeEnv(t)
	j := x.confirmMerge(x.readyJob(), nil)
	x.propose(j, 1)
	for i := 0; i < 5 && x.choice(j).Output == nil; i++ { // merged
		x.pass(j)
		x.worker(nil)
	}
	x.movies.Library[0].File.ID = 13 // Radarr took another file: back to probed, before the import
	x.pass(j)
	if j = x.jobNow(j); j.State != store.Probed {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	// Matched again to the film's new file, which is the same size.
	x.movies.Library[0].File = libFile()
	x.movies.Library[0].File.ID = 13
	if err := x.st.SetTarget(x.ctx, j.ID, store.Target{App: AppRadarr, TitleID: filmID, Title: "Some Film", Year: 2001, MatchedBy: "user1"}); err != nil {
		t.Fatal(err)
	}
	nf := libFile()
	nf.ID = 13
	tg := x.tgt(j)
	tg.Options, _ = withOption(tg.Options, "flow2", newFlow2(nf, x.cfg))
	if err := x.st.SetTarget(x.ctx, j.ID, tg); err != nil {
		t.Fatal(err)
	}
	if _, err := x.st.Transition(x.ctx, j.ID, store.Probed, store.Matched, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	x.matched(j)
	task := x.q.Tasks()[0]
	x.q.Complete(task.ID, mustLibraryProbe(x, nf))
	if j = x.matched(j); j.State != store.AwaitingConfirmation {
		t.Fatalf("job %s", j.State)
	}
	x.preview(j, -800)
	j = x.confirmMerge(j, &MergeRequest{AudioTracks: []int{2}})
	if j = x.run(x.eng, j, nil); j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if x.merges != 2 || len(x.movies.Imports) != 1 {
		t.Fatalf("%d merges, %d imports", x.merges, len(x.movies.Imports))
	}
	files, _ := x.st.Files(x.ctx, j.ID)
	n := 0
	for _, f := range files {
		if f.Path == "merged/1/Some Film (2001) WEBDL-1080p.mkv" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d file rows for the merged file", n)
	}
}

func mustLibraryProbe(x *mergeEnv, f *arr.MovieFile) media.LibraryProbeOutput {
	rec := newFlow2(f, x.cfg)
	return media.LibraryProbeOutput{Path: rec.Library.Path, OK: true, FileID: f.ID, TrackedSize: f.Size, Media: libProbe(filmMinutes)}
}
