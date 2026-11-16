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
// The task. One spool id serves every request of the job (runner.PreviewID). A request
// cancels the task (a waiting one is removed, a running one stopped) and acknowledges the
// result it left; then the step below submits it again with the new request's args. So
// at most one preview of a job runs at a time, whatever the user sends.
//
// The step (Previews, run for every job awaiting confirmation) is safe to repeat. A result
// is the request's when its output echoes the request id; a result without output (a task
// error) is the request's unless it was cancelled or started before the request was made.
// The request's result is stored in the record (ready with the clips, or failed with a
// code), then acknowledged. Any other result is acknowledged and dropped. While the
// request is pending and there is no result, the task is submitted (Submit does nothing
// while it is waiting or running).

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
}

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
// is then false. The caller audits a created request.
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
	t, pv, created, err := p.storeRequest(ctx, user, job.ID, offset, req.AudioTrack)
	if err != nil || !created {
		return pv, false, err
	}
	p.log.Info("preview_requested", "job", job.ID, "request", pv.Request, "offsetMs", pv.OffsetMs, "audioTrack", pv.AudioTrack, "clips", len(pv.Planned))
	// The earlier request's task stops; its result, if any, is dropped by the step.
	if err := p.queue.Cancel(ctx, runner.PreviewID(job.ID)); err != nil {
		return pv, true, err
	}
	return pv, true, p.advancePreview(ctx, job, t)
}

