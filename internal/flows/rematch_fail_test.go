package flows

import (
	"context"
	"fmt"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/store"
)

// matchDuringRecheck chooses another film through the real Matcher.Match while the step's
// re-check is with the app, then answers the re-check as mode says.
type matchDuringRecheck struct {
	*arrtest.FakeMovies
	m    *Matcher
	st   store.Store
	job  string
	mode string
}

const newFilm = 8

func (r *matchDuringRecheck) Recheck(ctx context.Context, rows []arr.Row) ([]arr.Row, error) {
	j, err := r.st.Job(ctx, r.job)
	if err != nil {
		return nil, err
	}
	if _, err := r.m.Match(ctx, "user1", j, MatchRequest{App: AppRadarr, TMDBID: 608}); err != nil {
		return nil, fmt.Errorf("match: %w", err)
	}
	switch r.mode {
	case "refused":
		return nil, &arr.Error{App: "radarr", Method: "POST", Path: "/manualimport", Status: 400, Message: "bad movie 7"}
	case "unreachable":
		return nil, fmt.Errorf("radarr: POST /manualimport: %w: connection refused", arr.ErrUnreachable)
	}
	out, err := r.FakeMovies.Recheck(ctx, rows)
	for i := range out {
		switch r.mode {
		case "another title":
			out[i].MovieID = 99
		case "another file":
			out[i].Path = "/elsewhere/secret.mkv"
		}
	}
	return out, err
}

// Stalls under an earlier title do not count against a title chosen after them.
func TestStallsCountPerTitle(t *testing.T) {
	e := newEnv(t)
	j := e.job(AppSonarr, showID, vid("S01E01.mkv", epRuntime))
	e.series.Err = map[string]error{"Proposal": errRefused}
	for range 2 {
		if j = e.matched(j); j.State != store.Stalled {
			t.Fatalf("job %s", j.State)
		}
		if _, err := e.st.Transition(e.ctx, j.ID, store.Stalled, store.Matched, store.Change{Actor: "system"}); err != nil {
			t.Fatal(err)
		}
	}
	tg, _ := e.st.Target(e.ctx, j.ID)
	if n, _ := e.p.stalls(e.ctx, j.ID, store.Matched, tg.MatchedAt); n != 2 {
		t.Fatalf("%d stalls, want 2", n)
	}
	// A re-match of a matched job stays matched and records no event; the count restarts.
	if _, err := e.st.ReplaceTarget(e.ctx, j.ID, store.Matched, store.Matched, &store.Target{App: AppSonarr, TitleID: otherShow}, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	tg, _ = e.st.Target(e.ctx, j.ID)
	if n, _ := e.p.stalls(e.ctx, j.ID, store.Matched, tg.MatchedAt); n != 0 {
		t.Errorf("%d stalls counted against the new title", n)
	}
}

// A step that fails (or stalls) for a title the user has just replaced by another leaves
// the job alone: matched to the new title, no error, no stall counted against it. The next
// pass makes the new title's proposal.
func TestAStepFailingForTheOldTitleDoesNotTouchTheNewOne(t *testing.T) {
	for _, mode := range []string{"refused", "unreachable", "another title", "another file"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			e.cfg.Radarr.URL = "http://radarr.example.com:7878"
			e.movies.Results = []arr.Movie{{ID: newFilm, TMDBID: 608, Title: "New Film", Year: 2008}}
			e.movies.Library = append(e.movies.Library, arr.Movie{ID: newFilm, TMDBID: 608, Title: "New Film", Year: 2008})
			locks := NewJobLocks()
			j := e.job(AppRadarr, filmID, vid("Film.mkv", 100*minute))
			e.movies.Proposed[e.folder(j)] = []arr.Row{e.item(j, "Film.mkv", webdl1080)}
			r := &matchDuringRecheck{FakeMovies: e.movies, st: e.st, job: j.ID, mode: mode}
			r.m = NewMatcher(e.cfg, e.st, r, nil, locks, nil)
			e.p = NewProposals(e.cfg, nil, e.st, r, nil, locks)
			events := 0

			j = e.matched(j)
			tg, err := e.st.Target(e.ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			if j.State != store.Matched || j.ErrorCode != "" || tg.TitleID != newFilm || len(e.rows(j)) != 0 {
				t.Fatalf("job %s (%s %s), target %d, rows %d", j.State, j.ErrorCode, j.ErrorText, tg.TitleID, len(e.rows(j)))
			}
			h, _ := e.st.History(e.ctx, j.ID)
			for _, ev := range h {
				if ev.To == store.Failed || ev.To == store.Stalled {
					events++
				}
			}
			if events != 0 {
				t.Errorf("history %+v", h)
			}
			if n, _ := e.p.stalls(e.ctx, j.ID, store.Matched, tg.MatchedAt); n != 0 {
				t.Errorf("%d stalls counted against the new title", n)
			}

			// The next pass, with an app that answers, proposes for the new film.
			e.p = NewProposals(e.cfg, nil, e.st, e.movies, nil, locks)
			p := e.ready(j)
			if len(p.Rows) != 1 || !p.Rows[0].Importable || p.Target.TitleID != newFilm {
				t.Errorf("next pass: %+v", p)
			}
		})
	}
}
