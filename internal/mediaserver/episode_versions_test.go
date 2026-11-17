package mediaserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/mediaserver"
	"github.com/segun-io/fukku/internal/mediaserver/mediaservertest"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// eenv is the step of episodes' versions (#58) on a version file on disk, next to S01E01's
// file in Sonarr's season folder (a temporary folder: Sonarr sees it as /tv, the media server
// as /media/tv), with the fakes.
type eenv struct {
	t       *testing.T
	st      *storetest.Fake
	series  *arrtest.FakeSeries
	server  *mediaservertest.Fake
	step    *mediaserver.EpisodeVersionStep
	version string
	target  store.Target
	ms      [3]string // the media server's view: the episode's file, the version, the series
}

func newEpisodeStepEnv(t *testing.T) *eenv {
	t.Helper()
	lib := t.TempDir()
	season := filepath.Join(lib, "Show", "Season 01")
	if err := os.MkdirAll(season, 0o755); err != nil {
		t.Fatal(err)
	}
	x := &eenv{t: t, st: storetest.NewFake(nil), version: filepath.Join(season, "Show - S01E01 - Español.mkv")}
	if err := os.WriteFile(x.version, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := stepConfig()
	cfg.Paths.Mappings.Sonarr = []config.PathMapping{{From: lib, To: "/tv"}}
	cfg.Paths.Mappings.MediaServer = []config.PathMapping{{From: lib, To: "/media/tv"}}
	x.series = &arrtest.FakeSeries{
		Library: []arr.Series{{ID: 11, Title: "Show", Path: "/tv/Show"}},
		Eps:     map[int][]arr.Episode{11: {{ID: 101, SeasonNumber: 1, Number: 1, FileID: 601}}},
		Files:   map[int][]arr.EpisodeFile{11: {{ID: 601, Path: "/tv/Show/Season 01/Show - S01E01 - Pilot WEBDL-1080p.mkv"}}},
	}
	x.ms = [3]string{"/media/tv/Show/Season 01/Show - S01E01 - Pilot WEBDL-1080p.mkv", "/media/tv/Show/Season 01/Show - S01E01 - Español.mkv", "/media/tv/Show"}
	// Jellyfin 12.4 groups the two files as one episode by their SxxEyy (B2).
	x.server = &mediaservertest.Fake{Pending: map[string]string{x.ms[0]: "ep-1", x.ms[1]: "ep-1", x.ms[2]: "series-1"}}
	x.step = &mediaserver.EpisodeVersionStep{Server: x.server, Series: x.series, Store: x.st, Config: cfg}
	opts, _ := json.Marshal(map[string]any{"episodeVersions": map[string]any{"label": "Español", "versions": []map[string]any{{"path": x.version, "row": "r1"}}}})
	x.target = store.Target{App: "sonarr", TitleID: 11, Title: "Show", Options: opts}
	if _, err := x.st.PutVersion(t.Context(), store.Version{JobID: "j1", App: "sonarr", TitleID: 11, Title: "Show", Path: x.version,
		Label: "Español", Status: store.VersionLinked, EpisodeID: 101, SeasonNumber: 1, EpisodeNumber: 1}); err != nil {
		t.Fatal(err)
	}
	return x
}

func (x *eenv) run() (bool, string, error) {
	return x.step.Run(x.t.Context(), store.Job{ID: "j1"}, x.target, nil)
}

func (x *eenv) record() store.Version {
	x.t.Helper()
	rec, err := x.st.VersionByPath(x.t.Context(), x.version)
	if err != nil {
		x.t.Fatal(err)
	}
	return rec
}

func TestEpisodeVersionStepRecordsTheGrouping(t *testing.T) {
	x := newEpisodeStepEnv(t)
	done, detail, err := x.run()
	if err != nil || !done || detail != "item series-1" {
		t.Fatalf("run: %v %q %v", done, detail, err)
	}
	if len(x.server.Refreshed) != 1 || !slices.Equal(x.server.Refreshed[0], []string{x.ms[1]}) {
		t.Errorf("refreshed %v", x.server.Refreshed)
	}
	if len(x.server.MergeCalls()) != 0 {
		t.Errorf("an episode's version was merged: %v", x.server.MergeCalls())
	}
	rec := x.record()
	if rec.Status != store.VersionGrouped || rec.PrimaryFileID != 601 || rec.PrimaryItemID != "ep-1" || rec.GroupedAt.IsZero() ||
		rec.PrimaryPath == "" || filepath.Base(rec.PrimaryPath) != "Show - S01E01 - Pilot WEBDL-1080p.mkv" {
		t.Fatalf("record %+v", rec)
	}
	log, _ := x.st.AuditLog(t.Context(), "j1")
	if len(log) != 1 || log[0].Action != "version.grouped" || log[0].Detail["episode"] != "S01E01" {
		t.Fatalf("audit %+v", log)
	}
	if l, ok := x.step.Link("item series-1"); ok || l != "" {
		t.Errorf("a link without a template: %q", l)
	}
}

func TestEpisodeVersionStepWaitsForTheGrouping(t *testing.T) {
	x := newEpisodeStepEnv(t)
	x.server.Pending[x.ms[1]] = "ep-2" // listed as an episode of its own: not grouped
	_, _, err := x.run()
	if !errors.Is(err, mediaserver.ErrNotVisible) {
		t.Fatalf("run: %v", err)
	}
	if rec := x.record(); rec.Status != store.VersionWaiting || rec.Detail == "" {
		t.Fatalf("record %+v", rec)
	}
	if x.server.Scans != 1 {
		t.Errorf("fallback scans %d, want 1", x.server.Scans)
	}
}

func TestEpisodeVersionStepEdges(t *testing.T) {
	t.Run("no media server", func(t *testing.T) {
		x := newEpisodeStepEnv(t)
		x.step.Config.MediaServer.Provider = "none"
		if _, _, err := x.run(); !errors.Is(err, flows.ErrStepSkipped) {
			t.Fatalf("run: %v", err)
		}
	})
	t.Run("the version is gone", func(t *testing.T) {
		x := newEpisodeStepEnv(t)
		if err := os.Remove(x.version); err != nil {
			t.Fatal(err)
		}
		if done, _, err := x.run(); err != nil || !done {
			t.Fatalf("run: %v %v", done, err)
		}
		if rec := x.record(); rec.Status != store.VersionMissing {
			t.Fatalf("record %+v", rec)
		}
	})
	t.Run("the episode lost its file", func(t *testing.T) {
		x := newEpisodeStepEnv(t)
		x.series.Eps[11][0].FileID = 0
		if _, _, err := x.run(); err != nil {
			t.Fatalf("run: %v", err)
		}
		if rec := x.record(); rec.Status != store.VersionWaiting {
			t.Fatalf("record %+v", rec)
		}
	})
	t.Run("no version of the job", func(t *testing.T) {
		x := newEpisodeStepEnv(t)
		x.target.Options = json.RawMessage(`{}`)
		if _, _, err := x.run(); !errors.Is(err, flows.ErrStepSkipped) { // every version refused
			t.Fatalf("run: %v", err)
		}
	})
}

// The mediaServerRefresh step has nothing to do when only episodes' versions were placed.
func TestRefreshStepSkipsAJobOfOnlyEpisodeVersions(t *testing.T) {
	x := newEpisodeStepEnv(t)
	s := &mediaserver.Step{Server: x.server, Series: x.series, Config: x.step.Config}
	rows := []store.Row{{ID: "r1", Skip: true, Result: store.ResultSkipped, Proposal: json.RawMessage(`{"EpisodeIDs":[101]}`)}}
	if _, _, err := s.Run(t.Context(), store.Job{ID: "j1"}, x.target, rows); !errors.Is(err, flows.ErrStepSkipped) {
		t.Fatalf("run: %v", err)
	}
}

// The keeper leaves episodes' versions alone: Jellyfin groups them by their names.
func TestKeeperLeavesEpisodeVersions(t *testing.T) {
	x := newKeeperEnv(t)
	ctx := t.Context()
	ep, err := x.st.PutVersion(ctx, store.Version{JobID: "j9", App: "sonarr", TitleID: 7, EpisodeID: 101, Path: x.version + ".episode", Status: store.VersionLinked})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ended := make(chan struct{})
	go func() { _ = x.k.Run(runCtx); close(ended) }()
	waitFor(t, func() bool {
		rec, _ := x.st.VersionByPath(ctx, x.version)
		return rec.Status == store.VersionGrouped
	})
	x.k.Nudge(store.Target{App: "radarr", TitleID: 7})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-ended
	if rec, _ := x.st.VersionByPath(ctx, ep.Path); rec.Status != store.VersionLinked || rec.Detail != "" || !rec.CheckedAt.IsZero() {
		t.Fatalf("the keeper checked an episode's version (with Radarr's movie of the same id): %+v", rec)
	}
}
