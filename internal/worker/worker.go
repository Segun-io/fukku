// Package worker is the worker process. It opens untrusted files (unzip, ffprobe, mkvmerge,
// preview clips) and holds no credentials and no network. It talks to the web process only
// through task and result files in the spool directory (package spool), and it writes a
// heartbeat there.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/spool"
)

// HeartbeatFile is the name, inside the spool directory, of the worker's heartbeat.
const HeartbeatFile = "worker.heartbeat"

// Handler runs one task. job is the task's job directory; a handler can reach nothing else.
// It must be safe to run again after a crash. The returned value is the result's output.
type Handler func(ctx context.Context, job *os.Root, args json.RawMessage) (output any, err error)

// ErrBadTask is returned, wrapped, by a handler whose args cannot be used (they do not
// decode, or name a path outside the job). The result then has error code bad_task.
var ErrBadTask = errors.New("bad task")

// The causes a task's context is cancelled with.
var (
	errCancelled = errors.New("task cancelled")
	errTimeout   = errors.New("task timed out")
)

// Worker runs tasks from the spool directory.
type Worker struct {
	cfg      config.Config
	log      *slog.Logger
	handlers map[string]Handler
	now      func() time.Time

	retryMin, retryMax time.Duration // backoff while a result cannot be written
}

// New builds a worker. handlers maps a task kind to its handler; a task of any other kind
// gets the error code unknown_kind.
func New(cfg config.Config, log *slog.Logger, handlers map[string]Handler) *Worker {
	return &Worker{cfg: cfg, log: log, handlers: handlers, now: time.Now, retryMin: time.Second, retryMax: time.Minute}
}

// Run writes a heartbeat every interval and runs tasks until ctx is cancelled. It first
// takes the spool's worker lock and refuses to start while another worker holds it. At
// start-up it runs again the tasks a previous worker claimed and did not finish. When ctx
// is cancelled it cancels the running tasks and waits for their handlers to return; those
// tasks get no result and run again at the next start.
func (w *Worker) Run(ctx context.Context) error {
	wc := w.cfg.Worker
	sp, err := spool.Open(wc.SpoolDir)
	if err != nil {
		return err
	}
	defer func() { _ = sp.Close() }()
	lock, err := sp.LockWorker()
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := w.beat(); err != nil {
		return err
	}

	recovered, ignored, err := sp.Running()
	w.logIgnored(ignored)
	if err != nil {
		return err
	}
	w.log.Info("worker_started", "spool", wc.SpoolDir, "concurrency", wc.Concurrency, "recovered", len(recovered))

	l := &loop{
		w: w, sp: sp, ctx: ctx,
		active: map[string]context.CancelCauseFunc{},
		done:   make(chan string, wc.Concurrency),
		queue:  recovered,
	}
	return l.run()
}

// loop is the state of one Run. Only its goroutine touches it, except done.
type loop struct {
	w      *Worker
	sp     *spool.Dir
	ctx    context.Context
	active map[string]context.CancelCauseFunc // running tasks by id
	done   chan string                        // a task's goroutine sends its id when it ends
	queue  []spool.Task                       // claimed before this run, not started yet
}

func (l *loop) run() error {
	wc := l.w.cfg.Worker
	beat := time.NewTicker(wc.HeartbeatInterval.D())
	defer beat.Stop()
	poll := time.NewTicker(wc.PollInterval.D())
	defer poll.Stop()
	scan := true
	for {
		l.fill(scan)
		scan = false
		select {
		case <-l.ctx.Done():
			for len(l.active) > 0 {
				delete(l.active, <-l.done)
			}
			l.w.log.Info("worker_stopped")
			return nil
		case <-beat.C:
			if err := l.w.beat(); err != nil {
				l.w.log.Error("heartbeat_failed", "error", err)
			}
		case <-poll.C:
			l.cancelRequested()
			scan = true
		case id := <-l.done:
			delete(l.active, id)
			scan = true
		}
	}
}

