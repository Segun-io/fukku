// Package storetest has an in-memory store.Store for tests (Fake) and the conformance suite
// every implementation must pass (Run).
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/segun-io/fukku/internal/store"
)

// Fake is an in-memory store.Store with the same rules as the SQLite store (Run checks
// both). It is safe for concurrent use.
type Fake struct {
	now func() time.Time

	mu      sync.Mutex
	closed  bool
	seq     int64
	jobs    map[string]*fakeJob
	files   map[int64]store.File
	fileID  int64
	uploads map[string]store.Upload
	events  []store.Event
	audit   []store.AuditEntry
	m2      m2
}

type fakeJob struct {
	store.Job
	seq int64
}

var _ store.Store = (*Fake)(nil)

// NewFake returns an empty Fake that reads the time from now (nil: time.Now).
func NewFake(now func() time.Time) *Fake {
	if now == nil {
		now = time.Now
	}
	return &Fake{
		now:     now,
		jobs:    map[string]*fakeJob{},
		files:   map[int64]store.File{},
		uploads: map[string]store.Upload{},
		m2:      newM2(),
	}
}

var errClosed = errors.New("storetest: the fake store is closed")

// lock takes the mutex and fails once the store is closed or ctx is done.
func (f *Fake) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return errClosed
	}
	return nil
}

func (f *Fake) clock() time.Time { return norm(f.now()) }

// norm keeps what the SQLite store keeps: UTC, nanoseconds, no monotonic reading.
func norm(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return time.Unix(0, t.UnixNano()).UTC()
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func actorOr(a string) string {
	if a == "" {
		return "system"
	}
	return a
}

// ---- jobs -----------------------------------------------------------------------------

func (f *Fake) CreateJob(ctx context.Context, owner string) (store.Job, error) {
	if err := f.lock(ctx); err != nil {
		return store.Job{}, err
	}
	defer f.mu.Unlock()
	now := f.clock()
	f.seq++
	j := store.Job{ID: newID(), Owner: owner, State: store.Uploading, Attempt: 1, CreatedAt: now, UpdatedAt: now}
	f.jobs[j.ID] = &fakeJob{Job: j, seq: f.seq}
	return j, nil
}

func (f *Fake) Job(ctx context.Context, id string) (store.Job, error) {
	if err := f.lock(ctx); err != nil {
		return store.Job{}, err
	}
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return store.Job{}, store.ErrNotFound
	}
	return j.Job, nil
}

func (f *Fake) Jobs(ctx context.Context, flt store.JobFilter) ([]store.Job, error) {
	if err := f.lock(ctx); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	var all []*fakeJob
	for _, j := range f.jobs {
		if flt.Owner != "" && j.Owner != flt.Owner {
			continue
		}
		if len(flt.States) > 0 && !slices.Contains(flt.States, j.State) {
			continue
		}
		all = append(all, j)
	}
	sort.Slice(all, func(a, b int) bool {
		if !all[a].CreatedAt.Equal(all[b].CreatedAt) {
			return all[a].CreatedAt.After(all[b].CreatedAt)
		}
		return all[a].seq > all[b].seq
	})
	limit, offset := flt.Limit, flt.Offset
	if limit <= 0 {
		limit = 100
	}
	offset = min(max(offset, 0), len(all))
	all = all[offset:min(offset+limit, len(all))]
	var out []store.Job
	for _, j := range all {
		out = append(out, j.Job)
	}
	return out, nil
}

func (f *Fake) Transition(ctx context.Context, id string, from, to store.State, c store.Change) (store.Job, error) {
	if err := f.lock(ctx); err != nil {
		return store.Job{}, err
	}
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	if !ok {
		return store.Job{}, store.ErrNotFound
	}
	if j.State != from {
		return store.Job{}, store.ErrConflict
	}
	resuming := (from == store.Failed || from == store.Stalled) && j.Resume != "" && to == j.Resume
	if !store.CanTransition(from, to) && !resuming {
		return store.Job{}, fmt.Errorf("%w: %s → %s", store.ErrInvalidTransition, from, to)
	}
	if (to == store.Failed || to == store.Stalled) && c.ErrorCode == "" {
		return store.Job{}, fmt.Errorf("%w: moving to %s needs an error code", store.ErrInvalidTransition, to)
	}
	now := f.clock()
	next := j.Job
	next.State, next.UpdatedAt = to, now
	switch to {
	case store.Failed:
		next.Resume = store.ResumeState(from)
		if from == store.Stalled {
			next.Resume = store.ResumeState(j.Resume)
		}
		next.ErrorCode, next.ErrorText = c.ErrorCode, c.ErrorText
	case store.Stalled:
		next.Resume = from
		next.ErrorCode, next.ErrorText = c.ErrorCode, c.ErrorText
	default:
		next.Resume, next.ErrorCode, next.ErrorText = "", "", ""
	}
	if from == store.Failed && resuming {
		next.Attempt++
	}
	if c.ExpiresAt != nil {
		next.ExpiresAt = norm(*c.ExpiresAt)
	}
	j.Job = next
	f.events = append(f.events, store.Event{JobID: id, At: now, From: from, To: to, Actor: actorOr(c.Actor), Note: c.Note})
	return next, nil
}

