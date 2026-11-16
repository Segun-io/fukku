package flows

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/store"
)

// rematch runs a step of the match flow while the app re-checks: the user goes back and
// chooses another show, as Matcher.Unmatch and Matcher.Match write it.
type rematch struct {
	*arrtest.FakeSeries
	e    *env
	m    *Matcher
	job  string
	done bool
}

func (r *rematch) Recheck(ctx context.Context, rows []arr.Row) ([]arr.Row, error) {
	if !r.done {
		r.done = true
		j, err := r.e.st.Job(ctx, r.job)
		if err != nil {
			return nil, err
		}
		if _, err := r.m.Unmatch(ctx, "user1", j); err != nil {
			return nil, err
		}
		// Match's own write, under the shared lock.
		unlock := r.e.p.Lock(r.job)
		_, err = r.e.st.ReplaceTarget(ctx, r.job, store.Probed, store.Matched, &store.Target{App: AppSonarr, TitleID: otherShow, Title: "Other"}, store.Change{Actor: "user1"})
		unlock()
		if err != nil {
			return nil, err
		}
	}
	return r.FakeSeries.Recheck(ctx, rows)
}

// A re-match between the step's read of the title and its write: the step stores nothing,
// and its next pass makes the rows of the new title.
func TestRematchRacingTheStep(t *testing.T) {
	e := newEnv(t)
	e.series.Eps[otherShow] = showEpisodes()
	e.cfg.Sonarr.URL = "http://sonarr.example.com:8989"
	locks := NewJobLocks()
	j := e.job(AppSonarr, showID, vid("S01E01.mkv", epRuntime))
	e.series.Proposed[e.folder(j)] = []arr.Row{e.item(j, "S01E01.mkv", webdl1080)}
	r := &rematch{FakeSeries: e.series, e: e, job: j.ID}
	r.m = NewMatcher(e.cfg, e.st, nil, e.series, locks, nil)
	e.p = NewProposals(e.cfg, nil, e.st, nil, r, locks)

	j = e.matched(j)
	if j.State != store.Matched || len(e.rows(j)) != 0 {
		t.Fatalf("after a re-match during the step: %s, rows %+v", j.State, e.rows(j))
	}
	if tg, _ := e.st.Target(e.ctx, j.ID); tg.TitleID != otherShow {
		t.Fatalf("target %+v", tg)
	}
	j = e.matched(j)
	if j.State != store.AwaitingConfirmation {
		t.Fatalf("the next pass: %s", j.State)
	}
	var ar arr.Row
	if err := json.Unmarshal(e.rows(j)[0].Proposal, &ar); err != nil || ar.SeriesID != otherShow {
		t.Errorf("rows of series %d, want %d: %v", ar.SeriesID, otherShow, err)
	}
}

// The step and re-matches at the same time, many times: whenever the job waits for
// confirmation, its rows are for its current title.
func TestRematchRaceKeepsRowsForTheTitle(t *testing.T) {
	for i := range 50 {
		e := newEnv(t)
		e.series.Eps[otherShow] = showEpisodes()
		j := e.job(AppSonarr, showID, vid("S01E01.mkv", epRuntime))
		e.series.Proposed[e.folder(j)] = []arr.Row{e.item(j, "S01E01.mkv", webdl1080)}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 3 {
				_ = e.p.Matched(e.ctx, e.jobNow(j))
			}
		}()
		go func() {
			defer wg.Done()
			unlock := e.p.Lock(j.ID)
			defer unlock()
			cur, err := e.st.Job(e.ctx, j.ID)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = e.st.ReplaceTarget(e.ctx, j.ID, cur.State, store.Matched, &store.Target{App: AppSonarr, TitleID: otherShow}, store.Change{Actor: "user1"})
		}()
		wg.Wait()
		j = e.jobNow(j)
		tg, err := e.st.Target(e.ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range e.rows(j) {
			var ar arr.Row
			_ = json.Unmarshal(r.Proposal, &ar)
			if ar.SeriesID != tg.TitleID {
				t.Fatalf("round %d: job %s with title %d has a row for %d", i, j.State, tg.TitleID, ar.SeriesID)
			}
		}
		if j.State == store.AwaitingConfirmation && len(e.rows(j)) != 1 {
			t.Fatalf("round %d: awaiting confirmation with rows %+v", i, e.rows(j))
		}
	}
}

// otherTitle answers re-checks for another show than the one asked for.
type otherTitle struct{ *arrtest.FakeSeries }

func (o otherTitle) Recheck(ctx context.Context, rows []arr.Row) ([]arr.Row, error) {
	out, err := o.FakeSeries.Recheck(ctx, rows)
	for i := range out {
		out[i].SeriesID = otherShow
	}
	return out, err
}

// Rows naming another title than the job's are never stored.
func TestRowsForAnotherTitleAreRefused(t *testing.T) {
	e := newEnv(t)
	j := e.job(AppSonarr, showID, vid("S01E01.mkv", epRuntime))
	e.series.Proposed[e.folder(j)] = []arr.Row{e.item(j, "S01E01.mkv", webdl1080)}
	e.p = NewProposals(e.cfg, nil, e.st, nil, otherTitle{e.series}, nil)
	j = e.matched(j)
	if j.State != store.Failed || j.ErrorCode != CodeProposalFailed || len(e.rows(j)) != 0 {
		t.Fatalf("job %s %s %s, rows %d", j.State, j.ErrorCode, j.ErrorText, len(e.rows(j)))
	}

	e2 := newEnv(t)
	j2, p := seasonJob(e2, 1)
	before := string(e2.rows(j2)[0].Proposal)
	e2.p = NewProposals(e2.cfg, nil, e2.st, nil, otherTitle{e2.series}, nil)
	if _, err := e2.p.EditRow(e2.ctx, j2, p.Rows[0].ID, Edit{EpisodeIDs: &[]int{102}}); Code(err) != CodeArrUnavailable {
		t.Errorf("edit answered for another title: %v", err)
	}
	if after := string(e2.rows(j2)[0].Proposal); after != before {
		t.Errorf("stored %s", after)
	}
}
