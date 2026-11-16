package runner

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/extract"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/worker"
	"github.com/segun-io/fukku/test/fixtures"
)

// stack is the web side and the worker in one process: the runner and the real worker
// with its real handlers, talking through a real spool in a temporary directory.
//
// The store is the real SQLite store, in the sandbox.
type stack struct {
	t       *testing.T
	ctx     context.Context
	cfg     config.Config
	sandbox string // holds the intake root and the spool, and nothing else
	st      store.Store
	q       *spool.Dir
	logs    *syncBuffer
}

func newStack(t *testing.T, handlers map[string]worker.Handler, tweak ...func(*config.Config)) *stack {
	t.Helper()
	sandbox := t.TempDir()
	cfg := config.Defaults()
	cfg.Paths.IntakeRoot = filepath.Join(sandbox, "intake")
	cfg.Worker.SpoolDir = filepath.Join(sandbox, "spool")
	cfg.Worker.PollInterval = config.Duration(10 * time.Millisecond)
	cfg.Worker.Concurrency = 2
	// The default margin (100 GiB) is more than a CI runner has free: every extraction
	// would be refused free_space.
	cfg.Upload.FreeSpaceMargin = 0
	for _, fn := range tweak {
		fn(&cfg)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Paths.IntakeRoot, jobdir.Jobs), 0o750); err != nil {
		t.Fatal(err)
	}
	q, err := spool.Open(cfg.Worker.SpoolDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	if handlers == nil {
		handlers = worker.Handlers(cfg)
	}
	st, err := store.Open(t.Context(), store.Options{Dir: filepath.Join(sandbox, "data")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := &stack{t: t, cfg: cfg, sandbox: sandbox, st: st, q: q, logs: &syncBuffer{}}
	log := slog.New(slog.NewTextHandler(s.logs, nil))

	ctx, cancel := context.WithCancel(t.Context())
	s.ctx = ctx
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := worker.New(cfg, log.With("process", "worker"), handlers).Run(ctx); err != nil {
			t.Errorf("worker: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		wg.Wait()
		if t.Failed() {
			t.Logf("log:\n%s", s.logs)
		}
	})
	return s
}

// runner starts a runner on the stack.
func (s *stack) runner() {
	ctx, cancel := context.WithCancel(s.ctx)
	r := New(s.cfg, slog.New(slog.NewTextHandler(s.logs, nil)).With("process", "web"), s.st, s.q, time.Now)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Run(ctx)
	}()
	s.t.Cleanup(func() { cancel(); <-done })
}

// newJob creates a job, lets build write its uploads into the job directory, records them
// as the upload API does and submits the job.
func (s *stack) newJob(build func(dir string) []upload) store.Job {
	s.t.Helper()
	j, err := s.st.CreateJob(s.ctx, "user1")
	if err != nil {
		s.t.Fatal(err)
	}
	dir := jobdir.Dir(s.cfg.Paths.IntakeRoot, j.ID)
	for _, sub := range []string{jobdir.Files, jobdir.Archives} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o750); err != nil {
			s.t.Fatal(err)
		}
	}
	for _, u := range build(dir) {
		st, err := os.Stat(filepath.Join(dir, u.path))
		if err != nil {
			s.t.Fatal(err)
		}
		if _, err := s.st.AddFile(s.ctx, store.File{JobID: j.ID, Path: u.path, Kind: u.kind, Size: st.Size(), Status: store.FilePending}); err != nil {
			s.t.Fatal(err)
		}
	}
	if j, err = s.st.Transition(s.ctx, j.ID, store.Uploading, store.Uploaded, store.Change{Actor: "user1"}); err != nil {
		s.t.Fatal(err)
	}
	return j
}

