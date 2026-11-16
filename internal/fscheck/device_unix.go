//go:build unix

package fscheck

import (
	"errors"
	"os"
	"syscall"
)

func statDevice(path string) (uint64, error) {
	info, err := os.Stat(path) //nolint:gosec // configured roots (and a test's own mounts)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("no device id")
	}
	return uint64(st.Dev), nil //nolint:unconvert // Dev is not a uint64 on every platform
}
