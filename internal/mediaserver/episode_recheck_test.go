package mediaserver_test

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/mediaserver"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// renv is the keeper's re-check of episodes' versions (#96): the step's version of S01E01 next
// to the episode's own file (both on disk), Sonarr's proposal of the season folder rejecting
// the version as not an upgrade, Jellyfin listing the two as one episode, and a clock.
type renv struct {
	*eenv
	k     *mediaserver.Keeper
	cfg   config.Config
	lib   string // the library root, which Sonarr sees as /tv
	own   string // the episode's file
	now   time.Time
	vapp  string // the version as Sonarr sees it
	locks *flows.JobLocks
	logs  *syncBuffer // the keeper's log
}

const (
	appSeason  = "/tv/Show/Season 01"
	appOwn     = appSeason + "/Show - S01E01 - Pilot WEBDL-1080p.mkv"
	notUpgrade = "Not an upgrade for existing episode file(s). Existing quality: WEBDL-1080p. New Quality WEBDL-1080p."
)

func newRecheckEnv(t *testing.T) *renv {
	t.Helper()
	x := &renv{eenv: newEpisodeStepEnv(t), now: time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)}
	x.lib = filepath.Dir(filepath.Dir(filepath.Dir(x.version)))
	x.own = filepath.Join(filepath.Dir(x.version), filepath.Base(appOwn))
	if err := os.WriteFile(x.own, []byte("episode"), 0o644); err != nil {
		t.Fatal(err)
	}
	x.vapp = appSeason + "/" + filepath.Base(x.version)
	x.cfg = x.step.Config
	x.cfg.Paths.LibraryRoots = []string{x.lib}
	x.server.Items, x.server.Pending = map[string]string{x.ms[0]: "ep-1", x.ms[1]: "ep-1"}, nil
	x.series.Info = arr.Status{AppName: "Sonarr", Version: "4.0.20"}
	x.series.Proposed = map[string][]arr.Row{appSeason: {
		{Path: appOwn, Size: 7, SeriesID: 11, EpisodeIDs: []int{101}, SeasonNumber: 1},
		{Path: x.vapp, Size: 5, SeriesID: 11, EpisodeIDs: []int{101}, SeasonNumber: 1},
	}}
	x.series.Verdicts = map[string][]arr.Rejection{x.vapp: {{Reason: notUpgrade, Permanent: true}}}
	x.locks, x.logs = flows.NewJobLocks(), &syncBuffer{}
	x.k = mediaserver.NewKeeper(x.cfg, slog.New(slog.NewTextHandler(x.logs, nil)), x.st, nil, x.server, x.locks)
	x.k.SetSeries(x.series)
	x.k.SetNow(func() time.Time { return x.now })
	return x
}

// syncBuffer is a log sink safe for concurrent use.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// pass runs one scheduled pass and returns the record of path (the version's, by default).
func (x *renv) pass(path ...string) store.Version {
	x.t.Helper()
	x.k.Pass(x.t.Context(), 0)
	p := x.version
	if len(path) > 0 {
		p = path[0]
	}
	rec, err := x.st.VersionByPath(x.t.Context(), p)
	if err != nil {
		x.t.Fatal(err)
	}
	return rec
}

func (x *renv) actions() []string {
	x.t.Helper()
	log, err := x.st.AuditLog(x.t.Context(), "j1")
	if err != nil {
		x.t.Fatal(err)
	}
	var out []string
	for _, e := range log {
		out = append(out, e.Action)
	}
	return out
}

func (x *renv) want(rec store.Version, status, detail string) {
	x.t.Helper()
	if rec.Status != status || !strings.Contains(rec.Detail, detail) {
		x.t.Fatalf("status %s (%s), want %s with %q", rec.Status, rec.Detail, status, detail)
	}
}

// adopt makes Sonarr track the version as S01E01's file, as its rescan does once the
// episode's own file is gone (probe B3-sonarr).
func (x *renv) adopt() {
	x.series.Eps[11][0].FileID = 602
	x.series.Files[11] = []arr.EpisodeFile{{ID: 602, Path: x.vapp}}
}

