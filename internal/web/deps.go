package web

import "github.com/segun-io/fukku/internal/spool"

// Deps are the services the web process is built with. Each issue that needs one adds its
// own field.
type Deps struct {
	// Queue carries tasks to the worker and their results back (package spool).
	Queue spool.Queue
}
