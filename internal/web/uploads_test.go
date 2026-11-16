package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/segun-io/fukku/internal/store/storetest"
)

func TestUploadsNeedAStore(t *testing.T) {
	h := handler(t, testConfig(), fstest.MapFS{})
	wantError(t, serve(h, fromProxy(http.MethodPost, "/api/uploads/", nil)), http.StatusServiceUnavailable, "storage_unavailable")
}

// TestUploadsThroughTheChain runs an upload through the whole web handler: authentication,
// the Origin check and the body limit, which does not apply to uploads.
func TestUploadsThroughTheChain(t *testing.T) {
	cfg := testConfig()
	cfg.Paths.IntakeRoot = t.TempDir()
	cfg.Upload.FreeSpaceMargin = 0
	st := storetest.NewFake(nil)
	h := New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, Deps{Store: st}).Handler()
	job, err := st.CreateJob(context.Background(), "user1")
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("x"), 2*maxJSONBody+1)

	tus := func(r *http.Request) *http.Request {
		r.Header.Set("Tus-Resumable", "1.0.0")
		return r
	}
	create := tus(fromProxy(http.MethodPost, "/api/uploads/", nil))
	create.Header.Set("Upload-Length", strconv.Itoa(len(data)))
	create.Header.Set("Upload-Metadata", "filename "+base64.StdEncoding.EncodeToString([]byte("film.mkv"))+
		",jobId "+base64.StdEncoding.EncodeToString([]byte(job.ID)))

	// Without a user (an untrusted address) nothing reaches the upload handler.
	anon := create.Clone(context.Background())
	anon.RemoteAddr = untrustedAddr
	if w := serve(h, anon); w.Code != http.StatusForbidden {
		t.Fatalf("untrusted: %d", w.Code)
	}

	w := serve(h, create)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/api/uploads/") {
		t.Fatalf("Location %q", loc)
	}

	patch := tus(fromProxy(http.MethodPatch, loc, bytes.NewReader(data)))
	patch.Header.Set("Upload-Offset", "0")
	patch.Header.Set("Content-Type", "application/offset+octet-stream")
	if w := serve(h, patch); w.Code != http.StatusNoContent || w.Header().Get("Upload-Offset") != strconv.Itoa(len(data)) {
		t.Fatalf("PATCH: %d %v %s", w.Code, w.Header(), w.Body)
	}
	got, err := os.ReadFile(filepath.Join(cfg.Paths.IntakeRoot, "jobs", job.ID, "files", "film.mkv"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("the file is not in the job: %v", err)
	}
	if files, _ := st.Files(context.Background(), job.ID); len(files) != 1 {
		t.Fatalf("files %+v", files)
	}

	// Another user's HEAD is a 404.
	head := fromProxy(http.MethodHead, loc, nil)
	head.Header.Set("Remote-User", "user2")
	if w := serve(h, head); w.Code != http.StatusNotFound {
		t.Fatalf("HEAD by user2: %d", w.Code)
	}
}
