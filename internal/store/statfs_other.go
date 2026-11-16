//go:build !linux

package store

func statfsType(string) (int64, bool) { return 0, false }
