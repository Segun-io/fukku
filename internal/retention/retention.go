// Package retention deletes what nobody is going to use any more: idle uploads, jobs that
// waited too long for a decision, the records of finished jobs, and files in the intake root
// that belong to nothing (plan §10).
//
// Safety rules, each covered by a test:
//   - nothing happens while a paths.sentinels file is missing;
//   - every file operation goes through an os.Root of the intake root, and only under
//     .incoming/ and jobs/; symlinks are never followed and never removed;
//   - a job in importing, imported or post-processing (or any state not named below) is
//     never touched;
//   - a directory in jobs/ without a job row is deleted only when it is older than
//     upload.jobRetention, so a lost database does not empty the intake root.
package retention

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"iter"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/fscheck"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

// Actor is the audit actor of everything the sweeper does.
const Actor = "system"

// OrphanFileAge is how old an unknown file in .incoming/ must be before it is deleted, so
// an upload that is being created right now is never mistaken for an orphan.
const OrphanFileAge = time.Hour

// Sweeper runs the retention rules.
type Sweeper struct {
	cfg   config.Config
	store store.Store
	queue spool.Queue // may be nil: no task cleanup
	log   *slog.Logger
	now   func() time.Time

	mu     sync.Mutex
	logged map[string]struct{} // paths already reported as unknown
	worker *jobdir.WorkerSweeper
}

// New returns a Sweeper. q is the spool queue, used to cancel and acknowledge a job's tasks
// before the job is deleted (nil: skip that). now is the clock; nil means time.Now.
func New(cfg config.Config, st store.Store, q spool.Queue, log *slog.Logger, now func() time.Time) *Sweeper {
	if now == nil {
		now = time.Now
	}
	return &Sweeper{cfg: cfg, store: st, queue: q, log: log, now: now, logged: map[string]struct{}{},
		worker: &jobdir.WorkerSweeper{Limits: SweepLimits(cfg)}}
}

// Run sweeps once and then every upload.sweepInterval until ctx is done.
func (s *Sweeper) Run(ctx context.Context) error {
	s.Sweep(ctx)
	t := time.NewTicker(s.cfg.Upload.SweepInterval.D())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.Sweep(ctx)
		}
	}
}

// Sweep applies every rule once. Failures are logged and the sweep goes on with the next
// item; the next sweep retries them.
func (s *Sweeper) Sweep(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := fscheck.Sentinels(s.cfg.Paths); err != nil {
		s.log.Warn("retention_skipped", "reason", "sentinel_missing", "error", err.Error())
		return
	}
	root, err := os.OpenRoot(s.cfg.Paths.IntakeRoot)
	if err != nil {
		s.log.Error("retention_skipped", "reason", "intake_root", "error", err.Error())
		return
	}
	defer func() { _ = root.Close() }()

	now := s.now()
	uploads, err := s.store.Uploads(ctx, "", "")
	if err != nil {
		s.log.Error("retention_store", "op", "uploads", "error", err.Error())
		return
	}
	uploads = s.expireUploads(ctx, root, now, uploads)
	s.expireJobs(ctx, root, now, uploads)
	s.deleteFinishedJobs(ctx, root, now)
	s.sweepIncoming(ctx, root, now, uploads)
	s.sweepJobsDir(ctx, root, now)
}

func (s *Sweeper) audit(ctx context.Context, action, jobID string, detail map[string]string) {
	err := s.store.Audit(ctx, store.AuditEntry{At: s.now(), Actor: Actor, Action: action, JobID: jobID, Detail: detail})
	if err != nil {
		s.log.Error("retention_audit", "action", action, "job", jobID, "error", err.Error())
	}
}

// expireUploads deletes unfinished uploads without progress for upload.idleUploadExpiry and
// returns the rows that remain.
func (s *Sweeper) expireUploads(ctx context.Context, root *os.Root, now time.Time, uploads []store.Upload) []store.Upload {
	limit := s.cfg.Upload.IdleUploadExpiry.D()
	var kept []store.Upload
	for _, u := range uploads {
		if now.Sub(u.ProgressAt) <= limit {
			kept = append(kept, u)
			continue
		}
		if err := removeUploadFiles(root, u.ID); err != nil {
			s.log.Error("retention_remove", "upload", u.ID, "error", err.Error())
			kept = append(kept, u)
			continue
		}
		if err := s.store.DeleteUpload(ctx, u.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.log.Error("retention_store", "op", "delete_upload", "upload", u.ID, "error", err.Error())
			kept = append(kept, u)
			continue
		}
		s.audit(ctx, "upload.expired", u.JobID, map[string]string{
			"upload": u.ID, "name": u.Name, "idle": now.Sub(u.ProgressAt).Truncate(time.Second).String(),
		})
	}
	return kept
}

