package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/store"
)

// BeginImport and CancelUnsubmitted (#92): of a cancel and the write that submits an
// import, whichever comes first decides. An import that is submitted is never cancelled
// this way, and one that is cancelled is never submitted.
func testCancelUnsubmitted(t *testing.T, s store.Store) {
	ctx := t.Context()
	sub := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	cancel := func(id string, from store.State) error {
		_, err := s.CancelUnsubmitted(ctx, id, from, store.Change{Actor: "user1", Note: "no import yet"})
		return err
	}

	// No import record: cancelled, with the event.
	a := drive(t, s, newJob(t, s, "user1").ID, store.Importing)
	got, err := s.CancelUnsubmitted(ctx, a.ID, store.Importing, store.Change{Actor: "user1", Note: "no import yet"})
	if err != nil || got.State != store.Cancelled {
		t.Fatalf("cancel without an import record: %+v, %v", got, err)
	}
	h := must(s.History(ctx, a.ID))(t)
	if ev := h[len(h)-1]; ev.From != store.Importing || ev.To != store.Cancelled || ev.Actor != "user1" || ev.Note != "no import yet" {
		t.Errorf("event %+v", ev)
	}
	// The write that submits is refused once the job is cancelled, and writes nothing.
	if err := s.BeginImport(ctx, store.Import{JobID: a.ID, SubmittedAt: sub}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("BeginImport after the cancel: %v, want ErrConflict", err)
	}
	if _, err := s.Import(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an import record after a refused BeginImport: %v", err)
	}

	// A record that is not submitted (zero SubmittedAt) does not stop the cancel.
	b := drive(t, s, newJob(t, s, "user1").ID, store.Importing)
	if err := s.PutImport(ctx, store.Import{JobID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if err := cancel(b.ID, store.Importing); err != nil {
		t.Fatalf("cancel with an unsubmitted record: %v", err)
	}

	// Submitted first: the cancel is refused and changes nothing.
	c := drive(t, s, newJob(t, s, "user1").ID, store.Importing)
	if err := s.BeginImport(ctx, store.Import{JobID: c.ID, SubmittedAt: sub}); err != nil {
		t.Fatal(err)
	}
	if err := cancel(c.ID, store.Importing); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cancel after the submission: %v, want ErrConflict", err)
	}
	if j := must(s.Job(ctx, c.ID))(t); j.State != store.Importing {
		t.Errorf("job %s", j.State)
	}
	if err := s.BeginImport(ctx, store.Import{JobID: c.ID, CommandID: 5, SubmittedAt: sub}); err != nil {
		t.Fatalf("BeginImport of an importing job: %v", err)
	}
	if i := must(s.Import(ctx, c.ID))(t); i.CommandID != 5 || !i.SubmittedAt.Equal(sub) {
		t.Errorf("import %+v", i)
	}

	// Another state than the job's, an impossible move and an unknown job.
	d := drive(t, s, newJob(t, s, "user1").ID, store.Importing)
	if err := cancel(d.ID, store.Stalled); !errors.Is(err, store.ErrConflict) {
		t.Errorf("cancel from the wrong state: %v, want ErrConflict", err)
	}
	e := drive(t, s, newJob(t, s, "user1").ID, store.Imported)
	if err := cancel(e.ID, store.Imported); !errors.Is(err, store.ErrInvalidTransition) {
		t.Errorf("cancel of an imported job: %v, want ErrInvalidTransition", err)
	}
	if err := cancel(unknownJob, store.Importing); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cancel of an unknown job: %v, want ErrNotFound", err)
	}
	if err := s.BeginImport(ctx, store.Import{JobID: unknownJob}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("BeginImport of an unknown job: %v, want ErrNotFound", err)
	}
	// A stalled job on its way through the import may be cancelled too.
	f := drive(t, s, newJob(t, s, "user1").ID, store.Stalled)
	if err := cancel(f.ID, store.Stalled); err != nil {
		t.Errorf("cancel of a stalled job: %v", err)
	}
}
