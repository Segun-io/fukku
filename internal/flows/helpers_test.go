package flows

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

const (
	intake    = "/data/intake"
	appIntake = "/downloads/intake" // the intake root as the apps see it
	filmID    = 7
	showID    = 11
	otherShow = 12
	minute    = int64(60_000)
	epRuntime = 22 * minute
)

var (
	webdl1080 = arr.Quality{ID: 3, Name: "WEBDL-1080p"}
	quals     = []arr.Quality{{ID: 0, Name: "Unknown"}, {ID: 1, Name: "SDTV"}, {ID: 4, Name: "HDTV-720p"}, webdl1080, {ID: 7, Name: "Bluray-1080p"}}
	english   = arr.Language{ID: 1, Name: "English"}
	spanish   = arr.Language{ID: 3, Name: "Spanish"}
	langs     = []arr.Language{english, {ID: 2, Name: "French"}, spanish}
)

// episodes of the test show: season 1 has 101–106, season 2 has 201–203, 22 minutes each.
func showEpisodes() []arr.Episode {
	var eps []arr.Episode
	for n := 1; n <= 6; n++ {
		eps = append(eps, arr.Episode{ID: 100 + n, SeasonNumber: 1, Number: n, Title: fmt.Sprintf("Episode %d", n), RuntimeMs: epRuntime})
	}
	for n := 1; n <= 3; n++ {
		eps = append(eps, arr.Episode{ID: 200 + n, SeasonNumber: 2, Number: n, Title: fmt.Sprintf("Second %d", n), RuntimeMs: epRuntime})
	}
	return eps
}

type env struct {
	t      *testing.T
	ctx    context.Context
	cfg    config.Config
	st     store.Store
	movies *arrtest.FakeMovies
	series *arrtest.FakeSeries
	p      *Proposals
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, storetest.NewFake(nil))
}

func newEnvWith(t *testing.T, st store.Store) *env {
	t.Helper()
	cfg := config.Defaults()
	cfg.Paths.IntakeRoot = intake
	cfg.Paths.Mappings.Radarr = []config.PathMapping{{From: intake, To: appIntake}}
	cfg.Paths.Mappings.Sonarr = []config.PathMapping{{From: intake, To: appIntake}}
	e := &env{t: t, ctx: t.Context(), cfg: cfg, st: st, movies: &arrtest.FakeMovies{}, series: &arrtest.FakeSeries{}}
	e.movies.Quals, e.movies.Langs = quals, langs
	e.movies.Library = []arr.Movie{{ID: filmID, Title: "Some Film", Year: 2001}}
	e.series.Quals, e.series.Langs = quals, langs
	e.series.Library = []arr.Series{{ID: showID, Title: "Some Show", Year: 2010}}
	e.series.Eps = map[int][]arr.Episode{showID: showEpisodes()}
	e.movies.Proposed, e.series.Proposed = map[string][]arr.Row{}, map[string][]arr.Row{}
	e.p = NewProposals(cfg, nil, st, e.movies, e.series, nil)
	return e
}

// file is a video of a job: its name under files/ and its probe.
type file struct {
	name     string
	duration int64
	w, h     int
}

func vid(name string, duration int64) file {
	return file{name: name, duration: duration, w: 1920, h: 1080}
}

// job makes a matched job with the files, for app and title.
func (e *env) job(app string, titleID int, files ...file) store.Job {
	e.t.Helper()
	j, err := e.st.CreateJob(e.ctx, "user1")
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := storetest.Drive(e.ctx, e.st, j.ID, store.Probed); err != nil {
		e.t.Fatal(err)
	}
	for _, f := range files {
		pr, _ := json.Marshal(media.Probe{Container: "matroska,webm", DurationMs: f.duration, Size: 1000,
			Video: []media.VideoTrack{{Codec: "h264", Width: f.w, Height: f.h}}})
		if _, err := e.st.AddFile(e.ctx, store.File{JobID: j.ID, Path: "files/" + f.name, Kind: store.KindVideo, Size: 1000, Status: store.FileOK, Report: pr}); err != nil {
			e.t.Fatal(err)
		}
	}
	// A subtitle and an archive never become rows.
	for _, f := range []store.File{
		{JobID: j.ID, Path: "files/extra.srt", Kind: store.KindSubtitle, Size: 10, Status: store.FileOK},
		{JobID: j.ID, Path: "archives/season.zip", Kind: store.KindArchive, Size: 10, Status: store.FileOK},
	} {
		if _, err := e.st.AddFile(e.ctx, f); err != nil {
			e.t.Fatal(err)
		}
	}
	if err := e.st.SetTarget(e.ctx, j.ID, store.Target{App: app, TitleID: titleID, Title: "Some Title", Year: 2010, MatchedBy: "user1"}); err != nil {
		e.t.Fatal(err)
	}
	if j, err = e.st.Transition(e.ctx, j.ID, store.Probed, store.Matched, store.Change{Actor: "user1"}); err != nil {
		e.t.Fatal(err)
	}
	return j
}