// expireJobs cancels jobs that sat idle or waited too long. The runner removes a cancelled
// job's files (#29).
func (s *Sweeper) expireJobs(ctx context.Context, root *os.Root, now time.Time, uploads []store.Upload) {
	hasUpload := map[string]bool{}
	for _, u := range uploads {
		hasUpload[u.JobID] = true
	}

	idle := s.cfg.Upload.IdleUploadExpiry.D()
	for _, j := range s.jobsIn(ctx, store.Uploading) {
		if hasUpload[j.ID] {
			continue
		}
		if now.Sub(lastActivity(root, j)) > idle {
			s.cancel(ctx, j, "idle")
		}
	}

	for _, j := range s.jobsIn(ctx, store.Probed, store.Matched, store.AwaitingConfirmation) {
		if !j.ExpiresAt.IsZero() && now.After(j.ExpiresAt) {
			s.cancel(ctx, j, "expired")
		}
	}

	retention := s.cfg.Upload.JobRetention.D()
	for _, j := range s.jobsIn(ctx, store.Failed) {
		if now.Sub(j.UpdatedAt) > retention {
			s.cancel(ctx, j, "failed_too_long")
		}
	}
}

func (s *Sweeper) cancel(ctx context.Context, j store.Job, reason string) {
	_, err := s.store.Transition(ctx, j.ID, j.State, store.Cancelled, store.Change{Actor: Actor, Note: "retention: " + reason})
	switch {
	case err == nil:
		s.audit(ctx, "job.expired", j.ID, map[string]string{"reason": reason, "state": string(j.State)})
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrNotFound):
		// The job moved on or is gone since it was listed: leave it alone.
	default:
		s.log.Error("retention_store", "op", "transition", "job", j.ID, "error", err.Error())
	}
}

// deleteFinishedJobs deletes the rows (and the directory, if one is left) of jobs that have
// been done or cancelled for upload.finishedJobRetention. Audit entries stay.
func (s *Sweeper) deleteFinishedJobs(ctx context.Context, root *os.Root, now time.Time) {
	limit := s.cfg.Upload.FinishedJobRetention.D()
	for _, j := range s.jobsIn(ctx, store.Done, store.Cancelled) {
		if now.Sub(j.UpdatedAt) <= limit || !jobdir.ValidID(j.ID) {
			continue
		}
		if err := s.dropTasks(ctx, j); err != nil {
			s.log.Error("retention_tasks", "job", j.ID, "error", err.Error())
			continue
		}
		if err := removeJobDirs(root, s.cfg.Paths.IntakeRoot, j.ID); err != nil {
			s.log.Error("retention_remove", "job", j.ID, "error", err.Error())
			continue
		}
		if err := s.store.DeleteJob(ctx, j.ID); err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.log.Error("retention_store", "op", "delete_job", "job", j.ID, "error", err.Error())
			}
			continue
		}
		s.audit(ctx, "job.deleted", j.ID, map[string]string{"reason": "finished_retention", "state": string(j.State)})
	}
}

// removeJobDirs removes a job's directories through root, the intake root: the worker's
// (worker/<id>) first and jobs/<id> last, as jobdir.Remove does.
func removeJobDirs(root *os.Root, intakeRoot, id string) error {
	// The worker's folder may hold anything the worker made, however many entries and
	// levels: it is set aside for the bounded sweep of worker/ (jobdir.SpillWorker).
	if err := jobdir.SpillWorker(intakeRoot, id, ""); err != nil {
		return err
	}
	return root.RemoveAll(path.Join(jobdir.Jobs, id))
}

// dropTasks cancels and then acknowledges every task id the job's runner may have used, so
// no late result outlives the job. The ids are the runner's (#29), so runner.Discard knows
// them; it is harmless for ids that never existed.
func (s *Sweeper) dropTasks(ctx context.Context, j store.Job) error {
	if s.queue == nil {
		return nil
	}
	files, err := s.store.Files(ctx, j.ID)
	if err != nil {
		return err
	}
	return runner.Discard(ctx, s.queue, j, files)
}

