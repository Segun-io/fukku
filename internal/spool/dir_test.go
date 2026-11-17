package spool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/spool/spooltest"
)

// dirBackend plays the worker on a real spool directory for the conformance suite.
type dirBackend struct {
	*spool.Dir
	t *testing.T
}

func (b dirBackend) Tasks() []spool.Task {
	waiting, _, err := b.Waiting()
	if err != nil {
		b.t.Fatal(err)
	}
	running, _, err := b.Running()
	if err != nil {
		b.t.Fatal(err)
	}
	return append(running, waiting...)
}

func (b dirBackend) finish(id string, r spool.Result) {
	waiting, _, err := b.Waiting()
	if err != nil {
		b.t.Fatal(err)
	}
	i := slices.IndexFunc(waiting, func(t spool.Task) bool { return t.ID == id })
	if i < 0 {
		b.t.Fatalf("task %s is not waiting", id)
	}
	if ok, err := b.Claim(id); !ok || err != nil {
		b.t.Fatalf("claim %s: %v %v", id, ok, err)
	}
	r.ID, r.Kind, r.Job = id, waiting[i].Kind, waiting[i].Job
	r.Started, r.Finished = time.Now().UTC(), time.Now().UTC()
	if err := b.Finish(r); err != nil {
		b.t.Fatal(err)
	}
}

func (b dirBackend) Complete(id string, output any) {
	r := spool.Result{Status: spool.StatusOK}
	if output != nil {
		data, err := json.Marshal(output)
		if err != nil {
			b.t.Fatal(err)
		}
		r.Output = data
	}
	b.finish(id, r)
}

func (b dirBackend) Fail(id, code, message string) {
	b.finish(id, spool.Result{Status: spool.StatusError, Error: &spool.Error{Code: code, Message: message}})
}

func open(t *testing.T) (*spool.Dir, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "spool")
	d, err := spool.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, dir
}

func TestDirQueue(t *testing.T) {
	spooltest.Run(t, func(t *testing.T) spooltest.Backend {
		d, _ := open(t)
		return dirBackend{Dir: d, t: t}
	})
}

const job = spooltest.Job

func submit(t *testing.T, d *spool.Dir, id string) {
	t.Helper()
	err := d.Submit(context.Background(), spool.Task{ID: id, Kind: spool.KindExtract, Job: job, Args: json.RawMessage(`{"archive":"archives/Season 01.zip","dest":"files/Season 01"}`)})
	if err != nil {
		t.Fatal(err)
	}
}

// names lists a directory, temporary files included.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestLayout(t *testing.T) {
	d, dir := open(t)
	for _, sub := range []string{"tasks", "running", "results", "cancel"} {
		if fi, err := os.Stat(filepath.Join(dir, sub)); err != nil || !fi.IsDir() {
			t.Fatalf("%s: %v", sub, err)
		}
	}
	submit(t, d, "t-1")
	if got := names(t, filepath.Join(dir, "tasks")); !slices.Equal(got, []string{"t-1.json"}) {
		t.Fatalf("tasks/ holds %v", got)
	}
	var raw map[string]any
	data, err := os.ReadFile(filepath.Join(dir, "tasks", "t-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"v", "id", "kind", "job", "created", "args"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("task file has no %q: %s", k, data)
		}
	}

	if ok, err := d.Claim("t-1"); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if ok, err := d.Claim("t-1"); ok || err != nil {
		t.Fatalf("second claim: %v %v", ok, err)
	}
	if got := names(t, filepath.Join(dir, "tasks")); len(got) != 0 {
		t.Fatalf("tasks/ holds %v after the claim", got)
	}
	if got := names(t, filepath.Join(dir, "running")); !slices.Equal(got, []string{"t-1.json"}) {
		t.Fatalf("running/ holds %v", got)
	}
	if err := d.Finish(spool.Result{ID: "t-1", Kind: spool.KindExtract, Job: job, Status: spool.StatusOK}); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(dir, "running")); len(got) != 0 {
		t.Fatalf("running/ holds %v after finishing", got)
	}
	if got := names(t, filepath.Join(dir, "results")); !slices.Equal(got, []string{"t-1.json"}) {
		t.Fatalf("results/ holds %v", got)
	}
}

