package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/extract"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/spool/spooltest"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
	"github.com/segun-io/fukku/internal/worker"
)

var t0 = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// count returns how many log lines have the message msg.
func (b *syncBuffer) count(msg string) int {
	n := 0
	for _, line := range strings.Split(b.String(), "\n") {
		if slices.Contains(strings.Fields(line), "msg="+msg) {
			n++
		}
	}
	return n
}

// env is one runner with a fake store, a fake queue, a fake clock, a job directory under
// a temporary intake root, and a fake worker.
type env struct {
	t    *testing.T
	ctx  context.Context
	cfg  config.Config
	clk  *clock
	st   *storetest.Fake
	q    *spooltest.Queue
	logs *syncBuffer
	w    *fakeWorker

	// What the runner is built on; the crash test wraps the two.
	rst store.Store
	rq  spool.Queue
	r   *Runner

	rounds int   // for the outside world of a crash scenario
	free   int64 // what the runner measures as free space
}

func newEnv(t *testing.T) *env {
	t.Helper()
	cfg := config.Defaults()
	cfg.Paths.IntakeRoot = t.TempDir()
	cfg.Worker.SpoolDir = t.TempDir()
	clk := &clock{t: t0}
	e := &env{
		t: t, ctx: t.Context(), cfg: cfg, clk: clk,
		st: storetest.NewFake(clk.Now), q: &spooltest.Queue{Now: clk.Now},
		logs: &syncBuffer{}, w: &fakeWorker{}, free: math.MaxInt64,
	}
	e.w.e = e
	e.rst, e.rq = e.st, e.q
	e.beat(0)
	e.restart()
	return e
}

// restart replaces the runner by a new one on the same store and spool.
func (e *env) restart() {
	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.r = New(e.cfg, log, e.rst, e.rq, e.clk.Now)
	e.r.freeSpace = func(string) (int64, error) { return e.free, nil }
}

// beat writes the worker heartbeat as of age ago.
func (e *env) beat(age time.Duration) {
	e.t.Helper()
	ts := e.clk.Now().Add(-age).Unix()
	p := filepath.Join(e.cfg.Worker.SpoolDir, worker.HeartbeatFile)
	if err := os.WriteFile(p, []byte(strconv.FormatInt(ts, 10)+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func removeHeartbeat(e *env) error {
	return os.Remove(filepath.Join(e.cfg.Worker.SpoolDir, worker.HeartbeatFile))
}

// upload is a file of a new job: under files/ or archives/.
type upload struct {
	path    string
	kind    store.FileKind
	content string
}

func video(name string) upload { return upload{"files/" + name, store.KindVideo, "video " + name} }
func subtitle(name string) upload {
	return upload{"files/" + name, store.KindSubtitle, "1\n00:00:01,000 --> 00:00:02,000\nhi\n"}
}
func archive(name string) upload { return upload{"archives/" + name, store.KindArchive, "zip " + name} }

// newJob creates a job with the files on disk and in the store, as the upload API does,
// and submits it (uploading → uploaded).
func (e *env) newJob(files ...upload) store.Job {
	e.t.Helper()
	j, err := e.st.CreateJob(e.ctx, "user1")
	if err != nil {
		e.t.Fatal(err)
	}
	dir := jobdir.Dir(e.cfg.Paths.IntakeRoot, j.ID)
	for _, sub := range []string{jobdir.Files, jobdir.Archives} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o750); err != nil {
			e.t.Fatal(err)
		}
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.path), []byte(f.content), 0o600); err != nil {
			e.t.Fatal(err)
		}
		if _, err := e.st.AddFile(e.ctx, store.File{
			JobID: j.ID, Path: f.path, Kind: f.kind, Size: int64(len(f.content)), Status: store.FilePending,
		}); err != nil {
			e.t.Fatal(err)
		}
	}
	if j, err = e.st.Transition(e.ctx, j.ID, store.Uploading, store.Uploaded, store.Change{Actor: "user1"}); err != nil {
		e.t.Fatal(err)
	}
	return j
}

