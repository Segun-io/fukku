package web

import (
	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/metrics"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

// Deps are the services the web process is built with. Each issue that needs one adds its
// own field.
type Deps struct {
	// Queue carries tasks to the worker and their results back (package spool).
	Queue spool.Queue
	// Store holds jobs, their files, unfinished uploads and the audit log (package store).
	Store store.Store
	// Radarr and Sonarr are the clients of the apps, nil for one that is not configured
	// (the match API, #37).
	Radarr arr.MovieManager
	Sonarr arr.SeriesManager
	// Locks is shared by every step that writes a job's target, rows or state from what it
	// read (the match step, the proposal step). Nil: the match step has its own.
	Locks *flows.JobLocks
	// Proposals is the proposal flow (#38), shared with the job runner, which runs its
	// matched step. Nil: no proposal routes.
	Proposals *flows.Proposals
	// Imports is the import engine (#40), built on Proposals and shared with the job
	// runner, which runs its importing, imported and post-processing steps. Nil: no
	// confirmation route, and JobDetail has no import or result.
	Imports *flows.Engine
	// Metrics are the counters the process updates (package metrics); nil: none are kept.
	// /metrics is served only when server.metrics is on.
	Metrics *metrics.Set
}
