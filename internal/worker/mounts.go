package worker

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
)

// CheckMounts checks the worker's view of the files against the reference layout
// (docs/install.md, docs/threat-model.md B5): it must be able to write its own folder of the
// intake root, worker/, and nothing else that the web process hands to Radarr or Sonarr or
// links into the library: not jobs/ (the jobs' files, read-only to it), not .incoming/ (the
// unfinished uploads, not mounted at all), not a library root (read-only). The error names
// every path that is wrong. `fukku worker` refuses to start on it unless
// worker.requireSeparateMounts is off.
//
// The check asks the kernel (access(2) with W_OK, which fails on a read-only mount), so it
// sees what the process can do, whatever the compose file says.
func CheckMounts(cfg config.Config) error {
	intake := cfg.Paths.IntakeRoot
	var bad []string
	for _, p := range append([]string{filepath.Join(intake, jobdir.Jobs), filepath.Join(intake, jobdir.Incoming)}, cfg.Paths.LibraryRoots...) {
		if writable(p) {
			bad = append(bad, p)
		}
	}
	var errs []error
	own := filepath.Join(intake, jobdir.Worker)
	if len(bad) > 0 {
		errs = append(errs, fmt.Errorf("the worker can write %s: give it only %s writable, %s and the library roots read-only, and no %s (docs/install.md)",
			strings.Join(bad, ", "), own, filepath.Join(intake, jobdir.Jobs), filepath.Join(intake, jobdir.Incoming)))
	}
	if !writable(own) {
		errs = append(errs, fmt.Errorf("the worker cannot write %s, its own folder: it must exist, belong to the worker's user and be mounted writable (docs/install.md)", own))
	}
	return errors.Join(errs...)
}