func TestCancelRunning(t *testing.T) {
	d, dir := open(t)
	ctx := context.Background()
	submit(t, d, "t-1")
	if _, err := d.Claim("t-1"); err != nil {
		t.Fatal(err)
	}
	if err := d.Cancel(ctx, "t-1"); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(dir, "cancel")); !slices.Equal(got, []string{"t-1"}) {
		t.Fatalf("cancel/ holds %v", got)
	}
	if fi, err := os.Stat(filepath.Join(dir, "cancel", "t-1")); err != nil || fi.Size() != 0 {
		t.Fatalf("marker: %v", err)
	}
	if ids, err := d.CancelRequests(); err != nil || !slices.Equal(ids, []string{"t-1"}) {
		t.Fatalf("cancel requests %v %v", ids, err)
	}
	if ok, err := d.CancelRequested("t-1"); !ok || err != nil {
		t.Fatalf("requested %v %v", ok, err)
	}
	// The running task is not removed: the worker stops it and writes its result.
	if got := names(t, filepath.Join(dir, "running")); !slices.Equal(got, []string{"t-1.json"}) {
		t.Fatalf("running/ holds %v", got)
	}
	if err := d.Finish(spool.Result{ID: "t-1", Status: spool.StatusError, Error: &spool.Error{Code: spool.CodeCancelled}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Ack(ctx, "t-1"); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"tasks", "running", "results", "cancel"} {
		if got := names(t, filepath.Join(dir, sub)); len(got) != 0 {
			t.Errorf("%s/ holds %v after Ack", sub, got)
		}
	}
	// Unknown and finished tasks leave no marker.
	if err := d.Cancel(ctx, "unknown"); err != nil {
		t.Fatal(err)
	}
	if got := names(t, filepath.Join(dir, "cancel")); len(got) != 0 {
		t.Fatalf("cancel/ holds %v", got)
	}
}

