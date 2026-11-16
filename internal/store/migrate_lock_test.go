package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/segun-io/fukku/internal/store/migrations"
)

// Many processes starting at once on a fresh data directory: exactly one gets it, the
// others are told it is in use, and the schema is applied once.
func TestConcurrentOpen(t *testing.T) {
	for round := range 20 {
		dir := t.TempDir()
		const n = 6
		var (
			wg     sync.WaitGroup
			start  = make(chan struct{})
			stores = make([]Store, n)
			errs   = make([]error, n)
		)
		for i := range n {
			wg.Go(func() {
				<-start
				stores[i], errs[i] = Open(t.Context(), Options{Dir: dir})
			})
		}
		close(start)
		wg.Wait()
		won := 0
		for i, err := range errs {
			switch {
			case err == nil:
				won++
				if err := stores[i].Close(); err != nil {
					t.Fatal(err)
				}
			case !errors.Is(err, ErrLocked):
				t.Fatalf("round %d: %v, want ErrLocked", round, err)
			}
		}
		if won != 1 {
			t.Fatalf("round %d: %d opens succeeded, want 1", round, won)
		}
		// Released on Close: the next start gets it, and has nothing to migrate.
		s, logs := openLogged(t, dir)
		if strings.Contains(logs, "store_migrated") {
			t.Fatalf("round %d: migrated again:\n%s", round, logs)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// Without the data-directory lock (another tool, say), migrators racing on one database
// still apply each migration once: the version is re-read inside the write transaction.
func TestConcurrentMigrate(t *testing.T) {
	ms, err := loadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	for round := range 20 {
		path := filepath.Join(t.TempDir(), FileName)
		const n = 4
		var (
			wg      sync.WaitGroup
			start   = make(chan struct{})
			applied = make([][]int, n)
			errs    = make([]error, n)
		)
		for i := range n {
			db := rawDB(t, dsn(path, false))
			wg.Go(func() {
				<-start
				applied[i], errs[i] = migrate(t.Context(), db, migrations.FS)
			})
		}
		close(start)
		wg.Wait()
		total := 0
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d: migrator %d: %v", round, i, err)
			}
			total += len(applied[i])
		}
		if total != len(ms) {
			t.Fatalf("round %d: %d migrations applied in total, want %d (%v)", round, total, len(ms), applied)
		}
	}
}

func TestApplySkipsAppliedMigration(t *testing.T) {
	db := rawDB(t, filepath.Join(t.TempDir(), "m.db"))
	src := fstest.MapFS{"0001_one.sql": {Data: []byte("CREATE TABLE a (x INTEGER);")}}
	if _, err := migrate(t.Context(), db, src); err != nil {
		t.Fatal(err)
	}
	ms, err := loadMigrations(src)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	done, err := apply(t.Context(), conn, ms[0])
	if err != nil || done {
		t.Fatalf("re-applying 0001: applied %v, %v; want skipped", done, err)
	}
}

// The switch to WAL waits for a lock another process holds for a moment.
func TestWALSwitchWaitsForLock(t *testing.T) {
	dir := t.TempDir()
	other := rawDB(t, filepath.Join(dir, FileName))
	conn, err := other.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "CREATE TABLE other (x INTEGER)"); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(300 * time.Millisecond)
		_, _ = conn.ExecContext(context.Background(), "COMMIT")
		_ = conn.Close()
	}()
	s, err := Open(t.Context(), Options{Dir: dir})
	<-released
	if err != nil {
		t.Fatalf("open while another connection held the lock: %v", err)
	}
	defer func() { _ = s.Close() }()
	if m := scalar[string](t, s.(*sqliteStore).w, "PRAGMA journal_mode"); m != "wal" {
		t.Fatalf("journal mode %s", m)
	}
}

