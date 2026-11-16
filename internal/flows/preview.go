package flows

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/preview"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

// Flow 2a's sync check (#53, plan §8): before a merge, the user watches short clips of the
// library file's video with the upload's audio, at the start, the middle and the end, and
// shifts the audio by an offset until the flash and the beep (or the lips and the voice)
// agree. The worker renders the clips (task kind preview, package preview); the web
// process plans them, keeps the request in the job's target, and streams the files.
//
// A request (RequestPreview) is the offset and the upload's audio track. It is stored in
// Target.Options["preview"] (the Preview record) before anything is submitted, with a new
// request id, and it replaces the earlier one: the earlier clips are no longer served, and
// the worker removes them when it renders the new ones. The last request's offset and track
// are what the merge (#55) uses: PreviewOf. A request is refused for a job that is not flow
// 2 (CodeNotFlow2), not awaiting confirmation (wrong_state), or whose upload cannot be
// merged (CodeMergeNotOffered: the finding is not a match, or merge.enabled is off).
//
// The tasks. Each request takes the next of the job's runner.PreviewSlots task ids
// (Preview.Seq), so a result always belongs to the request of its slot, task errors
// included. The earlier request's slot is listed in Preview.Earlier: its task is cancelled
// (a waiting one is removed, a running one stopped) and its result acknowledged when it
// comes; a slot without a result is forgotten after previewGrace (a waiting task that
// Cancel removed leaves none), and any later result is acknowledged when the slot comes
// round again, or when the job is discarded (runner.Discard covers every slot). A rematch
// that drops the record (another title or library file) and an unmatch cancel its tasks,
// current and earlier (Matcher, under the job's lock); a job without a record has its slots
// swept for the results they leave, at most once a minute (sweepOrphans).
//
// The step (advancePreview) runs under the job's lock, from the request and from the
// runner's pass over jobs awaiting confirmation (Previews), reads the job again under the
// lock and does nothing once it is no longer awaiting confirmation, and is safe to repeat. For a
// pending request it first clears its slot (cancel, acknowledge what an earlier occupant
// left), submits the task, and records that it did (Preview.Submitted); a crash between
// the two submits it again. Then the slot's result is the request's: it is stored in the
// record (ready with the clips, or failed with a code) and then acknowledged. A cancelled
// result (only the job's end or a re-submission cancels a current task) or one that names
// another request is acknowledged and the task submitted again.

// Refusal codes of a preview request.
const (
	CodeNotFlow2        = "not_flow2"         // the job is not flow 2
	CodeMergeNotOffered = "merge_not_offered" // the upload cannot be merged (Flow2View.notOffered says why)
	CodeNoAudio         = "no_audio"          // the upload has no audio track
)

// Preview states.
const (
	PreviewPending = "pending" // the worker has not rendered the clips yet
	PreviewReady   = "ready"
	PreviewFailed  = "failed" // ErrorCode says why; a new request tries again
)

// previewGrace is how long an earlier request's slot is watched for the result of its
// cancelled task. A running task stops within seconds of the cancel.
const previewGrace = 10 * time.Minute

// Preview is the job's last preview request (Target.Options["preview"]).
type Preview struct {
	Request string `json:"request"` // preview.ValidRequest; names the clip files
	// OffsetMs delays the upload's audio against the library file's video (negative:
	// advances it), as mkvmerge's --sync does.
	OffsetMs int64 `json:"offsetMs"`
	// AudioTrack is the upload's audio track: its ffprobe stream index (media.AudioTrack.Index).
	AudioTrack   int            `json:"audioTrack"`
	UploadFileID int64          `json:"uploadFileId"` // the job file the audio comes from (Flow2Upload)
	Planned      []preview.Clip `json:"planned"`      // the clips asked for
	State        string         `json:"state"`
	ErrorCode    string         `json:"errorCode,omitempty"` // when failed: a preview refusal code, task_failed or task_timeout
	ErrorText    string         `json:"errorText,omitempty"` // technical detail, for admins
	// Clips are the rendered clips, when ready: preview.Dir/preview.ClipName(Request, N).
	Clips       []preview.ClipFile `json:"clips,omitempty"`
	RequestedBy string             `json:"requestedBy"`
	RequestedAt time.Time          `json:"requestedAt"`

	// Seq counts the job's requests (1 for the first); the request's task is in slot
	// previewSlot(Seq).
	Seq int `json:"seq"`
	// Submitted: the request's task has been submitted (it may have ended since).
	Submitted bool `json:"submitted,omitempty"`
	// Earlier are the slots of earlier requests whose cancelled task may still leave a result.
	Earlier []EarlierPreview `json:"earlier,omitempty"`
}