func (f *Fake) History(ctx context.Context, jobID string) ([]store.Event, error) {
	if err := f.lock(ctx); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	var out []store.Event
	for _, e := range f.events {
		if e.JobID == jobID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (f *Fake) DeleteJob(ctx context.Context, id string) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if _, ok := f.jobs[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.jobs, id)
	f.m2.dropJob(id)
	maps.DeleteFunc(f.files, func(_ int64, fl store.File) bool { return fl.JobID == id })
	maps.DeleteFunc(f.uploads, func(_ string, u store.Upload) bool { return u.JobID == id })
	f.events = slices.DeleteFunc(f.events, func(e store.Event) bool { return e.JobID == id })
	return nil
}

// ---- files ----------------------------------------------------------------------------

func cloneFile(fl store.File) store.File {
	if len(fl.Report) == 0 {
		fl.Report = nil
	} else {
		fl.Report = slices.Clone(fl.Report)
	}
	return fl
}

func (f *Fake) pathTaken(jobID, path string, except int64) bool {
	for id, fl := range f.files {
		if id != except && fl.JobID == jobID && fl.Path == path {
			return true
		}
	}
	return false
}

func (f *Fake) AddFile(ctx context.Context, fl store.File) (store.File, error) {
	if err := f.lock(ctx); err != nil {
		return store.File{}, err
	}
	defer f.mu.Unlock()
	if _, ok := f.jobs[fl.JobID]; !ok {
		return store.File{}, store.ErrNotFound
	}
	if f.pathTaken(fl.JobID, fl.Path, 0) {
		return store.File{}, store.ErrExists
	}
	if fl.Status == "" {
		fl.Status = store.FilePending
	}
	f.fileID++
	fl.ID = f.fileID
	fl = cloneFile(fl)
	f.files[fl.ID] = fl
	return cloneFile(fl), nil
}

func (f *Fake) UpdateFile(ctx context.Context, fl store.File) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	old, ok := f.files[fl.ID]
	if !ok {
		return store.ErrNotFound
	}
	if f.pathTaken(old.JobID, fl.Path, fl.ID) {
		return store.ErrExists
	}
	if fl.Status == "" {
		fl.Status = store.FilePending
	}
	fl.JobID = old.JobID // a file never moves to another job
	f.files[fl.ID] = cloneFile(fl)
	return nil
}

func (f *Fake) DeleteFile(ctx context.Context, id int64) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if _, ok := f.files[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.files, id)
	f.m2.dropFile(id)
	return nil
}

func (f *Fake) Files(ctx context.Context, jobID string) ([]store.File, error) {
	if err := f.lock(ctx); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	var out []store.File
	for _, fl := range f.files {
		if fl.JobID == jobID {
			out = append(out, cloneFile(fl))
		}
	}
	slices.SortFunc(out, func(a, b store.File) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// ---- uploads --------------------------------------------------------------------------

func (f *Fake) PutUpload(ctx context.Context, u store.Upload) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if u.ID == "" {
		return errors.New("storetest: upload id is empty")
	}
	if _, ok := f.jobs[u.JobID]; !ok {
		return store.ErrNotFound
	}
	old, ok := f.uploads[u.ID]
	if !ok {
		old = store.Upload{CreatedAt: f.clock()}
	}
	u.CreatedAt, u.ProgressAt = norm(u.CreatedAt), norm(u.ProgressAt)
	if u.CreatedAt.IsZero() {
		u.CreatedAt = old.CreatedAt
	}
	if u.ProgressAt.IsZero() {
		u.ProgressAt = old.ProgressAt
	}
	if u.ProgressAt.IsZero() {
		u.ProgressAt = u.CreatedAt
	}
	f.uploads[u.ID] = u
	return nil
}

func (f *Fake) Upload(ctx context.Context, id string) (store.Upload, error) {
	if err := f.lock(ctx); err != nil {
		return store.Upload{}, err
	}
	defer f.mu.Unlock()
	u, ok := f.uploads[id]
	if !ok {
		return store.Upload{}, store.ErrNotFound
	}
	return u, nil
}

func (f *Fake) Uploads(ctx context.Context, owner, jobID string) ([]store.Upload, error) {
	if err := f.lock(ctx); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	var out []store.Upload
	for _, u := range f.uploads {
		if (owner == "" || u.Owner == owner) && (jobID == "" || u.JobID == jobID) {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b store.Upload) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (f *Fake) DeleteUpload(ctx context.Context, id string) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if _, ok := f.uploads[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.uploads, id)
	return nil
}

// ---- audit ----------------------------------------------------------------------------

func cloneAudit(e store.AuditEntry) store.AuditEntry {
	if len(e.Detail) == 0 {
		e.Detail = nil
	} else {
		e.Detail = maps.Clone(e.Detail)
	}
	return e
}

// Audit stores the entry. Unlike the SQLite store, the fake does not log it.
func (f *Fake) Audit(ctx context.Context, e store.AuditEntry) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	defer f.mu.Unlock()
	if e.Action == "" {
		return errors.New("storetest: an audit entry needs an action")
	}
	e.ID = int64(len(f.audit) + 1)
	e.At = norm(e.At)
	if e.At.IsZero() {
		e.At = f.clock()
	}
	e.Actor = actorOr(e.Actor)
	if _, err := json.Marshal(e.Detail); err != nil {
		return err
	}
	f.audit = append(f.audit, cloneAudit(e))
	return nil
}

func (f *Fake) AuditLog(ctx context.Context, jobID string) ([]store.AuditEntry, error) {
	if err := f.lock(ctx); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()
	var out []store.AuditEntry
	for _, e := range f.audit {
		if jobID == "" || e.JobID == jobID {
			out = append(out, cloneAudit(e))
		}
	}
	slices.SortStableFunc(out, func(a, b store.AuditEntry) int { return a.At.Compare(b.At) })
	return out, nil
}

// ---- lifecycle ------------------------------------------------------------------------

func (f *Fake) Ping(ctx context.Context) error {
	if err := f.lock(ctx); err != nil {
		return err
	}
	f.mu.Unlock()
	return nil
}

func (f *Fake) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}
