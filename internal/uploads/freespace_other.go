//go:build !linux

package uploads

// freeSpace is not measured on this platform (the image runs on Linux): -1 skips the check.
func freeSpace(string) (int64, error) { return -1, nil }
