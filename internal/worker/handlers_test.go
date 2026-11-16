package worker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/extract"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/spool"
)

func TestHandlers(t *testing.T) {
	cfg := config.Defaults()
	h := Handlers(cfg)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "archives"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "files.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	for _, tc := range []struct {
		kind, args string
	}{
		{spool.KindProbe, `{"paths":`},
		{spool.KindProbe, `{"paths":[],"extra":1}`},
		{spool.KindProbe, `{"paths":[]} {}`},
		{spool.KindExtract, `[]`},
		{spool.KindExtract, `{"archive":"../x.zip","dest":"files/x"}`},              // extract.ErrBadArgs
		{spool.KindExtract, `{"archive":"archives/x.zip","dest":"archives/x.zip"}`}, // dest contains the archive
		{spool.KindLibraryProbe, `{"path":1}`},
		{spool.KindLibraryProbe, `{"path":"/x","paths":[]}`},
	} {
		_, err := h[tc.kind](t.Context(), root, json.RawMessage(tc.args))
		if !errors.Is(err, ErrBadTask) {
			t.Errorf("%s %s: %v, want ErrBadTask", tc.kind, tc.args, err)
		}
	}

	// Well-formed args reach the packages: a file with another extension is refused.
	out, err := h[spool.KindProbe](t.Context(), root, json.RawMessage(`{"paths":["files.txt"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if po, ok := out.(media.ProbeOutput); !ok || len(po.Files) != 1 || po.Files[0].Refusal != media.RefusalExtension {
		t.Errorf("probe output %+v", out)
	}
	if err := os.WriteFile(filepath.Join(dir, "archives/a.zip"), []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = h[spool.KindExtract](t.Context(), root, json.RawMessage(`{"archive":"archives/a.zip","dest":"files/a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if eo, ok := out.(extract.Output); !ok || eo.Refused != extract.RefusedNotAZip {
		t.Errorf("extract output %+v", out)
	}
}

// The library probe reads only inside the worker's own paths.libraryRoots, whatever the
// task says, and never through the job directory.
func TestLibraryProbeHandler(t *testing.T) {
	lib, job := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(job, "film.mkv"), []byte("not a film"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "film.mkv"), []byte("not a film either"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(job)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	cfg := config.Defaults()
	cfg.Paths.LibraryRoots = []string{lib}
	h := Handlers(cfg)[spool.KindLibraryProbe]
	for _, p := range []string{filepath.Join(job, "film.mkv"), "film.mkv", lib + "/../" + filepath.Base(job) + "/film.mkv"} {
		args, _ := json.Marshal(media.LibraryProbeArgs{Path: p})
		out, err := h(t.Context(), root, args)
		if err != nil {
			t.Fatal(err)
		}
		if lo, ok := out.(media.LibraryProbeOutput); !ok || lo.OK || lo.Refusal != media.RefusalOutsideLibrary || lo.Path != p {
			t.Errorf("%s: %+v", p, out)
		}
	}
	// A library roots setting changed after Handlers does not widen what it reads.
	cfg.Paths.LibraryRoots[0] = job
	args, _ := json.Marshal(media.LibraryProbeArgs{Path: filepath.Join(job, "film.mkv")})
	if out, _ := h(t.Context(), root, args); out.(media.LibraryProbeOutput).Refusal != media.RefusalOutsideLibrary {
		t.Errorf("roots changed afterwards: %+v", out)
	}
}
