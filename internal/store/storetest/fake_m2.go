package storetest

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/segun-io/fukku/internal/store"
)

// The M2 additions of Fake (store.M2), with the rules of the SQLite store.

// m2 is the M2 data of a Fake.
type m2 struct {
	targets   map[string]store.Target
	rows      map[string][]store.Row // by job, in PutRows order
	imports   map[string]store.Import
	postSteps map[string][]store.PostStep // by job, in the order first put
}

func newM2() m2 {
	return m2{
		targets:   map[string]store.Target{},
		rows:      map[string][]store.Row{},
		imports:   map[string]store.Import{},
		postSteps: map[string][]store.PostStep{},
	}
}

// dropJob removes everything M2 holds for a job.
func (m *m2) dropJob(id string) {
	delete(m.targets, id)
	delete(m.rows, id)
	delete(m.imports, id)
	delete(m.postSteps, id)
}

// dropFile removes the rows of a deleted file (ON DELETE CASCADE).
func (m *m2) dropFile(fileID int64) {
	for id, rs := range m.rows {
		m.rows[id] = slices.DeleteFunc(rs, func(r store.Row) bool { return r.FileID == fileID })
	}
}

func cloneRow(r store.Row) store.Row {
	r.Edit, r.Proposal = cloneJSON(r.Edit), cloneJSON(r.Proposal)
	return r
}

func cloneJSON(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return slices.Clone(b)
}

func (f *Fake) SetTarget(ctx context.Context, jobID string, t store.Target) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if t.App == "" {
		return errors.New("storetest: a target needs an app")
	}
	if _, ok := f.jobs[jobID]; !ok {
		return store.ErrNotFound
	}
	t.MatchedAt = norm(t.MatchedAt)
	if t.MatchedAt.IsZero() {
		t.MatchedAt = f.clock()
	}
	t.Options = cloneJSON(t.Options)
	f.m2.targets[jobID] = t
	return nil
}

func (f *Fake) Target(ctx context.Context, jobID string) (store.Target, error) {
	if err := f.lock(ctx); err != nil {
		return store.Target{}, err
	}
	defer f.mu.Unlock()
	t, ok := f.m2.targets[jobID]
	if !ok {
		return store.Target{}, store.ErrNotFound
	}
	t.Options = cloneJSON(t.Options)
	return t, nil
}

func (f *Fake) ClearTarget(ctx context.Context, jobID string) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if _, ok := f.jobs[jobID]; !ok {
		return store.ErrNotFound
	}
	delete(f.m2.targets, jobID)
	delete(f.m2.rows, jobID)
	return nil
}

func (f *Fake) PutRows(ctx context.Context, jobID string, rows []store.Row) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if _, ok := f.jobs[jobID]; !ok {
		return store.ErrNotFound
	}
	seen := map[string]bool{}
	out := make([]store.Row, 0, len(rows))
	for _, r := range rows {
		if r.ID == "" {
			return errors.New("storetest: a row needs an id")
		}
		if fl, ok := f.files[r.FileID]; !ok || fl.JobID != jobID {
			return fmt.Errorf("%w: row %q: file %d", store.ErrNotFound, r.ID, r.FileID)
		}
		if seen[r.ID] {
			return fmt.Errorf("%w: row %q", store.ErrExists, r.ID)
		}
		seen[r.ID] = true
		r.JobID = jobID
		out = append(out, cloneRow(r))
	}
	f.m2.rows[jobID] = out
	return nil
}

func (f *Fake) Rows(ctx context.Context, jobID string) ([]store.Row, error) {
	if err := f.lock(ctx); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	var out []store.Row
	for _, r := range f.m2.rows[jobID] {
		out = append(out, cloneRow(r))
	}
	return out, nil
}

func (f *Fake) UpdateRow(ctx context.Context, r store.Row) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	rows := f.m2.rows[r.JobID]
	i := slices.IndexFunc(rows, func(x store.Row) bool { return x.ID == r.ID })
	if i < 0 {
		return store.ErrNotFound
	}
	rows[i].Skip, rows[i].Override, rows[i].Result = r.Skip, r.Override, r.Result
	rows[i].Edit, rows[i].Proposal = cloneJSON(r.Edit), cloneJSON(r.Proposal)
	return nil
}

func (f *Fake) PutImport(ctx context.Context, i store.Import) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if _, ok := f.jobs[i.JobID]; !ok {
		return store.ErrNotFound
	}
	i.SubmittedAt, i.FinishedAt = norm(i.SubmittedAt), norm(i.FinishedAt)
	f.m2.imports[i.JobID] = i
	return nil
}

func (f *Fake) Import(ctx context.Context, jobID string) (store.Import, error) {
	if err := f.lock(ctx); err != nil {
		return store.Import{}, err
	}
	defer f.mu.Unlock()
	i, ok := f.m2.imports[jobID]
	if !ok {
		return store.Import{}, store.ErrNotFound
	}
	return i, nil
}

func (f *Fake) PutPostStep(ctx context.Context, p store.PostStep) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if p.Name == "" {
		return errors.New("storetest: a post-processing step needs a name")
	}
	if p.Status == "" {
		p.Status = store.StepPending
	}
	p.UpdatedAt = norm(p.UpdatedAt)
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = f.clock()
	}
	if _, ok := f.jobs[p.JobID]; !ok {
		return store.ErrNotFound
	}
	steps := f.m2.postSteps[p.JobID]
	if i := slices.IndexFunc(steps, func(x store.PostStep) bool { return x.Name == p.Name }); i >= 0 {
		steps[i] = p
	} else {
		f.m2.postSteps[p.JobID] = append(steps, p)
	}
	return nil
}

func (f *Fake) PostSteps(ctx context.Context, jobID string) ([]store.PostStep, error) {
	if err := f.lock(ctx); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	return slices.Clone(f.m2.postSteps[jobID]), nil
}

func (f *Fake) ActiveJobsFor(ctx context.Context, app string, titleID int) ([]store.Job, error) {
	if err := f.lock(ctx); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	var found []*fakeJob
	for id, t := range f.m2.targets {
		j, ok := f.jobs[id]
		if ok && t.App == app && t.TitleID == titleID && j.State != store.Done && j.State != store.Cancelled {
			found = append(found, j)
		}
	}
	slices.SortFunc(found, func(a, b *fakeJob) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.seq, a.seq)
	})
	var out []store.Job
	for _, j := range found {
		out = append(out, j.Job)
	}
	return out, nil
}