// fill starts tasks while there is a free slot: first those recovered at start-up, then,
// when scan is set, waiting ones, oldest first.
func (l *loop) fill(scan bool) {
	if l.ctx.Err() != nil {
		return
	}
	limit := l.w.cfg.Worker.Concurrency
	for len(l.active) < limit && len(l.queue) > 0 {
		t := l.queue[0]
		l.queue = l.queue[1:]
		l.start(t)
	}
	if !scan || len(l.active) >= limit {
		return
	}
	waiting, ignored, err := l.sp.Waiting()
	l.w.logIgnored(ignored)
	if err != nil {
		l.w.log.Error("spool_failed", "error", err)
		return
	}
	for _, t := range waiting {
		if len(l.active) >= limit {
			return
		}
		ok, err := l.sp.Claim(t.ID)
		if err != nil {
			l.w.log.Error("spool_failed", "task", t.ID, "error", err)
			continue
		}
		if ok { // otherwise cancelled or claimed by someone else since the listing
			l.start(t)
		}
	}
}

// start runs a claimed task in its own goroutine.
func (l *loop) start(t spool.Task) {
	ctx, cancel := context.WithCancelCause(l.ctx)
	l.active[t.ID] = cancel
	go func() {
		defer func() { l.done <- t.ID }()
		defer cancel(nil)
		l.w.runTask(ctx, l.ctx, l.sp, t)
	}()
}

// cancelRequested cancels the running tasks that have a cancel marker.
func (l *loop) cancelRequested() {
	if len(l.active) == 0 {
		return
	}
	ids, err := l.sp.CancelRequests()
	if err != nil {
		l.w.log.Error("spool_failed", "error", err)
		return
	}
	for _, id := range ids {
		if cancel, ok := l.active[id]; ok {
			cancel(errCancelled)
		}
	}
}

// runTask runs one claimed task and writes its result. stop is the worker's own context:
// when it ends the task, the task gets no result and stays claimed, to run again.
func (w *Worker) runTask(ctx, stop context.Context, sp *spool.Dir, t spool.Task) {
	log := w.log.With("task", t.ID, "kind", t.Kind, "job", t.Job)
	started := w.now()
	res := spool.Result{ID: t.ID, Kind: t.Kind, Job: t.Job, Started: started.UTC()}
	fail := func(code, msg string) {
		res.Status, res.Error = spool.StatusError, &spool.Error{Code: code, Message: msg}
	}

	// The deadline covers the whole task; its cause tells a timeout from a cancel.
	ctx, cancel := context.WithTimeoutCause(ctx, w.cfg.Worker.TaskTimeout.D(), errTimeout)
	defer cancel()
	output, err := w.execute(ctx, sp, t, log)
	cause := context.Cause(ctx)
	switch {
	case err == nil:
		res.Status = spool.StatusOK
		if output != nil {
			data, merr := json.Marshal(output)
			if merr != nil {
				fail(spool.CodeInternal, "output: "+merr.Error())
			} else {
				res.Output = data
			}
		}
	case errors.Is(err, errCancelled) || errors.Is(cause, errCancelled):
		fail(spool.CodeCancelled, "cancelled")
	case errors.Is(cause, errTimeout):
		fail(spool.CodeTimeout, "ran past worker.taskTimeout ("+w.cfg.Worker.TaskTimeout.String()+")")
	case stop.Err() != nil:
		log.Info("task_interrupted", "duration", w.now().Sub(started), "note", "runs again at the next start")
		return
	default:
		var te *taskError
		if errors.As(err, &te) {
			fail(te.code, te.msg)
		} else if errors.Is(err, ErrBadTask) {
			fail(spool.CodeBadTask, err.Error())
		} else {
			fail(spool.CodeInternal, err.Error())
		}
	}
	res.Finished = w.now().UTC()
	if !w.finish(stop, sp, res, log) {
		return
	}
	attrs := []any{"status", res.Status, "duration", res.Finished.Sub(started)}
	if res.Error != nil {
		attrs = append(attrs, "code", res.Error.Code, "error", res.Error.Message)
	}
	log.Info("task_finished", attrs...)
}

