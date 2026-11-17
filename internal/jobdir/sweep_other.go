//go:build !linux

package jobdir

import (
	"errors"
	"os"
)

// The sweep of worker/ opens every directory without following links, through descriptors
// and /proc (Linux, where the image runs); on this platform it does nothing.
var errNoSweep = errors.New("jobdir: the sweep of worker/ is not supported on this platform")

func (w *WorkerSweeper) sweep(*os.Root) (SweepResult, error) {
	return SweepResult{Done: true}, errNoSweep
}

func spillWorker(string, string, string) error { return errNoSweep }
