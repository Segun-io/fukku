package storetest

import (
	"context"
	"errors"
	"slices"

	"github.com/segun-io/fukku/internal/store"
)

// The second versions of Fake (store.Versions), with the rules of the SQLite store.

func normVersion(v store.Version) store.Version {
	v.CreatedAt, v.CheckedAt, v.GroupedAt, v.PrimaryChangedAt = norm(v.CreatedAt), norm(v.CheckedAt), norm(v.GroupedAt), norm(v.PrimaryChangedAt)
	return v
}

func (f *Fake) PutVersion(ctx context.Context, v store.Version) (store.Version, error) {
	if err := f.lock(ctx); err != nil {
		return store.Version{}, err
	}
	defer f.mu.Unlock()
	if v.Path == "" {
		return store.Version{}, errors.New("storetest: a version needs a path")
	}
	v = normVersion(v)
	i := slices.IndexFunc(f.vers, func(o store.Version) bool { return o.ID == v.ID })
	if v.ID != 0 && i < 0 {
		return store.Version{}, store.ErrNotFound
	}
	for _, o := range f.vers {
		if o.Path == v.Path && o.ID != v.ID {
			return store.Version{}, store.ErrExists
		}
	}
	if v.ID == 0 {
		if v.CreatedAt.IsZero() {
			v.CreatedAt = f.clock()
		}
		f.verID++
		v.ID = f.verID
		f.vers = append(f.vers, v)
		return v, nil
	}
	v.CreatedAt = f.vers[i].CreatedAt
	f.vers[i] = v
	return v, nil
}

func (f *Fake) VersionByPath(ctx context.Context, path string) (store.Version, error) {
	if err := f.lock(ctx); err != nil {
		return store.Version{}, err
	}
	defer f.mu.Unlock()
	for _, v := range f.vers {
		if v.Path == path {
			return v, nil
		}
	}
	return store.Version{}, store.ErrNotFound
}

func (f *Fake) VersionsOf(ctx context.Context, jobID string) ([]store.Version, error) {
	if err := f.lock(ctx); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	var out []store.Version
	for _, v := range f.vers {
		if v.JobID == jobID {
			out = append(out, v)
		}
	}
	return out, nil
}

func (f *Fake) AllVersions(ctx context.Context) ([]store.Version, error) {
	if err := f.lock(ctx); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	return slices.Clone(f.vers), nil
}
