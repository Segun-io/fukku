package runner

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/segun-io/fukku/internal/extract"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

// Task ids are derived, never stored: <job>-<attempt>-x<archive file id> for an extraction
// and <job>-<attempt>-p for the probe. A retry has a new attempt and so new ids.
func extractID(job store.Job, attempt int, fileID int64) string {
	return job.ID + "-" + strconv.Itoa(attempt) + "-x" + strconv.FormatInt(fileID, 10)
}

func probeID(job store.Job, attempt int) string {
	return job.ID + "-" + strconv.Itoa(attempt) + "-p"
}

// LibraryProbeID is the id of a flow 2 job's library probe (#52) in an attempt:
// <job>-<attempt>-l. The matched step of package flows submits it; it is here so that
// cancelling and discarding a job reach it too (taskIDs). One id serves every match of the
// attempt: the result names the file it probed, and a result for another file is dropped
// and the task submitted again.
func LibraryProbeID(jobID string, attempt int) string {
	return jobID + "-" + strconv.Itoa(attempt) + "-l"
}

// MergeID is the id of a flow 2a job's merge task (#55) in an attempt: <job>-<attempt>-m.
// Package flows submits it, with the destination MergeDest(attempt); it is here so that
// cancelling and discarding a job reach it too (taskIDs). A retry has a new attempt, so its
// merge has its own id and its own destination.
func MergeID(jobID string, attempt int) string {
	return jobID + "-" + strconv.Itoa(attempt) + "-m"
}

// MergeDest is the job-relative directory the merge task of an attempt writes its verified
// file into: merged/<attempt> (merge.Args.Dest).
func MergeDest(attempt int) string { return jobdir.Merged + "/" + strconv.Itoa(attempt) }

// PreviewSlots is how many task ids the sync-check clips of a flow 2 job (#53) take turns
// on. Each preview request takes the next slot, so a result always names the request it
// is for, and a slot comes round again only after this many requests. Package flows
// submits them; they are here so that cancelling and discarding a job reach every slot
// (taskIDs).
const PreviewSlots = 16

// PreviewID is the id of a flow 2 job's preview task in a slot (1 to PreviewSlots):
// <job>-v<slot>. It belongs to no attempt.
func PreviewID(jobID string, slot int) string { return jobID + "-v" + strconv.Itoa(slot) }

// extractScratch is where an extraction unpacks before its rename to the destination:
// work/extract-<attempt>-x<archive file id>. Each attempt has its own, so a retry that
// starts while the earlier attempt's extraction is still being cancelled cannot have its
// scratch directory removed by it, nor remove that one's.
func extractScratch(attempt int, fileID int64) string {
	return jobdir.Work + "/extract-" + strconv.Itoa(attempt) + "-x" + strconv.FormatInt(fileID, 10)
}

// uploaded starts a job's checks. On a retry it first undoes the earlier attempt: its
// tasks are cancelled and their results acknowledged, the rows of extracted files are
// deleted, and the files still on disk are pending again.
func (r *Runner) uploaded(ctx context.Context, job store.Job) error {
	if !jobdir.ValidID(job.ID) {
		return fmt.Errorf("malformed job id %q", job.ID)
	}
	if job.Attempt > 1 {
		if err := r.reset(ctx, job); err != nil {
			return err
		}
	}
	_, err := r.move(ctx, job, store.Probing, store.Change{})
	return err
}

