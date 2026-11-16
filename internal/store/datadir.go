package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// LockFileName is the lock file in server.dataDir. The process that has the database open
// holds an exclusive flock on it, so two web processes (or a web process and a combined
// one) never share a database.
const LockFileName = "fukku.lock"

// ErrLocked means another process has the database in the data directory open.
var ErrLocked = errors.New("store: the data directory is in use by another Fukku process")

// lockDataDir takes the data directory's exclusive lock. It is held until the returned file
// is closed or the process ends.
func lockDataDir(dir string) (*os.File, error) {
	path := filepath.Join(dir, LockFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NONBLOCK, 0o600) //nolint:gosec // the configured data directory
	if err != nil {
		return nil, fmt.Errorf("store: lock server.dataDir: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("store: lock server.dataDir: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("store: lock server.dataDir: %s is not a regular file", path)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w (server.dataDir %s)", ErrLocked, dir)
		}
		return nil, fmt.Errorf("store: lock server.dataDir %s: %w", dir, err)
	}
	return f, nil
}

// filesystemType reports the kind of filesystem that holds path (a statfs magic number on
// Linux). ok is false where it cannot be told. A variable so tests can pretend.
var filesystemType = statfsType

// networkFilesystems are the filesystem types SQLite must not live on: its locking does not
// work reliably over them. Best effort: FUSE is not listed, it is mostly local.
var networkFilesystems = map[int64]string{
	0x6969:     "nfs",
	0x517b:     "smb",
	0xff534d42: "cifs",
	0xfe534d42: "smb2",
	0x5346414f: "afs",
	0x6b414653: "kafs",
	0x73757245: "coda",
	0x564c:     "ncp",
	0x00c36400: "ceph",
	0x01021997: "9p",
	0x47504653: "gpfs",
	0x0bd00bd0: "lustre",
}

// checkDataDir creates dir if needed and refuses one that is not writable or that is on
// network storage.
func checkDataDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("store: server.dataDir %q must be an absolute path", dir)
	}
	if strings.ContainsRune(dir, '?') {
		return fmt.Errorf("store: server.dataDir %q must not contain '?'", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("store: server.dataDir: %w", err)
	}
	probe, err := os.CreateTemp(dir, ".fukku-write-check-*")
	if err != nil {
		return fmt.Errorf("store: server.dataDir %s is not writable: %w", dir, err)
	}
	name := probe.Name()
	err = errors.Join(probe.Close(), os.Remove(name))
	if err != nil {
		return fmt.Errorf("store: server.dataDir %s is not writable: %w", dir, err)
	}
	if t, ok := filesystemType(dir); ok {
		if name, network := networkFilesystems[t]; network {
			return fmt.Errorf("store: server.dataDir %s is on network storage (%s); the database needs a local disk", dir, name)
		}
	}
	return nil
}
