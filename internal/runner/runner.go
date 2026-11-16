// Package runner drives jobs through their states (plan §9). Every worker.pollInterval it
// loads the jobs in the states that have a step and calls the step, which looks at the
// store and the spool, acts, and returns without waiting.
//
// The runner keeps nothing in memory that a decision depends on: every decision comes from
// the store and the spool, so a restart at any point continues where the last run stopped.
// Steps only do what is safe to repeat (task ids are derived from the job, its attempt and
// the file, Submit and Ack are idempotent, file rows are unique by path), and each pass
// stores what it learned before it acknowledges the result it learned it from.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/metrics"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/worker"
)

// Job error codes of M1 (Job.ErrorCode).
const (
	CodeWorkerUnavailable = "worker_unavailable" // stalled: the worker's heartbeat is too old
	CodeTaskTimeout       = "task_timeout"       // failed: a task ran past worker.taskTimeout
	CodeTaskFailed        = "task_failed"        // failed: a task could not run (detail in the log)
	CodeNoUsableFiles     = "no_usable_files"    // failed: nothing passed the checks (the files list says why)
	CodeFreeSpace         = "free_space"         // stalled: too little free space to extract; resumes when there is room
)

// Step advances one job by at most one transition. It must not wait for outside work: it
// looks at what is there, acts, and returns. It is called again on the next pass, and
// after any crash, so everything it does must be safe to repeat.
type Step func(ctx context.Context, job store.Job) error

const (
	// sweepEvery is how often job_stuck is logged and settled jobs are swept (plan §13).
	sweepEvery = time.Minute
	pageSize   = 100
	actor      = "system"
)

// Runner drives jobs. Build it with New, register further steps with Handle, then Run.
type Runner struct {
	cfg   config.Config
	log   *slog.Logger
	st    store.Store
	q     spool.Queue
	now   func() time.Time
	steps map[store.State]Step
	// resumes holds, by error code, when a job stalled with that code may go on (ResumeWhen).
	resumes map[string]resume

	metrics *metrics.Set // task durations; nil: none

	freeSpace func(dir string) (int64, error) // bytes free on dir's filesystem; replaced in tests

	// For logging and pacing only; no decision depends on them.
	submitted map[string]bool // task ids logged as task_submitted by this process
	lastSweep time.Time
}

// New builds a runner with the steps of M1: uploaded, probing, stalled and cancelled.
func New(cfg config.Config, log *slog.Logger, st store.Store, q spool.Queue, now func() time.Time) *Runner {
	if now == nil {
		now = time.Now
	}
	r := &Runner{cfg: cfg, log: log, st: st, q: q, now: now, freeSpace: freeSpace, submitted: map[string]bool{}}
	r.steps = map[store.State]Step{
		store.Uploaded:  r.uploaded,
		store.Probing:   r.probing,
		store.Stalled:   r.stalled,
		store.Cancelled: r.cancelled,
	}
	return r
}

// SetMetrics makes the runner record the duration of every task result it uses. Call it
// before Run.
func (r *Runner) SetMetrics(m *metrics.Set) { r.metrics = m }

// Handle registers the step for jobs in a state. M2 registers matched, importing,
// imported and post-processing. Call it before Run.
func (r *Runner) Handle(state store.State, s Step) { r.steps[state] = s }

// Ready reports whether a stalled job may go back to its Resume state. Like a step it must
// not wait: it looks (asks an app whether it answers, say) and returns.
type Ready func(ctx context.Context, job store.Job) (bool, error)

type resume struct {
	note  string
	ready Ready
}

// ResumeWhen registers, for jobs stalled with the error code, the check that lets them go
// back to their Resume state, and the note recorded with that transition. M2 registers
// arr_unavailable. Call it before Run.
func (r *Runner) ResumeWhen(code, note string, ready Ready) {
	if r.resumes == nil {
		r.resumes = map[string]resume{}
	}
	r.resumes[code] = resume{note: note, ready: ready}
}

// Run makes a pass at once and then every worker.pollInterval, until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	poll := r.cfg.Worker.PollInterval.D()
	r.log.Info("runner_started", "pollInterval", poll, "states", r.states())
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		r.pass(ctx)
		select {
		case <-ctx.Done():
			r.log.Info("runner_stopped")
			return nil
		case <-t.C:
		}
	}
}

func (r *Runner) states() []store.State { return slices.Sorted(maps.Keys(r.steps)) }