// EarlierPreview is the slot of an earlier request, cancelled at Since.
type EarlierPreview struct {
	Slot  int       `json:"slot"`
	Since time.Time `json:"since"`
}

func previewSlot(seq int) int { return (max(seq, 1)-1)%runner.PreviewSlots + 1 }

// PreviewOf returns the job's preview request; ok is false when there is none.
func PreviewOf(t store.Target) (Preview, bool) {
	var opts struct {
		Preview *Preview `json:"preview"`
	}
	if len(t.Options) > 0 {
		_ = json.Unmarshal(t.Options, &opts)
	}
	if opts.Preview == nil {
		return Preview{}, false
	}
	return *opts.Preview, true
}

// PreviewRequest is the body of POST /api/jobs/{id}/preview. Both fields are optional:
// offsetMs defaults to 0, audioTrack to the upload's default audio track (else its first).
type PreviewRequest struct {
	OffsetMs   *int64 `json:"offsetMs"`
	AudioTrack *int   `json:"audioTrack"`
}

// RequestPreview makes req the job's preview request and starts the clips. Asking again
// for the offset and track of a request that is pending or ready changes nothing; created
// is then false. The caller audits a created request. With created true and an error, the
// request is stored and the runner's next pass submits it.
func (p *Proposals) RequestPreview(ctx context.Context, user string, job store.Job, req PreviewRequest) (pv Preview, created bool, err error) {
	if p.queue == nil {
		return Preview{}, false, errors.New("preview: the proposal flow has no spool queue")
	}
	var offset int64
	if req.OffsetMs != nil {
		offset = *req.OffsetMs
	}
	if offset < -preview.MaxOffsetMs || offset > preview.MaxOffsetMs {
		return Preview{}, false, refuse(CodeBadRequest, "offsetMs must be from %d to %d.", -preview.MaxOffsetMs, preview.MaxOffsetMs)
	}
	pv, created, err = p.storeRequest(ctx, user, job.ID, offset, req.AudioTrack)
	if err != nil || !created {
		return pv, false, err
	}
	p.log.Info("preview_requested", "job", job.ID, "request", pv.Request, "offsetMs", pv.OffsetMs, "audioTrack", pv.AudioTrack, "clips", len(pv.Planned))
	return pv, true, p.advancePreview(ctx, job.ID)
}

// storeRequest checks the request and stores it as the job's preview record, under the
// job's lock.
func (p *Proposals) storeRequest(ctx context.Context, user, jobID string, offset int64, track *int) (Preview, bool, error) {
	defer p.Lock(jobID)()
	job, err := p.st.Job(ctx, jobID)
	if err != nil {
		return Preview{}, false, err
	}
	t, err := p.st.Target(ctx, jobID)
	if errors.Is(err, store.ErrNotFound) {
		return Preview{}, false, notFlow2()
	}
	if err != nil {
		return Preview{}, false, err
	}
	rec, ok := Flow2Of(t)
	if !ok {
		return Preview{}, false, notFlow2()
	}
	if job.State != store.AwaitingConfirmation || rec.Library.Probe == nil {
		return Preview{}, false, wrongState()
	}
	files, err := p.st.Files(ctx, jobID)
	if err != nil {
		return Preview{}, false, err
	}
	v := ViewFlow2(p.cfg, t, files)
	if v == nil || !slices.Contains(v.Offers, OfferMerge) {
		return Preview{}, false, refuse(CodeMergeNotOffered, "The upload cannot be merged into the library file, so there is nothing to check.")
	}
	f, up, _ := Flow2Upload(files)
	audio, err := audioTrack(up, track)
	if err != nil {
		return Preview{}, false, err
	}
	old, hadOld := PreviewOf(t)
	if hadOld && old.OffsetMs == offset && old.AudioTrack == audio &&
		old.UploadFileID == f.ID && (old.State == PreviewPending || old.State == PreviewReady) {
		return old, false, nil
	}
	planned := preview.Plan(CutDuration(*rec.Library.Probe), p.cfg.Merge.PreviewClips, p.cfg.Merge.PreviewClipLength.D())
	if len(planned) == 0 {
		return Preview{}, false, refuse(CodeMergeNotOffered, "The library file has no duration to take clips from.")
	}
	now := p.now().UTC()
	pv := Preview{
		Request: newRequestID(), OffsetMs: offset, AudioTrack: audio, UploadFileID: f.ID,
		Planned: planned, State: PreviewPending, RequestedBy: user, RequestedAt: now,
		Seq: old.Seq + 1,
	}
	// The earlier request's task stops (the step cancels its slot). Its own slot is cleared
	// before the new task goes into it, so it is not watched twice.
	slot := previewSlot(pv.Seq)
	for _, e := range old.Earlier {
		if e.Slot != slot {
			pv.Earlier = append(pv.Earlier, e)
		}
	}
	if hadOld && old.State == PreviewPending && previewSlot(old.Seq) != slot {
		pv.Earlier = append(pv.Earlier, EarlierPreview{Slot: previewSlot(old.Seq), Since: now})
	}
	if t.Options, err = withOption(t.Options, "preview", pv); err != nil {
		return Preview{}, false, err
	}
	if err := p.st.SetTarget(ctx, jobID, t); err != nil {
		return Preview{}, false, err
	}
	return pv, true, nil
}

