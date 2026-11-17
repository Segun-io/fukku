package uploads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	tusd "github.com/tus/tusd/v2/pkg/handler"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// Regression tests for #137: incomingStore takes no path from the files in .incoming/, which
// the worker can write (docs/threat-model.md, B4), and only works inside a real .incoming/.

const (
	storeID   = "0123456789abcdef0123456789abcdef"
	otherID   = "fedcba9876543210fedcba9876543210"
	untouched = "must stay as it is"
)

// storeEnv is an intake root with .incoming/ and, next to it, a directory outside the intake
// root with one file whose content must never change.
type storeEnv struct {
	t       *testing.T
	intake  string
	outside string
	victim  string
	s       incomingStore
}

func newStoreEnv(t *testing.T) *storeEnv {
	t.Helper()
	e := &storeEnv{t: t, intake: t.TempDir(), outside: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(e.intake, ".incoming"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.victim = filepath.Join(e.outside, "file")
	if err := os.WriteFile(e.victim, []byte(untouched), 0o644); err != nil {
		t.Fatal(err)
	}
	e.s = incomingStore{intakeRoot: e.intake, fileMode: fileMode}
	return e
}

func (e *storeEnv) create(id string) tusd.Upload {
	e.t.Helper()
	u, err := e.s.NewUpload(context.Background(), tusd.FileInfo{ID: id, Size: 10})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *storeEnv) incoming(name string) string { return filepath.Join(e.intake, ".incoming", name) }

// checkOutside fails unless the outside directory holds exactly the one file, unchanged.
func (e *storeEnv) checkOutside() {
	e.t.Helper()
	entries, err := os.ReadDir(e.outside)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "file" {
		e.t.Fatalf("the directory outside the intake root now holds %v", entries)
	}
	data, err := os.ReadFile(e.victim)
	if err != nil || string(data) != untouched {
		e.t.Fatalf("the file outside the intake root changed: %q, %v", data, err)
	}
}

// rewriteInfo replaces the upload's .info with one whose Storage is st.
func (e *storeEnv) rewriteInfo(id string, st map[string]string) {
	e.t.Helper()
	data, err := json.Marshal(tusd.FileInfo{ID: id, Size: 10, Storage: st})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.incoming(id+".info"), data, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestIncomingStoreKeepsItsOwnLayout(t *testing.T) {
	e := newStoreEnv(t)
	u := e.create(storeID)
	if n, err := u.WriteChunk(context.Background(), 0, strings.NewReader("hello")); err != nil || n != 5 {
		t.Fatalf("WriteChunk: %d, %v", n, err)
	}
	got, err := e.s.GetUpload(context.Background(), storeID)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := got.GetInfo(context.Background())
	if info.Offset != 5 || info.Storage["Path"] != e.incoming(storeID) || info.Storage["InfoPath"] != e.incoming(storeID+".info") {
		t.Fatalf("info = %+v", info)
	}
	// An .info as tusd's filestore wrote it, and one without Storage, are still read.
	for _, st := range []map[string]string{
		{"Type": "filestore", "Path": e.incoming(storeID), "InfoPath": e.incoming(storeID + ".info")},
		nil,
	} {
		e.rewriteInfo(storeID, st)
		if _, err := e.s.GetUpload(context.Background(), storeID); err != nil {
			t.Fatalf("Storage %v: %v", st, err)
		}
	}
}

func TestIncomingStoreRefusesStoragePathsOutsideItsOwn(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   func(e *storeEnv) map[string]string
	}{
		{"absolute path outside", func(e *storeEnv) map[string]string { return map[string]string{"Path": e.victim} }},
		{"relative path up", func(e *storeEnv) map[string]string {
			rel, _ := filepath.Rel(filepath.Join(e.intake, ".incoming"), e.victim)
			return map[string]string{"Path": rel}
		}},
		{"dot-dot name", func(*storeEnv) map[string]string { return map[string]string{"Path": "../" + storeID} }},
		{"another upload's data", func(e *storeEnv) map[string]string { return map[string]string{"Path": e.incoming(otherID)} }},
		{"info path outside", func(e *storeEnv) map[string]string { return map[string]string{"InfoPath": e.victim} }},
		{"info path of another upload", func(*storeEnv) map[string]string { return map[string]string{"InfoPath": otherID + ".info"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newStoreEnv(t)
			e.create(storeID)
			e.create(otherID)
			e.rewriteInfo(storeID, tc.st(e))
			if _, err := e.s.GetUpload(context.Background(), storeID); err == nil {
				t.Fatal("an .info naming a path other than its own was accepted")
			}
			e.checkOutside()
		})
	}
}

func TestIncomingStoreRefusesAnInfoForAnotherID(t *testing.T) {
	e := newStoreEnv(t)
	e.create(storeID)
	data, _ := json.Marshal(tusd.FileInfo{ID: otherID, Size: 10})
	if err := os.WriteFile(e.incoming(storeID+".info"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.GetUpload(context.Background(), storeID); err == nil {
		t.Fatal("an .info for another upload was accepted")
	}
	if _, err := e.s.GetUpload(context.Background(), "../"+storeID); err == nil {
		t.Fatal("a malformed id was accepted")
	}
}

func TestIncomingStoreRefusesASymlinkedIncoming(t *testing.T) {
	// A link out of the intake root (which os.Root alone refuses) and a link to another
	// directory inside it (which only the check in openIncoming refuses).
	// Each gives the target directory and what the link holds (a relative link for the one
	// inside, which os.Root would follow).
	targets := map[string]func(e *storeEnv) (dir, link string){
		"to outside the intake root": func(e *storeEnv) (string, string) { return e.outside, e.outside },
		"to a directory inside the intake root": func(e *storeEnv) (string, string) {
			dir := filepath.Join(e.intake, "elsewhere")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				e.t.Fatal(err)
			}
			return dir, "elsewhere"
		},
	}
	for name, target := range targets {
		t.Run("at start-up, "+name, func(t *testing.T) {
			e := newStoreEnv(t)
			dir, link := target(e)
			before, _ := os.ReadDir(dir)
			inc := filepath.Join(e.intake, ".incoming")
			if err := os.Remove(inc); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(link, inc); err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.Paths.IntakeRoot = e.intake
			h, err := New(Options{Config: cfg, Log: slog.New(slog.DiscardHandler), Store: storetest.NewFake(nil)})
			if err != nil {
				t.Fatal(err)
			}
			if err := h.prepare(context.Background()); err == nil {
				t.Fatal("a symlinked .incoming was accepted at start-up")
			}
			if _, err := e.s.NewUpload(context.Background(), tusd.FileInfo{ID: storeID, Size: 10}); err == nil {
				t.Fatal("NewUpload worked in a symlinked .incoming")
			}
			if after, _ := os.ReadDir(dir); len(after) != len(before) {
				t.Fatalf("the link's target directory changed: %v", after)
			}
			e.checkOutside()
		})

		t.Run("swapped while running, "+name, func(t *testing.T) {
			e := newStoreEnv(t)
			dir, link := target(e)
			u := e.create(storeID)
			// The same layout at the target, then .incoming replaced by a link to it.
			for _, f := range []string{storeID, storeID + ".info"} {
				data, err := os.ReadFile(e.incoming(f))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			inc := filepath.Join(e.intake, ".incoming")
			if err := os.RemoveAll(inc); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(link, inc); err != nil {
				t.Fatal(err)
			}
			if _, err := u.WriteChunk(context.Background(), 0, strings.NewReader("x")); err == nil {
				t.Fatal("WriteChunk wrote through a symlinked .incoming")
			}
			if _, err := e.s.GetUpload(context.Background(), storeID); err == nil {
				t.Fatal("GetUpload read through a symlinked .incoming")
			}
			if _, ok := dataSize(e.intake, storeID); ok {
				t.Fatal("dataSize read through a symlinked .incoming")
			}
			if data, _ := os.ReadFile(filepath.Join(dir, storeID)); len(data) != 0 {
				t.Fatalf("the data at the link's target was written: %q", data)
			}
		})
	}
}

func TestIncomingStoreRefusesUploadDataThatIsNotARegularFile(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		e := newStoreEnv(t)
		u := e.create(storeID)
		if err := os.Remove(e.incoming(storeID)); err != nil {
			t.Fatal(err)
		}
		// A link to another file inside .incoming: still not the upload's own file.
		if err := os.WriteFile(e.incoming("target"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("target", e.incoming(storeID)); err != nil {
			t.Fatal(err)
		}
		if _, err := e.s.GetUpload(context.Background(), storeID); err == nil {
			t.Fatal("GetUpload accepted symlinked data")
		}
		if _, err := u.WriteChunk(context.Background(), 0, strings.NewReader("x")); err == nil {
			t.Fatal("WriteChunk wrote through symlinked data")
		}
		if _, ok := dataSize(e.intake, storeID); ok {
			t.Fatal("dataSize followed symlinked data")
		}
		if data, _ := os.ReadFile(e.incoming("target")); len(data) != 0 {
			t.Fatalf("the link's target was written: %q", data)
		}
	})

	t.Run("symlinked info", func(t *testing.T) {
		e := newStoreEnv(t)
		e.create(storeID)
		if err := os.Rename(e.incoming(storeID+".info"), e.incoming("elsewhere.info")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("elsewhere.info", e.incoming(storeID+".info")); err != nil {
			t.Fatal(err)
		}
		if _, err := e.s.GetUpload(context.Background(), storeID); err == nil {
			t.Fatal("GetUpload read a symlinked .info")
		}
	})

	t.Run("fifo", func(t *testing.T) {
		e := newStoreEnv(t)
		u := e.create(storeID)
		if err := os.Remove(e.incoming(storeID)); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(e.incoming(storeID), 0o644); err != nil {
			t.Skip("no FIFOs here:", err)
		}
		if _, err := e.s.GetUpload(context.Background(), storeID); err == nil {
			t.Fatal("GetUpload accepted a FIFO")
		}
		if _, err := u.WriteChunk(context.Background(), 0, bytes.NewReader([]byte("x"))); err == nil {
			t.Fatal("WriteChunk wrote into a FIFO")
		}
	})

	t.Run("directory", func(t *testing.T) {
		e := newStoreEnv(t)
		e.create(storeID)
		if err := os.Remove(e.incoming(storeID)); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(e.incoming(storeID), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := e.s.GetUpload(context.Background(), storeID); err == nil {
			t.Fatal("GetUpload accepted a directory")
		}
	})
}

// moveNoReplace never moves upload data that is not a regular file into a job.
func TestCompleteRefusesUploadDataThatIsALink(t *testing.T) {
	e := newStoreEnv(t)
	if err := os.WriteFile(e.incoming("target"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", e.incoming(storeID)); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(e.intake)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	dst := filepath.Join("jobs", otherID, "files", "a.mkv")
	// The expected size is the link's own, so only the regular-file check can refuse it.
	li, err := os.Lstat(e.incoming(storeID))
	if err != nil {
		t.Fatal(err)
	}
	if err := moveNoReplace(root, filepath.Join(".incoming", storeID), dst, li.Size()); err == nil {
		t.Fatal("symlinked upload data was moved into the job")
	}
	if _, err := os.Lstat(filepath.Join(e.intake, dst)); err == nil {
		t.Fatal("the job got a file")
	}
}

// writeInfoFile replaces the upload's .info with info.
func (e *storeEnv) writeInfoFile(info tusd.FileInfo) {
	e.t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.incoming(info.ID+".info"), data, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// The fields of an .info besides its paths can be written by whoever writes .incoming/ too,
// so GetUpload refuses any shape the endpoint never creates, and a size other than the
// upload row's.
func TestIncomingStoreRefusesAnInfoOfAnotherShape(t *testing.T) {
	const rowSize = 10
	for _, tc := range []struct {
		name string
		mod  func(*tusd.FileInfo)
	}{
		{"deferred length", func(i *tusd.FileInfo) { i.SizeIsDeferred = true }},
		{"negative size", func(i *tusd.FileInfo) { i.Size = -1 }},
		{"larger than the row", func(i *tusd.FileInfo) { i.Size = 1 << 40 }},
		{"smaller than the row", func(i *tusd.FileInfo) { i.Size = 1 }},
		{"partial", func(i *tusd.FileInfo) { i.IsPartial = true }},
		{"final", func(i *tusd.FileInfo) { i.IsFinal = true }},
		{"partial uploads", func(i *tusd.FileInfo) { i.PartialUploads = []string{otherID} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newStoreEnv(t)
			e.s.sizeOf = func(context.Context, string) (int64, error) { return rowSize, nil }
			e.create(storeID)
			info := tusd.FileInfo{ID: storeID, Size: rowSize}
			tc.mod(&info)
			e.writeInfoFile(info)
			if _, err := e.s.GetUpload(context.Background(), storeID); err == nil {
				t.Fatalf("an .info with %s was accepted", tc.name)
			}
		})
	}

	t.Run("the row's own size", func(t *testing.T) {
		e := newStoreEnv(t)
		e.s.sizeOf = func(context.Context, string) (int64, error) { return rowSize, nil }
		e.create(storeID)
		e.writeInfoFile(tusd.FileInfo{ID: storeID, Size: rowSize})
		if _, err := e.s.GetUpload(context.Background(), storeID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("no row", func(t *testing.T) {
		e := newStoreEnv(t)
		e.s.sizeOf = func(context.Context, string) (int64, error) { return 0, store.ErrNotFound }
		e.create(storeID)
		if _, err := e.s.GetUpload(context.Background(), storeID); !errors.Is(err, tusd.ErrNotFound) {
			t.Fatalf("err = %v, want tusd.ErrNotFound", err)
		}
	})

	t.Run("data past the size", func(t *testing.T) {
		e := newStoreEnv(t)
		e.s.sizeOf = func(context.Context, string) (int64, error) { return rowSize, nil }
		e.create(storeID)
		if err := os.WriteFile(e.incoming(storeID), bytes.Repeat([]byte("x"), rowSize+1), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := e.s.GetUpload(context.Background(), storeID); err == nil {
			t.Fatal("data larger than the upload's size was accepted")
		}
	})

	t.Run("at creation", func(t *testing.T) {
		e := newStoreEnv(t)
		for _, info := range []tusd.FileInfo{
			{ID: storeID, SizeIsDeferred: true},
			{ID: storeID, Size: -1},
			{ID: storeID, Size: 10, IsFinal: true},
		} {
			if _, err := e.s.NewUpload(context.Background(), info); err == nil {
				t.Fatalf("NewUpload accepted %+v", info)
			}
		}
	})
}

// The endpoint speaks tus 1.0 only: the headers of the IETF resumable-upload drafts are
// removed before tusd sees them, so a PATCH that carries them is a plain tus PATCH.
func TestIETFDraftHeadersAreIgnored(t *testing.T) {
	e := newEnv(t, nil)
	j := e.job("user1")
	c := e.client("user1")
	data := randomData(1000)
	id := c.mustCreate(j.ID, "a.mkv", int64(len(data)))
	r := c.do(http.MethodPatch, BasePath+id, map[string]string{
		"Upload-Offset":                "0",
		"Content-Type":                 "application/offset+octet-stream",
		"Upload-Draft-Interop-Version": "6",
		"Upload-Complete":              "?0",
	}, data[:100])
	if r.status != http.StatusNoContent || r.offset() != 100 {
		t.Fatalf("PATCH: %d %s, offset %d", r.status, r.body, r.offset())
	}
	for _, h := range unsupportedHeaders {
		if v := r.header.Get(h); v != "" {
			t.Errorf("the answer carries %s: %s", h, v)
		}
	}
}

func TestDropUnsupportedHeaders(t *testing.T) {
	h := http.Header{}
	for _, k := range []string{"X-HTTP-Method-Override", "Upload-Draft-Interop-Version", "Upload-Complete", "Upload-Incomplete"} {
		h.Set(k, "x")
	}
	h.Set("Upload-Offset", "0")
	dropUnsupportedHeaders(h)
	if len(h) != 1 || h.Get("Upload-Offset") != "0" {
		t.Fatalf("headers left: %v", h)
	}
}
