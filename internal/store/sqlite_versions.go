package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// The second versions (contract_versions.go). Writes go through the one write connection.

const versionCols = `id, job_id, app, title_id, tmdb_id, title, year, path, label, status, detail,
	primary_file_id, primary_path, primary_item_id, item_id, created_at, checked_at, grouped_at, primary_changed_at`

func scanVersion(sc interface{ Scan(...any) error }) (Version, error) {
	var (
		v                         Version
		created                   int64
		checked, grouped, changed sql.NullInt64
	)
	err := sc.Scan(&v.ID, &v.JobID, &v.App, &v.TitleID, &v.TMDBID, &v.Title, &v.Year, &v.Path, &v.Label, &v.Status, &v.Detail,
		&v.PrimaryFileID, &v.PrimaryPath, &v.PrimaryItemID, &v.ItemID, &created, &checked, &grouped, &changed)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, err
	}
	v.CreatedAt = fromNanos(sql.NullInt64{Int64: created, Valid: true})
	v.CheckedAt, v.GroupedAt, v.PrimaryChangedAt = fromNanos(checked), fromNanos(grouped), fromNanos(changed)
	return v, nil
}

func (s *sqliteStore) PutVersion(ctx context.Context, v Version, audit ...AuditEntry) (Version, error) {
	if v.Path == "" {
		return Version{}, errors.New("store: a version needs a path")
	}
	for _, e := range audit {
		if e.Action == "" {
			return Version{}, errors.New("store: an audit entry needs an action")
		}
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Version{}, err
	}
	defer func() { _ = tx.Rollback() }()
	v.CreatedAt, v.CheckedAt, v.GroupedAt, v.PrimaryChangedAt = normTime(v.CreatedAt), normTime(v.CheckedAt), normTime(v.GroupedAt), normTime(v.PrimaryChangedAt)
	if v.ID == 0 {
		if v.CreatedAt.IsZero() {
			v.CreatedAt = s.clock()
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO versions (job_id, app, title_id, tmdb_id, title, year, path, label, status, detail,
			primary_file_id, primary_path, primary_item_id, item_id, created_at, checked_at, grouped_at, primary_changed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			v.JobID, v.App, v.TitleID, v.TMDBID, v.Title, v.Year, v.Path, v.Label, v.Status, v.Detail,
			v.PrimaryFileID, v.PrimaryPath, v.PrimaryItemID, v.ItemID, v.CreatedAt.UnixNano(), nanos(v.CheckedAt), nanos(v.GroupedAt), nanos(v.PrimaryChangedAt))
		if isUnique(err) {
			return Version{}, ErrExists
		}
		if err != nil {
			return Version{}, err
		}
		if v.ID, err = res.LastInsertId(); err != nil {
			return Version{}, err
		}
	} else {
		old, err := scanVersion(tx.QueryRowContext(ctx, "SELECT "+versionCols+" FROM versions WHERE id = ?", v.ID))
		if err != nil {
			return Version{}, err
		}
		v.CreatedAt = old.CreatedAt
		_, err = tx.ExecContext(ctx, `UPDATE versions SET job_id = ?, app = ?, title_id = ?, tmdb_id = ?, title = ?, year = ?, path = ?, label = ?,
			status = ?, detail = ?, primary_file_id = ?, primary_path = ?, primary_item_id = ?, item_id = ?,
			checked_at = ?, grouped_at = ?, primary_changed_at = ? WHERE id = ?`,
			v.JobID, v.App, v.TitleID, v.TMDBID, v.Title, v.Year, v.Path, v.Label, v.Status, v.Detail,
			v.PrimaryFileID, v.PrimaryPath, v.PrimaryItemID, v.ItemID, nanos(v.CheckedAt), nanos(v.GroupedAt), nanos(v.PrimaryChangedAt), v.ID)
		if isUnique(err) {
			return Version{}, ErrExists
		}
		if err != nil {
			return Version{}, err
		}
	}
	entries := make([]AuditEntry, 0, len(audit))
	for _, e := range audit {
		if e.JobID == "" {
			e.JobID = v.JobID
		}
		e.Actor = actorOr(e.Actor)
		if e.At = normTime(e.At); e.At.IsZero() {
			e.At = s.clock()
		}
		var detail any
		if len(e.Detail) > 0 {
			b, err := json.Marshal(e.Detail)
			if err != nil {
				return Version{}, err
			}
			detail = string(b)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO audit (at, actor, email, action, job_id, detail) VALUES (?, ?, ?, ?, ?, ?)",
			nanos(e.At), e.Actor, e.Email, e.Action, e.JobID, detail); err != nil {
			return Version{}, err
		}
		entries = append(entries, e)
	}
	if err := tx.Commit(); err != nil {
		return Version{}, err
	}
	for _, e := range entries {
		s.log.InfoContext(ctx, "audit", auditAttrs(e)...)
	}
	return v, nil
}

func (s *sqliteStore) DeleteVersion(ctx context.Context, id int64) error {
	res, err := s.w.ExecContext(ctx, "DELETE FROM versions WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *sqliteStore) VersionByPath(ctx context.Context, path string) (Version, error) {
	return scanVersion(s.r.QueryRowContext(ctx, "SELECT "+versionCols+" FROM versions WHERE path = ?", path))
}

func (s *sqliteStore) VersionsOf(ctx context.Context, jobID string) ([]Version, error) {
	return s.versions(ctx, "SELECT "+versionCols+" FROM versions WHERE job_id = ? ORDER BY id", jobID)
}

func (s *sqliteStore) AllVersions(ctx context.Context) ([]Version, error) {
	return s.versions(ctx, "SELECT "+versionCols+" FROM versions ORDER BY id")
}

func (s *sqliteStore) versions(ctx context.Context, query string, args ...any) ([]Version, error) {
	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