// addUnfinishedUpload gives the job an unfinished upload, in the store and on disk.
func (e *env) addUnfinishedUpload(job store.Job) string {
	e.t.Helper()
	id := strings.Repeat("ab", 16)
	if err := e.st.PutUpload(e.ctx, store.Upload{ID: id, JobID: job.ID, Owner: job.Owner, Name: "late.mkv", Size: 10}); err != nil {
		e.t.Fatal(err)
	}
	inc := filepath.Join(e.cfg.Paths.IntakeRoot, jobdir.Incoming)
	if err := os.MkdirAll(inc, 0o750); err != nil {
		e.t.Fatal(err)
	}
	for _, name := range []string{id, id + ".info"} {
		if err := os.WriteFile(filepath.Join(inc, name), []byte("x"), 0o600); err != nil {
			e.t.Fatal(err)
		}
	}
	return id
}

// run makes passes, letting the fake worker finish tasks after each, until the job is in
// one of the states and a further pass changes nothing. It fails the test after 50 rounds.
func (e *env) run(id string, states ...store.State) store.Job {
	e.t.Helper()
	prev := ""
	for range 50 {
		e.r.pass(e.ctx)
		e.w.work()
		s := e.snapshot(id)
		if slices.Contains(states, store.State(strings.Fields(s)[1])) && s == prev {
			return e.job(id)
		}
		prev = s
	}
	e.t.Fatalf("job %s did not settle in %v:\n%s", id, states, prev)
	return store.Job{}
}