func notFound404() error {
	return &arr.Error{App: "sonarr", Method: "GET", Path: "/series/11", Status: 404}
}

// A version as the step left it is grouped on the first pass, after Sonarr's verdict asked of
// the file where it is; the verdict is asked again when the episode's file changes and every
// six intervals, not on every pass.
func TestRecheckGroupsAndAsksTheVerdictWhereTheVersionIs(t *testing.T) {
	x := newRecheckEnv(t)
	rec := x.pass()
	x.want(rec, store.VersionGrouped, "")
	if rec.PrimaryFileID != 601 || rec.PrimaryPath != x.own || rec.ItemID != "ep-1" || rec.GroupedAt.IsZero() || !rec.CheckedAt.Equal(x.now) {
		t.Fatalf("record %+v", rec)
	}
	if got := x.series.ProposalCalls(); !slices.Equal(got, []string{appSeason}) {
		t.Fatalf("proposals %v, want the season folder once", got)
	}
	if rc := x.series.RecheckCalls(); len(rc) != 1 || len(rc[0]) != 1 || rc[0][0].Path != x.vapp || !slices.Equal(rc[0][0].EpisodeIDs, []int{101}) {
		t.Fatalf("rechecks %+v", rc)
	}
	if got := x.actions(); !slices.Equal(got, []string{"version.grouped"}) {
		t.Errorf("audit %v", got)
	}
	x.now = x.now.Add(10 * time.Minute)
	x.pass()
	if n := len(x.series.ProposalCalls()); n != 1 {
		t.Errorf("the verdict was asked again on the next pass (%d proposals)", n)
	}
	// Sonarr upgrades the episode (a new file id): the verdict is asked against it.
	x.series.Eps[11][0].FileID = 603
	x.series.Files[11] = []arr.EpisodeFile{{ID: 603, Path: appOwn}}
	x.now = x.now.Add(10 * time.Minute)
	if rec = x.pass(); rec.Status != store.VersionGrouped || rec.PrimaryFileID != 603 || rec.PrimaryChangedAt.IsZero() {
		t.Fatalf("after the upgrade %+v", rec)
	}
	if n := len(x.series.ProposalCalls()); n != 2 {
		t.Errorf("%d proposals after the episode's file changed, want 2", n)
	}
	x.now = x.now.Add(time.Hour)
	x.pass()
	if n := len(x.series.ProposalCalls()); n != 3 {
		t.Errorf("%d proposals an hour later, want 3", n)
	}
	// versions.requireNotUpgradeVerdict off: never asked.
	cfg := x.cfg
	cfg.Versions.RequireNotUpgradeVerdict = false
	k := mediaserver.NewKeeper(cfg, nil, x.st, nil, x.server, nil)
	k.SetSeries(x.series)
	k.Pass(t.Context(), 0)
	if n := len(x.series.ProposalCalls()); n != 3 {
		t.Errorf("asked with versions.requireNotUpgradeVerdict off (%d proposals)", n)
	}
}