// sweepIncoming deletes regular files in .incoming/ that belong to no upload row and are
// older than OrphanFileAge. Anything else is left alone and logged once.
func (s *Sweeper) sweepIncoming(ctx context.Context, root *os.Root, now time.Time, uploads []store.Upload) {
	known := map[string]bool{}
	for _, u := range uploads {
		known[u.ID] = true
	}
	for e := range s.entries(root, jobdir.Incoming) {
		name := e.Name()
		rel := path.Join(jobdir.Incoming, name)
		if known[strings.TrimSuffix(name, ".info")] {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			s.ignore(rel, "not a regular file")
			continue
		}
		if now.Sub(info.ModTime()) <= OrphanFileAge {
			continue
		}
		if err := root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.log.Error("retention_remove", "path", rel, "error", err.Error())
			continue
		}
		s.log.Warn("orphan_upload", "path", rel, "size", info.Size())
		s.audit(ctx, "upload.orphan_deleted", "", map[string]string{"path": rel})
	}
}

// sweepJobsDir handles the directories in jobs/ and worker/ that have no job row: logged
// as orphan_job_dir, deleted only once older than upload.jobRetention.
func (s *Sweeper) sweepJobsDir(ctx context.Context, root *os.Root, now time.Time) {
	s.sweepWorker(ctx, root)
	for _, top := range []string{jobdir.Worker, jobdir.Jobs} {
		s.sweepTop(ctx, root, now, top)
	}
}

// sweepWorker keeps worker/ to what the worker makes for a job (jobdir.WorkerSweeper): a hard
// link it made there of a file the web process took into a job, through a descriptor it
// kept, would be a writable name of an imported or placed file (#114). The worker's folder
// of a job that is done is removed too: nothing in it is needed any more.
func (s *Sweeper) sweepWorker(ctx context.Context, root *os.Root) {
	for _, j := range s.jobsIn(ctx, store.Done) {
		if !jobdir.ValidID(j.ID) {
			continue
		}
		if err := jobdir.SpillWorker(s.cfg.Paths.IntakeRoot, j.ID, ""); err != nil {
			s.log.Error("retention_remove", "job", j.ID, "path", jobdir.Worker, "error", err.Error())
		}
	}
	res, err := s.worker.Sweep(root)
	LogWorkerSweep(s.log, res, err)
}

// SweepLimits are the bounds of one sweep of worker/ (jobdir.WorkerSweeper) and of the
// listings of the intake root: worker.sweepMaxEntries entries, and directories no deeper
// than an extraction path (extract.maxPathSegments) can make, with room for the folders the
// worker puts it in.
func SweepLimits(cfg config.Config) jobdir.SweepLimits {
	return jobdir.SweepLimits{MaxEntries: cfg.Worker.SweepMaxEntries, MaxDepth: cfg.Extract.MaxPathSegments + 8}
}

// LogWorkerSweep logs what a sweep of worker/ removed (a sample of the paths) and why it
// stopped early.
func LogWorkerSweep(log *slog.Logger, res jobdir.SweepResult, err error) {
	if res.Removed > 0 {
		log.Warn("worker_entries_removed", "count", res.Removed, "sample", res.Sample)
	}
	if !res.Done {
		log.Warn("worker_sweep_unfinished", "note", "worker.sweepMaxEntries reached; the next sweep carries on")
	}
	if err != nil {
		log.Error("retention_remove", "path", jobdir.Worker, "error", err.Error())
	}
}

func (s *Sweeper) sweepTop(ctx context.Context, root *os.Root, now time.Time, top string) {
	limit := s.cfg.Upload.JobRetention.D()
	for e := range s.entries(root, top) {
		name := e.Name()
		rel := path.Join(top, name)
		info, err := e.Info()
		if top == jobdir.Worker && jobdir.IsSpill(name) {
			continue // the sweep of worker/ removes it
		}
		if err != nil || !info.IsDir() || !jobdir.ValidID(name) {
			s.ignore(rel, "not a job directory")
			continue
		}
		if _, err := s.store.Job(ctx, name); err == nil {
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("retention_store", "op", "job", "job", name, "error", err.Error())
			continue
		}
		if now.Sub(info.ModTime()) <= limit {
			s.once(rel, "orphan_job_dir", "action", "kept", "path", rel, "modified", info.ModTime().UTC().Format(time.RFC3339))
			continue
		}
		var rerr error
		if top == jobdir.Worker { // set aside for the bounded sweep of worker/
			rerr = jobdir.SpillWorker(s.cfg.Paths.IntakeRoot, name, "")
		} else {
			rerr = root.RemoveAll(rel)
		}
		if rerr != nil {
			s.log.Error("retention_remove", "path", rel, "error", rerr.Error())
			continue
		}
		s.log.Warn("orphan_job_dir", "action", "deleted", "path", rel)
		s.audit(ctx, "job.orphan_deleted", name, map[string]string{"path": rel})
	}
}

