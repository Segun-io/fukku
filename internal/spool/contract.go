// Package spool is the task and result protocol between the web process and the worker:
// JSON files in worker.spoolDir, written to a temporary name and renamed into place.
package spool

import (
	"context"
	"encoding/json"
	"time"
)

// Version is the protocol version written to every file. Files with another version are
// left alone and logged.
const Version = 1

// Task kinds. M3 adds "preview" and "merge".
const (
	KindProbe   = "probe"   // args and output: #4
	KindExtract = "extract" // args and output: #3
)

// Task is <spool>/tasks/<ID>.json, written by the web process.
type Task struct {
	V       int             `json:"v"`
	ID      string          `json:"id"` // [a-z0-9-]{1,96}, chosen by the submitter; the same ID is the same task
	Kind    string          `json:"kind"`
	Job     string          `json:"job"` // job id: the task may touch only <intakeRoot>/jobs/<job>/
	Created time.Time       `json:"created"`
	Args    json.RawMessage `json:"args"` // per kind; every path in it is job-relative
}

// Result is <spool>/results/<ID>.json, written by the worker.
type Result struct {
	V        int             `json:"v"`
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Job      string          `json:"job"`
	Status   Status          `json:"status"`
	Error    *Error          `json:"error,omitempty"` // when Status is error
	Started  time.Time       `json:"started"`
	Finished time.Time       `json:"finished"`
	Output   json.RawMessage `json:"output,omitempty"` // per kind, when Status is ok
}

type Status string

const (
	StatusOK    Status = "ok"    // the task ran to its end; a refused archive or file is still ok
	StatusError Status = "error" // the task could not be run to its end
)

// Error codes: timeout, cancelled, bad_task, unknown_kind, internal.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Queue is the web side of the spool.
type Queue interface {
	// Submit writes the task. It does nothing if a task with this ID is waiting, running or
	// has a result, so a restarted submitter can repeat it.
	Submit(ctx context.Context, t Task) error
	// Result returns the task's result; ok is false while there is none.
	Result(ctx context.Context, id string) (r Result, ok bool, err error)
	// Ack deletes the result once the caller has stored what it needs.
	Ack(ctx context.Context, id string) error
	// Cancel removes a waiting task, or asks the worker to stop a running one (its result
	// then has error code cancelled). Unknown IDs are not an error.
	Cancel(ctx context.Context, id string) error
}
