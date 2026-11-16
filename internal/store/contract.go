// Package store persists jobs, their files, unfinished uploads and the audit log.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// State is a job state (plan §9).
type State string

const (
	Uploading            State = "uploading"
	Uploaded             State = "uploaded"
	Probing              State = "probing"
	Probed               State = "probed"
	Matched              State = "matched"
	AwaitingConfirmation State = "awaiting-confirmation"
	Importing            State = "importing"
	Imported             State = "imported"
	PostProcessing       State = "post-processing"
	Done                 State = "done"
	Failed               State = "failed"
	Stalled              State = "stalled"
	Cancelled            State = "cancelled"
)

// Job is one user submission.
type Job struct {
	ID        string // 32 lower-case hex characters; also the directory jobs/<ID>
	Owner     string // auth user name
	State     State
	Resume    State  // while failed or stalled: the state a retry returns to
	Attempt   int    // starts at 1; +1 each time the job leaves failed for Resume
	ErrorCode string // while failed or stalled: a stable code, e.g. worker_unavailable
	ErrorText string // technical detail for logs and admins
	CreatedAt time.Time
	UpdatedAt time.Time // last state change
	ExpiresAt time.Time // zero: never; retention deletes the job after this
}

type (
	FileKind   string
	FileStatus string
)

const (
	KindVideo    FileKind = "video"
	KindSubtitle FileKind = "subtitle"
	KindArchive  FileKind = "archive"

	FilePending FileStatus = "pending" // not checked by the worker yet
	FileOK      FileStatus = "ok"
	FileRefused FileStatus = "refused" // see Reason; the file is no longer on disk
)

// File is one file of a job: an upload, or an entry extracted from an uploaded archive.
type File struct {
	ID     int64
	JobID  string
	Path   string // job-relative, slash-separated: "files/…" or "archives/…"
	Kind   FileKind
	Size   int64
	Status FileStatus
	Reason string          // refusal code when Status is refused
	From   string          // Path of the archive it was extracted from; empty for an upload
	Report json.RawMessage // the worker's report: a media probe for video, an extraction report for an archive
}

// Upload is an unfinished tus upload. The row is deleted when the upload completes.
type Upload struct {
	ID         string // tus upload id
	JobID      string
	Owner      string
	Name       string // cleaned file name
	Size       int64
	Offset     int64
	CreatedAt  time.Time
	ProgressAt time.Time // last time Offset grew
}

// Event is one state change of a job.
type Event struct {
	JobID    string
	At       time.Time
	From, To State
	Actor    string // user name, or "system"
	Note     string
}

// AuditEntry records who did what (plan §12). Entries outlive their job.
type AuditEntry struct {
	ID     int64
	At     time.Time
	Actor  string // user name, or "system"
	Email  string
	Action string // e.g. job.created, job.submitted, job.cancelled, job.retried, job.deleted, job.expired, upload.expired
	JobID  string
	Detail map[string]string
}

var (
	ErrNotFound          = errors.New("store: not found")
	ErrConflict          = errors.New("store: the job is not in the expected state")
	ErrInvalidTransition = errors.New("store: transition not allowed")
	ErrExists            = errors.New("store: already exists")
)

// JobFilter selects jobs; the zero value selects all.
type JobFilter struct {
	Owner  string  // empty: every owner
	States []State // empty: every state
	Limit  int     // 0: 100
	Offset int
}

// Change describes a transition.
type Change struct {
	Actor     string // user name, or "system"
	Note      string
	ErrorCode string // required when moving to failed or stalled
	ErrorText string
	ExpiresAt *time.Time // when set, replaces the job's retention date
}

// Store is safe for concurrent use.
type Store interface {
	M2 // the M2 additions: contract_m2.go

	CreateJob(ctx context.Context, owner string) (Job, error) // state uploading, attempt 1
	Job(ctx context.Context, id string) (Job, error)
	Jobs(ctx context.Context, f JobFilter) ([]Job, error) // newest first
	// Transition moves a job from one state to another and records an Event, in one
	// transaction. ErrConflict: the job is not in from. ErrInvalidTransition: CanTransition
	// forbids it. Leaving failed or stalled is allowed to the job's Resume state (and to
	// the states the table lists); leaving failed for Resume adds 1 to Attempt. ErrorCode
	// and ErrorText are cleared when a job leaves failed or stalled.
	Transition(ctx context.Context, id string, from, to State, c Change) (Job, error)
	History(ctx context.Context, jobID string) ([]Event, error)
	DeleteJob(ctx context.Context, id string) error // its files, uploads, events and M2 data too; audit entries stay

	AddFile(ctx context.Context, f File) (File, error) // ErrExists when (JobID, Path) is taken
	UpdateFile(ctx context.Context, f File) error      // by ID
	DeleteFile(ctx context.Context, id int64) error
	Files(ctx context.Context, jobID string) ([]File, error) // ordered by Path

	PutUpload(ctx context.Context, u Upload) error // insert, or update by ID
	Upload(ctx context.Context, id string) (Upload, error)
	Uploads(ctx context.Context, owner, jobID string) ([]Upload, error) // empty arguments: no filter
	DeleteUpload(ctx context.Context, id string) error

	Audit(ctx context.Context, e AuditEntry) error // stores the entry and logs it as one structured line
	AuditLog(ctx context.Context, jobID string) ([]AuditEntry, error)

	Ping(ctx context.Context) error // for /readyz
	Close() error
}

// CanTransition reports whether the state machine allows from → to.
func CanTransition(from, to State) bool { return transitions[from][to] }

// ResumeState is the state a retry returns to after a failure in from. Transition records
// it in Job.Resume when a job moves to failed; a job that moves to stalled resumes in the
// state it stalled in.
func ResumeState(from State) State {
	switch from {
	case Probing:
		return Uploaded // run the worker tasks again
	case PostProcessing:
		return Imported
	default:
		return from
	}
}

var transitions = map[State]map[State]bool{
	Uploading:            {Uploaded: true, Cancelled: true, Failed: true},
	Uploaded:             {Probing: true, Cancelled: true, Failed: true},
	Probing:              {Probed: true, Cancelled: true, Failed: true, Stalled: true},
	Probed:               {Matched: true, Cancelled: true, Failed: true},
	Matched:              {AwaitingConfirmation: true, Probed: true, Cancelled: true, Failed: true, Stalled: true},
	AwaitingConfirmation: {Importing: true, Matched: true, Probed: true, Cancelled: true, Failed: true},
	Importing:            {Imported: true, Failed: true, Stalled: true},
	Imported:             {PostProcessing: true, Failed: true},
	PostProcessing:       {Done: true, Failed: true, Stalled: true},
	// Failed and Stalled also go to the job's own Resume state; Transition checks that.
	Failed:  {Cancelled: true},
	Stalled: {Failed: true, Cancelled: true},
}
