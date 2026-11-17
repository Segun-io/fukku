package flows

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/merge"
	"github.com/segun-io/fukku/internal/preview"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/spool/spooltest"
	"github.com/segun-io/fukku/internal/store"
)

// mergeEnv is ienv for flow 2a: the job's directory is on disk, the spool is a fake with a
// fake worker (worker), and Radarr's film has its tracked file (libFile).
type mergeEnv struct {
	*ienv
	q *spooltest.Queue
	// merges counts the merge tasks the fake worker ran.
	merges int
}

// mergedSize is the size of the merged file the fake worker writes; an output whose Detail
// is noFile makes it write none, linkFile a symbolic link to a file of that size outside
// the job instead, and linkDir its folder as a link to a folder holding such a file (what a
// compromised worker may leave, #113).
const (
	mergedSize = 5000
	noFile     = "no file"
	linkFile   = "link as the file"
	linkDir    = "link as the folder"
)

var latino = arr.Language{ID: 37, Name: "Spanish (Latino)"}

func newMergeEnv(t *testing.T) *mergeEnv {
	t.Helper()
	return newMergeEnvWith(t, nil)
}

func newMergeEnvWith(t *testing.T, tweak func(*config.Config)) *mergeEnv {
	t.Helper()
	x := &mergeEnv{ienv: newImportEnv(t), q: &spooltest.Queue{}}
	x.cfg.Paths.LibraryRoots = []string{libRoot}
	x.cfg.Paths.Mappings.Radarr = append(x.cfg.Paths.Mappings.Radarr, config.PathMapping{From: libRoot, To: appLibRoot})
	if tweak != nil {
		tweak(&x.cfg)
	}
	x.movies.Library = []arr.Movie{{ID: filmID, Title: "Some Film", Year: 2001, OriginalLanguage: "English",
		Monitored: true, HasFile: true, Path: appLibRoot + "/Some Film (2001)", File: libFile()}}
	x.movies.Langs = append(slices.Clone(langs), latino)
	x.eng = x.process(x.st, x.movies, x.q)
	x.p = x.eng.p
	return x
}

// process is a new process on st, the film app and the spool given.
func (x *mergeEnv) process(st store.Store, movies arr.MovieManager, q spool.Queue) *Engine {
	p := NewProposals(x.cfg, nil, st, movies, x.series, nil)
	p.now = x.clock.now
	p.SetQueue(q)
	p.SetWorkerDown(func() (string, bool) { return "", false })
	eng := NewEngine(p)
	eng.checkStorage = func() error { return nil }
	if x.eng != nil {
		for _, s := range x.eng.steps {
			eng.AddPostStep(s)
		}
	}
	return eng
}

