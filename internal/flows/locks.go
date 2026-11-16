package flows

import "sync"

// JobLocks hands out one lock per key. Every writer of a job's target, rows or state that
// works from what it read (the match step, the proposal step) takes the lock of the job
// (its id as the key) around the read and the write, so they do not overwrite each other.
// Other keys serve the same purpose for other things, such as "title:radarr:603". A key's
// lock is its own: keys never share one, and an idle key costs nothing.
type JobLocks struct {
	mu sync.Mutex
	m  map[string]*keyLock
}

type keyLock struct {
	mu   sync.Mutex
	refs int // holders and waiters
}

// NewJobLocks returns an empty set of locks.
func NewJobLocks() *JobLocks { return &JobLocks{m: map[string]*keyLock{}} }

// Lock waits for the lock of key and returns the function that releases it. Locks are
// not re-entrant: take the lock once per call chain. When two are needed, take the one of
// the title before the one of the job.
func (l *JobLocks) Lock(key string) (unlock func()) {
	l.mu.Lock()
	k := l.m[key]
	if k == nil {
		k = &keyLock{}
		l.m[key] = k
	}
	k.refs++
	l.mu.Unlock()
	k.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			k.mu.Unlock()
			l.mu.Lock()
			if k.refs--; k.refs == 0 {
				delete(l.m, key)
			}
			l.mu.Unlock()
		})
	}
}
