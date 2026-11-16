package flows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/worker"
)

// The matched step of a flow 2 job (#52): probe the library file, then wait for the user.
//
// It is safe to repeat at every point, like the runner's steps: each pass decides from the
// store and the spool. The task id is derived (runner.LibraryProbeID), Submit and Ack are
// idempotent, the probe is stored in the target's options before its result is
// acknowledged, and the move to awaiting-confirmation comes last:
//
//   - The probe is stored: acknowledge its result (it is still there after a crash before
//     the ack), then move to awaiting-confirmation.
//   - A result for the library file: a refusal fails the job (library_unreadable), a task
//     error fails it as the runner's tasks do (task_failed, task_timeout); either way the
//     result is acknowledged after the move. A probe is stored under the job's lock while
//     the job is still matched to the same title, then acknowledged, then the job moves.
//   - A result for another file (from an earlier match in the same attempt): acknowledged
//     and dropped; the next pass submits the task again.
//   - No result: the path must lie under paths.libraryRoots (else library_unreadable);
//     the task is submitted, and the job stalls with worker_unavailable while the
//     worker's heartbeat is too old, as the runner's probing does.
//
// Results of earlier attempts are cancelled and acknowledged first.

// SetQueue gives the matched step the spool, for the library probe of flow 2 jobs. Call it
// before the runner starts. Without one, the matched step of a flow 2 job fails (logged)
// and leaves the job as it is.
func (p *Proposals) SetQueue(q spool.Queue) { p.queue = q }

// flow2 is the matched step of a job whose target is flow 2.
func (p *Proposals) flow2(ctx context.Context, job store.Job, t store.Target, rec Flow2) error {
	if p.queue == nil {
		return errors.New("flow 2: the proposal flow has no spool queue")
	}
	for a := 1; a < job.Attempt; a++ {
		if err := p.discardLibraryProbe(ctx, runner.LibraryProbeID(job.ID, a)); err != nil {
			return err
		}
	}
	id := runner.LibraryProbeID(job.ID, job.Attempt)
	if rec.Library.Probe != nil {
		if err := p.queue.Ack(ctx, id); err != nil {
			return err
		}
		if err := staleProbe(rec, *rec.Library.Probe); err != nil {
			return p.rescan(ctx, job, t, err)
		}
		return p.flow2Ready(ctx, job, t, *rec.Library.Probe)
	}
	res, ok, err := p.queue.Result(ctx, id)
	if err != nil {
		return err
	}
	if ok {
		return p.useLibraryProbe(ctx, job, t, rec, res)
	}
	if _, _, in := media.LibraryPath(p.cfg.Paths.LibraryRoots, rec.Library.Path); !in {
		return p.move(ctx, job, &t, store.Failed, store.Change{
			ErrorCode: CodeLibraryUnreadable,
			ErrorText: fmt.Sprintf("the library file %q (%s: %q) is not under paths.libraryRoots %q",
				rec.Library.Path, t.App, rec.Library.AppPath, p.cfg.Paths.LibraryRoots),
		})
	}
	args, err := json.Marshal(media.LibraryProbeArgs{Path: rec.Library.Path, FileID: rec.Library.FileID, TrackedSize: rec.Library.Size})
	if err != nil {
		return err
	}
	if err := p.queue.Submit(ctx, spool.Task{ID: id, Kind: spool.KindLibraryProbe, Job: job.ID, Args: args}); err != nil {
		return err
	}
	if why, down := p.workerDown(); down {
		return p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: runner.CodeWorkerUnavailable, ErrorText: why})
	}
	return nil
}

func (p *Proposals) discardLibraryProbe(ctx context.Context, id string) error {
	if err := p.queue.Cancel(ctx, id); err != nil {
		return err
	}
	_, ok, err := p.queue.Result(ctx, id)
	if err != nil || !ok {
		return err
	}
	return p.queue.Ack(ctx, id)
}

// useLibraryProbe uses the library probe's result (see the list above).
func (p *Proposals) useLibraryProbe(ctx context.Context, job store.Job, t store.Target, rec Flow2, res spool.Result) error {
	fail := func(code, text string) error {
		if err := p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: code, ErrorText: text}); err != nil {
			return err
		}
		return p.queue.Ack(ctx, res.ID)
	}
	if res.Status != spool.StatusOK {
		code, text := runner.CodeTaskFailed, "no error given"
		if res.Error != nil {
			if res.Error.Code == spool.CodeTimeout {
				code = runner.CodeTaskTimeout
			}
			text = res.Error.Code + ": " + res.Error.Message
		}
		return fail(code, res.Kind+" task "+res.ID+": "+text)
	}
	var out media.LibraryProbeOutput
	if err := json.Unmarshal(res.Output, &out); err != nil {
		return fail(runner.CodeTaskFailed, res.Kind+" task "+res.ID+": unreadable output: "+err.Error())
	}
	// A result for another file: another path, or the same path with another tracked file
	// (the film was matched again after Radarr replaced its file). A worker that does not
	// echo the file id or size (zero) is not compared on them.
	if out.Path != rec.Library.Path ||
		(out.FileID != 0 && out.FileID != rec.Library.FileID) ||
		(out.TrackedSize != 0 && out.TrackedSize != rec.Library.Size) {
		p.log.Info("task_discarded", "job", job.ID, "kind", res.Kind, "task", res.ID, "note", "a probe of another library file")
		return p.queue.Ack(ctx, res.ID)
	}
	if !out.OK || out.Media == nil {
		why := out.Refusal
		if why == "" {
			why = media.RefusalUnreadable
		}
		return fail(CodeLibraryUnreadable, fmt.Sprintf("the worker could not probe the library file %q: %s", out.Path, why))
	}
	if err := staleProbe(rec, *out.Media); err != nil {
		if err := p.rescan(ctx, job, t, err); err != nil {
			return err
		}
		return p.queue.Ack(ctx, res.ID)
	}
	stored, err := p.storeLibraryProbe(ctx, job, t, rec, *out.Media)
	if err != nil || !stored {
		return err // not stored: the job changed meanwhile, and the next pass sees how
	}
	if err := p.queue.Ack(ctx, res.ID); err != nil {
		return err
	}
	return p.flow2Ready(ctx, job, t, *out.Media)
}