// readyJob is a flow 2 job awaiting confirmation whose upload is the same cut as the library
// file, with an English and a Spanish (default) audio track and a Spanish subtitle track,
// and a ready preview of the Spanish track at -800 ms.
func (x *mergeEnv) readyJob() store.Job {
	x.t.Helper()
	j := x.job(AppRadarr, filmID, vid("Some Film (2001).es.mkv", filmMinutes+200))
	x.onDisk(j)
	t, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	if t.Options, err = withOption(t.Options, "flow2", newFlow2(libFile(), x.cfg)); err != nil {
		x.t.Fatal(err)
	}
	if err := x.st.SetTarget(x.ctx, j.ID, t); err != nil {
		x.t.Fatal(err)
	}
	rec := x.flow2Record(j)
	x.matched(j)
	tasks := x.q.Tasks()
	if len(tasks) != 1 || tasks[0].Kind != spool.KindLibraryProbe {
		x.t.Fatalf("tasks %+v", tasks)
	}
	x.q.Complete(tasks[0].ID, media.LibraryProbeOutput{Path: rec.Library.Path, OK: true, FileID: 12, TrackedSize: 4000, Media: libProbe(filmMinutes)})
	if j = x.matched(j); j.State != store.AwaitingConfirmation {
		x.t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	files, _ := x.st.Files(x.ctx, j.ID)
	f, pr, _ := Flow2Upload(files)
	pr.Audio = []media.AudioTrack{{Index: 1, Codec: "aac", Language: "eng"}, {Index: 2, Codec: "aac", Language: "spa", Default: true}}
	pr.Subtitles = []media.SubtitleTrack{{Index: 3, Codec: "subrip", Language: "spa"}}
	f.Report, _ = json.Marshal(pr)
	if err := x.st.UpdateFile(x.ctx, f); err != nil {
		x.t.Fatal(err)
	}
	x.preview(j, -800)
	return x.jobNow(j)
}

// preview requests the clips at the offset and has the worker render them.
func (x *mergeEnv) preview(j store.Job, ms int64) Preview {
	x.t.Helper()
	pv, _, err := x.p.RequestPreview(x.ctx, "user1", x.jobNow(j), offset(ms))
	if err != nil {
		x.t.Fatal(err)
	}
	id := runner.PreviewID(j.ID, previewSlot(pv.Seq))
	for _, task := range x.q.Tasks() {
		if task.ID == id {
			var a preview.Args
			_ = json.Unmarshal(task.Args, &a)
			x.q.Complete(id, rendered(a))
		}
	}
	if err := x.p.Previews(x.ctx, x.jobNow(j)); err != nil {
		x.t.Fatal(err)
	}
	got, _ := PreviewOf(x.tgt(j))
	if got.State != PreviewReady {
		x.t.Fatalf("preview %+v", got)
	}
	return got
}

func (x *mergeEnv) tgt(j store.Job) store.Target {
	x.t.Helper()
	t, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	return t
}

func (x *mergeEnv) flow2Record(j store.Job) Flow2 {
	x.t.Helper()
	rec, ok := Flow2Of(x.tgt(j))
	if !ok {
		x.t.Fatal("not flow 2")
	}
	return rec
}

func (x *mergeEnv) choice(j store.Job) mergeChoice {
	x.t.Helper()
	c, ok := mergeOf(x.tgt(j))
	if !ok {
		x.t.Fatal("no merge choice")
	}
	return c
}

func mergeReq(m *MergeRequest) ConfirmRequest { return ConfirmRequest{Flow2: OfferMerge, Merge: m} }

func (x *mergeEnv) confirmMerge(j store.Job, m *MergeRequest) store.Job {
	x.t.Helper()
	c, err := x.eng.Confirm(x.ctx, "user1", "user1@example.com", true, x.jobNow(j), mergeReq(m))
	if err != nil {
		x.t.Fatalf("confirm: %v", err)
	}
	if c.Job.State != store.Importing {
		x.t.Fatalf("confirmed job is %s", c.Job.State)
	}
	return c.Job
}

// mergedFolder is the job's merged/<attempt> as Radarr sees it; propose puts the merged
// file into Radarr's proposal of it, as Radarr parses it.
func (x *mergeEnv) mergedFolder(j store.Job, attempt int) string {
	return appIntake + "/jobs/" + j.ID + "/" + runner.MergeDest(attempt)
}

func (x *mergeEnv) propose(j store.Job, attempt int) string {
	folder := x.mergedFolder(j, attempt)
	p := folder + "/Some Film (2001) WEBDL-1080p.mkv"
	raw, _ := json.Marshal(map[string]any{"path": p, "size": mergedSize, "quality": map[string]any{"quality": map[string]any{"id": 3, "name": "WEBDL-1080p"}, "revision": map[string]any{"version": 1}}})
	x.movies.Proposed[folder] = []arr.Row{{Path: p, Size: mergedSize, Quality: webdl1080, Languages: []arr.Language{english}, Raw: raw}}
	return p
}

// worker runs the waiting merge task as the worker does: the merged file into the task's
// dest, and a verified output. change edits the output first (a refusal writes no file).
func (x *mergeEnv) worker(change func(*merge.Output)) bool {
	x.t.Helper()
	for _, task := range x.q.Tasks() {
		if task.Kind != spool.KindMerge {
			continue
		}
		var a merge.Args
		if err := json.Unmarshal(task.Args, &a); err != nil {
			x.t.Fatal(err)
		}
		out := merge.Output{
			OK: true, Path: a.Dest + "/" + merge.OutputName(a.Library), Size: mergedSize,
			Media:       &media.Probe{Container: "matroska,webm", DurationMs: filmMinutes, Size: mergedSize},
			Library:     &merge.LibraryFile{Path: a.Library, Size: 4000},
			LanguageTag: a.LanguageTag, TrackName: a.TrackName, KeepOriginalDefault: a.KeepOriginalDefault != nil && *a.KeepOriginalDefault,
		}
		if change != nil {
			change(&out)
		}
		if out.OK && out.Path != "" && out.Detail != noFile {
			// Into the worker's directory of the job: the web process moves it into the job.
			p := filepath.Join(x.root, "worker", task.Job, filepath.FromSlash(out.Path))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				x.t.Fatal(err)
			}
			switch out.Detail {
			case linkFile, linkDir:
				outside := filepath.Join(x.t.TempDir(), filepath.Base(p))
				if err := os.WriteFile(outside, make([]byte, out.Size), 0o644); err != nil {
					x.t.Fatal(err)
				}
				link, target := p, outside
				if out.Detail == linkDir {
					link, target = filepath.Dir(p), filepath.Dir(outside)
					if err := os.Remove(link); err != nil {
						x.t.Fatal(err)
					}
				}
				if err := os.Symlink(target, link); err != nil {
					x.t.Fatal(err)
				}
				out.Detail = ""
			default:
				if err := os.WriteFile(p, make([]byte, out.Size), 0o644); err != nil {
					x.t.Fatal(err)
				}
			}
		}
		x.q.Complete(task.ID, out)
		x.merges++
		return true
	}
	return false
}

