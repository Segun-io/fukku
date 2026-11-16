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