func TestRunningAfterCrash(t *testing.T) {
	d, dir := open(t)
	for _, id := range []string{"a", "b"} {
		submit(t, d, id)
		if _, err := d.Claim(id); err != nil {
			t.Fatal(err)
		}
	}
	// "b" crashed after its result was written but before running/b.json was removed.
	if err := os.WriteFile(filepath.Join(dir, "results", "b.json"), []byte(`{"v":1,"id":"b","status":"ok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tasks, ignored, err := d.Running()
	if err != nil || len(ignored) != 0 {
		t.Fatal(err, ignored)
	}
	if len(tasks) != 1 || tasks[0].ID != "a" {
		t.Fatalf("running %+v", tasks)
	}
	if got := names(t, filepath.Join(dir, "running")); !slices.Equal(got, []string{"a.json"}) {
		t.Fatalf("running/ holds %v", got)
	}
}

func TestIgnoredOnce(t *testing.T) {
	d, dir := open(t)
	submit(t, d, "good")
	tasks := filepath.Join(dir, "tasks")
	files := map[string]string{
		"corrupt.json":      `{"v":1,"id":"corrupt",`,
		"notes.txt":         "not a task",
		"newer.json":        `{"v":2,"id":"newer","kind":"probe","job":"` + job + `"}`,
		"mismatch.json":     `{"v":1,"id":"other","kind":"probe","job":"` + job + `"}`,
		".tmp-0123456789ab": `{"v":1,"id":"half`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(tasks, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(tasks, "dir.json"), 0o750); err != nil {
		t.Fatal(err)
	}
	got, ignored, err := d.Waiting()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("tasks %+v", got)
	}
	var paths []string
	for _, ig := range ignored {
		paths = append(paths, ig.Path)
		if ig.Reason == "" {
			t.Errorf("%s: no reason", ig.Path)
		}
	}
	slices.Sort(paths)
	want := []string{"tasks/corrupt.json", "tasks/dir.json", "tasks/mismatch.json", "tasks/newer.json", "tasks/notes.txt"}
	if !slices.Equal(paths, want) {
		t.Fatalf("ignored %v, want %v", paths, want)
	}

	// Reported once while unchanged.
	if _, ignored, err := d.Waiting(); err != nil || len(ignored) != 0 {
		t.Fatalf("second scan ignored %v %v", ignored, err)
	}
	// A changed file is looked at again: completing it makes it a task.
	fixed := `{"v":1,"id":"corrupt","kind":"probe","job":"` + job + `","created":"2026-01-01T12:00:00Z","args":{}}`
	if err := os.WriteFile(filepath.Join(tasks, "corrupt.json"), []byte(fixed), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ignored, err = d.Waiting()
	if err != nil || len(ignored) != 0 || len(got) != 2 || got[0].ID != "corrupt" {
		t.Fatalf("after the fix: %+v %v %v", got, ignored, err)
	}
	// Nothing was deleted.
	for name := range files {
		if _, err := os.Stat(filepath.Join(tasks, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestResultChecks(t *testing.T) {
	d, dir := open(t)
	ctx := context.Background()
	results := filepath.Join(dir, "results")
	for name, body := range map[string]string{
		"corrupt.json": `{"v":1,`,
		"older.json":   `{"v":0,"id":"older","status":"ok"}`,
		"other.json":   `{"v":1,"id":"someone-else","status":"ok"}`,
	} {
		if err := os.WriteFile(filepath.Join(results, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		id := strings.TrimSuffix(name, ".json")
		assertBadResult(t, d, id)
	}
	// A result of a newer protocol version is not ours to read or to remove: it stays
	// for a web process that matches, and is not reported as an error.
	newer := filepath.Join(results, "newer.json")
	if err := os.WriteFile(newer, []byte(`{"v":2,"id":"newer","status":"ok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := d.Result(ctx, "newer"); ok || err != nil {
		t.Errorf("newer: ok %v, err %v", ok, err)
	}
	if _, err := os.Stat(newer); err != nil {
		t.Errorf("the newer result was touched: %v", err)
	}
	// A link out of the spool is not followed.
	outside := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(outside, []byte(`{"v":1,"id":"link","status":"ok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(results, "link.json")); err != nil {
		t.Fatal(err)
	}
	assertBadResult(t, d, "link")
	// An oversized file is refused.
	big := `{"v":1,"id":"big","status":"ok","output":"` + strings.Repeat("x", 17<<20) + `"}`
	if err := os.WriteFile(filepath.Join(results, "big.json"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	assertBadResult(t, d, "big")
	// Each is removed by Ack, once, whatever it holds.
	for _, id := range []string{"corrupt", "older", "other", "link", "big"} {
		if err := d.Ack(ctx, id); err != nil {
			t.Fatalf("Ack(%s): %v", id, err)
		}
		if _, ok, err := d.Result(ctx, id); ok || err != nil {
			t.Errorf("%s after Ack: ok %v, err %v", id, ok, err)
		}
	}
}

// assertBadResult checks that a result file that cannot be used comes back as a failed
// result, never as an error that repeats on every pass.
func assertBadResult(t *testing.T, d *spool.Dir, id string) {
	t.Helper()
	r, ok, err := d.Result(context.Background(), id)
	if err != nil || !ok || r.ID != id || r.Status != spool.StatusError || r.Error == nil || r.Error.Code != spool.CodeBadResult {
		t.Errorf("%s: %+v, ok %v, err %v", id, r, ok, err)
	}
}

func TestNoTemporaryFilesLeft(t *testing.T) {
	d, dir := open(t)
	ctx := context.Background()
	for i := range 20 {
		id := "t-" + string(rune('a'+i))
		submit(t, d, id)
		if _, err := d.Claim(id); err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			if err := d.Cancel(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
		if err := d.Finish(spool.Result{ID: id, Status: spool.StatusOK}); err != nil {
			t.Fatal(err)
		}
	}
	for _, sub := range []string{"tasks", "running", "results", "cancel"} {
		for _, n := range names(t, filepath.Join(dir, sub)) {
			if strings.HasPrefix(n, ".") {
				t.Errorf("%s/%s left behind", sub, n)
			}
		}
	}
}
