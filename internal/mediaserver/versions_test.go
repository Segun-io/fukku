package mediaserver_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/mediaserver"
	"github.com/segun-io/fukku/internal/mediaserver/mediaservertest"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// kenv is a keeper with a version on disk (a temporary folder, which the media server sees
// at the same path), the film in Radarr with its file, and the fakes.
type kenv struct {
	t       *testing.T
	st      *storetest.Fake
	movies  *arrtest.FakeMovies
	server  *mediaservertest.Fake
	k       *mediaserver.Keeper
	version string // the version file
}

const upgradedApp = "/movies/Film (2020)/Film (2020) Remux-1080p.mkv"
const upgradedServer = "/media/movies/Film (2020)/Film (2020) Remux-1080p.mkv"

func newKeeperEnv(t *testing.T) *kenv {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Film (2020)")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	x := &kenv{t: t, st: storetest.NewFake(nil), version: filepath.Join(dir, "Film (2020) - Español.mkv")}
	if err := os.WriteFile(x.version, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	x.movies = &arrtest.FakeMovies{Library: []arr.Movie{{ID: 7, Title: "Film", File: &arr.MovieFile{ID: 1, Path: appFile, Size: 10}}}}
	x.server = &mediaservertest.Fake{Pending: map[string]string{serverFile: "film-1", x.version: "ver-1"}}
	x.k = mediaserver.NewKeeper(stepConfig(), nil, x.st, x.movies, x.server, nil)
	if _, err := x.st.PutVersion(t.Context(), store.Version{JobID: "j1", App: "radarr", TitleID: 7, Title: "Film", Year: 2020,
		Path: x.version, Label: "Español", Status: store.VersionLinked}); err != nil {
		t.Fatal(err)
	}
	return x
}

func (x *kenv) check(scan bool) store.Version {
	x.t.Helper()
	rec, err := x.k.Check(x.t.Context(), x.version, 200*time.Millisecond, scan)
	if err != nil {
		x.t.Fatalf("Check: %v", err)
	}
	return rec
}

func (x *kenv) actions() []string {
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

// The first grouping: both files are refreshed into the server, merged, and seen grouped.
func TestKeeperGroupsTheVersion(t *testing.T) {
	x := newKeeperEnv(t)
	rec := x.check(false)
	if rec.Status != store.VersionGrouped || rec.PrimaryItemID != "film-1" || rec.ItemID != "ver-1" || rec.PrimaryFileID != 1 ||
		rec.PrimaryPath != "/data/media/movies/Film (2020)/Film (2020) WEBDL-1080p.mkv" || rec.GroupedAt.IsZero() || !rec.PrimaryChangedAt.IsZero() {
		t.Fatalf("record %+v", rec)
	}
	if len(x.server.Refreshed) != 1 || !slices.Equal(x.server.Refreshed[0], []string{serverFile, x.version}) {
		t.Errorf("refreshed %v, want the server's view of both files", x.server.Refreshed)
	}
	if len(x.server.Merged) != 1 || !slices.Equal(x.server.Merged[0], []string{"film-1", "ver-1"}) {
		t.Errorf("merged %v", x.server.Merged)
	}
	if got := x.actions(); !slices.Equal(got, []string{"version.grouped"}) {
		t.Errorf("audit %v", got)
	}
	// Still grouped: nothing is merged again, nothing audited.
	x.check(false)
	if len(x.server.Merged) != 1 || len(x.actions()) != 1 {
		t.Errorf("a grouped version was merged again: %v, audit %v", x.server.Merged, x.actions())
	}
}

// Radarr upgrades the film (B3e, B3f): the new file is another item, the grouping is gone;
// the keeper groups the version with the new file and says that the film's file changed.
func TestKeeperRegroupsAfterAnUpgrade(t *testing.T) {
	x := newKeeperEnv(t)
	first := x.check(false)
	x.movies.Library[0].File = &arr.MovieFile{ID: 2, Path: upgradedApp, Size: 20}
	x.server.Ungroup()
	x.server.SetItem(serverFile, "")
	x.server.Pending = map[string]string{upgradedServer: "film-2"} // listed after a refresh
	rec := x.check(false)
	if rec.Status != store.VersionGrouped || rec.PrimaryItemID != "film-2" || rec.PrimaryFileID != 2 || rec.PrimaryChangedAt.IsZero() ||
		!rec.GroupedAt.After(first.GroupedAt) && !rec.GroupedAt.Equal(first.GroupedAt) {
		t.Fatalf("record %+v", rec)
	}
	if !slices.Equal(x.server.Merged[len(x.server.Merged)-1], []string{"film-2", "ver-1"}) {
		t.Errorf("merged %v", x.server.Merged)
	}
	log, _ := x.st.AuditLog(t.Context(), "j1")
	if len(log) != 2 || log[1].Action != "version.regrouped" || log[1].Detail["fileChanged"] != "true" ||
		log[1].Detail["previous"] != "/data/media/movies/Film (2020)/Film (2020) WEBDL-1080p.mkv" ||
		log[1].Detail["primary"] != "/data/media/movies/Film (2020)/Film (2020) Remux-1080p.mkv" {
		t.Errorf("audit %+v", log)
	}
}

// A grouping lost without a new file (a rename keeps Radarr's file id): merged again, and
// the record says whether the file changed.
func TestKeeperRegroupsALostGrouping(t *testing.T) {
	x := newKeeperEnv(t)
	x.check(false)
	x.server.Ungroup()
	rec := x.check(false)
	if rec.Status != store.VersionGrouped || !rec.PrimaryChangedAt.IsZero() || len(x.server.Merged) != 2 {
		t.Fatalf("record %+v, merged %v", rec, x.server.Merged)
	}
	log, _ := x.st.AuditLog(t.Context(), "j1")
	if len(log) != 2 || log[1].Action != "version.regrouped" || log[1].Detail["fileChanged"] != "false" {
		t.Errorf("audit %+v", log)
	}
}

// What ends the checks, once: the film deleted in Radarr, the version file gone.
func TestKeeperFinalStates(t *testing.T) {
	t.Run("film deleted", func(t *testing.T) {
		x := newKeeperEnv(t)
		x.movies.Err = map[string]error{"Movie": &arr.Error{App: "radarr", Method: "GET", Path: "/movie/7", Status: 404}}
		for range 2 {
			if rec := x.check(false); rec.Status != store.VersionFilmDeleted || !strings.Contains(rec.Detail, "left on disk") {
				t.Fatalf("record %+v", rec)
			}
		}
		if got := x.actions(); !slices.Equal(got, []string{"version.orphaned"}) {
			t.Errorf("audit %v", got)
		}
		if _, err := os.Stat(x.version); err != nil {
			t.Errorf("the version was touched: %v", err)
		}
	})
	t.Run("version gone", func(t *testing.T) {
		x := newKeeperEnv(t)
		if err := os.Remove(x.version); err != nil {
			t.Fatal(err)
		}
		if rec := x.check(false); rec.Status != store.VersionMissing {
			t.Fatalf("record %+v", rec)
		}
		if got := x.actions(); !slices.Equal(got, []string{"version.missing"}) {
			t.Errorf("audit %v", got)
		}
	})
}

// Waiting: the server does not list a file (the versions folder is not in a library, say),
// or Radarr has no file; a call that fails keeps the status and says why.
func TestKeeperWaits(t *testing.T) {
	x := newKeeperEnv(t)
	x.server.Pending = map[string]string{serverFile: "film-1"} // the version is never listed
	rec := x.check(false)
	if rec.Status != store.VersionWaiting || !strings.Contains(rec.Detail, x.version) || len(x.server.Merged) != 0 {
		t.Fatalf("record %+v", rec)
	}
	if x.server.Scans != 0 {
		t.Errorf("a scheduled check scanned every library")
	}
	rec = x.check(true) // the job's step may scan once
	if x.server.Scans != 1 || rec.Status != store.VersionWaiting {
		t.Errorf("scans %d, record %+v", x.server.Scans, rec)
	}

	x.movies.Library[0].File = nil
	if rec := x.check(false); rec.Status != store.VersionWaiting || !strings.Contains(rec.Detail, "no file") {
		t.Errorf("record %+v", rec)
	}

	x.movies.Err = map[string]error{"Movie": errors.New("connection refused")}
	if _, err := x.k.Check(t.Context(), x.version, 0, false); err == nil {
		t.Error("a failed call is not an error")
	}
	if rec, _ := x.st.VersionByPath(t.Context(), x.version); rec.Status != store.VersionWaiting || !strings.Contains(rec.Detail, "connection refused") {
		t.Errorf("record %+v", rec)
	}
}

// The step of a version job: grouped gives the film's item (the title link); a version not
// grouped yet is retried; a deleted film gives up; no media server skips.
func TestVersionStep(t *testing.T) {
	x := newKeeperEnv(t)
	cfg := stepConfig()
	cfg.MediaServer.TitleLinkTemplate = "https://media.example.com/web/#/details?id={itemId}"
	s := &mediaserver.VersionStep{Keeper: x.k, Store: x.st, Config: cfg}
	done, detail, err := s.Run(t.Context(), store.Job{ID: "j1"}, target, nil)
	if err != nil || !done || detail != "item film-1" {
		t.Fatalf("Run = %v %q %v", done, detail, err)
	}
	if l, ok := s.Link(detail); !ok || l != "https://media.example.com/web/#/details?id=film-1" {
		t.Errorf("link %q", l)
	}

	x.server.Ungroup()
	x.server.Err = map[string]error{"MergeVersions": errors.New("500")}
	if _, _, err := s.Run(t.Context(), store.Job{ID: "j1"}, target, nil); err == nil || errors.Is(err, flows.ErrStepGaveUp) {
		t.Errorf("a failed merge: %v, want an error to retry", err)
	}

	x.movies.Err = map[string]error{"Movie": &arr.Error{Status: 404}}
	if _, _, err := s.Run(t.Context(), store.Job{ID: "j1"}, target, nil); !errors.Is(err, flows.ErrStepGaveUp) {
		t.Errorf("deleted film: %v, want give up", err)
	}

	none := &mediaserver.VersionStep{Keeper: mediaserver.NewKeeper(stepConfig(), nil, x.st, x.movies, nil, nil), Store: x.st, Config: stepConfig()}
	if _, _, err := none.Run(t.Context(), store.Job{ID: "j1"}, target, nil); !errors.Is(err, flows.ErrStepSkipped) {
		t.Errorf("no media server: %v, want skipped", err)
	}
}

// The schedule checks grouped and waiting versions (and linked ones a step gave up on),
// never one being placed or a final one; a nudge checks the film's versions.
func TestKeeperRun(t *testing.T) {
	x := newKeeperEnv(t)
	ctx := t.Context()
	for _, v := range []store.Version{
		{JobID: "j2", App: "radarr", TitleID: 7, Path: x.version + ".placing", Status: store.VersionPlacing},
		{JobID: "j3", App: "radarr", TitleID: 7, Path: x.version + ".deleted", Status: store.VersionFilmDeleted, CheckedAt: time.Now()},
	} {
		if _, err := x.st.PutVersion(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ended := make(chan struct{})
	go func() { _ = x.k.Run(runCtx); close(ended) }()
	waitFor(t, func() bool {
		rec, _ := x.st.VersionByPath(ctx, x.version)
		return rec.Status == store.VersionGrouped
	})
	for _, p := range []string{x.version + ".placing", x.version + ".deleted"} {
		if rec, _ := x.st.VersionByPath(ctx, p); rec.Detail != "" {
			t.Errorf("%s was checked: %+v", p, rec)
		}
	}
	// A nudge after an import of the film: the lost grouping comes back.
	x.server.Ungroup()
	x.k.Nudge(store.Target{App: "radarr", TitleID: 7})
	waitFor(t, func() bool { return len(x.server.MergeCalls()) == 2 })
	cancel()
	<-ended
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
