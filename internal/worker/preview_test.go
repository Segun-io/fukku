package worker

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/preview"
	"github.com/segun-io/fukku/internal/spool"
)

// The preview handler (#53): args that cannot be used are a bad task, and the library
// file is read only inside the worker's own paths.libraryRoots.
func TestPreviewHandler(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed (run the tests with make test)")
	}
	lib, job := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(job, "files"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "files", "film.mkv"), []byte("not a film"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(job)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	cfg := config.Defaults()
	cfg.Paths.LibraryRoots = []string{lib}
	h := Handlers(cfg)[spool.KindPreview]

	good := preview.Args{
		Request: "0123456789abcdef", Library: filepath.Join(job, "files", "film.mkv"), Upload: "files/film.mkv",
		AudioIndex: 1, Clips: []preview.Clip{{StartMs: 0, LengthMs: 1000}},
	}
	for _, raw := range []string{`{"request":`, `{"request":"0123456789abcdef","extra":1}`, `{"request":"nope","upload":"files/film.mkv","clips":[{"lengthMs":1}]}`} {
		if _, err := h(t.Context(), root, root, json.RawMessage(raw)); !errors.Is(err, ErrBadTask) {
			t.Errorf("%s: %v, want ErrBadTask", raw, err)
		}
	}
	bad := good
	bad.Upload = "../outside.mkv"
	args, _ := json.Marshal(bad)
	if _, err := h(t.Context(), root, root, args); !errors.Is(err, ErrBadTask) {
		t.Errorf("upload outside the job: %v, want ErrBadTask", err)
	}
	// The job's own file is not a library file.
	args, _ = json.Marshal(good)
	out, err := h(t.Context(), root, root, args)
	if err != nil {
		t.Fatal(err)
	}
	if po, ok := out.(preview.Output); !ok || po.OK || po.Refusal != preview.RefusalOutsideLibrary || po.Request != good.Request {
		t.Errorf("library file in the job: %+v", out)
	}
}
