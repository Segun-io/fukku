package fscheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
)

func TestSentinels(t *testing.T) {
	dir := t.TempDir()
	have := filepath.Join(dir, ".mounted")
	if err := os.WriteFile(have, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "gone", ".mounted")
	if err := Sentinels(config.Paths{Sentinels: []string{have}}); err != nil {
		t.Fatal(err)
	}
	if err := Sentinels(config.Paths{}); err != nil {
		t.Fatal(err)
	}
	err := Sentinels(config.Paths{Sentinels: []string{have, missing}})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("got %v", err)
	}
	if _, serr := os.Stat(filepath.Dir(missing)); serr == nil {
		t.Fatal("a directory was created under the missing sentinel")
	}
}

func TestWritable(t *testing.T) {
	dir := t.TempDir()
	if err := Writable(dir); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left files behind: %v", entries)
	}
	missing := filepath.Join(dir, "nope")
	if err := Writable(missing); err == nil {
		t.Fatal("missing dir passed")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("Writable created the directory")
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Writable(file); err == nil {
		t.Fatal("a file passed")
	}
	if os.Geteuid() != 0 {
		ro := filepath.Join(dir, "ro")
		if err := os.Mkdir(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		if err := Writable(ro); err == nil {
			t.Fatal("read-only dir passed")
		}
	}
}

func fakeDevices(t *testing.T, ids map[string]uint64) {
	t.Helper()
	old := deviceID
	deviceID = func(p string) (uint64, error) { return ids[p], nil }
	t.Cleanup(func() { deviceID = old })
}

func TestSameFilesystem(t *testing.T) {
	fakeDevices(t, map[string]uint64{"/intake": 1, "/lib": 2, "/lib2": 1})
	cfg := func(mode, importMode string, roots ...string) config.Config {
		c := config.Defaults()
		c.Paths.IntakeRoot = "/intake"
		c.Paths.LibraryRoots = roots
		c.Paths.RequireSameFilesystem = mode
		c.Radarr.URL = "http://radarr.example.com"
		c.Radarr.ImportMode = importMode
		return c
	}
	tests := []struct {
		name    string
		cfg     config.Config
		wantErr bool
	}{
		{"auto, copy, two devices", cfg("auto", "copy", "/lib"), false},
		{"auto, move, two devices", cfg("auto", "move", "/lib"), true},
		{"auto, move, same device", cfg("auto", "move", "/lib2"), false},
		{"always, copy, two devices", cfg("always", "copy", "/lib"), true},
		{"always, second root differs", cfg("always", "copy", "/lib2", "/lib"), true},
		{"never, move, two devices", cfg("never", "move", "/lib"), false},
		{"no library roots", cfg("always", "move"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := SameFilesystem(tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("got %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
	// A disabled app (no URL) with importMode=move does not make auto strict.
	c := cfg("auto", "move", "/lib")
	c.Radarr.URL = ""
	if err := SameFilesystem(c); err != nil {
		t.Fatal(err)
	}
}

func TestSameFilesystemRealDevices(t *testing.T) {
	dir := t.TempDir()
	c := config.Defaults()
	c.Paths.IntakeRoot = dir
	c.Paths.LibraryRoots = []string{dir}
	c.Paths.RequireSameFilesystem = "always"
	if err := SameFilesystem(c); err != nil {
		t.Fatal(err)
	}
}