// storeRequest checks the request and stores it as the job's preview record, under the
// job's lock. It returns the target as stored.
func (p *Proposals) storeRequest(ctx context.Context, user, jobID string, offset int64, track *int) (store.Target, Preview, bool, error) {
	defer p.Lock(jobID)()
	job, err := p.st.Job(ctx, jobID)
	if err != nil {
		return store.Target{}, Preview{}, false, err
	}
	t, err := p.st.Target(ctx, jobID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Target{}, Preview{}, false, notFlow2()
	}
	if err != nil {
		return store.Target{}, Preview{}, false, err
	}
	rec, ok := Flow2Of(t)
	if !ok {
		return store.Target{}, Preview{}, false, notFlow2()
	}
	if job.State != store.AwaitingConfirmation || rec.Library.Probe == nil {
		return store.Target{}, Preview{}, false, wrongState()
	}
	files, err := p.st.Files(ctx, jobID)
	if err != nil {
		return store.Target{}, Preview{}, false, err
	}
	v := ViewFlow2(p.cfg, t, files)
	if v == nil || !slices.Contains(v.Offers, OfferMerge) {
		return store.Target{}, Preview{}, false, refuse(CodeMergeNotOffered, "The upload cannot be merged into the library file, so there is nothing to check.")
	}
	f, up, _ := Flow2Upload(files)
	audio, err := audioTrack(up, track)
	if err != nil {
		return store.Target{}, Preview{}, false, err
	}
	if old, ok := PreviewOf(t); ok && old.OffsetMs == offset && old.AudioTrack == audio &&
		old.UploadFileID == f.ID && (old.State == PreviewPending || old.State == PreviewReady) {
		return t, old, false, nil
	}
	planned := preview.Plan(CutDuration(*rec.Library.Probe), p.cfg.Merge.PreviewClips, p.cfg.Merge.PreviewClipLength.D())
	if len(planned) == 0 {
		return store.Target{}, Preview{}, false, refuse(CodeMergeNotOffered, "The library file has no duration to take clips from.")
	}
	pv := Preview{
		Request: newRequestID(), OffsetMs: offset, AudioTrack: audio, UploadFileID: f.ID,
		Planned: planned, State: PreviewPending, RequestedBy: user, RequestedAt: p.now().UTC(),
	}
	if t.Options, err = withOption(t.Options, "preview", pv); err != nil {
		return store.Target{}, Preview{}, false, err
	}
	if err := p.st.SetTarget(ctx, jobID, t); err != nil {
		return store.Target{}, Preview{}, false, err
	}
	return t, pv, true, nil
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
	t, err := p.st.Target(ctx, job.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, ok := Flow2Of(t); !ok {
		return nil
	}
	return p.advancePreview(ctx, job, t)
}

// advancePreview is one pass of the step for a flow 2 job with target t.
func (p *Proposals) advancePreview(ctx context.Context, job store.Job, t store.Target) error {
	id := runner.PreviewID(job.ID)
	pv, has := PreviewOf(t)
	pending := has && pv.State == PreviewPending
	res, got, err := p.queue.Result(ctx, id)
	if err != nil {
		return err
	}
	if got {
		if pending {
			if outcome, mine := previewOutcome(res, pv); mine {
				if err := p.finishPreview(ctx, job.ID, pv.Request, outcome); err != nil {
					return err
				}
				p.logPreview(job.ID, pv.Request, outcome)
				return p.queue.Ack(ctx, id)
			}
		}
		p.log.Info("task_discarded", "job", job.ID, "kind", res.Kind, "task", id, "status", res.Status, "note", "a preview of an earlier request")
		if err := p.queue.Ack(ctx, id); err != nil {
			return err
		}
	}
	if !pending {
		return nil
	}
	args, failed := p.previewArgs(ctx, job, t, pv)
	if failed != nil {
		if err := p.finishPreview(ctx, job.ID, pv.Request, *failed); err != nil {
			return err
		}
		p.logPreview(job.ID, pv.Request, *failed)
		return nil
	}
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return p.queue.Submit(ctx, spool.Task{ID: id, Kind: spool.KindPreview, Job: job.ID, Args: data})
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
func (p *Proposals) previewArgs(ctx context.Context, job store.Job, t store.Target, pv Preview) (preview.Args, *Preview) {
	fail := func(code, text string) (preview.Args, *Preview) {
		return preview.Args{}, &Preview{State: PreviewFailed, ErrorCode: code, ErrorText: text}
	}
	rec, _ := Flow2Of(t)
	if _, _, in := media.LibraryPath(p.cfg.Paths.LibraryRoots, rec.Library.Path); !in {
		return fail(preview.RefusalOutsideLibrary, fmt.Sprintf("the library file %q is not under paths.libraryRoots %q", rec.Library.Path, p.cfg.Paths.LibraryRoots))
	}
	files, err := p.st.Files(ctx, job.ID)
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

// previewOutcome reads a result of the preview task: what the record becomes, and whether
// the result is the request pv's at all (see the top of this file).
func previewOutcome(res spool.Result, pv Preview) (Preview, bool) {
	failed := func(code, text string) (Preview, bool) {
		return Preview{State: PreviewFailed, ErrorCode: code, ErrorText: text}, true
	}
	if res.Status != spool.StatusOK {
		code, text := runner.CodeTaskFailed, "no error given"
		if res.Error != nil {
			if res.Error.Code == spool.CodeCancelled {
				return Preview{}, false // only a newer request (or the job's end) cancels it
			}
			if res.Error.Code == spool.CodeTimeout {
				code = runner.CodeTaskTimeout
			}
			text = res.Error.Code + ": " + res.Error.Message
		}
		if res.Started.Before(pv.RequestedAt) {
			return Preview{}, false // it ran before this request was made
		}
		return failed(code, res.Kind+" task "+res.ID+": "+text)
	}
	var out preview.Output
	if err := json.Unmarshal(res.Output, &out); err != nil {
		if res.Started.Before(pv.RequestedAt) {
			return Preview{}, false
		}
		return failed(runner.CodeTaskFailed, res.Kind+" task "+res.ID+": unreadable output: "+err.Error())
	}
	if out.Request != pv.Request {
		return Preview{}, false
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
	return Preview{State: PreviewReady, Clips: out.Clips}, true
}

// finishPreview stores the outcome of request in the job's preview record, under the
// job's lock, while that request is still the job's and pending. Otherwise it changes
// nothing: the user asked again meanwhile, or the job was matched to another title.
func (p *Proposals) finishPreview(ctx context.Context, jobID, request string, outcome Preview) error {
	defer p.Lock(jobID)()
	t, err := p.st.Target(ctx, jobID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	pv, ok := PreviewOf(t)
	if !ok || pv.Request != request || pv.State != PreviewPending {
		return nil
	}
	pv.State, pv.ErrorCode, pv.ErrorText, pv.Clips = outcome.State, outcome.ErrorCode, outcome.ErrorText, outcome.Clips
	if t.Options, err = withOption(t.Options, "preview", pv); err != nil {
		return err
	}
	return p.st.SetTarget(ctx, jobID, t)
}
