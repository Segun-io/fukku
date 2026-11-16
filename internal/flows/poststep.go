package flows

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/segun-io/fukku/internal/store"
)

// PostStep runs after the import (#40's contract, used by #41 and #42). Run is called until
// it returns done; it must be safe to repeat. A returned error is retried with backoff
// (server.postStepBackoff); after server.postStepAttempts runs the step is recorded as
// failed and the job still finishes: the import is never undone. Two errors are outcomes
// rather than failures to retry:
//
//   - Skip(reason): the step did nothing on purpose (no provider, switched off). It is
//     recorded as skipped with the reason, at once.
//   - GiveUp(err): the step has retried by itself and gave up, or retrying cannot help. It
//     is recorded as failed at once: a step's own retries count as one engine attempt, so
//     they are not multiplied by server.postStepAttempts.
//
// rows are the job's rows with their results: a step works on the rows imported.
type PostStep interface {
	Name() string
	Run(ctx context.Context, job store.Job, target store.Target, rows []store.Row) (done bool, detail string, err error)
}

// Linker is implemented by a step whose detail, once it is ok, says where the title can be
// opened (the media server's item). The job's result shows the first link a step gives.
type Linker interface {
	Link(detail string) (url string, ok bool)
}

// ErrStepSkipped is in the chain of the error Skip returns.
var ErrStepSkipped = errors.New("step skipped")

// ErrStepGaveUp is in the chain of the error GiveUp returns.
var ErrStepGaveUp = errors.New("step gave up")

type skipped struct{ reason string }

func (s *skipped) Error() string        { return "skipped: " + s.reason }
func (s *skipped) Is(target error) bool { return target == ErrStepSkipped }

// Skip is what Run returns when the step did nothing on purpose; reason is recorded as the
// step's detail.
func Skip(reason string) error { return &skipped{reason} }

// SkipReason returns the reason of a Skip error in err's chain.
func SkipReason(err error) (string, bool) {
	var s *skipped
	if errors.As(err, &s) {
		return s.reason, true
	}
	return "", false
}

type gaveUp struct{ err error }

func (g *gaveUp) Error() string        { return g.err.Error() }
func (g *gaveUp) Unwrap() error        { return g.err }
func (g *gaveUp) Is(target error) bool { return target == ErrStepGaveUp }

// GiveUp marks err as final: the step is recorded as failed without further attempts.
func GiveUp(err error) error {
	if err == nil {
		return nil
	}
	return &gaveUp{err}
}

// AddPostStep registers a post-processing step; steps run in the order they were added.
// Call it before the runner starts.
func (e *Engine) AddPostStep(s PostStep) { e.steps = append(e.steps, s) }

// PostProcessing is the runner step for post-processing jobs. Steps can wait for minutes
// (the subtitle hand-over confirms each upload), and a runner step must not wait, so the
// steps of a job run in their own goroutine, one per job; a pass while it runs does nothing.
// Everything it decides comes from the store: after a restart the next pass starts it again
// and it goes on with the steps not finished.
func (e *Engine) PostProcessing(ctx context.Context, job store.Job) error {
	e.mu.Lock()
	if e.running[job.ID] {
		e.mu.Unlock()
		return nil
	}
	e.running[job.ID] = true
	e.wg.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.wg.Done()
		defer func() {
			e.mu.Lock()
			delete(e.running, job.ID)
			e.mu.Unlock()
		}()
		defer func() {
			if v := recover(); v != nil {
				e.log.Error("job_step_failed", "job", job.ID, "state", job.State, "error", fmt.Sprintf("post-processing panicked: %v", v))
			}
		}()
		if err := e.postProcess(ctx, job); err != nil && ctx.Err() == nil {
			e.log.Error("job_step_failed", "job", job.ID, "state", job.State, "attempt", job.Attempt, "error", err)
		}
	}()
	return nil
}

// Wait waits for the post-processing goroutines to end (they end with the runner's context).
func (e *Engine) Wait() { e.wg.Wait() }

// postProcess runs each registered step that has not finished and is due, records it, and
// moves the job to done once every step is ok, skipped or failed. Each run is counted
// (recorded as an attempt) before it starts, so a step that brings the process down is not
// retried for ever.
func (e *Engine) postProcess(ctx context.Context, job store.Job) error {
	t, err := e.st.Target(ctx, job.ID)
	if err != nil {
		return err
	}
	rows, err := e.st.Rows(ctx, job.ID)
	if err != nil {
		return err
	}
	recorded, err := e.st.PostSteps(ctx, job.ID)
	if err != nil {
		return err
	}
	byName := map[string]store.PostStep{}
	for _, ps := range recorded {
		byName[ps.Name] = ps
	}
	// Every step is listed from the start, in its order, so the result shows what is to come.
	for _, s := range e.steps {
		if _, ok := byName[s.Name()]; !ok {
			ps := store.PostStep{JobID: job.ID, Name: s.Name(), Status: store.StepPending, UpdatedAt: e.now()}
			if err := e.st.PutPostStep(ctx, ps); err != nil {
				return err
			}
			byName[s.Name()] = ps
		}
	}
	limit := max(e.p.cfg.Server.PostStepAttempts, 1)
	pending := false
	for _, s := range e.steps {
		ps := byName[s.Name()]
		if ps.Status != store.StepPending && ps.Status != "" {
			continue
		}
		if ps.Attempts >= limit { // the last run did not record its end (a crash)
			ps.Status, ps.UpdatedAt = store.StepFailed, e.now()
			if ps.Detail == "" {
				ps.Detail = fmt.Sprintf("did not finish after %d attempt(s)", ps.Attempts)
			}
			if err := e.st.PutPostStep(ctx, ps); err != nil {
				return err
			}
			e.logStep(job, ps)
			continue
		}
		if ps.Attempts > 0 && e.now().Before(ps.UpdatedAt.Add(backoff(e.p.cfg.Server.PostStepBackoff.D(), ps.Attempts))) {
			pending = true
			continue
		}
		ps.Attempts++
		ps.Status, ps.UpdatedAt = store.StepPending, e.now()
		if err := e.st.PutPostStep(ctx, ps); err != nil {
			return err
		}
		done, detail, runErr := s.Run(ctx, job, t, rows)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ps.UpdatedAt = e.now()
		reason, skip := SkipReason(runErr)
		switch {
		case skip:
			ps.Status, ps.Detail = store.StepSkipped, reason
		case runErr == nil && done:
			ps.Status, ps.Detail = store.StepOK, detail
		case errors.Is(runErr, ErrStepGaveUp):
			ps.Status, ps.Detail = store.StepFailed, runErr.Error()
		default:
			ps.Detail = detail
			if runErr != nil {
				ps.Detail = runErr.Error()
			}
			if ps.Attempts >= limit {
				ps.Status = store.StepFailed
			} else {
				pending = true
			}
		}
		if err := e.st.PutPostStep(ctx, ps); err != nil {
			return err
		}
		e.logStep(job, ps)
	}
	if pending {
		return nil
	}
	return e.p.move(ctx, job, &t, store.Done, store.Change{Note: fmt.Sprintf("%d post-processing step(s) finished", len(e.steps))})
}

func (e *Engine) logStep(job store.Job, ps store.PostStep) {
	level := slog.LevelInfo
	if ps.Status == store.StepFailed || ps.Status == store.StepPending {
		level = slog.LevelWarn
	}
	e.log.Log(context.Background(), level, "post_step", "job", job.ID, "step", ps.Name, "status", ps.Status,
		"attempt", ps.Attempts, "detail", ps.Detail)
}
