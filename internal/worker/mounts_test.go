//go:build unix

package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
)

// TestCheckMounts: the worker may write its own folder of the intake root and nothing that
// the web process hands over (#118, #138). A read-only folder stands for a read-only mount
// here (access(2) refuses both).
func TestCheckMounts(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may write a read-only folder")
	}
	layout := func(t *testing.T) (config.Config, string) {
		base := t.TempDir()
		cfg := config.Defaults()
		cfg.Paths.IntakeRoot = filepath.Join(base, "intake")
		cfg.Paths.LibraryRoots = []string{filepath.Join(base, "movies"), filepath.Join(base, "tv")}
		for _, d := range []string{"intake/jobs", "intake/worker", "movies", "tv"} {
			if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for _, d := range []string{"intake/jobs", "movies", "tv"} {
			readOnly(t, filepath.Join(base, d))
		}
		return cfg, base
	}

	cfg, _ := layout(t)
	if err := CheckMounts(cfg); err != nil {
		t.Fatalf("the reference layout: %v", err)
	}

	for name, c := range map[string]struct {
		change func(t *testing.T, base string)
		want   string
	}{
		"jobs writable":    {func(t *testing.T, b string) { writableAgain(t, filepath.Join(b, "intake/jobs")) }, "intake/jobs"},
		"incoming there":   {func(t *testing.T, b string) { mkdir(t, filepath.Join(b, "intake/.incoming")) }, "intake/.incoming"},
		"a library root":   {func(t *testing.T, b string) { writableAgain(t, filepath.Join(b, "tv")) }, "/tv"},
		"no worker folder": {func(t *testing.T, b string) { rm(t, filepath.Join(b, "intake/worker")) }, "cannot write"},
		"worker read-only": {func(t *testing.T, b string) { readOnly(t, filepath.Join(b, "intake/worker")) }, "cannot write"},
		"the old layout": {func(t *testing.T, b string) {
			writableAgain(t, filepath.Join(b, "intake/jobs"))
			mkdir(t, filepath.Join(b, "intake/.incoming"))
		}, ".incoming"},
		"a library missing": {func(t *testing.T, b string) { writableAgain(t, filepath.Join(b, "tv")); rm(t, filepath.Join(b, "tv")) }, ""},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, base := layout(t)
			c.change(t, base)
			err := CheckMounts(cfg)
			if c.want == "" {
				if err != nil {
					t.Fatalf("%v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("CheckMounts = %v, want it to name %q", err, c.want)
			}
		})
	}
}

func readOnly(t *testing.T, p string) {
	t.Helper()
	if err := os.Chmod(p, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o755) }) // so that the temporary folder can go
}

func writableAgain(t *testing.T, p string) {
	t.Helper()
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func rm(t *testing.T, p string) {
	t.Helper()
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
}