// pass runs the job's step once with the env's engine.
func (x *mergeEnv) pass(j store.Job) {
	x.t.Helper()
	if _, err := x.step(x.eng, j); err != nil {
		x.t.Fatalf("step: %v", err)
	}
}

// run drives the job with eng until it is in a state without a step, the worker merging
// and the app importing between passes.
func (x *mergeEnv) run(eng *Engine, j store.Job, change func(*merge.Output)) store.Job {
	x.t.Helper()
	for range 50 {
		j = x.jobNow(j)
		if !slices.Contains([]store.State{store.Importing, store.Imported, store.PostProcessing, store.Stalled}, j.State) {
			return j
		}
		if _, err := x.step(eng, j); err != nil {
			x.t.Fatalf("%s step: %v", j.State, err)
		}
		x.worker(change)
		x.appWorks()
		x.clock.advance(time.Hour) // past any backoff
	}
	x.t.Fatalf("job still %s", x.jobNow(j).State)
	return j
}

// untouched checks that the fake library is as it was: the film's tracked file, no import,
// still monitored.
func (x *mergeEnv) untouched() {
	x.t.Helper()
	m := x.movies.Library[0]
	if m.File == nil || m.File.Size == mergedSize || !m.Monitored || len(x.movies.Imports) != 0 {
		x.t.Errorf("the library changed: file %+v, monitored %t, %d import(s)", m.File, m.Monitored, len(x.movies.Imports))
	}
}