// The episode's own file is deleted and Sonarr's rescan adopts the version: the re-check says
// that the next rescan will take it, then that Sonarr took it. Nothing on disk changes.
func TestRecheckReportsTheAdoption(t *testing.T) {
	x := newRecheckEnv(t)
	x.pass()
	// The file is deleted outside Sonarr: Sonarr still lists it.
	if err := os.Remove(x.own); err != nil {
		t.Fatal(err)
	}
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(), store.VersionEpisodeFileMissing, "is not on disk: its next rescan drops it and takes the version")
	// Sonarr drops it (its rescan, or a delete in Sonarr): it lists no file for the episode.
	x.series.Eps[11][0].FileID = 0
	x.series.Files[11] = nil
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(), store.VersionEpisodeFileMissing, "Sonarr lists no file for S01E01")
	x.adopt()
	x.now = x.now.Add(10 * time.Minute)
	rec := x.pass()
	x.want(rec, store.VersionAdopted, "Sonarr tracks the version as the file of S01E01 (episode file 602)")
	if !strings.Contains(rec.Detail, "earlier file "+x.own+" is gone") {
		t.Errorf("detail %q", rec.Detail)
	}
	if got := x.actions(); !slices.Equal(got, []string{"version.grouped", "version.episode_file_missing", "version.adopted"}) {
		t.Errorf("audit %v", got)
	}
	log, _ := x.st.AuditLog(t.Context(), "j1")
	if d := log[2].Detail; d["episode"] != "S01E01" || d["previous"] != store.VersionEpisodeFileMissing || d["path"] != x.version || log[2].Actor != "system" {
		t.Errorf("audit detail %+v", log[2])
	}
	if _, err := os.Stat(x.version); err != nil {
		t.Fatalf("the version file: %v", err)
	}
	// adopted is checked again only every six intervals (at least an hour).
	calls := len(x.series.ProposalCalls())
	x.now = x.now.Add(30 * time.Minute)
	if again := x.pass(); !again.CheckedAt.Equal(rec.CheckedAt) {
		t.Errorf("an adopted version was checked after 30 minutes")
	}
	// Sonarr gets the episode's own file back (an import of it): the version is a second
	// version again.
	if err := os.WriteFile(x.own, []byte("episode"), 0o644); err != nil {
		t.Fatal(err)
	}
	x.series.Eps[11][0].FileID = 604
	x.series.Files[11] = []arr.EpisodeFile{{ID: 604, Path: appOwn}}
	x.now = x.now.Add(31 * time.Minute)
	x.want(x.pass(), store.VersionGrouped, "")
	if len(x.series.ProposalCalls()) != calls+1 {
		t.Errorf("the verdict was not asked again after the episode's file came back")
	}
	if got := x.actions(); got[len(got)-1] != "version.recovered" {
		t.Errorf("audit %v", got)
	}
}

// A version whose verdict changed (a worse file replaced the episode's, or the profile now
// prefers it) is adoptable: Sonarr's next rescan takes it. Its verdict is asked on every pass
// then, and it comes back once Sonarr rejects it again.
func TestRecheckReportsAVersionSonarrWouldTake(t *testing.T) {
	x := newRecheckEnv(t)
	x.pass()
	// A worse file replaces the episode's: the version is an upgrade for it now.
	x.series.Eps[11][0].FileID = 605
	x.series.Files[11] = []arr.EpisodeFile{{ID: 605, Path: appOwn}}
	x.series.Verdicts[x.vapp] = nil
	x.now = x.now.Add(10 * time.Minute)
	rec := x.pass()
	x.want(rec, store.VersionAdoptable, "it would take it for the episode's file on its next rescan")
	x.now = x.now.Add(10 * time.Minute)
	x.pass()
	if n := len(x.series.ProposalCalls()); n != 3 {
		t.Errorf("%d proposals, want one per pass while adoptable", n)
	}
	x.series.Verdicts[x.vapp] = []arr.Rejection{{Reason: notUpgrade}}
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(), store.VersionGrouped, "")
	if got := x.actions(); !slices.Equal(got, []string{"version.grouped", "version.adoptable", "version.recovered"}) {
		t.Errorf("audit %v", got)
	}
	// A failed verdict call decides nothing.
	x.series.Verdicts[x.vapp] = nil
	x.series.Err = map[string]error{"Proposal": errors.New("connection refused")}
	x.series.Eps[11][0].FileID = 606
	x.series.Files[11] = []arr.EpisodeFile{{ID: 606, Path: appOwn}}
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(), store.VersionGrouped, "connection refused")
}

