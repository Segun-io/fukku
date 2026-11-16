package flows_test

import (
	"encoding/json"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/store"
)

// withFile gives the library film (tmdb 604, id 7) a tracked file.
func (f *fixture) withFile(mf *arr.MovieFile) {
	f.movies.Library[0].File = mf
	f.movies.Library[0].HasFile = mf != nil
}

func trackedFile() *arr.MovieFile {
	return &arr.MovieFile{
		ID: 12, Path: "/movies/Other (2003)/Other (2003) WEBDL-1080p.mkv", Size: 4000,
		Quality: arr.Quality{ID: 3, Name: "WEBDL-1080p"}, Languages: []arr.Language{{ID: 1, Name: "English"}},
		RuntimeMs: 5_400_000,
	}
}

func (f *fixture) target(j store.Job) store.Target {
	f.t.Helper()
	t, err := f.st.Target(f.t.Context(), j.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return t
}

// A film that already has a file is matched as flow 2: nothing is added, and the job
// carries the tracked file, its path mapped to how Fukku sees it.
func TestMatchFilmWithFileIsFlow2(t *testing.T) {
	f := newFixture(t)
	cfg := config.Defaults()
	cfg.Radarr.URL = "http://radarr.example.com:7878"
	cfg.Paths.Mappings.Radarr = []config.PathMapping{{From: "/data/media/movies", To: "/movies"}}
	f.m = flows.NewMatcher(cfg, f.st, f.movies, f.series, f.locks, nil)
	f.withFile(trackedFile())

	j := f.job(store.Probed)
	got, err := f.m.Match(t.Context(), "user1", j, flows.MatchRequest{App: "radarr", TMDBID: 604})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.Matched || len(f.movies.Added) != 0 {
		t.Fatalf("job %s, added %+v", got.State, f.movies.Added)
	}
	tg := f.target(j)
	rec, ok := flows.Flow2Of(tg)
	if !ok || tg.TitleID != 7 {
		t.Fatalf("target %+v (%s)", tg, tg.Options)
	}
	want := flows.LibraryFile{
		FileID: 12, AppPath: "/movies/Other (2003)/Other (2003) WEBDL-1080p.mkv",
		Path: "/data/media/movies/Other (2003)/Other (2003) WEBDL-1080p.mkv", Size: 4000,
		Quality: flows.Ref{ID: 3, Name: "WEBDL-1080p"}, Languages: []flows.Ref{{ID: 1, Name: "English"}}, RuntimeMs: 5_400_000,
	}
	if g := rec.Library; g.FileID != want.FileID || g.AppPath != want.AppPath || g.Path != want.Path || g.Size != want.Size ||
		g.Quality != want.Quality || len(g.Languages) != 1 || g.Languages[0] != want.Languages[0] || g.RuntimeMs != want.RuntimeMs || g.Probe != nil {
		t.Errorf("library %+v, want %+v", g, want)
	}
	var opts map[string]json.RawMessage
	if err := json.Unmarshal(tg.Options, &opts); err != nil || string(opts["added"]) != "false" {
		t.Errorf("options %s", tg.Options)
	}

	// The rules of a match still hold: a title another job holds is refused.
	other := f.job(store.Probed)
	if _, err := f.m.Match(t.Context(), "user1", other, flows.MatchRequest{App: "radarr", TMDBID: 604}); flows.Code(err) != flows.CodeTitleInUse {
		t.Errorf("a second job for the title: %v", err)
	}
}

// So is one the app is downloading.
func TestMatchBusyFilmWithFile(t *testing.T) {
	f := newFixture(t)
	f.withFile(trackedFile())
	f.movies.Queue = map[int]bool{7: true}
	j := f.job(store.Probed)
	if _, err := f.m.Match(t.Context(), "user1", j, flows.MatchRequest{App: "radarr", TMDBID: 604}); flows.Code(err) != flows.CodeTitleBusy {
		t.Errorf("busy: %v", err)
	}
	if f.state(j) != store.Probed {
		t.Errorf("job %s", f.state(j))
	}
}

// Matching the same film again keeps the library file's probe while the file is the same,
// and drops it when Radarr tracks another file now; a film without a file is flow 1 again.
func TestRematchFlow2(t *testing.T) {
	f := newFixture(t)
	f.withFile(trackedFile())
	ctx := t.Context()
	j := f.job(store.Probed)
	match := func() flows.Flow2 {
		t.Helper()
		if _, err := f.m.Match(ctx, "user1", j, flows.MatchRequest{App: "radarr", TMDBID: 604}); err != nil {
			t.Fatal(err)
		}
		rec, _ := flows.Flow2Of(f.target(j))
		return rec
	}
	match()
	// The matched step stored the probe.
	tg := f.target(j)
	var opts map[string]any
	if err := json.Unmarshal(tg.Options, &opts); err != nil {
		t.Fatal(err)
	}
	opts["flow2"].(map[string]any)["library"].(map[string]any)["probe"] = media.Probe{DurationMs: 5_400_000}
	tg.Options, _ = json.Marshal(opts)
	if err := f.st.SetTarget(ctx, j.ID, tg); err != nil {
		t.Fatal(err)
	}

	if rec := match(); rec.Library.Probe == nil || rec.Library.Probe.DurationMs != 5_400_000 {
		t.Errorf("the same file again lost its probe: %+v", rec.Library)
	}
	upgraded := trackedFile()
	upgraded.ID, upgraded.Path, upgraded.Size = 13, "/movies/Other (2003)/Other (2003) Bluray-1080p.mkv", 9000
	f.withFile(upgraded)
	if rec := match(); rec.Library.Probe != nil || rec.Library.FileID != 13 {
		t.Errorf("a new file kept the old probe: %+v", rec.Library)
	}
	f.withFile(nil)
	match()
	if _, ok := flows.Flow2Of(f.target(j)); ok {
		t.Errorf("a film without a file is still flow 2: %s", f.target(j).Options)
	}
}