// entries yields the entries of a top-level directory of the intake root, only when it is a
// real directory (not a symlink) inside the root. It reads 1024 at a time and stops after
// worker.sweepMaxEntries of them (logged once), so a folder with millions of entries costs
// a bounded amount of memory and time per sweep.
func (s *Sweeper) entries(root *os.Root, dir string) iter.Seq[fs.DirEntry] {
	return func(yield func(fs.DirEntry) bool) {
		fi, err := root.Lstat(dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				s.log.Error("retention_list", "path", dir, "error", err.Error())
			}
			return
		}
		if !fi.IsDir() {
			s.ignore(dir, "not a directory")
			return
		}
		d, err := root.Open(dir)
		if err != nil {
			s.log.Error("retention_list", "path", dir, "error", err.Error())
			return
		}
		defer func() { _ = d.Close() }()
		seen := 0
		for {
			list, err := d.ReadDir(1024)
			for _, e := range list {
				if seen >= s.cfg.Worker.SweepMaxEntries {
					s.once(dir, "retention_list_capped", "path", dir, "entries", seen, "note", "worker.sweepMaxEntries reached; the rest waits for the next sweep")
					return
				}
				seen++
				if !yield(e) {
					return
				}
			}
			if errors.Is(err, io.EOF) || (err == nil && len(list) == 0) {
				return
			}
			if err != nil {
				s.log.Error("retention_list", "path", dir, "error", err.Error())
				return
			}
		}
	}
}

func (s *Sweeper) ignore(rel, why string) {
	s.once(rel, "retention_ignored", "path", rel, "reason", why)
}

// once logs a warning the first time it is called for key.
func (s *Sweeper) once(key, msg string, args ...any) {
	if _, ok := s.logged[key+"\x00"+msg]; ok {
		return
	}
	s.logged[key+"\x00"+msg] = struct{}{}
	s.log.Warn(msg, args...)
}

// jobsIn lists every job in the given states, paging through the store.
func (s *Sweeper) jobsIn(ctx context.Context, states ...store.State) []store.Job {
	const page = 500
	var all []store.Job
	for off := 0; ; off += page {
		jobs, err := s.store.Jobs(ctx, store.JobFilter{States: states, Limit: page, Offset: off})
		if err != nil {
			s.log.Error("retention_store", "op", "jobs", "error", err.Error())
			return nil
		}
		all = append(all, jobs...)
		if len(jobs) < page {
			return all
		}
	}
}

// removeUploadFiles deletes an unfinished upload's data and info files, through the root.
// Only regular files are removed.
func removeUploadFiles(root *os.Root, id string) error {
	if !jobdir.ValidID(id) {
		return errors.New("malformed upload id")
	}
	var errs []error
	for _, rel := range []string{path.Join(jobdir.Incoming, id), path.Join(jobdir.Incoming, id+".info")} {
		fi, err := root.Lstat(rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !fi.Mode().IsRegular() {
			errs = append(errs, errors.New(rel+" is not a regular file"))
			continue
		}
		if err := root.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// lastActivity is the latest sign of life of a job in uploading: its last state change, or
// the last time a file was moved into its directory (a finished upload leaves no row).
func lastActivity(root *os.Root, j store.Job) time.Time {
	last := j.UpdatedAt
	if !jobdir.ValidID(j.ID) {
		return last
	}
	for _, sub := range []string{"", jobdir.Files, jobdir.Archives} {
		fi, err := root.Lstat(path.Join(jobdir.Jobs, j.ID, sub))
		if err == nil && fi.IsDir() && fi.ModTime().After(last) {
			last = fi.ModTime()
		}
	}
	return last
}
