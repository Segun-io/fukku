package mediaserver_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/mediaserver"
	"github.com/segun-io/fukku/internal/mediaserver/mediaservertest"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// Fixes from the review of #89: no final status from a passing fault, one refresh per pass.

func notFound() error { return &arr.Error{App: "radarr", Method: "GET", Path: "/movie/7", Status: 404} }

// runKeeper runs k until its first pass has had time to end.
func runKeeper(t *testing.T, k *mediaserver.Keeper) {
	t.Helper()
	c, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { _ = k.Run(c); close(done) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
}

// A missing sentinel (a NAS mounted late) or a missing versions folder decides nothing: the
// record keeps its status, and its detail says why.
func TestKeeperDecidesNothingWithoutTheMounts(t *testing.T) {
	x := newKeeperEnv(t)
	x.check(false) // grouped
	if err := os.Remove(x.version); err != nil {
		t.Fatal(err)
	}
	cfg := stepConfig()
	cfg.Paths.Sentinels = []string{filepath.Join(t.TempDir(), "missing", ".mounted")}
	k := mediaserver.NewKeeper(cfg, nil, x.st, x.movies, x.server, nil)
	rec, err := k.Check(t.Context(), x.version, 0, false)
	if err == nil || rec.Status != store.VersionGrouped || !strings.Contains(rec.Detail, "not checked") {
		t.Fatalf("missing sentinel: %+v, %v", rec, err)
	}
	// The versions folder itself is gone (its mount, say).
	if err := os.RemoveAll(filepath.Dir(filepath.Dir(x.version))); err != nil {
		t.Fatal(err)
	}
	rec, err = x.k.Check(t.Context(), x.version, 0, false)
	if err == nil || rec.Status != store.VersionGrouped || !strings.Contains(rec.Detail, "versions folder") {
		t.Fatalf("missing versions folder: %+v, %v", rec, err)
	}
	if got := x.actions(); slices.Contains(got, "version.missing") {
		t.Errorf("audit %v", got)
	}
}

// A 404 for the film counts only when Radarr's status call answers: a wrong URL base or
// proxy answers 404 to everything.
func TestKeeperTrustsA404OnlyFromRadarr(t *testing.T) {
	x := newKeeperEnv(t)
	x.check(false)
	x.movies.Err = map[string]error{"Movie": notFound(), "Status": notFound()}
	rec, err := x.k.Check(t.Context(), x.version, 0, false)
	if err == nil || rec.Status != store.VersionGrouped || !strings.Contains(rec.Detail, "status call fails") {
		t.Fatalf("404 everywhere: %+v, %v", rec, err)
	}
	x.movies.Err = map[string]error{"Movie": notFound()}
	if rec := x.check(false); rec.Status != store.VersionFilmDeleted {
		t.Fatalf("Radarr answers, the film is gone: %+v", rec)
	}
}

// A film deleted and added back to Radarr has a new id: the version follows it by its TMDB
// id, and is grouped again.
func TestKeeperFindsTheFilmAgainByTMDB(t *testing.T) {
	x := newKeeperEnv(t)
	rec, err := x.st.VersionByPath(t.Context(), x.version)
	if err != nil {
		t.Fatal(err)
	}
	rec.TMDBID = 605
	if _, err := x.st.PutVersion(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	x.movies.Library = []arr.Movie{{ID: 70, TMDBID: 605, Title: "Film", File: &arr.MovieFile{ID: 9, Path: appFile}}}
	x.movies.Err = map[string]error{"Movie": notFound()} // id 7 is gone
	got, err := x.k.Check(t.Context(), x.version, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.TitleID != 70 || got.Status != store.VersionGrouped || got.PrimaryFileID != 9 {
		t.Fatalf("record %+v", got)
	}
	if acts := x.actions(); !slices.Equal(acts, []string{"version.reattached", "version.grouped"}) {
		t.Errorf("audit %v", acts)
	}
}

// film_deleted and missing are checked again, less often, and recover.
func TestKeeperRechecksFinalStatuses(t *testing.T) {
	x := newKeeperEnv(t)
	x.movies.Err = map[string]error{"Movie": notFound()}
	if rec := x.check(false); rec.Status != store.VersionFilmDeleted {
		t.Fatalf("record %+v", rec)
	}
	x.movies.Err = nil // the film answers again (a fixed URL base, say)
	cfg := stepConfig()
	cfg.Versions.RegroupInterval = config.Duration(time.Hour)
	k := mediaserver.NewKeeper(cfg, nil, x.st, x.movies, x.server, nil)
	ctx := t.Context()
	runKeeper(t, k) // checked just now: not due yet
	if rec, _ := x.st.VersionByPath(ctx, x.version); rec.Status != store.VersionFilmDeleted {
		t.Fatalf("a final status checked again too soon: %+v", rec)
	}
	rec, _ := x.st.VersionByPath(ctx, x.version)
	rec.CheckedAt = time.Now().Add(-7 * time.Hour) // more than six passes ago
	if _, err := x.st.PutVersion(ctx, rec); err != nil {
		t.Fatal(err)
	}
	runKeeper(t, k)
	if rec, _ := x.st.VersionByPath(ctx, x.version); rec.Status != store.VersionGrouped {
		t.Fatalf("not recovered: %+v", rec)
	}
}

// A pass refreshes the server once for every missing file and waits once for all of them;
// with libraryScan it asks for one scan, which the server skips while one runs.
func TestKeeperPassRefreshesOnce(t *testing.T) {
	for _, method := range []string{mediaserver.MethodLibraryRefresh, mediaserver.MethodLibraryScan} {
		t.Run(method, func(t *testing.T) {
			dir := t.TempDir()
			st := storetest.NewFake(nil)
			movies := &arrtest.FakeMovies{}
			pending := map[string]string{}
			for i := range 3 {
				letter := string(rune('A' + i))
				file := "/movies/F" + letter + " (2020)/F.mkv"
				movies.Library = append(movies.Library, arr.Movie{ID: 7 + i, File: &arr.MovieFile{ID: 7 + i, Path: file}})
				pending["/media"+file] = "film-" + letter
				p := filepath.Join(dir, "F"+letter+" (2020)", "F (2020) - Español.mkv")
				pending[p] = "ver-" + letter
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("v"), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := st.PutVersion(t.Context(), store.Version{JobID: "j", App: "radarr", TitleID: 7 + i, Path: p, Status: store.VersionLinked}); err != nil {
					t.Fatal(err)
				}
			}
			server := &mediaservertest.Fake{Pending: pending, ScanOnly: method == mediaserver.MethodLibraryScan}
			cfg := stepConfig()
			cfg.MediaServer.RefreshMethod = method
			cfg.Versions.RegroupInterval = 0
			k := mediaserver.NewKeeper(cfg, nil, st, movies, server, nil)
			c, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() { _ = k.Run(c); close(done) }()
			waitFor(t, func() bool {
				all, _ := st.AllVersions(t.Context())
				return !slices.ContainsFunc(all, func(v store.Version) bool { return v.Status != store.VersionGrouped })
			})
			cancel()
			<-done
			if method == mediaserver.MethodLibraryScan {
				if server.Scans != 1 || len(server.Refreshed) != 0 {
					t.Errorf("scans %d, refreshes %v", server.Scans, server.Refreshed)
				}
				return
			}
			if len(server.Refreshed) != 1 || len(server.Refreshed[0]) != 6 || server.Scans != 0 {
				t.Errorf("refreshes %v, scans %d; want one refresh of the six files", server.Refreshed, server.Scans)
			}
		})
	}
}
