//go:build linux

package flows

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
)

// The hand-over of a job's file to Radarr or Sonarr, and its link into the library, check
// the file right before (#113): a symbolic link as the file or on the way to it, a file of
// another size, or anything but a regular file is refused, nothing is sent to the apps and
// nothing is placed. In the reference deployment the worker cannot write the job directory
// at all (docs/install.md); these are the checks for one where it can.

// swaps are what a worker that can write the job's directory could leave there in place of
// the job's file rel: outside is a file of the same size outside the job (a library file).
var swaps = map[string]func(t *testing.T, dir, rel, outside string){
	"a link as the file": func(t *testing.T, dir, rel, outside string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		mustDo(t, os.Remove(p))
		mustDo(t, os.Symlink(outside, p))
	},
	"a link to a file inside": func(t *testing.T, dir, rel, _ string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		mustDo(t, os.Rename(p, p+".real"))
		mustDo(t, os.Symlink(filepath.Base(p)+".real", p))
	},
	"a linked folder": func(t *testing.T, dir, rel, outside string) {
		top := filepath.Join(dir, strings.SplitN(rel, "/", 2)[0])
		away := filepath.Join(filepath.Dir(outside), "away")
		mustDo(t, os.Rename(top, away))
		mustDo(t, os.Symlink(away, top))
	},
	// A name the worker gave the file in its own folder, through a descriptor it kept:
	// writes through it would change what is imported or placed (#114).
	"a second name": func(t *testing.T, dir, rel, outside string) {
		mustDo(t, os.Link(filepath.Join(dir, filepath.FromSlash(rel)), filepath.Join(filepath.Dir(outside), "alias.mkv")))
	},
	"another size": func(t *testing.T, dir, rel, _ string) {
		mustDo(t, os.Truncate(filepath.Join(dir, filepath.FromSlash(rel)), 7))
	},
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// outsideFile writes a file of size bytes in a folder of its own, for a swap to point at.
func outsideFile(t *testing.T, size int64) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "library.mkv")
	mustDo(t, os.WriteFile(p, []byte("library"), 0o644))
	mustDo(t, os.Truncate(p, size))
	return p
}

func TestImportRefusesAFileThatIsNotTheJobs(t *testing.T) {
	for name, swap := range swaps {
		t.Run(name, func(t *testing.T) {
			x := newImportEnv(t)
			j := x.filmJob()
			j = x.confirm(j)
			outside := outsideFile(t, 1000)
			swap(t, filepath.Join(x.root, "jobs", j.ID), "files/Some Film (2001).mkv", outside)
			j, err := x.step(x.eng, j)
			if err != nil {
				t.Fatal(err)
			}
			if j.State != store.Failed || j.ErrorCode != CodeImportFailed {
				t.Fatalf("job %s %s (%s)", j.State, j.ErrorCode, j.ErrorText)
			}
			if len(x.movies.Imports) != 0 {
				t.Errorf("sent to Radarr: %+v", x.movies.Imports)
			}
			if info, err := os.Stat(outside); err != nil || info.Size() != 1000 {
				t.Errorf("the outside file: %v", err)
			}
		})
	}
}

func TestVersionRefusesAFileThatIsNotTheJobs(t *testing.T) {
	for name, swap := range swaps {
		t.Run(name, func(t *testing.T) {
			x := newVersionEnv(t)
			j := x.versionJob(palMs, "spa")
			c, err := x.confirmVersion(j, "")
			if err != nil {
				t.Fatal(err)
			}
			swap(t, filepath.Join(x.root, "jobs", j.ID), "files/Some Film (2001).es.mkv", outsideFile(t, 1000))
			if j = x.drive(x.eng, c.Job); j.State != store.Failed || j.ErrorCode != CodeVersionFailed {
				t.Fatalf("job %s %s (%s)", j.State, j.ErrorCode, j.ErrorText)
			}
			if _, err := os.Lstat(x.dest("Some Film (2001) - Español.mkv")); !os.IsNotExist(err) {
				t.Errorf("placed: %v", err)
			}
			if recs, _ := x.st.VersionsOf(x.ctx, j.ID); len(recs) != 0 {
				t.Errorf("records %+v", recs)
			}
		})
	}
}