func TestCloseTwice(t *testing.T) {
	s, err := Open(t.Context(), Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestDataDirLock(t *testing.T) {
	dir := t.TempDir()
	s, _ := openLogged(t, dir)
	_, err := Open(t.Context(), Options{Dir: dir})
	if !errors.Is(err, ErrLocked) || !strings.Contains(err.Error(), "server.dataDir") {
		t.Fatalf("second open: %v, want ErrLocked naming server.dataDir", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(t.Context(), Options{Dir: dir})
	if err != nil {
		t.Fatalf("open after close: %v", err)
	}
	_ = again.Close()
}

// fkDB is a database with foreign keys on, a parent table and a child row that cascades.
func fkDB(t *testing.T) (*sql.DB, fstest.MapFS) {
	t.Helper()
	db := rawDB(t, filepath.Join(t.TempDir(), "fk.db")+"?_pragma=foreign_keys(1)")
	src := fstest.MapFS{"0001_init.sql": {Data: []byte(`
CREATE TABLE parent (id INTEGER PRIMARY KEY, name TEXT);
CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent (id) ON DELETE CASCADE);
INSERT INTO parent VALUES (1, 'a');
INSERT INTO child VALUES (10, 1);`)}}
	if _, err := migrate(t.Context(), db, src); err != nil {
		t.Fatal(err)
	}
	return db, src
}

// rebuild is a table rebuild of parent (the SQLite way to change a column).
const rebuild = `
CREATE TABLE parent_new (id INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT '');
INSERT INTO parent_new SELECT id, coalesce(name, '') FROM parent;
DROP TABLE parent;
ALTER TABLE parent_new RENAME TO parent;`

func TestMigrationWithForeignKeysOff(t *testing.T) {
	// Why the directive exists: with foreign keys on, the rebuild deletes the children.
	db, src := fkDB(t)
	src["0002_rebuild.sql"] = &fstest.MapFile{Data: []byte(rebuild)}
	if _, err := migrate(t.Context(), db, src); err != nil {
		t.Fatal(err)
	}
	if n := scalar[int](t, db, "SELECT count(*) FROM child"); n != 0 {
		t.Fatalf("expected the cascade to delete the child row, %d left", n)
	}

	db, src = fkDB(t)
	src["0002_rebuild.sql"] = &fstest.MapFile{Data: []byte(migrations.ForeignKeysOff + "\n" + rebuild)}
	applied, err := migrate(t.Context(), db, src)
	if err != nil || len(applied) != 1 {
		t.Fatalf("applied %v, %v", applied, err)
	}
	if n := scalar[int](t, db, "SELECT count(*) FROM child"); n != 1 {
		t.Fatalf("the rebuild with foreign keys off lost child rows: %d left", n)
	}
	if v := scalar[int](t, db, "PRAGMA foreign_keys"); v != 1 {
		t.Fatal("foreign keys stayed off after the migration")
	}
	if v := scalar[int](t, db, "PRAGMA user_version"); v != 2 {
		t.Fatalf("schema version %d", v)
	}
}

func TestMigrationWithForeignKeysOffChecksReferences(t *testing.T) {
	db, src := fkDB(t)
	// Replaces the parent without its rows: the child row now refers to nothing.
	src["0002_break.sql"] = &fstest.MapFile{Data: []byte(migrations.ForeignKeysOff + `
CREATE TABLE parent_new (id INTEGER PRIMARY KEY, name TEXT);
DROP TABLE parent;
ALTER TABLE parent_new RENAME TO parent;`)}
	_, err := migrate(t.Context(), db, src)
	if err == nil || !strings.Contains(err.Error(), "foreign key check failed") {
		t.Fatalf("got %v, want a foreign key check failure", err)
	}
	if v := scalar[int](t, db, "PRAGMA user_version"); v != 1 {
		t.Fatalf("schema version %d after a failed migration, want 1", v)
	}
	if n := scalar[int](t, db, "SELECT count(*) FROM parent"); n != 1 {
		t.Fatal("the failed migration was not rolled back")
	}
	if v := scalar[int](t, db, "PRAGMA foreign_keys"); v != 1 {
		t.Fatal("foreign keys stayed off after a failed migration")
	}
}

func TestEmbeddedMigrationsLoad(t *testing.T) {
	ms, err := loadMigrations(migrations.FS)
	if err != nil || len(ms) == 0 {
		t.Fatalf("%d migrations, %v", len(ms), err)
	}
}
