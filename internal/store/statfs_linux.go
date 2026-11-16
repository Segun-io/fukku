package store

import "syscall"

func statfsType(path string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	// Magic numbers are 32 bits; on some platforms the field is signed.
	return int64(uint32(st.Type)), true //nolint:gosec // a 32-bit magic number, truncation intended
}