// The series deleted in Sonarr: series_deleted only when Sonarr's status call answers too,
// and never while a sentinel is missing; the file is left alone, and the version comes back
// with the series.
func TestRecheckReportsADeletedSeries(t *testing.T) {
	x := newRecheckEnv(t)
	x.pass()
	x.series.Err = map[string]error{"Series": notFound404(), "Status": notFound404()}
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(), store.VersionGrouped, "its status call fails too")
	sentinel := filepath.Join(t.TempDir(), "mounted")
	x.cfg.Paths.Sentinels = []string{sentinel}
	k := mediaserver.NewKeeper(x.cfg, nil, x.st, nil, x.server, nil)
	k.SetSeries(x.series)
	delete(x.series.Err, "Status")
	k.Pass(t.Context(), 0)
	rec, _ := x.st.VersionByPath(t.Context(), x.version)
	x.want(rec, store.VersionGrouped, "not checked: "+sentinel+" is missing")

	rec = x.pass()
	x.want(rec, store.VersionSeriesDeleted, "sonarr answers 404 for series 11")
	if !strings.Contains(rec.Detail, "left on disk at "+x.version) {
		t.Errorf("detail %q", rec.Detail)
	}
	if _, err := os.Stat(x.version); err != nil {
		t.Fatalf("the version file: %v", err)
	}
	// Checked again after six intervals: back with the series.
	x.series.Err = nil
	x.now = x.now.Add(30 * time.Minute)
	if again := x.pass(); again.Status != store.VersionSeriesDeleted {
		t.Fatalf("checked again after 30 minutes: %+v", again)
	}
	x.now = x.now.Add(31 * time.Minute)
	x.want(x.pass(), store.VersionGrouped, "")
	// Sonarr gives the episode a new id (its metadata rebuilt): the record follows it by its
	// season and number.
	x.series.Eps[11] = []arr.Episode{{ID: 999, SeasonNumber: 1, Number: 1, FileID: 601}}
	x.now = x.now.Add(10 * time.Minute)
	if rec = x.pass(); rec.Status != store.VersionGrouped || rec.EpisodeID != 999 {
		t.Fatalf("after the episode's new id: %+v", rec)
	}
	// The series answers without the episode at all (removed upstream).
	x.series.Eps[11] = []arr.Episode{{ID: 998, SeasonNumber: 1, Number: 7, FileID: 601}}
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(), store.VersionSeriesDeleted, "has no episode 999, nor one S01E01")
	if got := x.actions(); !slices.Equal(got, []string{"version.grouped", "version.series_deleted", "version.recovered", "version.reattached", "version.series_deleted"}) {
		t.Errorf("audit %v", got)
	}
	// Sonarr not answering at all decides nothing either.
	x.series.Err = map[string]error{"Series": errors.New("connection refused")}
	x.now = x.now.Add(time.Hour)
	x.want(x.pass(), store.VersionSeriesDeleted, "connection refused")
}

// Sonarr moves the series with its files, the version with them (it moves the whole folder):
// the record follows the version, no file is touched. Moved without its files, the version is
// left behind: series_moved.
func TestRecheckFollowsAMovedSeries(t *testing.T) {
	x := newRecheckEnv(t)
	x.pass()
	if err := os.Rename(filepath.Join(x.lib, "Show"), filepath.Join(x.lib, "Show (2020)")); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(x.lib, "Show (2020)", "Season 01", filepath.Base(x.version))
	x.series.Library[0].Path = "/tv/Show (2020)"
	x.series.Files[11] = []arr.EpisodeFile{{ID: 601, Path: "/tv/Show (2020)/Season 01/" + filepath.Base(appOwn)}}
	x.server.Pending = map[string]string{
		"/media/tv/Show (2020)/Season 01/" + filepath.Base(appOwn): "ep-2", "/media/tv/Show (2020)/Season 01/" + filepath.Base(x.version): "ep-2"}
	x.now = x.now.Add(10 * time.Minute)
	rec := x.pass(moved)
	x.want(rec, store.VersionGrouped, "")
	if rec.ItemID != "ep-2" || len(x.server.Refreshed) != 1 {
		t.Fatalf("record %+v, refreshed %v", rec, x.server.Refreshed)
	}
	if _, err := x.st.VersionByPath(t.Context(), x.version); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the old path: %v", err)
	}
	log, _ := x.st.AuditLog(t.Context(), "j1")
	if len(log) != 2 || log[1].Action != "version.followed" || log[1].Detail["previous"] != x.version || log[1].Detail["path"] != moved {
		t.Fatalf("audit %+v", log)
	}

	// Moved again, without the files this time: the version stays behind.
	other := filepath.Join(x.lib, "Elsewhere", "Show (2020)")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	x.series.Library[0].Path = "/tv/Elsewhere/Show (2020)"
	x.now = x.now.Add(10 * time.Minute)
	x.want(x.pass(moved), store.VersionSeriesMoved, "the version stays at "+moved)
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("the version file: %v", err)
	}
	// Sonarr's folder for the series cannot be read (a mapping or a mount): nothing decided.
	x.series.Library[0].Path = "/tv/Nowhere/Show (2020)"
	x.now = x.now.Add(time.Hour)
	x.want(x.pass(moved), store.VersionSeriesMoved, "not checked: Sonarr's folder for the series")
}

