// Package mediaservertest has an in-memory mediaserver.MediaServer for the tests of code
// that uses one.
package mediaservertest

import (
	"context"
	"sync"

	"github.com/segun-io/fukku/internal/mediaserver"
)

var (
	_ mediaserver.MediaServer = (*Fake)(nil)
	_ mediaserver.Grouper     = (*Fake)(nil)
)

// Fake is a media server that lists the paths in Items (path to item id). Titles in Pending
// appear in Items once a Refresh has been made and more than FindsNeeded Find calls have
// followed it ("found late"). Set the fields before use.
type Fake struct {
	mu sync.Mutex

	// Err maps a method name ("Version", "Refresh", "Find") to the error that call returns.
	Err         map[string]error
	Info        string
	Items       map[string]string
	Pending     map[string]string
	FindsNeeded int
	// ScanOnly makes Pending appear only after a Scan, as in a library that was never scanned.
	ScanOnly bool
	// Unlisted paths are in no library of the server: Refresh refreshes the others and
	// returns a *mediaserver.UnlistedError naming them, and they never appear.
	Unlisted map[string]bool

	Refreshed [][]string // the paths of every Refresh call
	FindCalls []string   // the path of every Find call
	Scans     int        // the number of Scan calls
	Merged    [][]string // the item ids of every MergeVersions call

	groups    map[string]int // item id to its MergeVersions group
	group     int
	refreshed bool
	scanned   bool
	finds     int
}

func (f *Fake) Version(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Info, f.Err["Version"]
}

func (f *Fake) Refresh(_ context.Context, paths []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Refreshed = append(f.Refreshed, append([]string(nil), paths...))
	if err := f.Err["Refresh"]; err != nil {
		return err
	}
	f.refreshed = true
	var unlisted []string
	for _, p := range paths {
		if f.Unlisted[p] {
			unlisted = append(unlisted, p)
		}
	}
	if len(unlisted) > 0 {
		return &mediaserver.UnlistedError{Paths: unlisted}
	}
	return nil
}

func (f *Fake) Scan(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Scans++
	if err := f.Err["Scan"]; err != nil {
		return false, err
	}
	f.scanned = true
	return true, nil
}

func (f *Fake) Find(_ context.Context, path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.FindCalls = append(f.FindCalls, path)
	if err := f.Err["Find"]; err != nil {
		return "", err
	}
	if (f.refreshed && !f.ScanOnly) || f.scanned {
		f.finds++
		if f.finds > f.FindsNeeded {
			if f.Items == nil {
				f.Items = map[string]string{}
			}
			for p, id := range f.Pending {
				if !f.Unlisted[p] {
					f.Items[p] = id
					delete(f.Pending, p)
				}
			}
		}
	}
	return f.Items[path], nil
}