// wait waits until the job is in one of the states.
func (s *stack) wait(id string, states ...store.State) store.Job {
	s.t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		j, err := s.st.Job(s.ctx, id)
		if err != nil {
			s.t.Fatal(err)
		}
		if slices.Contains(states, j.State) {
			return j
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("job %s is %s (%s %s), want %v", id, j.State, j.ErrorCode, j.ErrorText, states)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *stack) files(id string) map[string]store.File {
	s.t.Helper()
	fs, err := s.st.Files(s.ctx, id)
	if err != nil {
		s.t.Fatal(err)
	}
	out := map[string]store.File{}
	for _, f := range fs {
		out[f.Path] = f
	}
	return out
}

// spoolEmpty waits until no task, claim, cancel marker or result is left in the spool.
func (s *stack) spoolEmpty() {
	s.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var left []string
		for _, d := range []string{spool.TasksDir, spool.RunningDir, spool.ResultsDir, spool.CancelDir} {
			entries, err := os.ReadDir(filepath.Join(s.cfg.Worker.SpoolDir, d))
			if err != nil {
				s.t.Fatal(err)
			}
			for _, e := range entries {
				left = append(left, d+"/"+e.Name())
			}
		}
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("left in the spool: %v", left)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// tree lists everything under root except the job directories named in skip, with the
// type of each entry.
func tree(t *testing.T, root string, skip ...string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() && slices.Contains(skip, d.Name()) {
			return filepath.SkipDir
		}
		out = append(out, rel+" "+d.Type().String())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func needTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed (run the tests with make test)", tool)
		}
	}
}

func TestIntegrationMedia(t *testing.T) {
	needTools(t)
	s := newStack(t, nil)
	s.runner()
	spec := fixtures.Video{Duration: 3 * time.Second, Audio: []fixtures.Audio{{Language: "eng"}}}

	film := s.newJob(func(dir string) []upload {
		if err := fixtures.Generate(s.ctx, filepath.Join(dir, "files/Film (2001).mkv"), spec); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "files/Film (2001).en.srt"), []byte(fixtures.SRT(3*time.Second, "eng")), 0o600); err != nil {
			t.Fatal(err)
		}
		return []upload{
			{path: "files/Film (2001).mkv", kind: store.KindVideo},
			{path: "files/Film (2001).en.srt", kind: store.KindSubtitle},
		}
	})
	season := s.newJob(func(dir string) []upload {
		zip, err := fixtures.SeasonZip(s.ctx, filepath.Join(dir, jobdir.Archives), "Show", 1, 2, spec)
		if err != nil {
			t.Fatal(err)
		}
		return []upload{{path: jobdir.Archives + "/" + filepath.Base(zip), kind: store.KindArchive}}
	})

	j := s.wait(film.ID, store.Probed, store.Failed, store.Stalled)
	if j.State != store.Probed || j.ExpiresAt.IsZero() {
		t.Fatalf("film: %+v", j)
	}
	files := s.files(film.ID)
	checkProbe(t, files["files/Film (2001).mkv"])
	if f := files["files/Film (2001).en.srt"]; f.Status != store.FileOK {
		t.Errorf("subtitle: %+v", f)
	}

	j = s.wait(season.ID, store.Probed, store.Failed, store.Stalled)
	if j.State != store.Probed {
		t.Fatalf("season: %+v", j)
	}
	files = s.files(season.ID)
	zip := files["archives/Show - Season 01.zip"]
	if zip.Status != store.FileOK {
		t.Fatalf("archive: %+v", zip)
	}
	dest := "files/Show - Season 01/Season 01/"
	for _, ep := range []string{"Show - S01E01.mkv", "Show - S01E02.mkv"} {
		f := files[dest+ep]
		checkProbe(t, f)
		if f.From != zip.Path {
			t.Errorf("%s from %q", f.Path, f.From)
		}
	}
	if f := files[dest+"Show - S01E01.en.srt"]; f.Status != store.FileOK || f.Kind != store.KindSubtitle {
		t.Errorf("season subtitle: %+v", f)
	}
	if len(files) != 4 {
		t.Errorf("season files: %v", files)
	}
	s.spoolEmpty()
}

