package mediaservertest

import (
	"context"
	"slices"
)

// The Grouper side of Fake (second versions, #56).

// MergeVersions groups the items: Sources of each then lists the files of all of them.
// Err["MergeVersions"] makes it fail.
func (f *Fake) MergeVersions(_ context.Context, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Merged = append(f.Merged, append([]string(nil), ids...))
	if err := f.Err["MergeVersions"]; err != nil {
		return err
	}
	if f.groups == nil {
		f.groups = map[string]int{}
	}
	f.group++
	for _, id := range ids {
		f.groups[id] = f.group
	}
	return nil
}

// Sources returns the path of the item (from Items) and those of the items grouped with
// it, in path order; nothing for an item Items does not list. Err["Sources"] makes it fail.
func (f *Fake) Sources(_ context.Context, id string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.Err["Sources"]; err != nil {
		return nil, err
	}
	var own string
	for p, i := range f.Items {
		if i == id {
			own = p
		}
	}
	if own == "" {
		return nil, nil
	}
	out := []string{own}
	if g, ok := f.groups[id]; ok {
		for p, i := range f.Items {
			if i != id && f.groups[i] == g {
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// MergeCalls returns the item ids of every MergeVersions call so far; safe while the fake
// is in use.
func (f *Fake) MergeCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Merged)
}

// Ungroup undoes every grouping, as Jellyfin does when a grouped file is renamed or
// replaced (probe B3e).
func (f *Fake) Ungroup() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups = nil
}

// SetItem lists path under id (the server scanned a new file, say); an empty id drops it.
func (f *Fake) SetItem(path, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Items == nil {
		f.Items = map[string]string{}
	}
	if id == "" {
		delete(f.Items, path)
		return
	}
	f.Items[path] = id
}