// staleProbe is an error when the probe cannot be of the file Radarr tracked at the match:
// the size differs, so a file was replaced under the same name between the match and the
// probe (or the stored probe's). The job goes back to probed (see rescan). A tracked
// size of 0 (unknown) is not compared.
func staleProbe(rec Flow2, probe media.Probe) error {
	if want := rec.Library.Size; want > 0 && probe.Size != want {
		return fmt.Errorf("the library file %q changed since the match (tracked size %d, probed size %d): rescan the film in Radarr, then match it again",
			rec.Library.Path, want, probe.Size)
	}
	return nil
}

// rescan sends a matched job whose library probe is stale back to probed, without its
// target (and so without the stale probe), so the user matches the film again and Radarr
// is asked for the tracked file again. A failed job would resume in matched and keep the
// old record. The reason is the transition's note. Nothing happens when the job changed
// meanwhile.
func (p *Proposals) rescan(ctx context.Context, job store.Job, t store.Target, why error) error {
	defer p.Lock(job.ID)()
	cur, err := p.st.Job(ctx, job.ID)
	if err != nil {
		return err
	}
	tg, err := p.st.Target(ctx, job.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if cur.State != store.Matched || tg.App != t.App || tg.TitleID != t.TitleID || !tg.MatchedAt.Equal(t.MatchedAt) {
		return nil
	}
	_, err = p.st.ReplaceTarget(ctx, job.ID, store.Matched, store.Probed, nil, store.Change{Actor: actor, Note: why.Error()})
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrInvalidTransition) {
		return nil
	}
	if err != nil {
		return err
	}
	p.log.Info("job_state", "job", job.ID, "from", store.Matched, "to", store.Probed, "note", why.Error())
	return nil
}

// storeLibraryProbe writes the probe into the target's flow 2 record, under the job's lock
// (the match flow holds it for its own write), while the job is still matched to the same
// title and library file. stored is false when it is not.
func (p *Proposals) storeLibraryProbe(ctx context.Context, job store.Job, t store.Target, rec Flow2, probe media.Probe) (stored bool, err error) {
	defer p.Lock(job.ID)()
	cur, err := p.st.Job(ctx, job.ID)
	if err != nil {
		return false, err
	}
	if cur.State != store.Matched {
		return false, nil
	}
	tg, err := p.st.Target(ctx, job.ID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	now, ok := Flow2Of(tg)
	if !ok || tg.App != t.App || tg.TitleID != t.TitleID || !tg.MatchedAt.Equal(t.MatchedAt) || now.Library.Path != rec.Library.Path {
		return false, nil
	}
	now.Library.Probe = &probe
	if tg.Options, err = withOption(tg.Options, "flow2", now); err != nil {
		return false, err
	}
	if err := p.st.SetTarget(ctx, job.ID, tg); err != nil {
		return false, err
	}
	return true, nil
}

// flow2Ready moves a flow 2 job whose library file is probed to awaiting-confirmation,
// with the finding in the transition's note.
func (p *Proposals) flow2Ready(ctx context.Context, job store.Job, t store.Target, library media.Probe) error {
	files, err := p.st.Files(ctx, job.ID)
	if err != nil {
		return err
	}
	_, up, ok := Flow2Upload(files)
	if !ok {
		return p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: CodeProposalFailed, ErrorText: "flow 2: the job has no video to compare"})
	}
	c := CompareProbes(library, up, p.cfg.Match)
	p.log.Info("flow2_compared", "job", job.ID, "finding", c.Finding, "deltaMs", c.DeltaMs, "ratio", c.Ratio)
	return p.move(ctx, job, &t, store.AwaitingConfirmation, store.Change{Note: flow2Note(c)})
}

func flow2Note(c Comparison) string {
	switch c.Finding {
	case FindingMatch:
		return fmt.Sprintf("flow 2: the same cut as the library file (%+d ms)", c.DeltaMs)
	case FindingPAL:
		return fmt.Sprintf("flow 2: a PAL speed-up of the library file (ratio %.4f)", c.Ratio)
	}
	return fmt.Sprintf("flow 2: a different cut from the library file (%+d ms)", c.DeltaMs)
}

// heartbeatDown reports whether the worker's heartbeat is missing or older than
// worker.heartbeatMaxAge, and why: the runner's own check.
func (p *Proposals) heartbeatDown() (string, bool) {
	limit := p.cfg.Worker.HeartbeatMaxAge.D()
	age, err := worker.HeartbeatAge(p.cfg.Worker.SpoolDir, p.now())
	if err != nil {
		return "no readable worker heartbeat: " + err.Error(), true
	}
	if age > limit {
		return fmt.Sprintf("the worker's heartbeat is %s old (limit %s)", age.Round(time.Second), limit), true
	}
	return "", false
}
