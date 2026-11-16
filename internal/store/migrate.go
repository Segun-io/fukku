package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/segun-io/fukku/internal/store/migrations"
)

// The rules for migration files are in package migrations.

type migration struct {
	version  int
	name     string
	sql      string
	fkeysOff bool // the file starts with migrations.ForeignKeysOff
}

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

// loadMigrations reads the .sql files of src and checks they are numbered 1, 2, 3, …
// without gaps.
func loadMigrations(src fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue // migrations.go
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			return nil, fmt.Errorf("store: unexpected migration file %q (want NNNN_name.sql)", e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		b, err := fs.ReadFile(src, e.Name())
		if err != nil {
			return nil, err
		}
		first, _, _ := strings.Cut(string(b), "\n")
		ms = append(ms, migration{
			version:  v,
			name:     e.Name(),
			sql:      string(b),
			fkeysOff: strings.TrimSpace(first) == migrations.ForeignKeysOff,
		})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("store: migration %s: expected version %d", m.name, i+1)
		}
	}
	return ms, nil
}

// errSchemaTooNew: the database was written by a newer Fukku.
var errSchemaTooNew = errors.New("store: the database schema is newer than this binary")

func userVersion(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
},
) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
	return v, nil
}

// migrate brings db to the latest version in src and returns the versions it applied.
func migrate(ctx context.Context, db *sql.DB, src fs.FS) ([]int, error) {
	ms, err := loadMigrations(src)
	if err != nil {
		return nil, err
	}
	// Every step runs on one pinned connection, so the foreign_keys setting of a
	// ForeignKeysOff migration applies to its transaction.
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	current, err := userVersion(ctx, conn)
	if err != nil {
		return nil, err
	}
	if current > len(ms) {
		return nil, fmt.Errorf("%w: database version %d, this binary knows up to %d; run a newer Fukku or restore a backup", errSchemaTooNew, current, len(ms))
	}
	var applied []int
	for _, m := range ms[current:] {
		done, err := apply(ctx, conn, m)
		if err != nil {
			return applied, err
		}
		if done {
			applied = append(applied, m.version)
		}
	}
	return applied, nil
}

// apply runs m in one transaction with the schema version bump. It re-reads the version
// inside the transaction and does nothing (false) when m is already applied.
func apply(ctx context.Context, conn *sql.Conn, m migration) (applied bool, err error) {
	wrap := func(err error) error { return fmt.Errorf("store: migration %s: %w", m.name, err) }
	if m.fkeysOff {
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return false, wrap(err)
		}
		defer func() {
			// Back on even when the context is done, or the connection would stay unsafe.
			if _, onErr := conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON"); onErr != nil {
				err = errors.Join(err, wrap(onErr))
			}
		}()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false, wrap(err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := userVersion(ctx, tx)
	if err != nil {
		return false, err
	}
	if current >= m.version {
		return false, nil // applied meanwhile by someone else
	}
	if current != m.version-1 {
		return false, wrap(fmt.Errorf("database is at version %d", current))
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return false, wrap(err)
	}
	if m.fkeysOff {
		if err := foreignKeyCheck(ctx, tx); err != nil {
			return false, wrap(err)
		}
	}
	// PRAGMA takes no parameters; the version is an integer we produced.
	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.version)); err != nil {
		return false, wrap(err)
	}
	if err := tx.Commit(); err != nil {
		return false, wrap(err)
	}
	return true, nil
}

// foreignKeyCheck fails when any row refers to a parent that does not exist.
func foreignKeyCheck(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var bad []string
	for rows.Next() {
		var (
			table, parent string
			rowid         sql.NullInt64
			fkid          int
		)
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return err
		}
		bad = append(bad, fmt.Sprintf("%s row %d → %s", table, rowid.Int64, parent))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("foreign key check failed: %s", strings.Join(bad, ", "))
	}
	return nil
}
