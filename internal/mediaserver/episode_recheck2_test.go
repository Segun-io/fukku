package mediaserver_test

import (
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// The review's follow-ups of the re-check (#96).

// A job stalled while importing or post-processing resumes into its step: its versions are
// the step's still, not the keeper's.
func TestRecheckLeavesStalledJobs(t *testing.T) {
	x := newRecheckEnv(t)
	j, err := x.st.CreateJob(t.Context(), "user1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storetest.Drive(t.Context(), x.st, j.ID, store.PostProcessing); err != nil {
		t.Fatal(err)
	}
	stalled, err := storetest.Drive(t.Context(), x.st, j.ID, store.Stalled)
	if err != nil || stalled.Resume != store.PostProcessing {
		t.Fatalf("stalled %+v, %v", stalled, err)
	}
	rec, _ := x.st.VersionByPath(t.Context(), x.version)
	rec.JobID = j.ID
	if _, err := x.st.PutVersion(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	if got := x.pass(); got.Status != store.VersionLinked || !got.CheckedAt.IsZero() {
		t.Fatalf("a stalled post-processing job's version was checked: %+v", got)
	}
}

// The job's step writes under the version's lock, onto the record as it is then: it never
// undoes a status of the keeper's re-check, nor writes a record that followed its version.
func TestEpisodeStepLeavesTheRecheckAlone(t *testing.T) {
	x := newRecheckEnv(t)
	x.step.Locks = x.locks
	rec, _ := x.st.VersionByPath(t.Context(), x.version)
	rec.Status, rec.Detail = store.VersionAdopted, "Sonarr tracks the version"
	if _, err := x.st.PutVersion(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.run(); err != nil && !strings.Contains(err.Error(), "not grouped") {
		t.Fatalf("run: %v", err)
	}
	if got := x.record(); got.Status != store.VersionAdopted || got.Detail != "Sonarr tracks the version" {
		t.Fatalf("the step undid the re-check: %+v", got)
	}
	// The step waits for the version's lock, held by the keeper's check.
	rec.Status, rec.Detail = store.VersionLinked, ""
	if _, err := x.st.PutVersion(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	unlock := x.locks.Lock(flows.VersionLock(x.version))
	ran := make(chan struct{})
	go func() { _, _, _ = x.run(); close(ran) }()
	select {
	case <-ran:
		t.Fatal("the step wrote while the version's lock was held")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	<-ran
	if got := x.record(); got.Status != store.VersionGrouped {
		t.Fatalf("after the lock: %+v", got)
	}
}

// Sonarr's verdict is asked outside the version's lock; a failed verdict lets the check go on
// (grouped, the detail says why) and backs off by season folder, once per pass.
func TestRecheckVerdictFailuresBackOff(t *testing.T) {
	x := newRecheckEnv(t)
	var held atomic.Int32
	x.series.OnProposal = func(string) {
		got := make(chan struct{})
		go func() { x.locks.Lock(flows.VersionLock(x.version))(); close(got) }()
		select {
		case <-got:
		case <-time.After(time.Second):
			held.Add(1)
		}
	}
	x.want(x.pass(), store.VersionGrouped, "")
	if held.Load() != 0 {
		t.Fatal("the verdict was asked while the version's lock was held")
	}
	x.series.Err = map[string]error{"Proposal": errors.New("timeout")}
	x.series.Eps[11][0].FileID = 610 // a new file: the verdict is due
	x.series.Files[11] = []arr.EpisodeFile{{ID: 610, Path: appOwn}}
	for i, want := range []struct {
		calls  int
		detail string
	}{{2, "could not be asked (asked again later): timeout"}, {3, "could not be asked"}, {3, "failed lately"}, {4, "could not be asked"}} {
		x.now = x.now.Add(10 * time.Minute)
		rec := x.pass()
		x.want(rec, store.VersionGrouped, want.detail)
		if n := len(x.series.ProposalCalls()); n != want.calls {
			t.Errorf("pass %d: %d proposals, want %d", i+2, n, want.calls)
		}
	}
	if n := strings.Count(x.logs.String(), "msg=version_verdict_failed"); n != 3 {
		t.Errorf("%d verdict failures logged, want one per attempt", n)
	}
	x.series.Err = nil
	x.now = x.now.Add(time.Hour)
	x.want(x.pass(), store.VersionGrouped, "")
	if strings.Contains(x.record().Detail, "verdict") {
		t.Errorf("detail %q", x.record().Detail)
	}
}

// An adoptable version stays adoptable while no verdict rejects it again: Sonarr giving none,
// or its call failing, writes no version.recovered.
func TestRecheckKeepsAdoptableWithoutAVerdict(t *testing.T) {
	x := newRecheckEnv(t)
	x.pass()
	x.series.Verdicts[x.vapp] = nil
	x.series.Eps[11][0].FileID = 611
	x.series.Files[11] = []arr.EpisodeFile{{ID: 611, Path: appOwn}}
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(), store.VersionAdoptable, "would take it")
	// Sonarr does not offer the version any more: no verdict.
	offered := x.series.Proposed[appSeason]
	x.series.Proposed[appSeason] = offered[:1]
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(), store.VersionAdoptable, "would take it")
	// Its call fails.
	x.series.Proposed[appSeason] = offered
	x.series.Err = map[string]error{"Proposal": errors.New("timeout")}
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(), store.VersionAdoptable, "would take it")
	if got := x.actions(); !slices.Equal(got, []string{"version.grouped", "version.adoptable"}) {
		t.Errorf("audit %v", got)
	}
}

// The series' identity is its TVDB id: a record learns it, a series id that answers with
// another show is treated like a 404, and the show is found again by its TVDB id under a new
// series id, with new episode ids (Sonarr rebuilt, or the show deleted and added back).
func TestRecheckFollowsTheShowByItsTVDBID(t *testing.T) {
	x := newRecheckEnv(t)
	x.series.Library[0].TVDBID = 276116
	if rec := x.pass(); rec.TVDBID != 276116 || rec.Status != store.VersionGrouped {
		t.Fatalf("record %+v", rec)
	}
	// Sonarr's series 11 is another show now; the show is series 12, episode 501.
	x.series.Library = []arr.Series{{ID: 11, TVDBID: 1, Title: "Other", Path: "/tv/Other"}, {ID: 12, TVDBID: 276116, Title: "Show", Path: "/tv/Show"}}
	x.series.Eps[12] = []arr.Episode{{ID: 501, SeasonNumber: 1, Number: 1, FileID: 701}}
	x.series.Files[12] = []arr.EpisodeFile{{ID: 701, Path: appOwn}}
	x.series.Eps[11], x.series.Files[11] = nil, nil
	x.now = x.now.Add(10 * time.Minute)
	rec := x.pass()
	x.want(rec, store.VersionGrouped, "")
	if rec.TitleID != 12 || rec.EpisodeID != 501 || rec.PrimaryFileID != 701 {
		t.Fatalf("record %+v", rec)
	}
	log, _ := x.st.AuditLog(t.Context(), "j1")
	var reattached []map[string]string
	for _, e := range log {
		if e.Action == "version.reattached" {
			reattached = append(reattached, e.Detail)
		}
	}
	if len(reattached) != 2 || reattached[0]["previousTitleId"] != "11" || reattached[0]["tvdbId"] != "276116" || reattached[1]["previousEpisodeId"] != "101" {
		t.Fatalf("reattached %+v", reattached)
	}
	// The show is gone from Sonarr: series_deleted, with why.
	x.series.Library = x.series.Library[:1]
	x.series.Err = map[string]error{"Series": notFound404()}
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(), store.VersionSeriesDeleted, "sonarr answers 404 for series 12")
	// Series 12 answers with another show, and no series has the version's TVDB id.
	x.series.Err = nil
	x.series.Library = []arr.Series{{ID: 12, TVDBID: 2, Title: "Another", Path: "/tv/Show"}}
	x.now = x.now.Add(time.Hour)
	x.want(x.pass(), store.VersionSeriesDeleted, "is another show now (TVDB 2, the version's is TVDB 276116)")
}

// While Sonarr is down, version_check_failed is logged once, not per record and pass, and
// version_check_recovered once it answers.
func TestRecheckLogsASonarrOutageOnce(t *testing.T) {
	x := newRecheckEnv(t)
	second := x.version[:len(x.version)-len("S01E01 - Español.mkv")] + "S01E02 - Español.mkv"
	if _, err := x.st.PutVersion(t.Context(), store.Version{JobID: "j1", App: "sonarr", TitleID: 11, Path: second,
		Status: store.VersionGrouped, EpisodeID: 102, SeasonNumber: 1, EpisodeNumber: 2}); err != nil {
		t.Fatal(err)
	}
	x.series.Err = map[string]error{"Series": errors.New("connection refused")}
	for range 3 {
		x.now = x.now.Add(10 * time.Minute)
		x.want(x.pass(), store.VersionLinked, "connection refused")
	}
	if n := strings.Count(x.logs.String(), "msg=version_check_failed"); n != 1 {
		t.Fatalf("%d version_check_failed lines, want 1:\n%s", n, x.logs)
	}
	x.series.Err = nil
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(), store.VersionGrouped, "")
	if n := strings.Count(x.logs.String(), "msg=version_check_recovered"); n != 1 {
		t.Errorf("%d version_check_recovered lines, want 1", n)
	}
}

// A film nudged during a long pass is checked within it, not after it.
func TestRecheckServesNudgesDuringAPass(t *testing.T) {
	x := newRecheckEnv(t)
	x.k.Nudge(store.Target{App: "radarr", TitleID: 7})
	if x.k.PendingNudges() != 1 {
		t.Fatal("no nudge pending")
	}
	x.pass()
	if n := x.k.PendingNudges(); n != 0 {
		t.Errorf("%d nudges left after the pass", n)
	}
}
