package web

import (
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
}
