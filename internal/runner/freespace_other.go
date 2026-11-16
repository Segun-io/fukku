//go:build !linux

package runner

import "math"

// freeSpace is not measured on this platform (Fukku runs on Linux); there is always room,
// as the extraction's own check also passes there.
func freeSpace(string) (int64, error) { return math.MaxInt64, nil }
