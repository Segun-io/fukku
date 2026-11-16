package merge

import "os"

// SetSyncDir replaces the directory sync for a test and returns what restores it.
func SetSyncDir(f func(*os.Root, string) error) (restore func()) {
	old := syncDirFunc
	syncDirFunc = f
	return func() { syncDirFunc = old }
}
