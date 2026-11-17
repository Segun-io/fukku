package mediaserver_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/mediaserver"
	"github.com/segun-io/fukku/internal/store"
)

// Fixes from the second review of #89.

func (x *kenv) setTMDB(id int) {
	x.t.Helper()
	rec, err := x.st.VersionByPath(x.t.Context(), x.version)
	if err != nil {
		x.t.Fatal(err)
	}
	rec.TMDBID = id
	if _, err := x.st.PutVersion(x.t.Context(), rec); err != nil {
		x.t.Fatal(err)
	}
}

// Radarr's id 7 is another film now (a restored or rebuilt Radarr, another instance): the
// version is never grouped with it. It follows its own film by TMDB id when Radarr has it,
// and is film_deleted when not.
func TestKeeperRefusesAnotherFilmUnderTheSameID(t *testing.T) {
	x := newKeeperEnv(t)
	x.setTMDB(605)
	x.movies.Library[0].TMDBID = 999 // id 7 is another film
	if rec := x.check(false); rec.Status != store.VersionFilmDeleted || !strings.Contains(rec.Detail, "another film") {
		t.Fatalf("record %+v", rec)
	}
	if len(x.server.MergeCalls()) != 0 {
		t.Errorf("merged with another film: %v", x.server.MergeCalls())
	}

	y := newKeeperEnv(t)
	y.setTMDB(605)
	y.movies.Library[0].TMDBID = 999
	y.movies.Library = append(y.movies.Library, arr.Movie{ID: 70, TMDBID: 605, File: &arr.MovieFile{ID: 9, Path: appFile}})
	rec := y.check(false)
	if rec.Status != store.VersionGrouped || rec.TitleID != 70 || rec.PrimaryFileID != 9 {
		t.Fatalf("record %+v", rec)
	}
	if got := y.actions(); !slices.Equal(got, []string{"version.reattached", "version.grouped"}) {
		t.Errorf("audit %v", got)
	}

	// Radarr does not answer its status call: nothing is decided.
	z := newKeeperEnv(t)
	z.setTMDB(605)
	z.movies.Library[0].TMDBID = 999
	z.movies.Err = map[string]error{"Status": notFound()}
	if rec, err := z.k.Check(t.Context(), z.version, 0, false); err == nil || rec.Status != store.VersionLinked {
		t.Fatalf("record %+v, %v", rec, err)
	}
}

// A record without a TMDB id learns it from Radarr's answer.
func TestKeeperLearnsTheTMDBID(t *testing.T) {
	x := newKeeperEnv(t)
	x.movies.Library[0].TMDBID = 605
	if rec := x.check(false); rec.TMDBID != 605 || rec.Status != store.VersionGrouped {
		t.Fatalf("record %+v", rec)
	}
	if rec, _ := x.st.VersionByPath(t.Context(), x.version); rec.TMDBID != 605 {
		t.Errorf("stored %+v", rec)
	}
}

// pairEnv is a keeper over two versions of films 7 and 8, with a clock of its own.
type pairEnv struct {
	*kenv
	other string
	now   time.Time
}

func newPairEnv(t *testing.T, unlisted bool) *pairEnv {
	x := &pairEnv{kenv: newKeeperEnv(t), now: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	x.other = filepath.Join(filepath.Dir(filepath.Dir(x.version)), "Other (2021)", "Other (2021) - Español.mkv")
	if err := os.MkdirAll(filepath.Dir(x.other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(x.other, []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := x.st.PutVersion(t.Context(), store.Version{JobID: "j2", App: "radarr", TitleID: 8, Path: x.other, Status: store.VersionLinked}); err != nil {
		t.Fatal(err)
	}
	x.movies.Library = append(x.movies.Library, arr.Movie{ID: 8, File: &arr.MovieFile{ID: 3, Path: "/movies/Other (2021)/Other (2021).mkv"}})
	x.server.Pending["/media/movies/Other (2021)/Other (2021).mkv"] = "film-8"
	x.server.Pending[x.other] = "ver-8"
	if unlisted {
		x.server.Unlisted = map[string]bool{x.other: true}
	}
	cfg := stepConfig()
	cfg.Versions.RegroupInterval = config.Duration(10 * time.Minute)
	x.k = mediaserver.NewKeeper(cfg, nil, x.st, x.movies, x.server, nil)
	x.k.SetNow(func() time.Time { return x.now })
	return x
}

func (x *pairEnv) status(path string) string {
	rec, _ := x.st.VersionByPath(x.t.Context(), path)
	return rec.Status
}

// One path in no library of the server does not hold up the others: they are refreshed,
// waited for and grouped in the same pass.
func TestKeeperPassGoesOnPastAnUnlistedPath(t *testing.T) {
	x := newPairEnv(t, true)
	x.k.Pass(t.Context(), 0)
	if got := x.status(x.version); got != store.VersionGrouped {
		t.Errorf("the listed version is %s", got)
	}
	if got := x.status(x.other); got != store.VersionWaiting {
		t.Errorf("the unlisted version is %s", got)
	}
	if len(x.server.Refreshed) != 1 {
		t.Errorf("refreshes %v", x.server.Refreshed)
	}
}

// A version the server still does not list after a pass backs off: it is tried in passes
// 1, 2, 4, 8… after the first, not in every pass; a nudge tries it at once.
func TestKeeperBacksOffAPathThatStaysMissing(t *testing.T) {
	x := newPairEnv(t, true)
	var tried []int
	for pass := 1; pass <= 9; pass++ {
		before := len(x.server.FindCalls)
		x.k.Pass(t.Context(), 0)
		for _, p := range x.server.FindCalls[before:] {
			if p == x.other {
				tried = append(tried, pass)
				break
			}
		}
		x.now = x.now.Add(10 * time.Minute)
	}
	if want := []int{1, 2, 4, 8}; !slices.Equal(tried, want) {
		t.Errorf("tried in passes %v, want %v", tried, want)
	}
	before := len(x.server.FindCalls)
	x.k.Pass(t.Context(), 8) // a nudge for the film
	if !slices.Contains(x.server.FindCalls[before:], x.other) {
		t.Error("a nudge did not try the version")
	}
}

// A placing record of a job that ended is settled: linked and grouped when the file is the
// job's upload, removed otherwise; one of a job still going is left to it.
func TestKeeperSettlesPlacingRecords(t *testing.T) {
	x := newKeeperEnv(t)
	ctx := t.Context()
	rec, _ := x.st.VersionByPath(ctx, x.version)
	rec.Status, rec.JobID = store.VersionPlacing, "0123456789abcdef0123456789abcdef" // no such job: it ended long ago
	if _, err := x.st.PutVersion(ctx, rec); err != nil {
		t.Fatal(err)
	}
	x.k.Pass(ctx, 0)
	if _, err := x.st.VersionByPath(ctx, x.version); err == nil {
		t.Fatal("the record of a gone job, whose upload cannot be compared, is still there")
	}
	if _, err := os.Stat(x.version); err != nil {
		t.Errorf("the file was touched: %v", err)
	}
}