func (e *env) job(id string) store.Job {
	e.t.Helper()
	j, err := e.st.Job(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return j
}

func (e *env) files(id string) []store.File {
	e.t.Helper()
	fs, err := e.st.Files(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return fs
}

func (e *env) file(id, p string) store.File {
	e.t.Helper()
	for _, f := range e.files(id) {
		if f.Path == p {
			return f
		}
	}
	e.t.Fatalf("no file %s in %v", p, e.files(id))
	return store.File{}
}

// onDisk lists the job directory: path → content, directories with a trailing slash.
func (e *env) onDisk(id string) map[string]string {
	e.t.Helper()
	out := map[string]string{}
	dir := filepath.Join(e.cfg.Paths.IntakeRoot, jobdir.Jobs, id)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			out[filepath.ToSlash(rel)+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// leftovers lists the job's task ids that are waiting or have a result, for attempts up to
// the job's.
func (e *env) leftovers(id string) []string {
	e.t.Helper()
	var out []string
	for _, t := range e.q.Tasks() {
		if t.Job == id {
			out = append(out, "task "+t.ID)
		}
	}
	j := e.job(id)
	for a := 1; a <= j.Attempt; a++ {
		ids := []string{probeID(j, a), LibraryProbeID(j.ID, a), MergeID(j.ID, a)}
		for _, f := range e.files(id) {
			if f.Kind == store.KindArchive {
				ids = append(ids, extractID(j, a, f.ID))
			}
		}
		for _, tid := range ids {
			if _, ok, err := e.q.Result(e.ctx, tid); err != nil {
				e.t.Fatal(err)
			} else if ok {
				out = append(out, "result "+tid)
			}
		}
	}
	return out
}

// snapshot describes everything the runner is responsible for, for comparisons. The second
// word is the state.
func (e *env) snapshot(id string) string {
	e.t.Helper()
	j := e.job(id)
	var b strings.Builder
	fmt.Fprintf(&b, "job %s attempt=%d resume=%q code=%q expires=%s\n", j.State, j.Attempt, j.Resume, j.ErrorCode, j.ExpiresAt.Format(time.RFC3339))
	for _, f := range e.files(id) {
		fmt.Fprintf(&b, "file %s kind=%s size=%d status=%s reason=%q from=%q report=%s\n", f.Path, f.Kind, f.Size, f.Status, f.Reason, f.From, f.Report)
	}
	disk := e.onDisk(id)
	keys := slices.Sorted(maps.Keys(disk))
	if disk == nil {
		b.WriteString("no job directory\n")
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "disk %s %q\n", k, disk[k])
	}
	ups, err := e.st.Uploads(e.ctx, "", id)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, u := range ups {
		fmt.Fprintf(&b, "upload %s\n", u.ID)
	}
	inc, _ := os.ReadDir(filepath.Join(e.cfg.Paths.IntakeRoot, jobdir.Incoming))
	for _, d := range inc {
		fmt.Fprintf(&b, "incoming %s\n", d.Name())
	}
	for _, l := range e.leftovers(id) {
		fmt.Fprintf(&b, "spool %s\n", l)
	}
	return b.String()
}

// fakeWorker plays the worker on the fake queue: it finishes every waiting task after each
// pass, as the plans say. By default an archive unpacks to nothing, a video passes the
// probe and so does a subtitle.
type fakeWorker struct {
	e        *env
	extracts map[string]extractPlan // by archive path
	refuse   map[string]string      // probe refusal by path
	fail     func(t spool.Task) *spool.Error
	hold     func(t spool.Task) bool // leave it waiting, as if it ran for long
	ran      []string                // task ids, in the order they finished
	noRoom   map[string]int          // by archive path: how many runs are refused free_space first
	args     []extract.Args          // of every extraction, in order
}

type extractPlan struct {
	refused string
	files   map[string]string // name under dest → content
	skipped []extract.Skipped
}

func (w *fakeWorker) work() {
	for _, t := range w.e.q.Tasks() {
		if w.hold != nil && w.hold(t) {
			continue
		}
		w.ran = append(w.ran, t.ID)
		if w.fail != nil {
			if e := w.fail(t); e != nil {
				w.e.q.Fail(t.ID, e.Code, e.Message)
				continue
			}
		}
		switch t.Kind {
		case spool.KindExtract:
			w.e.q.Complete(t.ID, w.extract(t))
		case spool.KindProbe:
			w.e.q.Complete(t.ID, w.probe(t))
		default:
			w.e.q.Fail(t.ID, spool.CodeUnknownKind, t.Kind)
		}
	}
}

func (w *fakeWorker) root(t spool.Task) string {
	return jobdir.Dir(w.e.cfg.Paths.IntakeRoot, t.Job)
}

// extract does what extract.Run does to the disk: Dest is replaced.
func (w *fakeWorker) extract(t spool.Task) extract.Output {
	var a extract.Args
	if err := json.Unmarshal(t.Args, &a); err != nil {
		w.e.t.Fatal(err)
	}
	plan := w.extracts[a.Archive]
	if w.noRoom[a.Archive] > 0 {
		w.noRoom[a.Archive]--
		plan = extractPlan{refused: extract.RefusedFreeSpace}
	}
	w.args = append(w.args, a)
	out := extract.Output{Refused: plan.refused, Files: []extract.Entry{}, Skipped: slices.Clone(plan.skipped)}
	if out.Skipped == nil {
		out.Skipped = []extract.Skipped{}
	}
	dest := filepath.Join(w.root(t), a.Dest)
	if err := os.RemoveAll(dest); err != nil {
		w.e.t.Fatal(err)
	}
	if plan.refused != "" {
		return out
	}
	for _, name := range slices.Sorted(maps.Keys(plan.files)) {
		p := filepath.Join(dest, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			w.e.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(plan.files[name]), 0o600); err != nil {
			w.e.t.Fatal(err)
		}
		out.Files = append(out.Files, extract.Entry{Path: a.Dest + "/" + name, Size: int64(len(plan.files[name]))})
	}
	return out
}

func (w *fakeWorker) probe(t spool.Task) media.ProbeOutput {
	var a media.ProbeArgs
	if err := json.Unmarshal(t.Args, &a); err != nil {
		w.e.t.Fatal(err)
	}
	out := media.ProbeOutput{Files: []media.ProbedFile{}}
	for _, p := range a.Paths {
		pf := media.ProbedFile{Path: p, Kind: "video"}
		if strings.HasSuffix(p, ".srt") {
			pf.Kind = "subtitle"
		}
		if why, ok := w.refuse[p]; ok {
			pf.Refusal = why
		} else {
			pf.OK = true
			if pf.Kind == "video" {
				pf.Media = fakeProbe(path.Base(p))
			}
		}
		out.Files = append(out.Files, pf)
	}
	return out
}

func fakeProbe(name string) *media.Probe {
	return &media.Probe{
		Container: "matroska,webm", DurationMs: int64(1000 * len(name)), Size: int64(len(name)),
		Video:     []media.VideoTrack{{Index: 0, Codec: "h264", Width: 640, Height: 360}},
		Audio:     []media.AudioTrack{{Index: 1, Codec: "aac", Language: "eng"}},
		Subtitles: []media.SubtitleTrack{},
	}
}

func probeJSON(t *testing.T, name string) string {
	t.Helper()
	data, err := json.Marshal(fakeProbe(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
