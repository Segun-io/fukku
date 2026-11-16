//go:build !unix

package fscheck

import "errors"

func statDevice(string) (uint64, error) {
	return 0, errors.New("device ids are not available on this platform")
}
