package flows

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/store"
)

// The store and app calls the merge adds to the fused store and app of the crash tests.

func (s fusedStore) AddFile(ctx context.Context, f store.File) (store.File, error) {
	if err := s.f.tick(); err != nil {
		return store.File{}, err
	}
	return s.Store.AddFile(ctx, f)
}

func (s fusedStore) UpdateFile(ctx context.Context, f store.File) error {
	if err := s.f.tick(); err != nil {
		return err
	}
	return s.Store.UpdateFile(ctx, f)
}

func (m fusedMovies) Proposal(ctx context.Context, folder string) ([]arr.Row, error) {
	if err := m.f.tick(); err != nil {
		return nil, err
	}
	return m.FakeMovies.Proposal(ctx, folder)
}

func (m fusedMovies) Languages(ctx context.Context) ([]arr.Language, error) {
	if err := m.f.tick(); err != nil {
		return nil, err
	}
	return m.FakeMovies.Languages(ctx)
}

// The process dies after call number N into the store, the spool or Radarr, for every N
// from the merge's confirmation to done; a new process (the user confirming again if the
// job still waits for it) always ends in done, with one merge task run, one merged file,
// one ManualImport of it, and nothing left in the spool. Radarr finishes a command at once
// (slow 1), or only after it has been looked at a few times (slow 3).
func TestMergeCrashAtEveryCall(t *testing.T) {
	for _, slow := range []int{1, 3} {
		t.Run(fmt.Sprintf("slow=%d", slow), func(t *testing.T) { testMergeCrash(t, slow) })
	}
}

func testMergeCrash(t *testing.T, slow int) {
	running := []store.State{store.Importing, store.Imported, store.PostProcessing}
	for n := 0; ; n++ {
		x := newMergeEnv(t)
		x.slow = slow
		step := &recordingStep{name: "test"}
		x.eng.AddPostStep(step)
		j := x.readyJob()
		file := x.propose(j, 1)

		f := &fuse{left: n}
		dying := x.process(fusedStore{x.st, f}, fusedMovies{x.movies, f}, fusedQueue{x.q, f})
		if _, err := dying.Confirm(x.ctx, "user1", "", true, x.jobNow(j), mergeReq(nil)); err != nil && !f.blown() {
			t.Fatalf("after %d calls: confirm: %v", n, err)
		}
		for range 50 {
			if f.blown() || !slices.Contains(running, x.jobNow(j).State) {
				break
			}
			if _, err := x.step(dying, j); err != nil && !f.blown() {
				t.Fatalf("after %d calls: an uninterrupted step failed: %v", n, err)
			}
			x.worker(nil)
			x.appWorks()
			x.clock.advance(1)
		}
		finished := !f.blown()
		if s := x.jobNow(j).State; s == store.Failed || s == store.Stalled || s == store.Probed {
			t.Fatalf("after %d calls: the dying process left the job %s (%s)", n, s, x.jobNow(j).ErrorText)
		}

		// A new process takes over; the worker and Radarr have finished meanwhile.
		x.worker(nil)
		x.appWorks()
		if x.jobNow(j).State == store.AwaitingConfirmation {
			x.confirmMerge(j, nil)
		}
		j = x.run(x.eng, j, nil)
		if j.State != store.Done {
			t.Fatalf("after %d calls: job %s (%s %s)", n, j.State, j.ErrorCode, j.ErrorText)
		}
		if x.merges != 1 {
			t.Fatalf("after %d calls: %d merge tasks ran", n, x.merges)
		}
		sent := x.movieImports()
		if len(sent) != 1 || len(sent[0]) != 1 || sent[0][0].Path != file {
			t.Fatalf("after %d calls: ManualImports %+v", n, sent)
		}
		if r := sent[0][0]; r.Quality != webdl1080 || !slices.Equal(r.Languages, []arr.Language{english, latino}) {
			t.Fatalf("after %d calls: imported row %+v", n, r)
		}
		rows := x.rows(j)
		if len(rows) != 1 || rows[0].Result != store.ResultImported {
			t.Fatalf("after %d calls: rows %+v", n, rows)
		}
		files, _ := x.st.Files(x.ctx, j.ID)
		merged := 0
		for _, f := range files {
			if f.Path == "merged/1/Some Film (2001) WEBDL-1080p.mkv" {
				merged++
			}
		}
		if merged != 1 {
			t.Fatalf("after %d calls: %d merged file rows", n, merged)
		}
		if m := x.movies.Library[0]; m.Monitored || m.File.Size != mergedSize {
			t.Fatalf("after %d calls: film %+v", n, m)
		}
		steps, _ := x.st.PostSteps(x.ctx, j.ID)
		if len(steps) != 1 || steps[0].Status != store.StepOK || step.count() < 1 {
			t.Fatalf("after %d calls: post steps %+v", n, steps)
		}
		x.noMergeLeftovers(j)
		if finished {
			t.Logf("an uninterrupted run takes %d calls", n)
			if n < 30 {
				t.Fatalf("the merge finished after %d calls: the fuse does not see it", n)
			}
			return
		}
	}
}