// finish writes the result, retrying with backoff while that fails (a full disk, say), so
// the web process gets it as soon as it can be written rather than after a restart. The
// task keeps its slot meanwhile. It gives up only when the worker stops; the task is then
// still claimed and runs again at the next start.
func (w *Worker) finish(stop context.Context, sp *spool.Dir, res spool.Result, log *slog.Logger) bool {
	delay := w.retryMin
	for attempt := 1; ; attempt++ {
		err := sp.Finish(res)
		if err == nil {
			return true
		}
		log.Error("result_write_failed", "attempt", attempt, "retry_in", delay, "error", err)
		select {
		case <-stop.Done():
			log.Info("task_interrupted", "note", "runs again at the next start")
			return false
		case <-time.After(delay):
		}
		delay = min(2*delay, w.retryMax)
	}
}

// taskError fails a task with a code of its own, before its handler runs.
type taskError struct{ code, msg string }

func (e *taskError) Error() string { return e.code + ": " + e.msg }

// execute checks the task, opens its job directory and calls its handler.
func (w *Worker) execute(ctx context.Context, sp *spool.Dir, t spool.Task, log *slog.Logger) (any, error) {
	if !jobdir.ValidID(t.Job) {
		return nil, &taskError{spool.CodeBadTask, fmt.Sprintf("malformed job id %q", t.Job)}
	}
	h, ok := w.handlers[t.Kind]
	if !ok {
		return nil, &taskError{spool.CodeUnknownKind, fmt.Sprintf("no handler for kind %q", t.Kind)}
	}
	// A cancel that arrived before the task started (or before a restart) stops it here.
	requested, err := sp.CancelRequested(t.ID)
	if err != nil {
		return nil, err
	}
	if requested {
		return nil, errCancelled
	}
	root, err := os.OpenRoot(jobdir.Dir(w.cfg.Paths.IntakeRoot, t.Job))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &taskError{spool.CodeBadTask, "the job directory does not exist"}
	}
	if err != nil {
		return nil, fmt.Errorf("job directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	log.Info("task_started")
	return callHandler(ctx, h, root, t.Args, log)
}

// callHandler turns a panic in the handler into an error.
func callHandler(ctx context.Context, h Handler, root *os.Root, args json.RawMessage, log *slog.Logger) (output any, err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Error("task_panicked", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			output, err = nil, fmt.Errorf("panic: %v", p)
		}
	}()
	return h(ctx, root, args)
}

func (w *Worker) logIgnored(ignored []spool.Ignored) {
	for _, ig := range ignored {
		w.log.Warn("spool_ignored", "file", ig.Path, "reason", ig.Reason)
	}
}

// beat atomically replaces the heartbeat file with the current Unix time.
func (w *Worker) beat() error {
	dir := w.cfg.Worker.SpoolDir
	tmp, err := os.CreateTemp(dir, ".heartbeat-*")
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	_, werr := tmp.WriteString(strconv.FormatInt(w.now().Unix(), 10) + "\n")
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("heartbeat: %w", err)
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, HeartbeatFile))
}

// HeartbeatAge returns how old the worker's last heartbeat in spoolDir is. The file is read
// through an os.Root of the spool, must be a small regular file, and is opened without
// blocking, so a link or FIFO put there cannot hang the health check or fill its memory.
func HeartbeatAge(spoolDir string, now time.Time) (time.Duration, error) {
	root, err := os.OpenRoot(spoolDir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()
	data, err := spool.ReadRegular(root, HeartbeatFile, 64)
	if err != nil {
		return 0, err
	}
	sec, err := strconv.ParseInt(string(trimNewline(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("heartbeat file is malformed: %w", err)
	}
	return now.Sub(time.Unix(sec, 0)), nil
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