// The whole merge with the fakes: the confirmation with the defaults, the worker's task,
// the merged file's row, the import with the existing quality and both languages, and the
// film unmonitored with the warning.
func TestMergeEndToEnd(t *testing.T) {
	x := newMergeEnv(t)
	j := x.readyJob()
	pv, _ := PreviewOf(x.tgt(j))
	// A preview task still waiting in another slot stops at the confirmation.
	stale := spool.Task{ID: runner.PreviewID(j.ID, 5), Kind: spool.KindPreview, Job: j.ID}
	if err := x.q.Submit(x.ctx, stale); err != nil {
		t.Fatal(err)
	}
	j = x.confirmMerge(j, nil)
	if tasks := x.q.Tasks(); len(tasks) != 0 {
		t.Errorf("tasks after the confirmation: %+v", tasks)
	}
	c := x.choice(j)
	if c.Upload != "files/Some Film (2001).es.mkv" || !slices.Equal(c.AudioTracks, []int{2}) || len(c.SubtitleTracks) != 0 ||
		c.OffsetMs != -800 || c.LanguageTag != "es-419" || c.TrackName != "Español (Latino)" || !c.KeepOriginalDefault ||
		c.ArrLanguage == nil || *c.ArrLanguage != (Ref{ID: 37, Name: "Spanish (Latino)"}) || c.Preview != pv.Request ||
		c.LibraryFileID != 12 || c.LibrarySize != 4000 || c.Attempt != 0 || c.Output != nil {
		t.Fatalf("choice %+v", c)
	}
	audit, _ := x.st.AuditLog(x.ctx, j.ID)
	last := audit[len(audit)-1]
	if last.Action != "merge.confirmed" || last.Email != "user1@example.com" || last.Detail["audioTracks"] != "2" ||
		last.Detail["offsetMs"] != "-800" || last.Detail["languageTag"] != "es-419" || last.Detail["arrLanguage"] != "Spanish (Latino)" ||
		last.Detail["libraryFileId"] != "12" || last.Detail["preview"] != pv.Request {
		t.Errorf("audit %+v", last)
	}

	// First pass: the task, nothing asked of Radarr but the tracked file.
	if j, _ = x.step(x.eng, j); j.State != store.Importing {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	tasks := x.q.Tasks()
	if len(tasks) != 1 || tasks[0].ID != runner.MergeID(j.ID, 1) || tasks[0].Kind != spool.KindMerge || tasks[0].Job != j.ID {
		t.Fatalf("tasks %+v", tasks)
	}
	var a merge.Args
	if err := json.Unmarshal(tasks[0].Args, &a); err != nil {
		t.Fatal(err)
	}
	if a.Library != libRoot+"/Some Film (2001)/Some Film (2001) WEBDL-1080p.mkv" || a.Upload != "files/Some Film (2001).es.mkv" ||
		!slices.Equal(a.Tracks, []int{2}) || a.OffsetMs != -800 || a.LanguageTag != "es-419" || a.TrackName != "Español (Latino)" ||
		a.KeepOriginalDefault == nil || !*a.KeepOriginalDefault || a.Dest != "merged/1" {
		t.Errorf("args %+v", a)
	}
	if v := x.importView(j); v.Merge == nil || v.Merge.State != MergeRunning || v.Status != ImportPending {
		t.Errorf("import view while merging %+v", v)
	}
	x.untouched()

	appFile := x.propose(j, 1)
	j = x.run(x.eng, j, nil)
	if j.State != store.Done {
		t.Fatalf("job %s (%s %s)", j.State, j.ErrorCode, j.ErrorText)
	}
	if x.merges != 1 || len(x.movies.Imports) != 1 {
		t.Fatalf("%d merge(s), %d import(s)", x.merges, len(x.movies.Imports))
	}
	call := x.movies.Imports[0]
	if len(call.Rows) != 1 || call.Mode != "move" {
		t.Fatalf("import %+v", call)
	}
	r := call.Rows[0]
	if r.Path != appFile || r.MovieID != filmID || r.Quality != webdl1080 || !slices.Equal(r.Languages, []arr.Language{english, latino}) {
		t.Errorf("imported row %+v", r)
	}
	if got := x.movies.Library[0]; got.Monitored || got.File == nil || got.File.Size != mergedSize || got.File.ID == 12 {
		t.Errorf("film after the merge %+v (file %+v)", got, got.File)
	}
	// The upload stays in the job directory; Radarr moved the merged file away.
	if _, err := os.Stat(x.src(j, "files/Some Film (2001).es.mkv")); err != nil {
		t.Errorf("the upload: %v", err)
	}
	if _, err := os.Stat(x.src(j, "merged/1/Some Film (2001) WEBDL-1080p.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the merged file after a move: %v", err)
	}
	iv, rv, err := x.eng.Detail(x.ctx, j, false)
	if err != nil {
		t.Fatal(err)
	}
	if iv.Status != ImportDone || len(iv.Rows) != 1 || iv.Rows[0].Name != "Some Film (2001) WEBDL-1080p.mkv" || iv.Rows[0].Result != store.ResultImported ||
		iv.Merge == nil || iv.Merge.State != MergeDone || iv.Merge.Name != "Some Film (2001) WEBDL-1080p.mkv" || iv.Merge.Size != mergedSize {
		t.Errorf("import view %+v (merge %+v)", iv, iv.Merge)
	}
	if rv.Monitored == nil || *rv.Monitored || rv.UnmonitorReason != UnmonitorMerged || rv.Merge == nil ||
		len(rv.Merge.Warnings) != 1 || rv.Merge.Warnings[0].Code != WarnSearchCanReplace || !strings.Contains(rv.Merge.Warnings[0].Message, "own Search") {
		t.Errorf("result %+v (merge %+v)", rv, rv.Merge)
	}
	x.noMergeLeftovers(j)
}

func (x *mergeEnv) importView(j store.Job) *ImportView {
	x.t.Helper()
	iv, _, err := x.eng.Detail(x.ctx, x.jobNow(j), true)
	if err != nil {
		x.t.Fatal(err)
	}
	return iv
}

// noMergeLeftovers checks that nothing of the job is left in the spool.
func (x *mergeEnv) noMergeLeftovers(j store.Job) {
	x.t.Helper()
	if tasks := x.q.Tasks(); len(tasks) != 0 {
		x.t.Errorf("tasks left: %+v", tasks)
	}
	ids := []string{}
	for a := 1; a <= j.Attempt; a++ {
		ids = append(ids, runner.MergeID(j.ID, a), runner.LibraryProbeID(j.ID, a))
	}
	for slot := 1; slot <= runner.PreviewSlots; slot++ {
		ids = append(ids, runner.PreviewID(j.ID, slot))
	}
	for _, id := range ids {
		if _, ok, _ := x.q.Result(x.ctx, id); ok {
			x.t.Errorf("result %s left", id)
		}
	}
}

// Radarr's language for a merge's tag: its own name for a regional variant it lists, else
// the base language's English name; none when Radarr lists no such language.
func TestArrLanguageFor(t *testing.T) {
	radarr := []arr.Language{{ID: -2, Name: "Original"}, {ID: 0, Name: "Unknown"}, english, spanish, latino,
		{ID: 18, Name: "Portuguese"}, {ID: 15, Name: "Norwegian"}, {ID: 23, Name: "Hebrew"}}
	for tag, want := range map[string]*Ref{
		"es-419": {ID: 37, Name: "Spanish (Latino)"}, "es": {ID: 3, Name: "Spanish"}, "es-ES": {ID: 3, Name: "Spanish"},
		"es-MX": {ID: 37, Name: "Spanish (Latino)"}, "es-AR": {ID: 37, Name: "Spanish (Latino)"}, "es-US": {ID: 3, Name: "Spanish"},
		"pt-PT": {ID: 18, Name: "Portuguese"},
		"pt-BR": {ID: 18, Name: "Portuguese"}, "nb": {ID: 15, Name: "Norwegian"}, "he": {ID: 23, Name: "Hebrew"},
		"en": {ID: 1, Name: "English"}, "eu": nil, "und": nil, "": nil,
	} {
		got := arrLanguageFor(tag, radarr)
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("%q: %+v, want %+v", tag, got, want)
		}
	}
	if got := mergedLanguages([]Ref{{ID: 0, Name: "Unknown"}}, &Ref{ID: 3, Name: "Spanish"}); !slices.Equal(got, []arr.Language{spanish}) {
		t.Errorf("unknown and an added language: %+v", got)
	}
	if got := mergedLanguages([]Ref{{ID: 1, Name: "English"}, {ID: 3, Name: "Spanish"}}, &Ref{ID: 3, Name: "Spanish"}); !slices.Equal(got, []arr.Language{english, spanish}) {
		t.Errorf("an added language the file has: %+v", got)
	}
	if got := mergedLanguages([]Ref{{ID: 0, Name: "Unknown"}}, nil); len(got) != 1 || got[0].ID != 0 {
		t.Errorf("no added language: %+v", got)
	}
}