func notFlow2() error {
	return refuse(CodeNotFlow2, "The job is not another language of a film already in the library, so it has no preview.")
}

// audioTrack picks the upload's audio track: the one asked for (by stream index), else the
// default track, else the first.
func audioTrack(up media.Probe, asked *int) (int, error) {
	if len(up.Audio) == 0 {
		return 0, refuse(CodeNoAudio, "The upload has no audio track.")
	}
	if asked != nil {
		for _, a := range up.Audio {
			if a.Index == *asked {
				return a.Index, nil
			}
		}
		return 0, refuse(CodeBadRequest, "audioTrack %d is not an audio track of the upload (flow2.upload.audio lists them by index).", *asked)
	}
	for _, a := range up.Audio {
		if a.Default {
			return a.Index, nil
		}
	}
	return up.Audio[0].Index, nil
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Previews is the runner's step for jobs awaiting confirmation: it takes in the result of
// a flow 2 job's preview, or submits the task (see the top of this file). Other jobs are
// left alone.
func (p *Proposals) Previews(ctx context.Context, job store.Job) error {
	if p.queue == nil {
		return nil
	}
	return p.advancePreview(ctx, job.ID)
}

// advancePreview is one pass of the step for a job, under its lock.
func (p *Proposals) advancePreview(ctx context.Context, jobID string) error {
	defer p.Lock(jobID)()
	// The job may have moved on since the caller looked (the user cancelled it, say).
	job, err := p.st.Job(ctx, jobID)
	if err != nil || job.State != store.AwaitingConfirmation {
		return err
	}
	t, err := p.st.Target(ctx, jobID)
	if errors.Is(err, store.ErrNotFound) {
		return p.sweepOrphans(ctx, jobID)
	}
	if err != nil {
		return err
	}
	pv, ok := PreviewOf(t)
	if _, isFlow2 := Flow2Of(t); !ok || !isFlow2 {
		return p.sweepOrphans(ctx, jobID)
	}
	save := func(ctx context.Context) error {
		var err error
		if t.Options, err = withOption(t.Options, "preview", pv); err != nil {
			return err
		}
		return p.st.SetTarget(ctx, jobID, t)
	}

	// Earlier requests: their tasks stop, and what they leave is dropped.
	earlier := len(pv.Earlier)
	var keep []EarlierPreview
	for _, e := range pv.Earlier {
		done, err := p.clearSlot(ctx, jobID, e.Slot)
		if err != nil {
			return err
		}
		if !done && p.now().Sub(e.Since) < previewGrace {
			keep = append(keep, e)
		}
	}
	pv.Earlier = keep
	if pv.State != PreviewPending {
		if len(keep) != earlier {
			return save(ctx)
		}
		return nil
	}

	id := runner.PreviewID(jobID, previewSlot(pv.Seq))
	if !pv.Submitted {
		// The slot's earlier occupant (PreviewSlots requests ago) goes first.
		if _, err := p.clearSlot(ctx, jobID, previewSlot(pv.Seq)); err != nil {
			return err
		}
		args, failed := p.previewArgs(ctx, jobID, t, pv)
		if failed != nil {
			pv.State, pv.ErrorCode, pv.ErrorText = failed.State, failed.ErrorCode, failed.ErrorText
			p.logPreview(jobID, pv.Request, pv)
			return save(ctx)
		}
		data, err := json.Marshal(args)
		if err != nil {
			return err
		}
		if err := p.queue.Submit(ctx, spool.Task{ID: id, Kind: spool.KindPreview, Job: jobID, Args: data}); err != nil {
			return err
		}
		pv.Submitted = true
		// The task is in the spool: recording that must not be lost to a client that went
		// away (the request's context), as the match's write is not.
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
		defer cancel()
		return save(wctx)
	}
	res, got, err := p.queue.Result(ctx, id)
	if err != nil || !got {
		if err == nil && len(keep) != earlier {
			return save(ctx)
		}
		return err
	}
	outcome, again := previewOutcome(res, pv)
	if again {
		p.log.Info("task_discarded", "job", jobID, "kind", res.Kind, "task", id, "status", res.Status, "note", "submitted again")
		pv.Submitted = false
	} else {
		pv.State, pv.ErrorCode, pv.ErrorText, pv.Clips = outcome.State, outcome.ErrorCode, outcome.ErrorText, outcome.Clips
		p.logPreview(jobID, pv.Request, pv)
	}
	if err := save(ctx); err != nil {
		return err
	}
	return p.queue.Ack(ctx, id)
}

// orphanSweepEvery is how often, at most, a job without a preview record has its slots
// looked at for orphaned results.
const orphanSweepEvery = time.Minute

// orphanSweeps remembers when each job's slots were last looked at. It is pacing only and
// forgets everything now and then.
type orphanSweeps struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// due reports whether job's slots are to be looked at now, and if so marks them looked at.
func (o *orphanSweeps) due(job string, now time.Time) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if t, ok := o.last[job]; ok && now.Sub(t) < orphanSweepEvery {
		return false
	}
	if o.last == nil || len(o.last) > 10_000 {
		o.last = map[string]time.Time{}
	}
	o.last[job] = now
	return true
}