func checkProbe(t *testing.T, f store.File) {
	t.Helper()
	if f.Status != store.FileOK || f.Kind != store.KindVideo {
		t.Errorf("%s: %+v", f.Path, f)
		return
	}
	var p media.Probe
	if err := json.Unmarshal(f.Report, &p); err != nil || p.DurationMs <= 0 || len(p.Video) == 0 || len(p.Audio) != 1 {
		t.Errorf("%s: probe %s (%v)", f.Path, f.Report, err)
	}
}

// Every hostile archive ends with the refusals of #3, exactly as extract.Run gives them,
// with nothing written outside the job directory.
func TestIntegrationHostileCorpus(t *testing.T) {
	needTools(t)
	s := newStack(t, nil)
	corpus := fixtures.HostileCorpus(fixtures.DefaultLimits)
	built := t.TempDir()
	reference := t.TempDir() // where extract.Run runs on its own, for the expected output

	jobs := map[string]store.Job{}
	for _, h := range corpus {
		src, err := h.Build(built)
		if err != nil {
			t.Fatal(err)
		}
		jobs[h.Name] = s.newJob(func(dir string) []upload {
			if err := os.Link(src, filepath.Join(dir, jobdir.Archives, h.Name)); err != nil {
				t.Fatal(err)
			}
			return []upload{{path: jobdir.Archives + "/" + h.Name, kind: store.KindArchive}}
		})
	}
	var ids []string
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	outside := tree(t, s.cfg.Paths.IntakeRoot, ids...)
	s.runner()

	expect := hostileExpect()
	for _, h := range corpus {
		if _, ok := expect[h.Name]; !ok {
			t.Errorf("no expectation for %s", h.Name)
		}
	}
	for _, h := range corpus {
		j := s.wait(jobs[h.Name].ID, store.Probed, store.Failed, store.Stalled)
		t.Run(h.Name, func(t *testing.T) {
			files := s.files(j.ID)
			// With the default extensions nothing in the corpus is a usable video.
			if j.State != store.Failed || j.ErrorCode != CodeNoUsableFiles {
				t.Fatalf("job: %+v\n%s", j, rows(files))
			}
			a := files[jobdir.Archives+"/"+h.Name]
			// The refusal of #3, as internal/extract/corpus_test.go expects it, and the
			// entry rule that must have skipped something.
			exp := expect[h.Name]
			if a.Reason != exp.refused {
				t.Errorf("archive refused %q, want %q\n%s", a.Reason, exp.refused, rows(files))
			}
			if exp.skipped != "" && !strings.Contains(string(a.Report), `"reason":"`+exp.skipped+`"`) {
				t.Errorf("no entry skipped as %s\n%s", exp.skipped, rows(files))
			}
			want := referenceOutput(t, reference, built, h.Name, s.cfg)
			var got extract.Output
			if err := json.Unmarshal(a.Report, &got); err != nil {
				t.Fatalf("archive report %s: %v", a.Report, err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("report\n got %s\nwant %s", gotJSON, wantJSON)
			}
			if want.Refused != "" {
				if a.Status != store.FileRefused || a.Reason != want.Refused {
					t.Errorf("archive %s (%q), want refused %q", a.Status, a.Reason, want.Refused)
				}
			} else if a.Status != store.FileOK {
				t.Errorf("archive %s (%q)", a.Status, a.Reason)
			}
			// Every extracted file has a row and was refused, or is a subtitle.
			for _, e := range want.Files {
				f, ok := files[e.Path]
				if !ok || f.From != a.Path {
					t.Errorf("no row for %s", e.Path)
				}
				if f.Kind == store.KindVideo && f.Status != store.FileRefused {
					t.Errorf("%s: %s", f.Path, f.Status)
				}
			}
			// Nothing in the job directory is a link or special file, and refused files are gone.
			dir := jobdir.Dir(s.cfg.Paths.IntakeRoot, j.ID)
			for _, e := range tree(t, dir) {
				if typ := e[strings.LastIndex(e, " ")+1:]; typ != "----------" && typ != "d---------" {
					t.Errorf("in the job directory: %s", e)
				}
			}
			for p, f := range files {
				if _, err := os.Lstat(filepath.Join(dir, p)); f.Status == store.FileRefused && err == nil {
					t.Errorf("refused %s is still on disk", p)
				}
			}
		})
	}
	if after := tree(t, s.cfg.Paths.IntakeRoot, ids...); !slices.Equal(after, outside) {
		t.Errorf("outside the job directories:\n got %v\nwant %v", after, outside)
	}
	if after := tree(t, s.sandbox, append(ids, "spool")...); slices.ContainsFunc(after, func(e string) bool {
		return strings.Contains(e, "evil")
	}) {
		t.Errorf("written outside: %v", after)
	}

	// renamed-non-video.zip: the text file named .mkv is refused by the probe and deleted.
	j := jobs["renamed-non-video.zip"]
	f := s.files(j.ID)["files/renamed-non-video/Show - S01E01.mkv"]
	if f.Status != store.FileRefused || f.Reason == "" {
		t.Errorf("renamed non-video: %+v", f)
	}
	s.spoolEmpty()
}

// The real extraction finds less free space than upload.freeSpaceMargin: the job stalls
// with free_space, and the archive is kept, pending, for when there is room.
func TestIntegrationNoRoom(t *testing.T) {
	s := newStack(t, nil, func(c *config.Config) { c.Upload.FreeSpaceMargin = 1 << 60 })
	s.runner()
	j := s.newJob(func(dir string) []upload {
		f, err := os.Create(filepath.Join(dir, "archives/s.zip"))
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		w, err := zw.Create("e1.mkv")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("video"))
		if err := errors.Join(zw.Close(), f.Close()); err != nil {
			t.Fatal(err)
		}
		return []upload{{path: "archives/s.zip", kind: store.KindArchive}}
	})
	j = s.wait(j.ID, store.Stalled, store.Failed, store.Probed)
	files := s.files(j.ID)
	if j.State != store.Stalled || j.ErrorCode != CodeFreeSpace {
		t.Fatalf("job: %+v\n%s", j, rows(files))
	}
	if a := files["archives/s.zip"]; a.Status != store.FilePending || len(files) != 1 {
		t.Errorf("files:\n%s", rows(files))
	}
	if _, err := os.Stat(filepath.Join(jobdir.Dir(s.cfg.Paths.IntakeRoot, j.ID), "archives/s.zip")); err != nil {
		t.Errorf("the archive is gone: %v", err)
	}
	s.spoolEmpty()
	time.Sleep(50 * time.Millisecond) // several passes: still no room, still stalled
	if j, _ = s.st.Job(s.ctx, j.ID); j.State != store.Stalled {
		t.Errorf("job: %+v", j)
	}
}

