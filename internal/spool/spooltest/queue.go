// Package spooltest has an in-memory spool.Queue for the tests of the web side, and the
// conformance tests that it and *spool.Dir both pass.
package spooltest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/segun-io/fukku/internal/spool"
)

// Queue is an in-memory spool.Queue. The test plays the worker: Tasks lists what was
// submitted and not finished, Complete and Fail finish a task. The zero value is ready to
// use, and Queue is safe for concurrent use.
//
// There is no worker in between, so every unfinished task counts as waiting: Cancel removes
// it and no result follows.
type Queue struct {
	// Now returns the time used for Created (when a task has none), Started and Finished.
	// Nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	tasks   map[string]spool.Task
	results map[string]spool.Result
}

var _ spool.Queue = (*Queue)(nil)

func (q *Queue) now() time.Time {
	if q.Now != nil {
		return q.Now().UTC()
	}
	return time.Now().UTC()
}

func (q *Queue) init() {
	if q.tasks == nil {
		q.tasks = map[string]spool.Task{}
		q.results = map[string]spool.Result{}
	}
}

// Submit stores the task unless one with its ID is waiting or has a result.
func (q *Queue) Submit(ctx context.Context, t spool.Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := spool.ValidateTask(t); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.init()
	if _, ok := q.tasks[t.ID]; ok {
		return nil
	}
	if _, ok := q.results[t.ID]; ok {
		return nil
	}
	t.V = spool.Version
	if t.Created.IsZero() {
		t.Created = q.now()
	}
	t.Args = bytes.Clone(t.Args)
	q.tasks[t.ID] = t
	return nil
}

// Result returns the task's result, if Complete or Fail has been called for it.
func (q *Queue) Result(ctx context.Context, id string) (spool.Result, bool, error) {
	if err := check(ctx, id); err != nil {
		return spool.Result{}, false, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	r, ok := q.results[id]
	r.Output = bytes.Clone(r.Output)
	return r, ok, nil
}

// Ack deletes the result.
func (q *Queue) Ack(ctx context.Context, id string) error {
	if err := check(ctx, id); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.results, id)
	return nil
}

// Cancel removes the task if it has not finished.
func (q *Queue) Cancel(ctx context.Context, id string) error {
	if err := check(ctx, id); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.tasks, id)
	return nil
}

// Tasks returns the submitted tasks that have not finished, oldest Created first.
func (q *Queue) Tasks() []spool.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	tasks := make([]spool.Task, 0, len(q.tasks))
	for _, t := range q.tasks {
		t.Args = bytes.Clone(t.Args)
		tasks = append(tasks, t)
	}
	slices.SortFunc(tasks, func(a, b spool.Task) int {
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return tasks
}

// Complete finishes the task with status ok and output as JSON (none when output is nil).
// It panics if the task is not waiting or output does not marshal: both are mistakes in
// the test.
func (q *Queue) Complete(id string, output any) {
	var data json.RawMessage
	if output != nil {
		var err error
		if data, err = json.Marshal(output); err != nil {
			panic(fmt.Sprintf("spooltest: output of %s: %v", id, err))
		}
	}
	q.finish(id, spool.StatusOK, nil, data)
}

// Fail finishes the task with status error. It panics if the task is not waiting.
func (q *Queue) Fail(id, code, message string) {
	q.finish(id, spool.StatusError, &spool.Error{Code: code, Message: message}, nil)
}

func (q *Queue) finish(id string, status spool.Status, e *spool.Error, output json.RawMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.tasks[id]
	if !ok {
		panic(fmt.Sprintf("spooltest: task %s is not waiting", id))
	}
	now := q.now()
	delete(q.tasks, id)
	q.results[id] = spool.Result{
		V: spool.Version, ID: t.ID, Kind: t.Kind, Job: t.Job,
		Status: status, Error: e, Started: now, Finished: now, Output: output,
	}
}

func check(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !spool.ValidID(id) {
		return fmt.Errorf("spool: malformed task id %q", id)
	}
	return nil
}
