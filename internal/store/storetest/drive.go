package storetest

import (
	"context"
	"fmt"

	"github.com/segun-io/fukku/internal/store"
)

// States lists every job state, in the order of plan §9.
var States = []store.State{
	store.Uploading, store.Uploaded, store.Probing, store.Probed, store.Matched,
	store.AwaitingConfirmation, store.Importing, store.Imported, store.PostProcessing,
	store.Done, store.Failed, store.Stalled, store.Cancelled,
}

// Path is the shortest way through the transition table from one state to another
// (excluding from, including to), or nil when the table has none.
func Path(from, to store.State) []store.State {
	if from == to {
		return []store.State{}
	}
	prev := map[store.State]store.State{from: ""}
	queue := []store.State{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range States {
			if _, seen := prev[next]; seen || !store.CanTransition(cur, next) {
				continue
			}
			prev[next] = cur
			if next == to {
				var path []store.State
				for s := to; s != from; s = prev[s] {
					path = append([]store.State{s}, path...)
				}
				return path
			}
			queue = append(queue, next)
		}
	}
	return nil
}

// Drive moves job id to state to along Path, as "system", giving the error code
// test_error where one is needed. Tests use it to put a job into a given state.
func Drive(ctx context.Context, s store.Store, id string, to store.State) (store.Job, error) {
	j, err := s.Job(ctx, id)
	if err != nil {
		return store.Job{}, err
	}
	path := Path(j.State, to)
	if path == nil {
		return store.Job{}, fmt.Errorf("storetest: no path from %s to %s", j.State, to)
	}
	for _, next := range path {
		c := store.Change{Actor: "system"}
		if next == store.Failed || next == store.Stalled {
			c.ErrorCode, c.ErrorText = "test_error", "driven there by a test"
		}
		if j, err = s.Transition(ctx, id, j.State, next, c); err != nil {
			return store.Job{}, err
		}
	}
	return j, nil
}