// The checks of a job still running are its step's; a placing record whose job ended is
// settled and checked; a version the server does not list backs off, one refresh per pass.
func TestRecheckLeavesRunningJobsAndBacksOff(t *testing.T) {
	x := newRecheckEnv(t)
	j, err := x.st.CreateJob(t.Context(), "user1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storetest.Drive(t.Context(), x.st, j.ID, store.PostProcessing); err != nil {
		t.Fatal(err)
	}
	rec, _ := x.st.VersionByPath(t.Context(), x.version)
	rec.JobID = j.ID
	if _, err := x.st.PutVersion(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	if got := x.pass(); got.Status != store.VersionLinked || !got.CheckedAt.IsZero() {
		t.Fatalf("a running job's version was checked: %+v", got)
	}
	if _, err := storetest.Drive(t.Context(), x.st, j.ID, store.Done); err != nil {
		t.Fatal(err)
	}
	// The server does not list the version.
	x.server.SetItem(x.ms[1], "")
	x.want(x.pass(), store.VersionWaiting, "does not list "+x.ms[1])
	if len(x.server.Refreshed) != 1 {
		t.Fatalf("refreshed %v", x.server.Refreshed)
	}
	// As for films: tried on the next pass, then after 1, 3, 7… skipped passes.
	for i, want := range []int{2, 2, 3} {
		x.now = x.now.Add(10 * time.Minute)
		x.pass()
		if len(x.server.Refreshed) != want {
			t.Errorf("pass %d: %d refreshes, want %d", i+2, len(x.server.Refreshed), want)
		}
	}
	x.now = x.now.Add(40 * time.Minute)
	x.server.Pending = map[string]string{x.ms[1]: "ep-1"}
	x.want(x.pass(), store.VersionGrouped, "")
}

// Without Sonarr, or outside paths.libraryRoots, nothing is decided.
func TestRecheckDecidesNothingWithoutSonarrOrTheLibrary(t *testing.T) {
	x := newRecheckEnv(t)
	k := mediaserver.NewKeeper(x.cfg, nil, x.st, nil, x.server, nil)
	k.Pass(t.Context(), 0)
	rec, _ := x.st.VersionByPath(t.Context(), x.version)
	x.want(rec, store.VersionLinked, "sonarr is not configured")
	cfg := x.cfg
	cfg.Paths.LibraryRoots = []string{filepath.Join(x.lib, "other")}
	k = mediaserver.NewKeeper(cfg, nil, x.st, nil, x.server, nil)
	k.SetSeries(x.series)
	k.Pass(t.Context(), 0)
	rec, _ = x.st.VersionByPath(t.Context(), x.version)
	x.want(rec, store.VersionLinked, "is not inside paths.libraryRoots")
	// The version file itself gone, with everything else there: missing.
	if err := os.Remove(x.version); err != nil {
		t.Fatal(err)
	}
	x.want(x.pass(), store.VersionMissing, "no longer on disk")
}
