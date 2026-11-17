package worker

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
)

// CheckMounts checks the worker's view of the files against the reference layout
// (docs/install.md, docs/threat-model.md B5): it must be able to write its own folder of the
// intake root, worker/, and nothing else that the web process hands to Radarr or Sonarr or
// links into the library. The error names every path that is wrong. `fukku worker` refuses
// to start on it unless worker.requireSeparateMounts is off.
//
// Must not be writable at all: <intake>/jobs (the jobs' files, a read-only mount),
// <intake>/.incoming (the unfinished uploads; missing is the reference layout, where it is
// not mounted, and read-only passes too), each of paths.libraryRoots and versions.root.
//
// Must not be writable on the host's filesystem: the intake root itself and the folders
// above it and above each library root (the data folder that also holds the downloads,
// which Fukku has no setting for). In the reference layout these are folders of the
// container's own read-only root; in a container without read_only they are writable but
// on the container's own filesystem, which is harmless, so a writable one counts only when
// it is on the same filesystem as worker/.
//
// The check asks the kernel (access(2) with W_OK, which fails on a read-only mount), so it
// sees what the process can do, whatever the compose file says.
func CheckMounts(cfg config.Config) error {
	intake := filepath.Clean(cfg.Paths.IntakeRoot)
	own := filepath.Join(intake, jobdir.Worker)
	strict := []string{filepath.Join(intake, jobdir.Jobs), filepath.Join(intake, jobdir.Incoming)}
	host := []string{intake, filepath.Dir(intake)}
	for _, r := range cfg.Paths.LibraryRoots {
		strict = append(strict, filepath.Clean(r))
		host = append(host, filepath.Dir(filepath.Clean(r)))
	}
	if cfg.Versions.Root != "" {
		strict = append(strict, filepath.Clean(cfg.Versions.Root))
	}
	var bad []string
	for _, p := range strict {
		if writable(p) && !slices.Contains(bad, p) {
			bad = append(bad, p)
		}
	}
	ownDev, ownOK := device(own)
	for _, p := range host {
		if p == "/" || p == "." || slices.Contains(bad, p) || !writable(p) {
			continue
		}
		if d, ok := device(p); ok && ownOK && d == ownDev {
			bad = append(bad, p)
		}
	}
	var errs []error
	if len(bad) > 0 {
		errs = append(errs, fmt.Errorf("the worker can write %s: give it only %s writable, %s and the library roots read-only, and nothing else of the data folder (docs/install.md)",
			strings.Join(bad, ", "), own, filepath.Join(intake, jobdir.Jobs)))
	}
	if !writable(own) {
		errs = append(errs, fmt.Errorf("the worker cannot write %s, its own folder: it must exist, belong to the worker's user and be mounted writable (docs/install.md)", own))
	}
	return errors.Join(errs...)
}
