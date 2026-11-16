package fscheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanLink(t *testing.T) {
	intake, versions := t.TempDir(), t.TempDir()
	if err := CanLink(intake, versions); err != nil {
		t.Fatalf("two folders of one mount: %v", err)
	}
	for _, d := range []string{intake, versions} { // the trial leaves nothing behind
		if entries, _ := os.ReadDir(d); len(entries) != 0 {
			t.Errorf("%s holds %v", d, entries)
		}
	}
	if err := CanLink(intake, filepath.Join(versions, "missing")); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing versions folder: %v", err)
	}
	if err := CanLink(filepath.Join(intake, "missing"), versions); err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Errorf("missing intake folder: %v", err)
	}
	file := filepath.Join(versions, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CanLink(intake, file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("a file as the versions folder: %v", err)
	}
}

// Two bind mounts of one host folder have the same device id, yet a link between them fails
// (EXDEV): CanLink must say so. Containers only: run with FUKKU_TEST_LINK_A and
// FUKKU_TEST_LINK_B naming two bind mounts of the same host folder.
func TestCanLinkAcrossBindMounts(t *testing.T) {
	a, b := os.Getenv("FUKKU_TEST_LINK_A"), os.Getenv("FUKKU_TEST_LINK_B")
	if a == "" || b == "" {
		t.Skip("needs FUKKU_TEST_LINK_A and FUKKU_TEST_LINK_B: two bind mounts of one folder")
	}
	da, errA := statDevice(a)
	db, errB := statDevice(b)
	if errA != nil || errB != nil || da != db {
		t.Fatalf("the mounts should share a device id: %d %v, %d %v", da, errA, db, errB)
	}
	err := CanLink(a, b)
	if err == nil || !strings.Contains(err.Error(), "not on the same mount") || !strings.Contains(err.Error(), "cross-device") {
		t.Fatalf("CanLink across bind mounts: %v", err)
	}
	t.Logf("same device id %d, and CanLink says: %v", da, err)
}