func (r *Runner) reset(ctx context.Context, job store.Job) error {
	files, err := r.st.Files(ctx, job.ID)
	if err != nil {
		return err
	}
	if err := r.drain(ctx, job, files, 1, job.Attempt-1, false); err != nil {
		return err
	}
	root, err := jobdir.Open(r.cfg.Paths.IntakeRoot, job.ID)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if root != nil {
		defer func() { _ = root.Close() }()
	}
	// Lstat only looks at the directory entry: the web process never opens media.
	onDisk := func(p string) (bool, error) {
		if root == nil {
			return false, nil
		}
		_, err := root.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return err == nil, err
	}
	// The archives that are unpacked again: pending ones, and the others still on disk.
	// The decision stays the same when an interrupted reset runs again, because an
	// archive it already set back to pending is still on disk.
	again := map[string]bool{}
	for _, f := range files {
		if f.Kind != store.KindArchive {
			continue
		}
		ok := f.Status == store.FilePending
		if !ok {
			if ok, err = onDisk(f.Path); err != nil {
				return err
			}
		}
		again[f.Path] = ok
	}
	// What an earlier attempt extracted from them and moved into the job goes: the new
	// extraction is moved there again (seal), and never replaces a file.
	if root != nil {
		dests := extractDests(files)
		for _, f := range files {
			if f.Kind == store.KindArchive && again[f.Path] {
				if err := root.RemoveAll(dests[f.ID]); err != nil {
					return err
				}
			}
		}
	}
	for _, f := range files {
		if f.From != "" && again[f.From] { // its archive writes it again
			if err := r.st.DeleteFile(ctx, f.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			continue
		}
		if f.Status == store.FilePending {
			continue
		}
		if ok, err := onDisk(f.Path); err != nil {
			return err
		} else if !ok {
			continue // refused earlier and deleted: it stays refused
		}
		f.Status, f.Reason, f.Report = store.FilePending, "", nil
		if err := r.st.UpdateFile(ctx, f); err != nil {
			return err
		}
	}
	return nil
}

// probing does as much as it can without waiting: it extracts every pending archive, then
// probes every pending video and subtitle in one task, then decides. Each result is stored
// before it is acknowledged, so a result that is still there after a crash is used again,
// and using it again changes nothing.
func (r *Runner) probing(ctx context.Context, job store.Job) error {
	if !jobdir.ValidID(job.ID) {
		return fmt.Errorf("malformed job id %q", job.ID)
	}
	files, err := r.st.Files(ctx, job.ID)
	if err != nil {
		return err
	}
	// A task of the earlier attempt that was still running at the retry ends later.
	if err := r.drain(ctx, job, files, 1, job.Attempt-1, false); err != nil {
		return err
	}

	waiting, changed := false, false
	dests := extractDests(files)
	for _, f := range files {
		if f.Kind != store.KindArchive {
			continue
		}
		id := extractID(job, job.Attempt, f.ID)
		res, ok, err := r.q.Result(ctx, id)
		if err != nil {
			return err
		}
		if ok {
			stop, err := r.useExtract(ctx, job, f, dests[f.ID], res)
			if err != nil || stop {
				return err
			}
			changed = true
			continue
		}
		if f.Status != store.FilePending {
			continue
		}
		args := extract.Args{Archive: f.Path, Dest: dests[f.ID], Scratch: extractScratch(job.Attempt, f.ID)}
		if err := r.submit(ctx, job, id, spool.KindExtract, args); err != nil {
			return err
		}
		waiting = true
	}

	if !waiting { // every archive is done: probe what is pending
		if changed {
			if files, err = r.st.Files(ctx, job.ID); err != nil {
				return err
			}
		}
		id := probeID(job, job.Attempt)
		res, ok, err := r.q.Result(ctx, id)
		if err != nil {
			return err
		}
		if ok {
			stop, err := r.useProbe(ctx, job, files, res)
			if err != nil || stop {
				return err
			}
			if files, err = r.st.Files(ctx, job.ID); err != nil {
				return err
			}
		} else if paths := pendingMedia(files); len(paths) > 0 {
			if err := r.submit(ctx, job, id, spool.KindProbe, media.ProbeArgs{Paths: paths}); err != nil {
				return err
			}
			waiting = true
		}
	}

	if waiting {
		if why, down := r.workerDown(); down {
			_, err := r.move(ctx, job, store.Stalled, store.Change{ErrorCode: CodeWorkerUnavailable, ErrorText: why})
			return err
		}
		return nil
	}

	// Nothing is pending: the job is probed if at least one video passed.
	usable := slices.ContainsFunc(files, func(f store.File) bool {
		return f.Kind == store.KindVideo && f.Status == store.FileOK
	})
	if !usable {
		_, err := r.move(ctx, job, store.Failed, store.Change{ErrorCode: CodeNoUsableFiles, ErrorText: "no video passed the checks"})
		return err
	}
	expires := r.now().Add(r.cfg.Upload.JobRetention.D()).UTC()
	_, err = r.move(ctx, job, store.Probed, store.Change{ExpiresAt: &expires})
	return err
}

// useExtract stores an extraction's result: a pending row for every extracted file and the
// archive's status and report. A refused archive is deleted, except for lack of free space
// (see noRoom). stop means the job failed or stalled.
func (r *Runner) useExtract(ctx context.Context, job store.Job, archive store.File, dest string, res spool.Result) (stop bool, err error) {
	r.finished(job, res)
	if res.Status != spool.StatusOK {
		return true, r.failTask(ctx, job, res, "")
	}
	var out extract.Output
	if err := json.Unmarshal(res.Output, &out); err != nil {
		return true, r.failTask(ctx, job, res, "unreadable output: "+err.Error())
	}
	for _, e := range out.Files {
		if !under(e.Path, dest) {
			return true, r.failTask(ctx, job, res, fmt.Sprintf("output names %q, outside %q", e.Path, dest))
		}
	}
	if out.Refused == extract.RefusedFreeSpace && archive.Status == store.FilePending {
		return true, r.noRoom(ctx, job, archive, res)
	}
	if archive.Status == store.FilePending { // otherwise stored before a crash: only the ack is missing
		if out.Refused == "" {
			if err := r.seal(job, dest, out.Files); err != nil {
				return true, r.failTask(ctx, job, res, "the extracted files cannot be taken into the job: "+err.Error())
			}
			for _, e := range out.Files {
				_, err := r.st.AddFile(ctx, store.File{
					JobID: job.ID, Path: e.Path, Kind: r.kindOf(e.Path), Size: e.Size,
					Status: store.FilePending, From: archive.Path,
				})
				if err != nil && !errors.Is(err, store.ErrExists) {
					return true, err
				}
			}
			archive.Status, archive.Reason = store.FileOK, ""
		} else {
			archive.Status, archive.Reason = store.FileRefused, out.Refused
		}
		archive.Report = res.Output
		if err := r.st.UpdateFile(ctx, archive); err != nil {
			return true, err
		}
	}
	if archive.Status == store.FileRefused {
		if err := r.unlink(job, archive.Path); err != nil {
			return true, err
		}
	}
	return false, r.ack(ctx, res.ID)
}

// seal moves the files an extraction reported from the worker's directory of the job into
// the job's directory (jobdir.Seal: regular files of the reported size, reached without a
// symbolic link and with no other name), and then removes what is left of dest in the
// worker's directory. From then on the worker cannot change them, and they are what the
// probe reads and what Radarr or Sonarr import (#113, #114). It is safe to repeat after a
// crash: a file already moved is checked where it is now.
func (r *Runner) seal(job store.Job, dest string, files []extract.Entry) error {
	to, err := jobdir.Open(r.cfg.Paths.IntakeRoot, job.ID)
	if err != nil {
		return err
	}
	defer func() { _ = to.Close() }()
	from, err := jobdir.MakeWorker(r.cfg.Paths.IntakeRoot, job.ID)
	if err != nil {
		return err
	}
	defer func() { _ = from.Close() }()
	for _, e := range files {
		if err := jobdir.Seal(from, to, e.Path, e.Size); err != nil {
			return err
		}
	}
	if err := jobdir.SpillWorker(r.cfg.Paths.IntakeRoot, job.ID, dest); err != nil {
		r.log.Warn("extract_leftover", "job", job.ID, "dir", dest, "error", err)
	}
	return nil
}

// noRoom handles an archive refused for lack of free space. That says nothing about the
// archive, so nothing is stored and the archive is kept, pending: the job waits in
// stalled (free_space) until there is room, and the same task runs again.
//
// Here the result is acknowledged first and the job moved second. The result holds
// nothing worth keeping, so after a crash between the two the archive is simply still
// pending with no result, and the next pass submits its task again. The other order would
// leave a stale free_space result that stalls the job again each time it resumes.
func (r *Runner) noRoom(ctx context.Context, job store.Job, archive store.File, res spool.Result) error {
	if err := r.ack(ctx, res.ID); err != nil {
		return err
	}
	text := fmt.Sprintf("not enough free space to extract %s (upload.freeSpaceMargin %s)", archive.Path, r.cfg.Upload.FreeSpaceMargin)
	_, err := r.move(ctx, job, store.Stalled, store.Change{ErrorCode: CodeFreeSpace, ErrorText: text})
	return err
}

// roomFor reports whether the job's filesystem has room again for its pending archives:
// at least upload.freeSpaceMargin plus their size free, so that the extraction, which
// stops below the margin, does not stall again at once.
func (r *Runner) roomFor(ctx context.Context, job store.Job) (bool, error) {
	files, err := r.st.Files(ctx, job.ID)
	if err != nil {
		return false, err
	}
	need := int64(r.cfg.Upload.FreeSpaceMargin)
	for _, f := range files {
		if f.Kind == store.KindArchive && f.Status == store.FilePending {
			need += f.Size
		}
	}
	dir := jobdir.Dir(r.cfg.Paths.IntakeRoot, job.ID)
	if _, err := os.Lstat(dir); err != nil {
		dir = r.cfg.Paths.IntakeRoot
	}
	free, err := r.freeSpace(dir)
	if err != nil {
		return false, fmt.Errorf("free space: %w", err)
	}
	return free >= need, nil
}

// useProbe stores a probe's result: each file is ok with its probe as report, or refused
// with the refusal code and deleted. stop means the job failed.
func (r *Runner) useProbe(ctx context.Context, job store.Job, files []store.File, res spool.Result) (stop bool, err error) {
	r.finished(job, res)
	if res.Status != spool.StatusOK {
		return true, r.failTask(ctx, job, res, "")
	}
	var out media.ProbeOutput
	if err := json.Unmarshal(res.Output, &out); err != nil {
		return true, r.failTask(ctx, job, res, "unreadable output: "+err.Error())
	}
	byPath := make(map[string]media.ProbedFile, len(out.Files))
	for _, pf := range out.Files {
		byPath[pf.Path] = pf
	}
	for _, f := range files {
		if f.Kind == store.KindArchive {
			continue
		}
		pf, ok := byPath[f.Path]
		if !ok {
			if f.Status == store.FilePending {
				return true, r.failTask(ctx, job, res, fmt.Sprintf("output has no entry for %q", f.Path))
			}
			continue
		}
		if f.Status == store.FilePending { // otherwise stored before a crash
			f.Report = nil
			if pf.OK {
				f.Status, f.Reason = store.FileOK, ""
				if pf.Media != nil {
					data, err := json.Marshal(pf.Media)
					if err != nil {
						return true, err
					}
					f.Report = data
				}
			} else {
				f.Status, f.Reason = store.FileRefused, pf.Refusal
				if f.Reason == "" {
					f.Reason = media.RefusalUnreadable
				}
			}
			if err := r.st.UpdateFile(ctx, f); err != nil {
				return true, err
			}
		}
		if f.Status == store.FileRefused {
			if err := r.unlink(job, f.Path); err != nil {
				return true, err
			}
		}
	}
	return false, r.ack(ctx, res.ID)
}

// failTask fails the job for a task result that cannot be used, then acknowledges it and
// cancels the attempt's other tasks.
func (r *Runner) failTask(ctx context.Context, job store.Job, res spool.Result, why string) error {
	code, text := CodeTaskFailed, why
	if res.Error != nil {
		if res.Error.Code == spool.CodeTimeout {
			code = CodeTaskTimeout
		}
		text = res.Error.Code + ": " + res.Error.Message
	}
	text = res.Kind + " task " + res.ID + ": " + text
	moved, err := r.move(ctx, job, store.Failed, store.Change{ErrorCode: code, ErrorText: text})
	if err != nil || !moved {
		return err
	}
	if err := r.ack(ctx, res.ID); err != nil {
		return err
	}
	job.State = store.Failed
	return r.drainJob(ctx, job, job.Attempt, job.Attempt, false)
}

// stalled resumes a job that waited for the worker as soon as its heartbeat is fresh.
func (r *Runner) stalled(ctx context.Context, job store.Job) error {
	if job.Resume == "" {
		return nil
	}
	var note string
	switch job.ErrorCode {
	case CodeWorkerUnavailable:
		if _, down := r.workerDown(); down {
			return nil
		}
		note = "the worker is back"
	case CodeFreeSpace:
		room, err := r.roomFor(ctx, job)
		if err != nil || !room {
			return err
		}
		note = "there is free space again"
	default:
		res, ok := r.resumes[job.ErrorCode]
		if !ok {
			return nil // nothing registered for this code
		}
		ready, err := res.ready(ctx, job)
		if err != nil || !ready {
			return err
		}
		note = res.note
	}
	_, err := r.move(ctx, job, job.Resume, store.Change{Note: note})
	return err
}

// cancelled removes what a cancelled job left: its tasks and their results, its unfinished
// uploads and its directory, which goes last. A cancelled job whose directory and uploads
// are gone needs nothing more; a task that was still running ends with a result later,
// which the sweep acknowledges.
func (r *Runner) cancelled(ctx context.Context, job store.Job) error {
	if !jobdir.ValidID(job.ID) {
		return fmt.Errorf("malformed job id %q", job.ID)
	}
	root := r.cfg.Paths.IntakeRoot
	dir := jobdir.Dir(root, job.ID)
	uploads, err := r.st.Uploads(ctx, "", job.ID)
	if err != nil {
		return err
	}
	_, statErr := os.Lstat(dir)
	if errors.Is(statErr, fs.ErrNotExist) && len(uploads) == 0 {
		return nil
	}
	if err := r.drainJob(ctx, job, 1, job.Attempt, true); err != nil {
		return err
	}
	for _, u := range uploads {
		if jobdir.ValidID(u.ID) {
			if err := jobdir.RemoveUpload(root, u.ID); err != nil {
				return err
			}
		}
		if err := r.st.DeleteUpload(ctx, u.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	if err := jobdir.Remove(root, job.ID); err != nil {
		return err
	}
	r.log.Info("job_cleaned", "job", job.ID, "uploads", len(uploads))
	return nil
}

// drainJob is drain with the job's files.
func (r *Runner) drainJob(ctx context.Context, job store.Job, from, to int, previews bool) error {
	if from > to && !previews {
		return nil
	}
	files, err := r.st.Files(ctx, job.ID)
	if err != nil {
		return err
	}
	return r.drain(ctx, job, files, from, to, previews)
}

// drain cancels the job's tasks of the attempts from..to (with previews, its preview tasks
// too) and acknowledges the results they left. Tasks that never existed cost a lookup each.
func (r *Runner) drain(ctx context.Context, job store.Job, files []store.File, from, to int, previews bool) error {
	for _, id := range taskIDs(job, files, from, to, previews) {
		if err := r.q.Cancel(ctx, id); err != nil {
			return err
		}
		res, ok, err := r.q.Result(ctx, id)
		if err != nil {
			return err
		}
		if ok {
			r.log.Info("task_discarded", "job", job.ID, "kind", res.Kind, "task", id, "status", res.Status, "code", errCode(res))
			if err := r.ack(ctx, id); err != nil {
				return err
			}
		}
		delete(r.submitted, id)
	}
	return nil
}

// taskIDs are the ids of the tasks the runner may have submitted for the job in the
// attempts from..to: the probe, one extraction per archive, the library probe of flow 2 and
// the merge of flow 2a in each, and with previews the preview clips of flow 2 in every slot, which belong to no
// attempt. Only the job's end (cancel, the sweep, Discard) asks for those; a retry's drain
// of earlier attempts and a failed task's leave them to package flows.
func taskIDs(job store.Job, files []store.File, from, to int, previews bool) []string {
	var ids []string
	if previews {
		for slot := 1; slot <= PreviewSlots; slot++ {
			ids = append(ids, PreviewID(job.ID, slot))
		}
	}
	for a := from; a <= to; a++ {
		ids = append(ids, probeID(job, a), LibraryProbeID(job.ID, a), MergeID(job.ID, a))
		for _, f := range files {
			if f.Kind == store.KindArchive {
				ids = append(ids, extractID(job, a, f.ID))
			}
		}
	}
	return ids
}

// Discard cancels every spool task the runner may have submitted for the job, in all its
// attempts, and acknowledges the results they left. files are the job's files (the
// archives name the extraction tasks). It is safe to repeat. Retention (#14) calls it
// before it deletes a job, so that no task or result of the job stays in the spool.
//
// A task that was running when it was cancelled writes its result a little later, once
// the worker has stopped it, and only a later Discard acknowledges that one; Queue cannot
// tell whether such a result is still to come. A job that retention deletes is normally
// settled (probed, failed or cancelled long ago), and its tasks have ended.
func Discard(ctx context.Context, q spool.Queue, job store.Job, files []store.File) error {
	for _, id := range taskIDs(job, files, 1, max(job.Attempt, 1), true) {
		if err := q.Cancel(ctx, id); err != nil {
			return err
		}
		if _, ok, err := q.Result(ctx, id); err != nil {
			return err
		} else if ok {
			if err := q.Ack(ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runner) submit(ctx context.Context, job store.Job, id, kind string, args any) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	if err := r.q.Submit(ctx, spool.Task{ID: id, Kind: kind, Job: job.ID, Args: data}); err != nil {
		return err
	}
	if !r.submitted[id] { // Submit is repeated every pass; log it once per process
		r.submitted[id] = true
		r.log.Info("task_submitted", "job", job.ID, "kind", kind, "task", id, "attempt", job.Attempt)
	}
	return nil
}

func (r *Runner) ack(ctx context.Context, id string) error {
	if err := r.q.Ack(ctx, id); err != nil {
		return err
	}
	delete(r.submitted, id)
	return nil
}

func (r *Runner) finished(job store.Job, res spool.Result) {
	r.metrics.ObserveTask(res)
	attrs := []any{"job", job.ID, "kind", res.Kind, "task", res.ID, "status", res.Status,
		"duration", res.Finished.Sub(res.Started)}
	if res.Error != nil {
		attrs = append(attrs, "code", res.Error.Code, "error", res.Error.Message)
	}
	r.log.Info("task_finished", attrs...)
}

func errCode(res spool.Result) string {
	if res.Error == nil {
		return ""
	}
	return res.Error.Code
}

// unlink deletes a refused file through an os.Root of the job directory.
func (r *Runner) unlink(job store.Job, p string) error {
	root, err := jobdir.Open(r.cfg.Paths.IntakeRoot, job.ID)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// kindOf classifies an extracted file by its extension. Extraction only writes accepted
// extensions; anything else counts as a video, which the probe then refuses.
func (r *Runner) kindOf(p string) store.FileKind {
	ext := path.Ext(p)
	for _, e := range r.cfg.Upload.SubtitleExtensions {
		if strings.EqualFold(e, ext) {
			return store.KindSubtitle
		}
	}
	return store.KindVideo
}

// pendingMedia returns the paths of the pending videos and subtitles, in path order.
func pendingMedia(files []store.File) []string {
	var paths []string
	for _, f := range files {
		if f.Kind != store.KindArchive && f.Status == store.FilePending {
			paths = append(paths, f.Path)
		}
	}
	return paths
}

// partialSuffix is extract.Run's default scratch suffix (<Dest>.partial).
const partialSuffix = ".partial"

// extractDests gives each archive its destination, files/<archive name without its
// extension>. A name another archive or an uploaded file already has (letter case aside),
// a name another destination's scratch directory has, and a name ending in .partial get
// the archive's file id appended, so one extraction never replaces another's files:
// extract.Run deletes its destination and its scratch directory before it starts.
func extractDests(files []store.File) map[int64]string {
	taken := map[string]bool{}
	var archives []store.File
	for _, f := range files {
		switch {
		case f.Kind == store.KindArchive:
			archives = append(archives, f)
		case f.From == "":
			taken[strings.ToLower(f.Path)] = true
		}
	}
	slices.SortFunc(archives, func(a, b store.File) int { return cmp.Compare(a.ID, b.ID) })
	dests := make(map[int64]string, len(archives))
	for _, a := range archives {
		base := path.Base(a.Path)
		stem := strings.TrimSuffix(base, path.Ext(base))
		if stem == "" || stem == "." || stem == ".." {
			stem = "archive"
		}
		d := jobdir.Files + "/" + stem
		low := strings.ToLower(d)
		// <dest>.partial is reserved too: it is extract.Run's scratch directory when a
		// task names none, and the run removes it first.
		if taken[low] || taken[low+partialSuffix] || strings.HasSuffix(low, partialSuffix) {
			d += " (" + strconv.FormatInt(a.ID, 10) + ")"
			low = strings.ToLower(d)
		}
		taken[low], taken[low+partialSuffix] = true, true
		dests[a.ID] = d
	}
	return dests
}

// under reports whether p is a clean, job-relative path inside dir.
func under(p, dir string) bool {
	return path.Clean(p) == p && strings.HasPrefix(p, dir+"/") && !strings.Contains(p, "\\")
}