type hostileWant struct {
	refused string // the archive's refusal code; empty: unpacked
	skipped string // a reason at least one entry must be skipped for
}

// hostileExpect is what each hostile archive gives with the default settings (and no
// free-space margin), from the expectations of internal/extract/corpus_test.go: .txt is
// not an accepted extension here, so text entries are skipped as "extension".
func hostileExpect() map[string]hostileWant {
	return map[string]hostileWant{
		"traversal.zip":          {skipped: extract.ReasonTraversal},
		"absolute.zip":           {skipped: extract.ReasonAbsolute},
		"drive-letter.zip":       {skipped: extract.ReasonDriveLetter},
		"backslash.zip":          {skipped: extract.ReasonBackslash},
		"symlink.zip":            {skipped: extract.ReasonSymlink},
		"device.zip":             {skipped: extract.ReasonSpecial},
		"bomb.zip":               {refused: extract.RefusedRatio},
		"lying-size.zip":         {refused: extract.RefusedNotAZip},
		"too-many-entries.zip":   {refused: extract.RefusedTooManyEntries},
		"nested.zip":             {skipped: extract.ReasonNestedArchive},
		"renamed-non-video.zip":  {},
		"duplicate-names.zip":    {skipped: extract.ReasonDuplicate},
		"unicode-collision.zip":  {skipped: extract.ReasonDuplicate},
		"control-characters.zip": {},
		"long-name.zip":          {skipped: extract.ReasonNameTooLong},
		"deep-nesting.zip":       {skipped: extract.ReasonNameTooLong},
		"many-directories.zip":   {refused: extract.RefusedTooManyEntries},
	}
}

