package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/segun-io/fukku/internal/store/migrations"
)

// FileName is the database file inside server.dataDir.
const FileName = "fukku.db"

// busyTimeout is how long a statement waits for a lock held by another connection or
// process (an online backup, say) before it fails.
const busyTimeout = 5 * time.Second

// Options configure Open.
type Options struct {
	Dir string           // server.dataDir; the database is <Dir>/fukku.db
	Log *slog.Logger     // nil: discard
	Now func() time.Time // nil: time.Now
}

// Open opens (creating it if needed) the database in o.Dir and applies the migrations this
// binary has. It refuses a data directory that is not writable or is on network storage,
// one that another process has open (ErrLocked), and a database written by a newer Fukku.
func Open(ctx context.Context, o Options) (Store, error) {
	if o.Dir == "" {
		return nil, errors.New("store: server.dataDir is empty")
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := checkDataDir(o.Dir); err != nil {
		return nil, err
	}
	// One process per database: held until Close (or the process ends).
	lock, err := lockDataDir(o.Dir)
	if err != nil {
		return nil, err
	}
	s, err := open(ctx, o, lock)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	return s, nil
}

func open(ctx context.Context, o Options, lock io.Closer) (*sqliteStore, error) {
	path := filepath.Join(o.Dir, FileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // the configured data directory
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	// One connection writes, so writes are serialised and a transaction never waits on
	// another one of ours; reads use their own pool (WAL lets them run alongside).
	w, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	w.SetMaxOpenConns(1)
	s := &sqliteStore{w: w, log: o.Log, now: o.Now, lock: lock}
	if err := s.init(ctx, path); err != nil {
		_ = w.Close()
		return nil, err
	}
	s.r, err = sql.Open("sqlite", dsn(path, true))
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("store: %w", err)
	}
	s.r.SetMaxOpenConns(8)
	return s, nil
}

func (s *sqliteStore) init(ctx context.Context, path string) error {
	if err := enableWAL(ctx, s.w, path); err != nil {
		return err
	}
	applied, err := migrate(ctx, s.w, migrations.FS)
	for _, v := range applied {
		s.log.Info("store_migrated", "version", v)
	}
	if err != nil {
		return err
	}
	var version int
	if err := s.w.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	s.log.Info("store_opened", "path", path, "schema", version)
	return nil
}

func dsn(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(FULL)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	} else {
		// Take the write lock at BEGIN, so a transaction that reads and then writes cannot
		// fail half-way on a lock upgrade.
		q.Set("_txlock", "immediate")
	}
	return path + "?" + q.Encode()
}

