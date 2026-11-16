//go:build !linux

package fscheck

import "errors"

// FreeBytes is measured on Linux only, where the product runs.
func FreeBytes(string) (int64, error) {
	return 0, errors.New("free space is only measured on Linux")
}
