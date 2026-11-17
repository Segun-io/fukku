// Package fscheck holds the filesystem checks of plan §9. /readyz runs them, and the import
// code runs them again before each import, so nothing is written onto a host disk under a
// missing mount and moves stay renames.
package fscheck

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/config"
)

// deviceID returns the device id of the filesystem holding path. Tests replace it.
var deviceID = statDevice

// Sentinels returns an error naming the first paths.sentinels file that is missing.
func Sentinels(p config.Paths) error {
	for _, s := range p.Sentinels {
		if _, err := os.Stat(s); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%s is missing", s)
			}
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

// SameFilesystem compares the device ids of paths.intakeRoot and every paths.libraryRoots
// entry, following paths.requireSameFilesystem: auto (only when an enabled app has
// importMode=move), always, never.
func SameFilesystem(cfg config.Config) error {
	if !sameFilesystemRequired(cfg) || len(cfg.Paths.LibraryRoots) == 0 {
		return nil
	}
	intake := cfg.Paths.IntakeRoot
	want, err := deviceID(intake)
	if err != nil {
		return fmt.Errorf("%s cannot be read", intake)
	}
	for _, root := range cfg.Paths.LibraryRoots {
		got, err := deviceID(root)
		if err != nil {
			return fmt.Errorf("%s cannot be read", root)
		}
		if got != want {
			return fmt.Errorf("%s is on a different filesystem than %s", root, intake)
		}
	}
	return nil
}

func sameFilesystemRequired(cfg config.Config) bool {
	switch cfg.Paths.RequireSameFilesystem {
	case "always":
		return true
	case "never":
		return false
	}
	for _, a := range []config.Arr{cfg.Radarr, cfg.Sonarr} {
		if a.URL != "" && a.ImportMode == "move" {
			return true
		}
	}
	return false
}

// CanLink checks by trial that a hard link from dir into other works: it creates an empty
// file in dir, links it into other under a name of its own, and removes both. Second
// versions are such links, from the intake root into versions.root (films, #56) and into
// Sonarr's season folders (episodes, #58). Comparing device
// ids is not enough: two bind mounts of one filesystem report the same st_dev, yet a link
// between them fails with EXDEV, so only the trial says "the same mount". Neither folder is
// created. (The move-import check, SameFilesystem, stays a comparison: those moves are
// Radarr's and Sonarr's, through their own mounts, and with versions off Fukku may see the
// library read-only.)
func CanLink(dir, other string) error {
	info, err := os.Stat(other) //nolint:gosec // versions.root or a season folder under paths.libraryRoots
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s does not exist", other)
	case err != nil:
		return fmt.Errorf("%s cannot be read", other)
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory", other)
	}
	// A crash between the trial file and its removal leaves them: the next check removes
	// those older than staleTrial.
	removeStaleTrials(dir)
	removeStaleTrials(other)
	f, err := createTrial(dir, TrialPrefix+"*")
	if err != nil {
		return fmt.Errorf("%s is not writable", dir)
	}
	src := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(src) }()
	dst := filepath.Join(other, filepath.Base(src)+".link") // another name: the two may be one folder
	if err := os.Link(src, dst); err != nil {
		switch {
		case errors.Is(err, os.ErrPermission):
			return fmt.Errorf("%s is not writable", other)
		case errors.Is(err, os.ErrExist):
			return fmt.Errorf("%s: the trial link %s already exists: %w", other, filepath.Base(dst), err)
		}
		return fmt.Errorf("%s is not on the same mount as %s: a hard link between them fails (%w)", other, dir, errors.Unwrap(err))
	}
	if err := os.Remove(dst); err != nil { //nolint:gosec // the trial link CanLink just made
		return fmt.Errorf("%s: the trial link cannot be removed: %w", other, err)
	}
	return nil
}

// TrialPrefix starts the names of CanLink's trial files (".fukku-linkcheck-…" in the first
// folder, the same name plus ".link" in the second).
const TrialPrefix = ".fukku-linkcheck-"

// createTrial makes the trial file. Tests replace it.
var createTrial = os.CreateTemp

// staleTrial is the age from which a trial file is taken for one a crash left behind (a
// trial lasts milliseconds).
const staleTrial = 5 * time.Minute

// removeStaleTrials removes the regular files of dir named TrialPrefix… that are older
// than staleTrial. Nothing else is touched; errors are ignored (the next check tries again).
func removeStaleTrials(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), TrialPrefix) || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < staleTrial {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name())) //nolint:gosec // a trial file of the configured folder
	}
}

// Writable checks that dir exists and a file can be created in it. It never creates dir.
func Writable(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s does not exist", dir)
		}
		return fmt.Errorf("%s cannot be read", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	f, err := os.CreateTemp(dir, ".fukku-writecheck-*")
	if err != nil {
		return fmt.Errorf("%s is not writable", dir)
	}
	name := f.Name()
	_ = f.Close()
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("%s is not writable", dir)
	}
	return nil
}
