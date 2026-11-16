package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/segun-io/fukku/internal/store"
)

// The second versions of Fake (store.Versions), with the rules of the SQLite store.

func normVersion(v store.Version) store.Version {
	v.CreatedAt, v.CheckedAt, v.GroupedAt, v.PrimaryChangedAt = norm(v.CreatedAt), norm(v.CheckedAt), norm(v.GroupedAt), norm(v.PrimaryChangedAt)
	return v
}

func (f *Fake) PutVersion(ctx context.Context, v store.Version, audit ...store.AuditEntry) (store.Version, error) {
	if err := f.lock(ctx); err != nil {
		return store.Version{}, err
	}
	defer f.mu.Unlock()
	if v.Path == "" {
		return store.Version{}, errors.New("storetest: a version needs a path")
	}
	for _, e := range audit {
		if e.Action == "" {
			return store.Version{}, errors.New("storetest: an audit entry needs an action")
		}
		if _, err := json.Marshal(e.Detail); err != nil {
			return store.Version{}, err
		}
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
	} else {
		v.CreatedAt = f.vers[i].CreatedAt
		f.vers[i] = v
	}
	for _, e := range audit {
		if e.JobID == "" {
			e.JobID = v.JobID
		}
		e.ID = int64(len(f.audit) + 1)
		if e.At = norm(e.At); e.At.IsZero() {
			e.At = f.clock()
		}
		e.Actor = actorOr(e.Actor)
		f.audit = append(f.audit, cloneAudit(e))
	}
	return v, nil
}

func (f *Fake) DeleteVersion(ctx context.Context, id int64) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.vers, func(o store.Version) bool { return o.ID == id })
	if i < 0 {
		return store.ErrNotFound
	}
	f.vers = slices.Delete(f.vers, i, i+1)
	return nil
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
