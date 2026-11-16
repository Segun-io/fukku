// Package fscheck holds the filesystem checks of plan §9. /readyz runs them, and the import
// code runs them again before each import, so nothing is written onto a host disk under a
// missing mount and moves stay renames.
package fscheck

import (
	"errors"
	"fmt"
	"os"

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

// SameDevice returns an error unless dir and other are on the same filesystem (device id):
// a hard link from one to the other is then possible. Second versions (#56) need it
// between the intake root and versions.root, whatever the import modes.
func SameDevice(dir, other string) error {
	a, err := deviceID(dir)
	if err != nil {
		return fmt.Errorf("%s cannot be read", dir)
	}
	b, err := deviceID(other)
	if err != nil {
		return fmt.Errorf("%s cannot be read", other)
	}
	if a != b {
		return fmt.Errorf("%s is on a different filesystem than %s", dir, other)
	}
	return nil
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