// enableWAL switches the database to WAL (a persistent setting). The switch needs a lock
// that another process (an online backup, say) may hold for a moment; it is retried for
// up to busyTimeout.
func enableWAL(ctx context.Context, db *sql.DB, path string) error {
	deadline := time.Now().Add(busyTimeout)
	for {
		var mode string
		err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode)
		if err == nil {
			if !strings.EqualFold(mode, "wal") {
				return fmt.Errorf("store: %s: journal mode is %q, want wal", path, mode)
			}
			return nil
		}
		if !isBusy(err) || time.Now().After(deadline) {
			return fmt.Errorf("store: open %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func isBusy(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	primary := se.Code() & 0xff // extended result codes keep the primary code in the low byte
	return primary == sqlite3.SQLITE_BUSY || primary == sqlite3.SQLITE_LOCKED
}

type sqliteStore struct {
	w    *sql.DB // one connection: every write
	r    *sql.DB // reads
	lock io.Closer
	log  *slog.Logger

	closeOnce sync.Once
	closeErr  error

	now func() time.Time
}

func (s *sqliteStore) clock() time.Time { return normTime(s.now()) }

// normTime drops the monotonic reading and the location, and keeps what the database keeps.
func normTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return time.Unix(0, t.UnixNano()).UTC()
}

func nanos(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixNano()
}

func fromNanos(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.Unix(0, n.Int64).UTC()
}

func actorOr(a string) string {
	if a == "" {
		return "system"
	}
	return a
}

type scanner interface{ Scan(dest ...any) error }

// ---- jobs -----------------------------------------------------------------------------

const jobCols = "id, owner, state, resume, attempt, error_code, error_text, created_at, updated_at, expires_at"

func scanJob(row scanner) (Job, error) {
	var (
		j                         Job
		created, updated, expires sql.NullInt64
	)
	err := row.Scan(&j.ID, &j.Owner, &j.State, &j.Resume, &j.Attempt, &j.ErrorCode, &j.ErrorText, &created, &updated, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	j.CreatedAt, j.UpdatedAt, j.ExpiresAt = fromNanos(created), fromNanos(updated), fromNanos(expires)
	return j, nil
}

func (s *sqliteStore) CreateJob(ctx context.Context, owner string) (Job, error) {
	now := s.clock()
	j := Job{ID: NewID(), Owner: owner, State: Uploading, Attempt: 1, CreatedAt: now, UpdatedAt: now}
	_, err := s.w.ExecContext(ctx,
		"INSERT INTO jobs (id, owner, state, attempt, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		j.ID, j.Owner, j.State, j.Attempt, nanos(now), nanos(now))
	if err != nil {
		return Job{}, err
	}
	return j, nil
}

func (s *sqliteStore) Job(ctx context.Context, id string) (Job, error) {
	return scanJob(s.r.QueryRowContext(ctx, "SELECT "+jobCols+" FROM jobs WHERE id = ?", id))
}

func (s *sqliteStore) Jobs(ctx context.Context, f JobFilter) ([]Job, error) {
	states, err := json.Marshal(f.States)
	if err != nil {
		return nil, err
	}
	limit, offset := pageOf(f)
	rows, err := s.r.QueryContext(ctx, "SELECT "+jobCols+` FROM jobs
		WHERE (? = '' OR owner = ?) AND (? = 0 OR state IN (SELECT value FROM json_each(?)))
		ORDER BY created_at DESC, seq DESC LIMIT ? OFFSET ?`,
		f.Owner, f.Owner, len(f.States), string(states), limit, offset)
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

// pageOf applies the JobFilter defaults: limit 0 (or less) means 100, a negative offset 0.
func pageOf(f JobFilter) (limit, offset int) {
	limit, offset = f.Limit, f.Offset
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (s *sqliteStore) Transition(ctx context.Context, id string, from, to State, c Change) (Job, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	cur, err := scanJob(tx.QueryRowContext(ctx, "SELECT "+jobCols+" FROM jobs WHERE id = ?", id))
	if err != nil {
		return Job{}, err
	}
	next, ev, err := applyTransition(cur, from, to, c, s.clock())
	if err != nil {
		return Job{}, err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET state = ?, resume = ?, attempt = ?, error_code = ?, error_text = ?, updated_at = ?, expires_at = ?
		 WHERE id = ? AND state = ?`,
		next.State, next.Resume, next.Attempt, next.ErrorCode, next.ErrorText, nanos(next.UpdatedAt), nanos(next.ExpiresAt),
		id, from)
	if err != nil {
		return Job{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Job{}, err
	} else if n != 1 {
		return Job{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO events (job_id, at, from_state, to_state, actor, note) VALUES (?, ?, ?, ?, ?, ?)",
		ev.JobID, nanos(ev.At), ev.From, ev.To, ev.Actor, ev.Note); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return next, nil
}

// applyTransition is the state machine's rule book: it checks that j may go from → to and
// returns the job after the change and the event that records it.
func applyTransition(j Job, from, to State, c Change, now time.Time) (Job, Event, error) {
	if j.State != from {
		return Job{}, Event{}, ErrConflict
	}
	leaving := from == Failed || from == Stalled
	resuming := leaving && j.Resume != "" && to == j.Resume
	if !CanTransition(from, to) && !resuming {
		return Job{}, Event{}, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
	}
	if (to == Failed || to == Stalled) && c.ErrorCode == "" {
		return Job{}, Event{}, fmt.Errorf("%w: moving to %s needs an error code", ErrInvalidTransition, to)
	}
	next := j
	next.State, next.UpdatedAt = to, now
	switch to {
	case Failed:
		next.Resume = ResumeState(from)
		if from == Stalled {
			next.Resume = ResumeState(j.Resume) // it failed in the state it had stalled in
		}
		next.ErrorCode, next.ErrorText = c.ErrorCode, c.ErrorText
	case Stalled:
		next.Resume = from
		next.ErrorCode, next.ErrorText = c.ErrorCode, c.ErrorText
	default:
		next.Resume, next.ErrorCode, next.ErrorText = "", "", ""
	}
	if from == Failed && resuming {
		next.Attempt++
	}
	if c.ExpiresAt != nil {
		next.ExpiresAt = normTime(*c.ExpiresAt)
	}
	return next, Event{JobID: j.ID, At: now, From: from, To: to, Actor: actorOr(c.Actor), Note: c.Note}, nil
}

func (s *sqliteStore) History(ctx context.Context, jobID string) ([]Event, error) {
	rows, err := s.r.QueryContext(ctx,
		"SELECT job_id, at, from_state, to_state, actor, note FROM events WHERE job_id = ? ORDER BY id", jobID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var (
			e  Event
			at sql.NullInt64
		)
		if err := rows.Scan(&e.JobID, &at, &e.From, &e.To, &e.Actor, &e.Note); err != nil {
			return nil, err
		}
		e.At = fromNanos(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *sqliteStore) DeleteJob(ctx context.Context, id string) error {
	// Files, uploads and events go with the job (ON DELETE CASCADE); audit entries stay.
	res, err := s.w.ExecContext(ctx, "DELETE FROM jobs WHERE id = ?", id)
	return affectedOne(res, err)
}

func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func isUnique(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) &&
		(se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || se.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY)
}

func jobExists(ctx context.Context, tx *sql.Tx, id string) error {
	var one int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM jobs WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ---- files ----------------------------------------------------------------------------

func report(r json.RawMessage) any {
	if len(r) == 0 {
		return nil
	}
	return []byte(r)
}

func (s *sqliteStore) AddFile(ctx context.Context, f File) (File, error) {
	if f.Status == "" {
		f.Status = FilePending
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return File{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := jobExists(ctx, tx, f.JobID); err != nil {
		return File{}, err
	}
	res, err := tx.ExecContext(ctx,
		"INSERT INTO files (job_id, path, kind, size, status, reason, from_path, report) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		f.JobID, f.Path, f.Kind, f.Size, f.Status, f.Reason, f.From, report(f.Report))
	if isUnique(err) {
		return File{}, ErrExists
	}
	if err != nil {
		return File{}, err
	}
	if f.ID, err = res.LastInsertId(); err != nil {
		return File{}, err
	}
	if err := tx.Commit(); err != nil {
		return File{}, err
	}
	if len(f.Report) == 0 {
		f.Report = nil
	} else {
		f.Report = slices.Clone(f.Report)
	}
	return f, nil
}

func (s *sqliteStore) UpdateFile(ctx context.Context, f File) error {
	if f.Status == "" {
		f.Status = FilePending
	}
	res, err := s.w.ExecContext(ctx,
		"UPDATE files SET path = ?, kind = ?, size = ?, status = ?, reason = ?, from_path = ?, report = ? WHERE id = ?",
		f.Path, f.Kind, f.Size, f.Status, f.Reason, f.From, report(f.Report), f.ID)
	if isUnique(err) {
		return ErrExists
	}
	return affectedOne(res, err)
}

func (s *sqliteStore) DeleteFile(ctx context.Context, id int64) error {
	res, err := s.w.ExecContext(ctx, "DELETE FROM files WHERE id = ?", id)
	return affectedOne(res, err)
}

func (s *sqliteStore) Files(ctx context.Context, jobID string) ([]File, error) {
	rows, err := s.r.QueryContext(ctx,
		"SELECT id, job_id, path, kind, size, status, reason, from_path, report FROM files WHERE job_id = ? ORDER BY path", jobID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []File
	for rows.Next() {
		var (
			f   File
			rep []byte
		)
		if err := rows.Scan(&f.ID, &f.JobID, &f.Path, &f.Kind, &f.Size, &f.Status, &f.Reason, &f.From, &rep); err != nil {
			return nil, err
		}
		if len(rep) > 0 {
			f.Report = json.RawMessage(rep)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---- uploads --------------------------------------------------------------------------

const uploadCols = "id, job_id, owner, name, size, offset_, created_at, progress_at"

func scanUpload(row scanner) (Upload, error) {
	var (
		u                 Upload
		created, progress sql.NullInt64
	)
	err := row.Scan(&u.ID, &u.JobID, &u.Owner, &u.Name, &u.Size, &u.Offset, &created, &progress)
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, err
	}
	u.CreatedAt, u.ProgressAt = fromNanos(created), fromNanos(progress)
	return u, nil
}

// PutUpload inserts or updates u. A zero CreatedAt or ProgressAt keeps the stored value; on
// insert they default to now and to CreatedAt.
func (s *sqliteStore) PutUpload(ctx context.Context, u Upload) error {
	if u.ID == "" {
		return errors.New("store: upload id is empty")
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := jobExists(ctx, tx, u.JobID); err != nil {
		return err
	}
	old, err := scanUpload(tx.QueryRowContext(ctx, "SELECT "+uploadCols+" FROM uploads WHERE id = ?", u.ID))
	switch {
	case errors.Is(err, ErrNotFound):
		old = Upload{CreatedAt: s.clock()}
	case err != nil:
		return err
	}
	u = uploadDefaults(u, old)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO uploads (`+uploadCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET job_id = excluded.job_id, owner = excluded.owner, name = excluded.name,
		   size = excluded.size, offset_ = excluded.offset_, created_at = excluded.created_at, progress_at = excluded.progress_at`,
		u.ID, u.JobID, u.Owner, u.Name, u.Size, u.Offset, nanos(u.CreatedAt), nanos(u.ProgressAt)); err != nil {
		return err
	}
	return tx.Commit()
}

// uploadDefaults fills the zero times of u from the stored row old (or, for a new upload,
// old.CreatedAt is now).
func uploadDefaults(u, old Upload) Upload {
	u.CreatedAt, u.ProgressAt = normTime(u.CreatedAt), normTime(u.ProgressAt)
	if u.CreatedAt.IsZero() {
		u.CreatedAt = old.CreatedAt
	}
	if u.ProgressAt.IsZero() {
		u.ProgressAt = old.ProgressAt
	}
	if u.ProgressAt.IsZero() {
		u.ProgressAt = u.CreatedAt
	}
	return u
}

func (s *sqliteStore) Upload(ctx context.Context, id string) (Upload, error) {
	return scanUpload(s.r.QueryRowContext(ctx, "SELECT "+uploadCols+" FROM uploads WHERE id = ?", id))
}

func (s *sqliteStore) Uploads(ctx context.Context, owner, jobID string) ([]Upload, error) {
	rows, err := s.r.QueryContext(ctx,
		"SELECT "+uploadCols+" FROM uploads WHERE (? = '' OR owner = ?) AND (? = '' OR job_id = ?) ORDER BY created_at, id",
		owner, owner, jobID, jobID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *sqliteStore) DeleteUpload(ctx context.Context, id string) error {
	res, err := s.w.ExecContext(ctx, "DELETE FROM uploads WHERE id = ?", id)
	return affectedOne(res, err)
}

// ---- audit ----------------------------------------------------------------------------

func (s *sqliteStore) Audit(ctx context.Context, e AuditEntry) error {
	if e.Action == "" {
		return errors.New("store: an audit entry needs an action")
	}
	e.At = normTime(e.At)
	if e.At.IsZero() {
		e.At = s.clock()
	}
	e.Actor = actorOr(e.Actor)
	var detail any
	if len(e.Detail) > 0 {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return err
		}
		detail = string(b)
	}
	if _, err := s.w.ExecContext(ctx,
		"INSERT INTO audit (at, actor, email, action, job_id, detail) VALUES (?, ?, ?, ?, ?, ?)",
		nanos(e.At), e.Actor, e.Email, e.Action, e.JobID, detail); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "audit", auditAttrs(e)...)
	return nil
}

// auditAttrs is the log line of an audit entry: action, actor, job and the detail keys in
// order. The email address is never logged; a detail key that would shadow one of the
// fixed keys gets the prefix detail_.
func auditAttrs(e AuditEntry) []any {
	attrs := []any{"action", e.Action, "actor", e.Actor, "job", e.JobID}
	keys := make([]string, 0, len(e.Detail))
	for k := range e.Detail {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		name := k
		switch k {
		case "email":
			continue
		case "action", "actor", "job", "msg", "level", "time":
			name = "detail_" + k
		}
		attrs = append(attrs, name, e.Detail[k])
	}
	return attrs
}

func (s *sqliteStore) AuditLog(ctx context.Context, jobID string) ([]AuditEntry, error) {
	rows, err := s.r.QueryContext(ctx,
		"SELECT id, at, actor, email, action, job_id, detail FROM audit WHERE (? = '' OR job_id = ?) ORDER BY at, id",
		jobID, jobID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AuditEntry
	for rows.Next() {
		var (
			e      AuditEntry
			at     sql.NullInt64
			detail sql.NullString
		)
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.Email, &e.Action, &e.JobID, &detail); err != nil {
			return nil, err
		}
		e.At = fromNanos(at)
		if detail.Valid {
			if err := json.Unmarshal([]byte(detail.String), &e.Detail); err != nil {
				return nil, fmt.Errorf("store: audit entry %d: %w", e.ID, err)
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- lifecycle ------------------------------------------------------------------------

func (s *sqliteStore) Ping(ctx context.Context) error {
	var one int
	return s.r.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// Close closes the database and releases the data directory. Closing twice is harmless.
func (s *sqliteStore) Close() error {
	s.closeOnce.Do(func() { s.closeErr = errors.Join(s.r.Close(), s.w.Close(), s.lock.Close()) })
	return s.closeErr
}
