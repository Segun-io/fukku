package store

import (
	"context"
	"encoding/json"
	"time"
)

// The M2 additions to the contract (#36): what the user chose and what the app answered,
// so that a restart in any state continues without asking again and without importing
// twice (plan §9). Store embeds M2. The methods hold data only; the rules that use them
// belong to the match, proposal and import code.

// Target is the title a job was matched to. A job has at most one.
type Target struct {
	App       string // "radarr" or "sonarr"
	TitleID   int    // the app's movie or series id
	Title     string
	Year      int
	Options   json.RawMessage // what the user chose: quality profile, monitored, series type…
	MatchedBy string          // the user name
	MatchedAt time.Time       // zero on SetTarget: now
}

// Row results.
const (
	ResultNone     = ""
	ResultImported = "imported"
	ResultFailed   = "failed"
	ResultSkipped  = "skipped"
)

// Row is one file's place in the import: what the app proposed and what the user edited.
type Row struct {
	ID       string // stable for the life of the job; unique within it
	JobID    string
	FileID   int64 // a file of the job; the row goes when the file does
	Skip     bool
	Edit     json.RawMessage // the user's edits (season, episodes, quality, languages)
	Proposal json.RawMessage // the app's latest verdict for this row (an arr.Row)
	Override bool            // the user confirmed despite rejections or warnings
	Result   string          // ResultNone, ResultImported, ResultFailed or ResultSkipped
}

// Import is the bookkeeping of one import attempt. A job has at most one.
type Import struct {
	JobID       string
	CommandID   int       // 0 until the app has answered
	SubmittedAt time.Time // zero: not submitted
	FinishedAt  time.Time // zero: not finished
}

// Post-processing step statuses.
const (
	StepPending = "pending"
	StepOK      = "ok"
	StepFailed  = "failed"
	StepSkipped = "skipped"
)

// PostStep is one post-processing step's state.
type PostStep struct {
	JobID, Name string // Name: e.g. mediaServerRefresh, subtitles
	Status      string // StepPending (the default), StepOK, StepFailed or StepSkipped
	Attempts    int
	Detail      string
	UpdatedAt   time.Time // zero: now
}

// M2 is the part of Store that M2 adds. A method that writes returns ErrNotFound for an
// unknown job; the list readers (Rows, PostSteps, ActiveJobsFor) return nothing for one.
// Returned JSON is a copy. DeleteJob removes everything here with the job.
type M2 interface {
	// SetTarget stores t as the job's target, replacing any earlier one. App must be set.
	// A zero MatchedAt becomes now.
	SetTarget(ctx context.Context, jobID string, t Target) error
	// Target returns the job's target; ErrNotFound when it has none.
	Target(ctx context.Context, jobID string) (Target, error)
	// ClearTarget removes the job's target and, with it, its rows (they belong to the
	// title). A job without a target is left alone: it is not an error.
	ClearTarget(ctx context.Context, jobID string) error

	// PutRows replaces all of the job's rows with rows, in that order, in one
	// transaction. The JobID of a row is ignored. A row needs an ID, unique within the
	// slice (else ErrExists), and a FileID of one of the job's files (else ErrNotFound).
	PutRows(ctx context.Context, jobID string, rows []Row) error
	// Rows returns the job's rows in the order PutRows was given them.
	Rows(ctx context.Context, jobID string) ([]Row, error)
	// UpdateRow replaces Skip, Edit, Proposal, Override and Result of the row
	// (r.JobID, r.ID); its FileID and place do not change. ErrNotFound: no such row.
	UpdateRow(ctx context.Context, r Row) error

	// PutImport inserts or replaces the job's import record.
	PutImport(ctx context.Context, i Import) error
	// Import returns the job's import record; ErrNotFound when it has none.
	Import(ctx context.Context, jobID string) (Import, error)

	// PutPostStep inserts or replaces the step (s.JobID, s.Name). Steps come back in the
	// order they were first put. An empty Status is StepPending; a zero UpdatedAt is now.
	PutPostStep(ctx context.Context, s PostStep) error
	PostSteps(ctx context.Context, jobID string) ([]PostStep, error)

	// ActiveJobsFor returns the jobs whose target is this title and that are not done or
	// cancelled (a failed or stalled job still holds the title: it can be retried),
	// newest first: the rule "one active job per app and title" (plan §10).
	ActiveJobsFor(ctx context.Context, app string, titleID int) ([]Job, error)
}
