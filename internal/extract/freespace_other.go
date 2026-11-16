//go:build !linux

package extract

import (
	"math"
	"os"
)

// freeSpace is not measured on this platform (the worker runs on Linux); the check passes.
func freeSpace(*os.File) (int64, error) { return math.MaxInt64, nil }
