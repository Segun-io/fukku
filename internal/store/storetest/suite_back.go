package storetest

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/store"
)

// BackToConfirmation (#95): a flow 2a merge refused for a reason that will fail again goes
// from importing back to awaiting confirmation, with its options and its audit entry, and
// only while no import is submitted.
func testBackToConfirmation(t *testing.T, s store.Store) {
	ctx := t.Context()
	target := store.Target{App: "radarr", TitleID: 5, Title: "Film", MatchedAt: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)}
	importing := func() store.Job {
		j := drive(t, s, newJob(t, s, "user1").ID, store.Importing)
		if err := s.SetTarget(ctx, j.ID, target); err != nil {
			t.Fatal(err)
		}
		return j
	}
	opts := json.RawMessage(`{"mergeRefusal":{"code":"verify_failed"}}`)
	audit := store.AuditEntry{Actor: "system", Action: "merge.refused", Detail: map[string]string{"refusal": "verify_failed"}}
	back := func(id string, tg store.Target) (store.Job, error) {
		return s.BackToConfirmation(ctx, id, tg, opts, audit, store.Change{Actor: "system", Note: "refused"})
	}

	// No import record, and one that is not submitted: back, with the options, the event
	// and the audit entry.
	for _, rec := range []bool{false, true} {
		j := importing()
		if rec {
			if err := s.PutImport(ctx, store.Import{JobID: j.ID}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := back(j.ID, target)
		if err != nil || got.State != store.AwaitingConfirmation {
			t.Fatalf("back (import record %t): %+v, %v", rec, got, err)
		}
		if tg := must(s.Target(ctx, j.ID))(t); string(tg.Options) != string(opts) {
			t.Errorf("options %s", tg.Options)
		}
		h := must(s.History(ctx, j.ID))(t)
		if ev := h[len(h)-1]; ev.From != store.Importing || ev.To != store.AwaitingConfirmation || ev.Note != "refused" {
			t.Errorf("event %+v", ev)
		}
		log := must(s.AuditLog(ctx, j.ID))(t)
		if len(log) != 1 || log[0].Action != "merge.refused" || log[0].Detail["refusal"] != "verify_failed" {
			t.Errorf("audit %+v", log)
		}
		// It can be confirmed again.
		if _, err := s.ConfirmImport(ctx, j.ID, target, nil, nil, nil, store.Change{Actor: "user1"}); err != nil {
			t.Errorf("confirm after going back: %v", err)
		}
	}

	// Once an import is submitted, never: nothing is changed.
	sub := importing()
	if err := s.BeginImport(ctx, store.Import{JobID: sub.ID, SubmittedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := back(sub.ID, target); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("back after the submission: %v, want ErrConflict", err)
	}
	if j := must(s.Job(ctx, sub.ID))(t); j.State != store.Importing {
		t.Errorf("job %s", j.State)
	}
	if tg := must(s.Target(ctx, sub.ID))(t); string(tg.Options) == string(opts) {
		t.Error("options written by a refused move")
	}
	if log := must(s.AuditLog(ctx, sub.ID))(t); len(log) != 0 {
		t.Errorf("audit written by a refused move: %+v", log)
	}
	// The write that submits is refused once the job is back.
	b := importing()
	if _, err := back(b.ID, target); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginImport(ctx, store.Import{JobID: b.ID, SubmittedAt: time.Now()}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("BeginImport after going back: %v, want ErrConflict", err)
	}

	// Another target, another state, an unknown job.
	c := importing()
	other := target
	other.TitleID = 6
	if _, err := back(c.ID, other); !errors.Is(err, store.ErrConflict) {
		t.Errorf("back with another target: %v, want ErrConflict", err)
	}
	d := drive(t, s, newJob(t, s, "user1").ID, store.AwaitingConfirmation)
	if err := s.SetTarget(ctx, d.ID, target); err != nil {
		t.Fatal(err)
	}
	if _, err := back(d.ID, target); !errors.Is(err, store.ErrConflict) {
		t.Errorf("back of an awaiting job: %v, want ErrConflict", err)
	}
	if _, err := back(unknownJob, target); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("back of an unknown job: %v, want ErrNotFound", err)
	}

	// The state machine allows the move, but Transition and TransitionFor do not make it.
	if !store.CanTransition(store.Importing, store.AwaitingConfirmation) {
		t.Error("importing → awaiting-confirmation is not in the state machine")
	}
	e := importing()
	if _, err := s.Transition(ctx, e.ID, store.Importing, store.AwaitingConfirmation, store.Change{}); !errors.Is(err, store.ErrInvalidTransition) {
		t.Errorf("Transition importing → awaiting: %v, want ErrInvalidTransition", err)
	}
	if _, err := s.TransitionFor(ctx, e.ID, target, store.Importing, store.AwaitingConfirmation, store.Change{}); !errors.Is(err, store.ErrInvalidTransition) {
		t.Errorf("TransitionFor importing → awaiting: %v, want ErrInvalidTransition", err)
	}
}
