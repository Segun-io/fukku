package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// The M2 additions (contract_m2.go). Writes go through the one write connection.

func rawJSON(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- target ---------------------------------------------------------------------------

func (s *sqliteStore) SetTarget(ctx context.Context, jobID string, t Target) error {
	if t.App == "" {
		return errors.New("store: a target needs an app")
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := jobExists(ctx, tx, jobID); err != nil {
		return err
	}
	if err := s.putTarget(ctx, tx, jobID, t); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) ReplaceTarget(ctx context.Context, jobID string, from, to State, t *Target, c Change) (Job, error) {
	if t != nil && t.App == "" {
		return Job{}, errors.New("store: a target needs an app")
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cur, err := scanJob(tx.QueryRowContext(ctx, "SELECT "+jobCols+" FROM jobs WHERE id = ?", jobID))
	if err != nil {
		return Job{}, err
	}
	if cur.State != from {
		return Job{}, ErrConflict
	}
	if c.ExpiresAt != nil {
		return Job{}, errors.New("store: ReplaceTarget does not change the retention date")
	}
	if !matchStates[from] || !replaceStates[to] {
		return Job{}, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
	}
	next := cur
	if to != from {
		var ev Event
		if next, ev, err = applyTransition(cur, from, to, c, s.clock()); err != nil {
			return Job{}, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET state = ?, resume = ?, error_code = ?, error_text = ?, updated_at = ? WHERE id = ?",
			next.State, next.Resume, next.ErrorCode, next.ErrorText, nanos(next.UpdatedAt), jobID); err != nil {
			return Job{}, err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO events (job_id, at, from_state, to_state, actor, note) VALUES (?, ?, ?, ?, ?, ?)",
			ev.JobID, nanos(ev.At), ev.From, ev.To, ev.Actor, ev.Note); err != nil {
			return Job{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM targets WHERE job_id = ?", jobID); err != nil {
		return Job{}, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM import_rows WHERE job_id = ?", jobID); err != nil {
		return Job{}, err
	}
	if t != nil {
		if err := s.putTarget(ctx, tx, jobID, *t); err != nil {
			return Job{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return next, nil
}

// ReplaceTarget starts from these states and ends in one of the second set: rows exist only
// from matched on, so a job never waits for confirmation with none.
var (
	matchStates   = map[State]bool{Probed: true, Matched: true, AwaitingConfirmation: true, Importing: true}
	replaceStates = map[State]bool{Probed: true, Matched: true}
)

func (s *sqliteStore) putTarget(ctx context.Context, tx *sql.Tx, jobID string, t Target) error {
	t.MatchedAt = normTime(t.MatchedAt)
	if t.MatchedAt.IsZero() {
		t.MatchedAt = s.clock()
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO targets (job_id, app, title_id, title, year, options, matched_by, matched_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (job_id) DO UPDATE SET app = excluded.app, title_id = excluded.title_id, title = excluded.title,
		   year = excluded.year, options = excluded.options, matched_by = excluded.matched_by, matched_at = excluded.matched_at`,
		jobID, t.App, t.TitleID, t.Title, t.Year, report(t.Options), t.MatchedBy, nanos(t.MatchedAt)); err != nil {
		return err
	}
	return nil
}

func (s *sqliteStore) Target(ctx context.Context, jobID string) (Target, error) {
	var (
		t       Target
		options []byte
		at      sql.NullInt64
	)
	err := s.r.QueryRowContext(ctx,
		"SELECT app, title_id, title, year, options, matched_by, matched_at FROM targets WHERE job_id = ?", jobID).
		Scan(&t.App, &t.TitleID, &t.Title, &t.Year, &options, &t.MatchedBy, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	if err != nil {
		return Target{}, err
	}
	t.Options, t.MatchedAt = rawJSON(options), fromNanos(at)
	return t, nil
}

func (s *sqliteStore) ClearTarget(ctx context.Context, jobID string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := jobExists(ctx, tx, jobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM targets WHERE job_id = ?", jobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM import_rows WHERE job_id = ?", jobID); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- rows -----------------------------------------------------------------------------

func (s *sqliteStore) PutRows(ctx context.Context, jobID string, rows []Row) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := jobExists(ctx, tx, jobID); err != nil {
		return err
	}
	if err := putRows(ctx, tx, jobID, rows); err != nil {
		return err
	}
	return tx.Commit()
}

// proposalStates are where PutProposal starts and where it ends.
var proposalStates = map[State]bool{Matched: true, AwaitingConfirmation: true}

func (s *sqliteStore) PutProposal(ctx context.Context, jobID string, from, to State, t Target, rows []Row, c Change) (Job, error) {
	if c.ExpiresAt != nil {
		return Job{}, errors.New("store: PutProposal does not change the retention date")
	}
	if !proposalStates[from] || to != AwaitingConfirmation {
		return Job{}, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cur, err := scanJob(tx.QueryRowContext(ctx, "SELECT "+jobCols+" FROM jobs WHERE id = ?", jobID))
	if err != nil {
		return Job{}, err
	}
	if cur.State != from {
		return Job{}, ErrConflict
	}
	if err := hasTarget(ctx, tx, jobID, t); err != nil {
		return Job{}, err
	}
	next := cur
	if to != from {
		var ev Event
		if next, ev, err = applyTransition(cur, from, to, c, s.clock()); err != nil {
			return Job{}, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE jobs SET state = ?, resume = ?, error_code = ?, error_text = ?, updated_at = ? WHERE id = ?",
			next.State, next.Resume, next.ErrorCode, next.ErrorText, nanos(next.UpdatedAt), jobID); err != nil {
			return Job{}, err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO events (job_id, at, from_state, to_state, actor, note) VALUES (?, ?, ?, ?, ?, ?)",
			ev.JobID, nanos(ev.At), ev.From, ev.To, ev.Actor, ev.Note); err != nil {
			return Job{}, err
		}
	}
	if err := putRows(ctx, tx, jobID, rows); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return next, nil
}

func (s *sqliteStore) TransitionFor(ctx context.Context, jobID string, t Target, from, to State, c Change) (Job, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := jobExists(ctx, tx, jobID); err != nil {
		return Job{}, err
	}
	if err := hasTarget(ctx, tx, jobID, t); err != nil {
		return Job{}, err
	}
	next, err := s.transitionTx(ctx, tx, jobID, from, to, c)
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return next, nil
}

// hasTarget fails with ErrConflict unless the job's target is t (app, title id, matched at).
func hasTarget(ctx context.Context, tx *sql.Tx, jobID string, t Target) error {
	var (
		app     string
		titleID int
		at      sql.NullInt64
	)
	err := tx.QueryRowContext(ctx, "SELECT app, title_id, matched_at FROM targets WHERE job_id = ?", jobID).Scan(&app, &titleID, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if app != t.App || titleID != t.TitleID || !fromNanos(at).Equal(normTime(t.MatchedAt)) {
		return ErrConflict
	}
	return nil
}

// putRows replaces the job's rows inside tx.
func putRows(ctx context.Context, tx *sql.Tx, jobID string, rows []Row) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM import_rows WHERE job_id = ?", jobID); err != nil {
		return err
	}
	for pos, r := range rows {
		if r.ID == "" {
			return errors.New("store: a row needs an id")
		}
		var one int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM files WHERE id = ? AND job_id = ?", r.FileID, jobID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: row %q: file %d", ErrNotFound, r.ID, r.FileID)
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO import_rows (job_id, id, file_id, pos, skip, edit, proposal, override, result)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			jobID, r.ID, r.FileID, pos, boolInt(r.Skip), report(r.Edit), report(r.Proposal), boolInt(r.Override), r.Result)
		if isUnique(err) {
			return fmt.Errorf("%w: row %q", ErrExists, r.ID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteStore) Rows(ctx context.Context, jobID string) ([]Row, error) {
	rows, err := s.r.QueryContext(ctx,
		"SELECT id, job_id, file_id, skip, edit, proposal, override, result FROM import_rows WHERE job_id = ? ORDER BY pos", jobID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Row
	for rows.Next() {
		var (
			r              Row
			skip, override int
			edit, proposal []byte
		)
		if err := rows.Scan(&r.ID, &r.JobID, &r.FileID, &skip, &edit, &proposal, &override, &r.Result); err != nil {
			return nil, err
		}
		r.Skip, r.Override, r.Edit, r.Proposal = skip != 0, override != 0, rawJSON(edit), rawJSON(proposal)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *sqliteStore) UpdateRow(ctx context.Context, r Row) error {
	res, err := s.w.ExecContext(ctx,
		"UPDATE import_rows SET skip = ?, edit = ?, proposal = ?, override = ?, result = ? WHERE job_id = ? AND id = ?",
		boolInt(r.Skip), report(r.Edit), report(r.Proposal), boolInt(r.Override), r.Result, r.JobID, r.ID)
	return affectedOne(res, err)
}

// ---- import ---------------------------------------------------------------------------

func (s *sqliteStore) PutImport(ctx context.Context, i Import) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := jobExists(ctx, tx, i.JobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO imports (job_id, command_id, submitted_at, finished_at, baseline, refusals_from) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (job_id) DO UPDATE SET command_id = excluded.command_id,
		   submitted_at = excluded.submitted_at, finished_at = excluded.finished_at, baseline = excluded.baseline,
		   refusals_from = excluded.refusals_from`,
		i.JobID, i.CommandID, nanos(normTime(i.SubmittedAt)), nanos(normTime(i.FinishedAt)), report(i.Baseline), i.RefusalsFrom); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) Import(ctx context.Context, jobID string) (Import, error) {
	i := Import{JobID: jobID}
	var submitted, finished sql.NullInt64
	var baseline []byte
	err := s.r.QueryRowContext(ctx,
		"SELECT command_id, submitted_at, finished_at, baseline, refusals_from FROM imports WHERE job_id = ?", jobID).
		Scan(&i.CommandID, &submitted, &finished, &baseline, &i.RefusalsFrom)
	if errors.Is(err, sql.ErrNoRows) {
		return Import{}, ErrNotFound
	}
	if err != nil {
		return Import{}, err
	}
	i.SubmittedAt, i.FinishedAt, i.Baseline = fromNanos(submitted), fromNanos(finished), rawJSON(baseline)
	return i, nil
}

func (s *sqliteStore) ConfirmImport(ctx context.Context, jobID string, t Target, rows []Row, options json.RawMessage, audit []AuditEntry, c Change) (Job, error) {
	if c.ExpiresAt != nil {
		return Job{}, errors.New("store: ConfirmImport does not change the retention date")
	}
	for _, e := range audit {
		if e.Action == "" {
			return Job{}, errors.New("store: an audit entry needs an action")
		}
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cur, err := scanJob(tx.QueryRowContext(ctx, "SELECT "+jobCols+" FROM jobs WHERE id = ?", jobID))
	if err != nil {
		return Job{}, err
	}
	if cur.State != AwaitingConfirmation {
		return Job{}, ErrConflict
	}
	if err := hasTarget(ctx, tx, jobID, t); err != nil {
		return Job{}, err
	}
	for _, r := range rows {
		res, err := tx.ExecContext(ctx, "UPDATE import_rows SET skip = ?, override = ?, result = ? WHERE job_id = ? AND id = ?",
			boolInt(r.Skip), boolInt(r.Override), r.Result, jobID, r.ID)
		if err := affectedOne(res, err); err != nil {
			return Job{}, err
		}
	}
	if options != nil {
		if _, err := tx.ExecContext(ctx, "UPDATE targets SET options = ? WHERE job_id = ?", report(options), jobID); err != nil {
			return Job{}, err
		}
	}
	next, err := s.transitionTx(ctx, tx, jobID, AwaitingConfirmation, Importing, c)
	if err != nil {
		return Job{}, err
	}
	entries := make([]AuditEntry, 0, len(audit))
	for _, e := range audit {
		e.JobID, e.Actor = jobID, actorOr(e.Actor)
		e.At = normTime(e.At)
		if e.At.IsZero() {
			e.At = s.clock()
		}
		var detail any
		if len(e.Detail) > 0 {
			b, err := json.Marshal(e.Detail)
			if err != nil {
				return Job{}, err
			}
			detail = string(b)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO audit (at, actor, email, action, job_id, detail) VALUES (?, ?, ?, ?, ?, ?)",
			nanos(e.At), e.Actor, e.Email, e.Action, e.JobID, detail); err != nil {
			return Job{}, err
		}
		entries = append(entries, e)
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	for _, e := range entries {
		s.log.InfoContext(ctx, "audit", auditAttrs(e)...)
	}
	return next, nil
}

// ---- post-processing ------------------------------------------------------------------

func (s *sqliteStore) PutPostStep(ctx context.Context, p PostStep) error {
	if p.Name == "" {
		return errors.New("store: a post-processing step needs a name")
	}
	if p.Status == "" {
		p.Status = StepPending
	}
	p.UpdatedAt = normTime(p.UpdatedAt)
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = s.clock()
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := jobExists(ctx, tx, p.JobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO post_steps (job_id, name, status, attempts, detail, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (job_id, name) DO UPDATE SET status = excluded.status, attempts = excluded.attempts,
		   detail = excluded.detail, updated_at = excluded.updated_at`,
		p.JobID, p.Name, p.Status, p.Attempts, p.Detail, nanos(p.UpdatedAt)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) PostSteps(ctx context.Context, jobID string) ([]PostStep, error) {
	rows, err := s.r.QueryContext(ctx,
		"SELECT job_id, name, status, attempts, detail, updated_at FROM post_steps WHERE job_id = ? ORDER BY seq", jobID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PostStep
	for rows.Next() {
		var (
			p  PostStep
			at sql.NullInt64
		)
		if err := rows.Scan(&p.JobID, &p.Name, &p.Status, &p.Attempts, &p.Detail, &at); err != nil {
			return nil, err
		}
		p.UpdatedAt = fromNanos(at)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- active jobs ----------------------------------------------------------------------

func (s *sqliteStore) ActiveJobsFor(ctx context.Context, app string, titleID int) ([]Job, error) {
	rows, err := s.r.QueryContext(ctx, "SELECT "+jobCols+` FROM jobs
		WHERE state NOT IN (?, ?)
		  AND id IN (SELECT job_id FROM targets WHERE app = ? AND title_id = ?)
		ORDER BY created_at DESC, seq DESC`, Done, Cancelled, app, titleID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
