package fscheck

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// Trial files a crash left behind are removed by the next check once they are stale; fresh
// ones (another check running) and other files are left alone.
func TestCanLinkRemovesStaleTrials(t *testing.T) {
	intake, versions := t.TempDir(), t.TempDir()
	old := time.Now().Add(-time.Hour)
	stale := []string{filepath.Join(intake, TrialPrefix+"111"), filepath.Join(versions, TrialPrefix+"111.link")}
	kept := []string{filepath.Join(intake, TrialPrefix+"222"), filepath.Join(versions, "Film (2001)"), filepath.Join(intake, ".fukku-other")}
	for _, p := range append(stale, kept...) {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range append(stale, kept[2]) {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := CanLink(intake, versions); err != nil {
		t.Fatal(err)
	}
	for _, p := range stale {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still there (%v)", p, err)
		}
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
}

// A trial link whose name is taken is an error of its own, not a different mount.
func TestCanLinkNameTaken(t *testing.T) {
	intake, versions := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(versions, TrialPrefix+"fixed.link"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	createTrial = func(dir, _ string) (*os.File, error) {
		return os.OpenFile(filepath.Join(dir, TrialPrefix+"fixed"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	}
	t.Cleanup(func() { createTrial = os.CreateTemp })
	err := CanLink(intake, versions)
	if err == nil || !strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "same mount") {
		t.Fatalf("CanLink = %v", err)
	}
	if _, err := os.Stat(filepath.Join(intake, TrialPrefix+"fixed")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the trial file is left: %v", err)
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
