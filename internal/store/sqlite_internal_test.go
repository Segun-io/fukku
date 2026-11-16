package store

import (
	"bytes"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/segun-io/fukku/internal/store/migrations"
)

func openLogged(t *testing.T, dir string) (*sqliteStore, string) {
	t.Helper()
	var buf bytes.Buffer
	s, err := Open(t.Context(), Options{Dir: dir, Log: slog.New(slog.NewTextHandler(&buf, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s.(*sqliteStore), buf.String()
}

func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func scalar[T any](t *testing.T, db *sql.DB, q string) T {
	t.Helper()
	var v T
	if err := db.QueryRowContext(t.Context(), q).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

func TestMigrateFromEmpty(t *testing.T) {
	s, logs := openLogged(t, t.TempDir())
	if !strings.Contains(logs, "msg=store_migrated version=1") {
		t.Fatalf("no migration logged:\n%s", logs)
	}
	ms, err := loadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if v := scalar[int](t, s.w, "PRAGMA user_version"); v != len(ms) {
		t.Fatalf("schema version %d, want %d", v, len(ms))
	}
	for _, table := range []string{"jobs", "files", "uploads", "events", "audit"} {
		if n := scalar[int](t, s.r, "SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = '"+table+"'"); n != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
}

func TestReopenAppliesNothing(t *testing.T) {
	dir := t.TempDir()
	s, _ := openLogged(t, dir)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, logs := openLogged(t, dir)
	if strings.Contains(logs, "store_migrated") {
		t.Fatalf("re-open applied a migration:\n%s", logs)
	}
	if !strings.Contains(logs, "msg=store_opened") {
		t.Fatalf("no store_opened line:\n%s", logs)
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	dir := t.TempDir()
	s, _ := openLogged(t, dir)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db := rawDB(t, filepath.Join(dir, FileName))
	if _, err := db.ExecContext(t.Context(), "PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	_, err := Open(t.Context(), Options{Dir: dir})
	if !errors.Is(err, errSchemaTooNew) {
		t.Fatalf("got %v, want errSchemaTooNew", err)
	}
}

func TestMigrationIsAtomic(t *testing.T) {
	ctx := t.Context()
	db := rawDB(t, filepath.Join(t.TempDir(), "m.db"))
	bad := fstest.MapFS{
		"0001_one.sql": {Data: []byte("CREATE TABLE a (x INTEGER);\nINSERT INTO a VALUES (1);")},
		"0002_two.sql": {Data: []byte("CREATE TABLE b (x INTEGER);\nINSERT INTO missing VALUES (1);")},
	}
	applied, err := migrate(ctx, db, bad)
	if err == nil || len(applied) != 1 || applied[0] != 1 {
		t.Fatalf("applied %v, err %v: want 0001 only and an error", applied, err)
	}
	if v := scalar[int](t, db, "PRAGMA user_version"); v != 1 {
		t.Fatalf("schema version %d after a failed migration, want 1", v)
	}
	if n := scalar[int](t, db, "SELECT count(*) FROM sqlite_schema WHERE name = 'b'"); n != 0 {
		t.Fatal("the failed migration left table b behind")
	}
	good := fstest.MapFS{
		"0001_one.sql": bad["0001_one.sql"],
		"0002_two.sql": {Data: []byte("CREATE TABLE b (x INTEGER);\nINSERT INTO b VALUES (2);")},
	}
	if applied, err = migrate(ctx, db, good); err != nil || len(applied) != 1 || applied[0] != 2 {
		t.Fatalf("applied %v, err %v: want 0002", applied, err)
	}
	if applied, err = migrate(ctx, db, good); err != nil || len(applied) != 0 {
		t.Fatalf("second run applied %v, err %v", applied, err)
	}
}

func TestLoadMigrationsChecksNames(t *testing.T) {
	for name, src := range map[string]fstest.MapFS{
		"gap":      {"0001_a.sql": {}, "0003_c.sql": {}},
		"bad name": {"0001_a.sql": {}, "two.sql": {}},
		"no one":   {"0002_b.sql": {}},
	} {
		if _, err := loadMigrations(src); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPragmas(t *testing.T) {
	s, _ := openLogged(t, t.TempDir())
	if m := scalar[string](t, s.w, "PRAGMA journal_mode"); m != "wal" {
		t.Fatalf("journal mode %s", m)
	}
	for name, db := range map[string]*sql.DB{"writer": s.w, "reader": s.r} {
		if v := scalar[int](t, db, "PRAGMA foreign_keys"); v != 1 {
			t.Fatalf("%s: foreign keys off", name)
		}
		if v := scalar[int](t, db, "PRAGMA busy_timeout"); v != int(busyTimeout.Milliseconds()) {
			t.Fatalf("%s: busy timeout %d", name, v)
		}
		if v := scalar[int](t, db, "PRAGMA synchronous"); v != 2 {
			t.Fatalf("%s: synchronous %d, want 2 (FULL)", name, v)
		}
	}
	if _, err := s.r.ExecContext(t.Context(), "DELETE FROM jobs"); err == nil {
		t.Fatal("the read pool can write")
	}
}

func TestNetworkStorageRefused(t *testing.T) {
	orig := filesystemType
	t.Cleanup(func() { filesystemType = orig })
	for magic, name := range map[int64]string{0x6969: "nfs", 0xff534d42: "cifs", 0xfe534d42: "smb2"} {
		filesystemType = func(string) (int64, bool) { return magic, true }
		_, err := Open(t.Context(), Options{Dir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "network storage ("+name+")") {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	filesystemType = func(string) (int64, bool) { return 0, false } // unknown: allowed
	if _, err := Open(t.Context(), Options{Dir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func TestLocalDiskAccepted(t *testing.T) {
	dir := t.TempDir()
	if typ, ok := statfsType(dir); ok {
		if name, bad := networkFilesystems[typ]; bad {
			t.Skipf("the test directory is on %s", name)
		}
	}
	if err := checkDataDir(dir); err != nil {
		t.Fatal(err)
	}
}

func TestNewID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{32}$`)
	seen := map[string]bool{}
	for range 1000 {
		id := NewID()
		if !re.MatchString(id) || seen[id] {
			t.Fatalf("id %q", id)
		}
		seen[id] = true
	}
}

func TestResumeState(t *testing.T) {
	for from, want := range map[State]State{
		Uploading: Uploading, Probing: Uploaded, Matched: Matched, Importing: Importing,
		Imported: Imported, PostProcessing: Imported,
	} {
		if got := ResumeState(from); got != want {
			t.Errorf("ResumeState(%s) = %s, want %s", from, got, want)
		}
	}
}

func TestAuditAttrs(t *testing.T) {
	got := auditAttrs(AuditEntry{
		Action: "job.deleted", Actor: "admin1", Email: "admin1@example.com", JobID: "j",
		Detail: map[string]string{"b": "2", "a": "1", "actor": "x", "email": "admin1@example.com", "msg": "m"},
	})
	want := []any{"action", "job.deleted", "actor", "admin1", "job", "j", "a", "1", "detail_actor", "x", "b", "2", "detail_msg", "m"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
