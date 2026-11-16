//go:build !linux

package merge

import (
	"math"
	"os"
)

// measureFreeSpace is not measured on this platform (the worker runs on Linux); the check
// passes.
func measureFreeSpace(*os.File) (int64, error) { return math.MaxInt64, nil }