// pass calls the step of every job in a state that has one, oldest job first. Once a
// minute it also sweeps (see sweep).
func (r *Runner) pass(ctx context.Context) {
	if now := r.now(); r.lastSweep.IsZero() || now.Sub(r.lastSweep) >= sweepEvery {
		r.lastSweep = now
		r.sweep(ctx)
	}
	jobs, err := r.jobs(ctx, r.states())
	if err != nil {
		if ctx.Err() == nil {
			r.log.Error("runner_failed", "error", err)
		}
		return
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		if err := r.steps[j.State](ctx, j); err != nil && ctx.Err() == nil {
			r.log.Error("job_step_failed", "job", j.ID, "state", j.State, "attempt", j.Attempt, "error", err)
		}
	}
}

// jobs lists every job in the states, oldest first.
func (r *Runner) jobs(ctx context.Context, states []store.State) ([]store.Job, error) {
	var all []store.Job
	seen := map[string]bool{}
	for off := 0; ; off += pageSize {
		page, err := r.st.Jobs(ctx, store.JobFilter{States: states, Limit: pageSize, Offset: off})
		if err != nil {
			return nil, err
		}
		for _, j := range page {
			// A job that changed state between two pages can show up twice; once is enough.
			if !seen[j.ID] {
				seen[j.ID] = true
				all = append(all, j)
			}
		}
		if len(page) < pageSize {
			break
		}
	}
	slices.Reverse(all)
	return all, nil
}

// sweep logs job_stuck for every job that has been failed or stalled for longer than
// server.jobStuckAfter, and cleans up after failed and cancelled jobs: a task of theirs
// that was still running when they stopped writes its result later, and only then can it
// be acknowledged.
func (r *Runner) sweep(ctx context.Context) {
	jobs, err := r.jobs(ctx, []store.State{store.Failed, store.Stalled, store.Cancelled})
	if err != nil {
		if ctx.Err() == nil {
			r.log.Error("runner_failed", "error", err)
		}
		return
	}
	now, after := r.now(), r.cfg.Server.JobStuckAfter.D()
	for _, j := range jobs {
		if j.State != store.Cancelled {
			if d := now.Sub(j.UpdatedAt); d > after {
				r.log.Warn("job_stuck", "job", j.ID, "state", j.State, "code", j.ErrorCode,
					"since", j.UpdatedAt, "for", d.Round(time.Second))
			}
		}
		if j.State == store.Stalled || (j.State == store.Failed && j.Resume != store.Uploaded) {
			continue // a stalled job resumes with its tasks; past probing there are none
		}
		if err := r.drainJob(ctx, j, 1, j.Attempt, true); err != nil && ctx.Err() == nil {
			r.log.Error("job_step_failed", "job", j.ID, "state", j.State, "attempt", j.Attempt, "error", err)
		}
	}
}

// move makes one transition from the state the job was loaded in and logs job_state. It
// reports false, with no error, when the job is no longer in that state (a user cancelled
// it meanwhile, say); the next pass sees the new state.
func (r *Runner) move(ctx context.Context, job store.Job, to store.State, c store.Change) (bool, error) {
	c.Actor = actor
	j, err := r.st.Transition(ctx, job.ID, job.State, to, c)
	if errors.Is(err, store.ErrConflict) {
		r.log.Debug("job_moved", "job", job.ID, "from", job.State, "to", to, "note", "the job changed state meanwhile")
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%s → %s: %w", job.State, to, err)
	}
	attrs := []any{"job", job.ID, "from", job.State, "to", j.State, "attempt", j.Attempt}
	if c.ErrorCode != "" {
		attrs = append(attrs, "code", c.ErrorCode, "detail", c.ErrorText)
	}
	r.log.Info("job_state", attrs...)
	return true, nil
}

// workerDown reports whether the worker's heartbeat is missing or older than
// worker.heartbeatMaxAge, and why.
func (r *Runner) workerDown() (string, bool) {
	limit := r.cfg.Worker.HeartbeatMaxAge.D()
	age, err := worker.HeartbeatAge(r.cfg.Worker.SpoolDir, r.now())
	if err != nil {
		return "no readable worker heartbeat: " + err.Error(), true
	}
	if age > limit {
		return fmt.Sprintf("the worker's heartbeat is %s old (limit %s)", age.Round(time.Second), limit), true
	}
	return "", false
}