func TestEpisodeVersionRefusesAFileThatIsNotTheJobs(t *testing.T) {
	for name, swap := range swaps {
		if name == "a linked folder" { // files/ holds every file of the job: see below
			continue
		}
		t.Run(name, func(t *testing.T) {
			x := newEpvEnv(t, nil)
			j := x.epvJob()
			before := x.libraryNames()
			c, err := x.confirmEpv(j, "")
			if err != nil {
				t.Fatal(err)
			}
			swap(t, filepath.Join(x.root, "jobs", j.ID), "files/Show.S01E01.mkv", outsideFile(t, 1000))
			j = x.drive(x.eng, c.Job)
			x.refusedRow(j, 0, CodeVersionFailed, "")
			got := x.libraryNames()
			if slices.Contains(got, "Some Show - S01E01 - Español.mkv") || len(got) != len(before)+1 {
				t.Errorf("the season folder: %v (before %v)", got, before)
			}
		})
	}
}

// Staged copies the web process made earlier (another label's, another row's), left when a
// version was confirmed again, are its own names: they are removed before the link checks,
// with Sonarr's verdict asked again right before the link (the version is staged once more)
// and without it, and the placement goes through.
func TestEpisodeVersionStaleStagedCopiesDoNotBlock(t *testing.T) {
	for _, verdict := range []bool{true, false} {
		t.Run(fmt.Sprintf("verdict %t", verdict), func(t *testing.T) {
			x := newEpvEnv(t, func(c *config.Config) { c.Versions.RequireNotUpgradeVerdict = verdict })
			j := x.epvJob()
			rows := x.rows(j)
			c, err := x.confirmEpv(j, "")
			if err != nil {
				t.Fatal(err)
			}
			src := x.src(j, "files/Show.S01E01.mkv")
			for _, p := range []string{
				filepath.Join(x.root, "jobs", j.ID, "versions", rows[0].ID, "Some Show - S01E01 - Old label.mkv"),
				filepath.Join(x.root, "jobs", j.ID, "versions", "r-earlier", "Some Show - S01E01 - Older.mkv"),
			} {
				mustDo(t, os.MkdirAll(filepath.Dir(p), 0o755))
				mustDo(t, os.Link(src, p))
			}
			if j = x.drive(x.eng, c.Job); j.State != store.Done {
				t.Fatalf("job %s (%s: %s)", j.State, j.ErrorCode, j.ErrorText)
			}
			if !slices.Contains(x.libraryNames(), "Some Show - S01E01 - Español.mkv") {
				t.Errorf("not placed: %v", x.libraryNames())
			}
		})
	}
}

// The staging folder of an episode's version (versions/<row>) swapped for a link to a folder
// outside the job: Sonarr is not asked about anything there, nothing is linked into it, and
// the version is not placed.
func TestEpisodeVersionStagingThroughALinkIsRefused(t *testing.T) {
	x := newEpvEnv(t, nil)
	j := x.epvJob()
	rows := x.rows(j)
	before := x.libraryNames()
	c, err := x.confirmEpv(j, "")
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(x.root, "jobs", j.ID, "versions", rows[0].ID)
	away := t.TempDir()
	mustDo(t, os.RemoveAll(staged))
	mustDo(t, os.Symlink(away, staged))
	j = x.drive(x.eng, c.Job)
	x.refusedRow(j, 0, CodeVersionFailed, "")
	if entries, _ := os.ReadDir(away); len(entries) != 0 {
		t.Errorf("linked into the folder outside: %v", entries)
	}
	if got := x.libraryNames(); slices.Contains(got, "Some Show - S01E01 - Español.mkv") || len(got) != len(before)+1 {
		t.Errorf("the season folder: %v (before %v)", got, before)
	}
}