func (e *env) folder(j store.Job) string { return appIntake + "/jobs/" + j.ID + "/files" }

func (e *env) appPath(j store.Job, name string) string { return e.folder(j) + "/" + name }

// item is a proposal item as the app sends it for a file it could not match to a title.
func (e *env) item(j store.Job, name string, q arr.Quality) arr.Row {
	p := e.appPath(j, name)
	raw, _ := json.Marshal(map[string]any{
		"path": p, "relativePath": name, "name": name, "size": 1000, "releaseType": "singleEpisode",
		"quality":    map[string]any{"quality": map[string]any{"id": q.ID, "name": q.Name}},
		"rejections": []map[string]any{{"reason": "Unknown Series", "type": "permanent"}},
	})
	return arr.Row{
		Path: p, Size: 1000, Quality: q, Languages: []arr.Language{english},
		Rejections: []arr.Rejection{{Reason: "Unknown Series", Permanent: true}}, Raw: raw,
	}
}

func (e *env) jobNow(j store.Job) store.Job {
	e.t.Helper()
	j, err := e.st.Job(e.ctx, j.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return j
}

// matched runs the matched step and returns the job afterwards.
func (e *env) matched(j store.Job) store.Job {
	e.t.Helper()
	if err := e.p.Matched(e.ctx, e.jobNow(j)); err != nil {
		e.t.Fatalf("matched step: %v", err)
	}
	return e.jobNow(j)
}

// ready runs the matched step, which must end in awaiting-confirmation, and returns the view.
func (e *env) ready(j store.Job) Proposal {
	e.t.Helper()
	if j = e.matched(j); j.State != store.AwaitingConfirmation {
		e.t.Fatalf("job is %s (%s: %s), want awaiting-confirmation", j.State, j.ErrorCode, j.ErrorText)
	}
	return e.view(j)
}

func (e *env) view(j store.Job) Proposal {
	e.t.Helper()
	p, err := e.p.View(e.ctx, j)
	if err != nil {
		e.t.Fatalf("view: %v", err)
	}
	return p
}

func (e *env) rows(j store.Job) []store.Row {
	e.t.Helper()
	rows, err := e.st.Rows(e.ctx, j.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func (e *env) edit(j store.Job, rowID string, ed Edit) RowView {
	e.t.Helper()
	r, err := e.p.EditRow(e.ctx, j, rowID, ed)
	if err != nil {
		e.t.Fatalf("edit %s: %v", rowID, err)
	}
	return r
}

func ids(rows []RowView) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func storedIDs(rows []store.Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func episodeIDs(r RowView) []int {
	var out []int
	if r.Episodes != nil {
		for _, ep := range *r.Episodes {
			out = append(out, ep.ID)
		}
	}
	return out
}

func codes(cs []Notice) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Code)
	}
	return out
}

func wantCodes(t *testing.T, what string, got []Notice, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if g := codes(got); !slices.Equal(g, want) {
		t.Errorf("%s: codes %v, want %v", what, g, want)
	}
}

func byName(p Proposal, name string) RowView {
	for _, r := range p.Rows {
		if r.Name == name {
			return r
		}
	}
	panic("no row " + name)
}

// errRefused is how the arr client reports an app it cannot reach.
var errRefused = fmt.Errorf("sonarr: GET /manualimport: %w: connection refused", arr.ErrUnreachable)

func probeWith(w, h int) media.Probe {
	if w == 0 {
		return media.Probe{}
	}
	return media.Probe{Video: []media.VideoTrack{{Width: w, Height: h}}}
}
