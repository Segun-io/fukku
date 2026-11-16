package flows

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/segun-io/fukku/internal/store"
)

// ConfirmRequest is the body of POST /api/jobs/{id}/confirm: the rows whose rejections or
// warnings the user accepts. Probes B8-radarr and B8-sonarr: with explicit ids, the apps
// import despite rejections.
type ConfirmRequest struct {
	Overrides []string `json:"overrides"`
}

// Confirmation is what a confirmation did, for the audit log.
type Confirmation struct {
	Job        store.Job
	Target     store.Target
	Rows       []string // the rows to import, in the job's order
	Overridden []Overridden
}

// Overridden is a row imported despite its rejections or warnings.
type Overridden struct {
	RowID      string
	File       string // the file's name
	Rejections []string
	Warnings   []string
}

// Confirm moves a job from awaiting-confirmation to importing. Every row that is not
// skipped must be importable, or have only rejections and warnings and be listed in
// req.Overrides. The rows are judged as the proposal shows them now (View), so the warnings
// are those the user saw; for a show the app is asked for its episodes. The job's lock is
// held for the write, never across the call to the app: the rows must be unchanged since
// they were judged (an edit meanwhile makes it judge again), and the transition checks
// that the job still has the same title.
//
// Refusals: wrong_state, bad_request (an override names no row of the job),
// storage_unavailable (the filesystem checks fail), incomplete_rows (rows without a title
// or episodes: edit or skip them), needs_override (Rows lists them), nothing_to_import;
// arr_unavailable when the app does not answer.
func (e *Engine) Confirm(ctx context.Context, user string, job store.Job, req ConfirmRequest) (Confirmation, error) {
	for range editAttempts {
		c, err := e.confirm(ctx, user, job.ID, req)
		if !errors.Is(err, errRetry) {
			return c, err
		}
	}
	return Confirmation{}, rowsChanged()
}

func (e *Engine) confirm(ctx context.Context, user, jobID string, req ConfirmRequest) (Confirmation, error) {
	s, err := e.p.edit(ctx, jobID)
	if err != nil {
		return Confirmation{}, err
	}
	for _, id := range req.Overrides {
		if s.find(id) < 0 {
			return Confirmation{}, badRequest("Row %s is not a row of this job.", id)
		}
	}
	if err := e.checkStorage(); err != nil {
		e.log.Warn("import_storage_unavailable", "job", jobID, "error", err)
		return Confirmation{}, refuse(CodeStorageUnavailable, "The storage is not available: a mount is missing, or the intake and library folders are on different filesystems.")
	}
	views, _, err := s.views(ctx)
	if err != nil {
		return Confirmation{}, err
	}
	var (
		c          = Confirmation{Target: s.t}
		incomplete []string
		needs      []string
	)
	override := map[string]bool{}
	for i, v := range views {
		if v.Skip {
			continue
		}
		ar, offered, err := decodeRow(s.rows[i])
		if err != nil {
			return Confirmation{}, err
		}
		if !offered || titleOK(s.t, ar) != nil {
			incomplete = append(incomplete, v.ID)
			continue
		}
		if len(v.Rejections) > 0 || len(v.Warnings) > 0 {
			if !slices.Contains(req.Overrides, v.ID) {
				needs = append(needs, v.ID)
				continue
			}
			o := Overridden{RowID: v.ID, File: v.Name}
			for _, r := range v.Rejections {
				o.Rejections = append(o.Rejections, r.Reason)
			}
			for _, w := range v.Warnings {
				o.Warnings = append(o.Warnings, w.Code)
			}
			c.Overridden = append(c.Overridden, o)
			override[v.ID] = true
		}
		c.Rows = append(c.Rows, v.ID)
	}
	switch {
	case len(incomplete) > 0:
		err := refuse(CodeIncompleteRows, "Some rows have no title or no episode: choose one or skip them.")
		err.Rows = incomplete
		return Confirmation{}, err
	case len(needs) > 0:
		err := refuse(CodeNeedsOverride, "Some rows have rejections or warnings: confirm them as overrides, or skip them.")
		err.Rows = needs
		return Confirmation{}, err
	case len(c.Rows) == 0:
		return Confirmation{}, refuse(CodeNothingToImport, "Every row is skipped: there is nothing to import.")
	}

	// The write, under the job's lock: the rows as judged, then the transition, which checks
	// the state and the title in its transaction.
	defer e.locks.Lock(jobID)()
	cur, err := e.st.Rows(ctx, jobID)
	if err != nil {
		return Confirmation{}, err
	}
	if len(cur) != len(s.rows) {
		return Confirmation{}, errRetry
	}
	for i := range cur {
		if !sameRow(cur[i], s.rows[i]) {
			return Confirmation{}, errRetry
		}
	}
	for i, r := range cur {
		want := r
		want.Skip = views[i].Skip
		want.Override = override[r.ID]
		want.Result = store.ResultNone
		if want.Skip {
			want.Result = store.ResultSkipped
		}
		if sameRow(want, r) {
			continue
		}
		if err := e.st.UpdateRow(ctx, want); err != nil {
			return Confirmation{}, err
		}
	}
	note := fmt.Sprintf("%d file(s) confirmed for %s", len(c.Rows), s.t.App)
	if len(c.Overridden) > 0 {
		note += fmt.Sprintf(", %d despite rejections or warnings", len(c.Overridden))
	}
	next, err := e.st.TransitionFor(ctx, jobID, s.t, store.AwaitingConfirmation, store.Importing, store.Change{Actor: user, Note: note})
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrInvalidTransition) || errors.Is(err, store.ErrNotFound) {
		return Confirmation{}, wrongState()
	}
	if err != nil {
		return Confirmation{}, err
	}
	e.log.Info("job_state", "job", jobID, "from", store.AwaitingConfirmation, "to", next.State, "attempt", next.Attempt,
		"rows", len(c.Rows), "overridden", len(c.Overridden))
	c.Job = next
	return c, nil
}