// rows prints a job's file rows, for failure messages.
func rows(files map[string]store.File) string {
	var b strings.Builder
	for _, p := range slices.Sorted(maps.Keys(files)) {
		f := files[p]
		fmt.Fprintf(&b, "  %s kind=%s status=%s reason=%q from=%q report=%s\n", f.Path, f.Kind, f.Status, f.Reason, f.From, f.Report)
	}
	return b.String()
}

// referenceOutput runs extract.Run on its own copy of the archive.
func referenceOutput(t *testing.T, base, built, name string, cfg config.Config) extract.Output {
	t.Helper()
	dir := filepath.Join(base, strings.TrimSuffix(name, ".zip"))
	if err := os.MkdirAll(filepath.Join(dir, jobdir.Archives), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, jobdir.Files), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(built, name), filepath.Join(dir, jobdir.Archives, name)); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	out, err := extract.Run(t.Context(), root, extract.Args{
		Archive: jobdir.Archives + "/" + name,
		Dest:    jobdir.Files + "/" + strings.TrimSuffix(name, ".zip"),
	}, extract.LimitsFrom(cfg))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A job cancelled while the worker runs its task: the task is stopped, its late result is
// acknowledged, and the directory and the unfinished upload are gone.
func TestIntegrationCancelMidTask(t *testing.T) {
	started := make(chan struct{}, 1)
	s := newStack(t, map[string]worker.Handler{
		spool.KindExtract: func(ctx context.Context, _ *os.Root, _ json.RawMessage) (any, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	j := s.newJob(func(dir string) []upload {
		if err := os.WriteFile(filepath.Join(dir, "archives/a.zip"), []byte("zip"), 0o600); err != nil {
			t.Fatal(err)
		}
		return []upload{{path: "archives/a.zip", kind: store.KindArchive}}
	})
	upload := strings.Repeat("cd", 16)
	if err := s.st.PutUpload(s.ctx, store.Upload{ID: upload, JobID: j.ID, Owner: j.Owner, Name: "b.mkv", Size: 9}); err != nil {
		t.Fatal(err)
	}
	inc := filepath.Join(s.cfg.Paths.IntakeRoot, jobdir.Incoming)
	if err := os.MkdirAll(inc, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inc, upload), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The runner's clock runs ahead on demand, so that its once-a-minute sweep comes now.
	var mu sync.Mutex
	var ahead time.Duration
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return time.Now().Add(ahead)
	}
	r := New(s.cfg, slog.New(slog.NewTextHandler(s.logs, nil)), s.st, s.q, now)
	for {
		r.pass(s.ctx)
		select {
		case <-started:
		case <-time.After(10 * time.Millisecond):
			continue
		}
		break
	}
	if _, err := s.st.Transition(s.ctx, j.ID, store.Probing, store.Cancelled, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	r.pass(s.ctx) // cancels the task, removes the upload and the directory

	if _, err := os.Lstat(jobdir.Dir(s.cfg.Paths.IntakeRoot, j.ID)); err == nil {
		t.Error("the job directory is still there")
	}
	if ups, _ := s.st.Uploads(s.ctx, "", j.ID); len(ups) != 0 {
		t.Errorf("uploads %v", ups)
	}
	if entries, _ := os.ReadDir(inc); len(entries) != 0 {
		t.Errorf("upload data %v", entries)
	}
	// The worker writes the result of the cancelled task; the sweep acknowledges it.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, ok, err := s.q.Result(s.ctx, extractID(j, 1, s.files(j.ID)["archives/a.zip"].ID)); err != nil {
			t.Fatal(err)
		} else if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no result for the cancelled task")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	ahead = sweepEvery
	mu.Unlock()
	r.pass(s.ctx)
	s.spoolEmpty()
	if !strings.Contains(s.logs.String(), "msg=task_discarded") || !strings.Contains(s.logs.String(), "code=cancelled") {
		t.Errorf("log:\n%s", s.logs)
	}
}
