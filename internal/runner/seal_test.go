//go:build linux

package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/store"
)

// The extracted files are moved from the worker's directory of the job into the job's, where
// the worker cannot change them, before anything is registered (#113, #114, #138).
func TestExtractedFilesAreMovedIntoTheJob(t *testing.T) {
	e := newEnv(t)
	e.w.extracts = map[string]extractPlan{"archives/a.zip": {files: map[string]string{"Season 01/e1.mkv": "e1"}}}
	j := e.newJob(archive("a.zip"))
	j = e.run(j.ID, store.Probed)
	wantFile(t, e.file(j.ID, "files/a/Season 01/e1.mkv"), store.FileOK, "")
	if got := e.onDisk(j.ID)["files/a/Season 01/e1.mkv"]; got != "e1" {
		t.Errorf("in the job: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(e.cfg.Paths.IntakeRoot, jobdir.Worker, j.ID, "files/a")); !os.IsNotExist(err) {
		t.Errorf("left in the worker's directory: %v", err)
	}
}

// What a compromised worker reports as extracted but is not a file of its own: a link to a
// library file (of the reported size), a file behind a linked directory, a file with a second
// name the worker keeps. None is registered or offered; the job fails; the outside file is
// untouched and nothing of the worker's is left in the job.
func TestExtractedFilesThatAreNotTheJobsOwn(t *testing.T) {
	for name, tamper := range map[string]func(t *testing.T, outside, dest string){
		"a link as the file": func(t *testing.T, outside, dest string) {
			p := filepath.Join(dest, "Season 01/e1.mkv")
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, p); err != nil {
				t.Fatal(err)
			}
		},
		"a linked directory": func(t *testing.T, outside, dest string) {
			if err := os.RemoveAll(filepath.Join(dest, "Season 01")); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(outside, filepath.Join(filepath.Dir(outside), "e1.mkv")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Dir(outside), filepath.Join(dest, "Season 01")); err != nil {
				t.Fatal(err)
			}
		},
		"a second name": func(t *testing.T, _, dest string) {
			if err := os.Link(filepath.Join(dest, "Season 01/e1.mkv"), filepath.Join(dest, "..", "kept.mkv")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			lib := t.TempDir()
			outside := filepath.Join(lib, "film.mkv")
			if err := os.WriteFile(outside, []byte("e1"), 0o644); err != nil { // the size reported
				t.Fatal(err)
			}
			e.w.extracts = map[string]extractPlan{"archives/a.zip": {files: map[string]string{"Season 01/e1.mkv": "e1"}}}
			e.w.tamper = func(dest string) { tamper(t, outside, dest) }
			j := e.newJob(archive("a.zip"))
			j = e.run(j.ID, store.Failed)
			wantState(t, j, store.Failed, CodeTaskFailed)
			if files := e.files(j.ID); len(files) != 1 {
				t.Errorf("registered: %v", files)
			}
			for p := range e.onDisk(j.ID) {
				if p != "archives/" && p != "archives/a.zip" && p != "files/" && p != "files/a/" && p != "files/a/Season 01/" {
					t.Errorf("in the job: %s", p)
				}
			}
			entries, _ := os.ReadDir(lib)
			for _, en := range entries {
				if data, err := os.ReadFile(filepath.Join(lib, en.Name())); err != nil || string(data) != "e1" {
					t.Errorf("the outside file: %q %v", data, err)
				}
			}
		})
	}
}