// sweepOrphans acknowledges the results in the job's preview slots when it has no preview
// record: a rematch or an unmatch dropped the record and cancelled its tasks (Matcher),
// and a task that was running leaves its result a little later. At most once every
// orphanSweepEvery per job.
func (p *Proposals) sweepOrphans(ctx context.Context, jobID string) error {
	if !p.orphans.due(jobID, p.now()) {
		return nil
	}
	for slot := 1; slot <= runner.PreviewSlots; slot++ {
		id := runner.PreviewID(jobID, slot)
		res, got, err := p.queue.Result(ctx, id)
		if err != nil {
			return err
		}
		if got {
			p.log.Info("task_discarded", "job", jobID, "kind", res.Kind, "task", id, "status", res.Status, "note", "a preview whose request was dropped")
			if err := p.queue.Ack(ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// cancelPreviews cancels the tasks of the preview record of t, which is about to be
// dropped with t's options (a rematch to another title or file, an unmatch): the current
// request's slot and the earlier ones. Their results are acknowledged later
// (sweepOrphans, or runner.Discard at the job's end). Without a record it does nothing.
func cancelPreviews(ctx context.Context, q spool.Queue, jobID string, t store.Target) error {
	pv, ok := PreviewOf(t)
	if !ok || q == nil {
		return nil
	}
	slots := []int{previewSlot(pv.Seq)}
	for _, e := range pv.Earlier {
		slots = append(slots, e.Slot)
	}
	for _, slot := range slots {
		if err := q.Cancel(ctx, runner.PreviewID(jobID, slot)); err != nil {
			return err
		}
	}
	return nil
}

// clearSlot cancels the task in a slot and acknowledges its result. done reports that the
// slot is empty for good: it had a result. (Without one, its task was waiting, and Cancel
// removed it, or is running and ends soon; Queue cannot tell which.)
func (p *Proposals) clearSlot(ctx context.Context, jobID string, slot int) (done bool, err error) {
	id := runner.PreviewID(jobID, slot)
	if err := p.queue.Cancel(ctx, id); err != nil {
		return false, err
	}
	res, got, err := p.queue.Result(ctx, id)
	if err != nil || !got {
		return false, err
	}
	p.log.Info("task_discarded", "job", jobID, "kind", res.Kind, "task", id, "status", res.Status, "note", "a preview of an earlier request")
	return true, p.queue.Ack(ctx, id)
}

func (p *Proposals) logPreview(jobID, request string, o Preview) {
	if o.State == PreviewReady {
		p.log.Info("preview_ready", "job", jobID, "request", request, "clips", len(o.Clips))
		return
	}
	p.log.Warn("preview_failed", "job", jobID, "request", request, "code", o.ErrorCode, "detail", o.ErrorText)
}

// previewArgs builds the task's args from the record and the job as it is now. failed is
// the outcome when the files are no longer there to render from.
func (p *Proposals) previewArgs(ctx context.Context, jobID string, t store.Target, pv Preview) (preview.Args, *Preview) {
	fail := func(code, text string) (preview.Args, *Preview) {
		return preview.Args{}, &Preview{State: PreviewFailed, ErrorCode: code, ErrorText: text}
	}
	rec, _ := Flow2Of(t)
	if _, _, in := media.LibraryPath(p.cfg.Paths.LibraryRoots, rec.Library.Path); !in {
		return fail(preview.RefusalOutsideLibrary, fmt.Sprintf("the library file %q is not under paths.libraryRoots %q", rec.Library.Path, p.cfg.Paths.LibraryRoots))
	}
	files, err := p.st.Files(ctx, jobID)
	if err != nil {
		return fail(runner.CodeTaskFailed, "the job's files: "+err.Error())
	}
	i := slices.IndexFunc(files, func(f store.File) bool { return f.ID == pv.UploadFileID })
	if i < 0 || files[i].Status != store.FileOK || !strings.HasPrefix(files[i].Path, jobdir.Files+"/") {
		return fail(preview.RefusalUploadUnreadable, fmt.Sprintf("the job has no accepted file %d", pv.UploadFileID))
	}
	return preview.Args{
		Request: pv.Request, Library: rec.Library.Path, Upload: files[i].Path,
		AudioIndex: pv.AudioTrack, OffsetMs: pv.OffsetMs, Clips: pv.Planned,
	}, nil
}

// previewOutcome reads the result in the request's slot: what the record becomes, or
// again when the task is to be submitted once more (it was cancelled, or it rendered
// another request: an occupant of the slot from PreviewSlots requests ago).
func previewOutcome(res spool.Result, pv Preview) (outcome Preview, again bool) {
	failed := func(code, text string) (Preview, bool) {
		return Preview{State: PreviewFailed, ErrorCode: code, ErrorText: text}, false
	}
	if res.Status != spool.StatusOK {
		code, text := runner.CodeTaskFailed, "no error given"
		if res.Error != nil {
			if res.Error.Code == spool.CodeCancelled {
				return Preview{}, true
			}
			if res.Error.Code == spool.CodeTimeout {
				code = runner.CodeTaskTimeout
			}
			text = res.Error.Code + ": " + res.Error.Message
		}
		return failed(code, res.Kind+" task "+res.ID+": "+text)
	}
	var out preview.Output
	if err := json.Unmarshal(res.Output, &out); err != nil {
		return failed(runner.CodeTaskFailed, res.Kind+" task "+res.ID+": unreadable output: "+err.Error())
	}
	if out.Request != pv.Request {
		return Preview{}, true
	}
	if !out.OK {
		code := out.Refusal
		if code == "" {
			code = preview.RefusalRenderFailed
		}
		return failed(code, out.Detail)
	}
	if len(out.Clips) != len(pv.Planned) {
		return failed(runner.CodeTaskFailed, fmt.Sprintf("%d clips rendered, %d asked for", len(out.Clips), len(pv.Planned)))
	}
	for i, c := range out.Clips {
		if c.N != i+1 || c.Size <= 0 {
			return failed(runner.CodeTaskFailed, fmt.Sprintf("clip %d of the output is malformed", i+1))
		}
	}
	return Preview{State: PreviewReady, Clips: out.Clips}, false
}
