package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// countingMovies counts lookups, to show the server-side cache.
type countingMovies struct {
	*arrtest.FakeMovies
	lookups atomic.Int32
	onAdd   func() // called after the fake added a film
}

func (c *countingMovies) Add(ctx context.Context, a arr.AddMovie) (arr.Movie, error) {
	m, err := c.FakeMovies.Add(ctx, a)
	if c.onAdd != nil {
		c.onAdd()
	}
	return m, err
}

func (c *countingMovies) Lookup(ctx context.Context, term string) ([]arr.Movie, error) {
	c.lookups.Add(1)
	return c.FakeMovies.Lookup(ctx, term)
}

// matchEnv is the match API behind the whole chain of Handler, with the arrtest fakes.
type matchEnv struct {
	t      *testing.T
	h      http.Handler
	store  *storetest.Fake
	movies *countingMovies
	series *arrtest.FakeSeries
}

func newMatchEnv(t *testing.T, tweak func(*config.Config)) *matchEnv {
	t.Helper()
	cfg := config.Defaults()
	cfg.Auth.TrustedProxies = []string{"127.0.0.1"}
	cfg.Auth.AdminGroup = "admins"
	cfg.Server.PublicBaseURL = "https://fukku.example.com"
	cfg.Paths.IntakeRoot = t.TempDir()
	cfg.Radarr.URL = "http://radarr.example.com:7878"
	cfg.Radarr.QualityProfile = "hd-1080p"
	cfg.Radarr.RootFolder = "/data/media/movies"
	cfg.Sonarr.URL = "http://sonarr.example.com:8989"
	cfg.Sonarr.RootFolder = "/data/media/tv"
	if tweak != nil {
		tweak(&cfg)
	}
	e := &matchEnv{t: t, store: storetest.NewFake(nil)}
	e.movies = &countingMovies{FakeMovies: &arrtest.FakeMovies{
		Profile: []arr.Profile{{ID: 3, Name: "SD"}, {ID: 4, Name: "HD-1080p"}},
		Roots:   []arr.RootFolder{{Path: "/data/media/other"}, {Path: "/data/media/movies"}},
		Results: []arr.Movie{
			{TMDBID: 603, Title: "The Matrix", Year: 1999, Overview: "A hacker."},
			{ID: 7, TMDBID: 604, Title: "Library Film", Year: 2003},
			{ID: 8, TMDBID: 605, Title: "Has File", Year: 2004, HasFile: true},
		},
		Library: []arr.Movie{
			{ID: 7, TMDBID: 604, Title: "Library Film", Year: 2003, Path: "/data/media/movies/Library Film (2003)"},
			{ID: 8, TMDBID: 605, Title: "Has File", Year: 2004, File: &arr.MovieFile{ID: 1, Path: "/data/media/movies/Has File (2004)/a.mkv"}},
		},
		Queue: map[int]bool{},
	}}
	e.series = &arrtest.FakeSeries{
		Profile: []arr.Profile{{ID: 9, Name: "Any"}},
		Roots:   []arr.RootFolder{{Path: "/data/media/tv"}},
		Results: []arr.Series{
			{TVDBID: 1, Title: "A Show", Year: 2010, Overview: "About a show.", Seasons: []int{1, 2}},
			{ID: 20, TVDBID: 2, Title: "Known Show", Year: 2012, Seasons: []int{1}},
		},
		Library: []arr.Series{{ID: 20, TVDBID: 2, Title: "Known Show", Year: 2012}},
		Queue:   map[int]bool{},
	}
	deps := Deps{Store: e.store, Radarr: e.movies, Sonarr: e.series}
	e.h = New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, deps).Handler()
	return e
}

// call sends a request as user (groups "users"; "admin1" is an admin) through the chain.
func (e *matchEnv) call(user, method, path, body string) (int, string) {
	e.t.Helper()
	r := httptest.NewRequestWithContext(e.t.Context(), method, "https://fukku.example.com"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Remote-User", user)
	r.Header.Set("Remote-Email", user+"@example.com")
	groups := "users"
	if user == "admin1" {
		groups = "users,admins"
	}
	r.Header.Set("Remote-Groups", groups)
	if method != http.MethodGet {
		r.Header.Set("Origin", "https://fukku.example.com")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

func (e *matchEnv) ok(user, method, path, body string, out any) {
	e.t.Helper()
	code, resp := e.call(user, method, path, body)
	if code != http.StatusOK {
		e.t.Fatalf("%s %s: %d %s", method, path, code, resp)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(resp), out); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, path, err, resp)
		}
	}
}

func (e *matchEnv) refused(user, method, path, body string, status int, code string) {
	e.t.Helper()
	got, resp := e.call(user, method, path, body)
	var b struct {
		Error struct{ Code, Message string }
	}
	_ = json.Unmarshal([]byte(resp), &b)
	if got != status || b.Error.Code != code || b.Error.Message == "" {
		e.t.Errorf("%s %s %s: %d %q (%q), want %d %q", method, path, body, got, b.Error.Code, b.Error.Message, status, code)
	}
}

// job creates a job for owner and walks it to state.
func (e *matchEnv) job(owner string, state store.State) store.Job {
	e.t.Helper()
	ctx := context.Background()
	j, err := e.store.CreateJob(ctx, owner)
	if err != nil {
		e.t.Fatal(err)
	}
	prev := store.Uploading
	for _, st := range []store.State{store.Uploaded, store.Probing, store.Probed, store.Matched, store.AwaitingConfirmation} {
		if prev == state {
			break
		}
		if j, err = e.store.Transition(ctx, j.ID, prev, st, store.Change{Actor: owner}); err != nil {
			e.t.Fatal(err)
		}
		prev = st
	}
	return j
}

type matchedJob struct {
	ID     string
	State  string
	Target *struct {
		App     string
		TitleID int
		Title   string
		Year    int
	}
}

func (e *matchEnv) detail(user string, j store.Job) matchedJob {
	e.t.Helper()
	var d matchedJob
	e.ok(user, "GET", "/api/jobs/"+j.ID, "", &d)
	return d
}

func audits(t *testing.T, e *matchEnv, jobID string) []string {
	t.Helper()
	log, err := e.store.AuditLog(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range log {
		out = append(out, a.Action)
	}
	return out
}

func TestAppsListsConfiguredAppsWithDefaults(t *testing.T) {
	e := newMatchEnv(t, nil)
	var got map[string]json.RawMessage
	_, body := e.call("user1", "GET", "/api/apps", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	equalJSON(t, string(got["radarr"]), `{
	  "qualityProfiles": [{"id": 3, "name": "SD"}, {"id": 4, "name": "HD-1080p"}],
	  "rootFolders": [{"path": "/data/media/other"}, {"path": "/data/media/movies"}],
	  "defaults": {"qualityProfileId": 4, "rootFolder": "/data/media/movies", "monitored": true}}`)
	equalJSON(t, string(got["sonarr"]), `{
	  "qualityProfiles": [{"id": 9, "name": "Any"}], "rootFolders": [{"path": "/data/media/tv"}],
	  "seriesTypes": ["standard", "anime", "daily"],
	  "defaults": {"qualityProfileId": null, "rootFolder": "/data/media/tv", "seriesType": "standard"}}`)
}

func TestAppsDefaultsFollowSettings(t *testing.T) {
	e := newMatchEnv(t, func(c *config.Config) {
		c.Radarr.QualityProfile = ""
		c.Radarr.RootFolder = ""
		c.Radarr.MonitorAfterImport = false
		c.Sonarr.URL = "" // not configured: left out
	})
	_, body := e.call("user1", "GET", "/api/apps", "")
	var got struct {
		Radarr struct {
			Defaults struct {
				QualityProfileID *int
				RootFolder       string
				Monitored        bool
			}
		}
		Sonarr *json.RawMessage
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	d := got.Radarr.Defaults
	if d.QualityProfileID != nil || d.RootFolder != "/data/media/other" || d.Monitored || got.Sonarr != nil {
		t.Errorf("defaults = %+v, sonarr %v (%s)", d, got.Sonarr, body)
	}
}

func TestAppsReportsAnUnreachableApp(t *testing.T) {
	e := newMatchEnv(t, nil)
	e.series.Err = map[string]error{"Profiles": errors.New("dial tcp 192.0.2.1:8989: refused")}
	code, body := e.call("user1", "GET", "/api/apps", "")
	if code != http.StatusOK || !strings.Contains(body, `"unavailable":true`) || strings.Contains(body, "192.0.2.1") {
		t.Errorf("%d %s", code, body)
	}
}

func TestLookupMoviesAndSeries(t *testing.T) {
	e := newMatchEnv(t, nil)
	var m struct{ Results []map[string]any }
	e.ok("user1", "GET", "/api/lookup/movies?term=matrix", "", &m)
	if len(m.Results) != 3 {
		t.Fatalf("movies: %+v", m)
	}
	sameJSON(t, m.Results[0], `{"tmdbId":603,"title":"The Matrix","year":1999,"overview":"A hacker.","inLibrary":false,"hasFile":false}`)
	sameJSON(t, m.Results[1], `{"tmdbId":604,"title":"Library Film","year":2003,"overview":"","inLibrary":true,"hasFile":false}`)
	sameJSON(t, m.Results[2], `{"tmdbId":605,"title":"Has File","year":2004,"overview":"","inLibrary":true,"hasFile":true}`)
	var s struct{ Results []map[string]any }
	e.ok("user1", "GET", "/api/lookup/series?term=tvdb:1", "", &s)
	sameJSON(t, s.Results[0], `{"tvdbId":1,"title":"A Show","year":2010,"overview":"About a show.","inLibrary":false,"seasons":[1,2]}`)
	sameJSON(t, s.Results[1], `{"tvdbId":2,"title":"Known Show","year":2012,"overview":"","inLibrary":true,"seasons":[1]}`)
}

// sameJSON compares got with the JSON text want, ignoring the order of keys.
func sameJSON(t *testing.T, got any, want string) {
	t.Helper()
	var w any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, w) {
		t.Errorf("got %v, want %v", got, w)
	}
}

func TestLookupRefusals(t *testing.T) {
	e := newMatchEnv(t, nil)
	e.refused("user1", "GET", "/api/lookup/movies", "", 400, "bad_request")
	e.refused("user1", "GET", "/api/lookup/movies?term=%20", "", 400, "bad_request")
	e.refused("user1", "GET", "/api/lookup/series?term="+strings.Repeat("a", 201), "", 400, "bad_request")
	e.movies.Err = map[string]error{"Lookup": errors.New("connection refused to 192.0.2.7")}
	code, body := e.call("user1", "GET", "/api/lookup/movies?term=x", "")
	if code != 502 || !strings.Contains(body, `"arr_unavailable"`) || strings.Contains(body, "192.0.2.7") {
		t.Errorf("unreachable: %d %s", code, body)
	}
	e.series.Err = map[string]error{"Lookup": &arr.Error{App: "sonarr", Status: 500}}
	e.refused("user1", "GET", "/api/lookup/series?term=x", "", 502, "arr_unavailable")

	off := newMatchEnv(t, func(c *config.Config) { c.Radarr.URL = "" })
	off.refused("user1", "GET", "/api/lookup/movies?term=x", "", 404, "app_not_configured")
}

func TestLookupIsCachedPerTermForAWhile(t *testing.T) {
	e := newMatchEnv(t, nil)
	for range 3 {
		e.ok("user1", "GET", "/api/lookup/movies?term=Matrix", "", nil)
	}
	e.ok("user2", "GET", "/api/lookup/movies?term=%20matrix", "", nil) // same term, shared
	if n := e.movies.lookups.Load(); n != 1 {
		t.Errorf("lookups = %d, want 1", n)
	}
	e.ok("user1", "GET", "/api/lookup/movies?term=other", "", nil)
	if n := e.movies.lookups.Load(); n != 2 {
		t.Errorf("lookups = %d, want 2", n)
	}
	// A failure is not cached.
	e.movies.Err = map[string]error{"Lookup": errors.New("down")}
	e.refused("user1", "GET", "/api/lookup/movies?term=fail", "", 502, "arr_unavailable")
	e.movies.Err = nil
	e.ok("user1", "GET", "/api/lookup/movies?term=fail", "", nil)

	off := newMatchEnv(t, func(c *config.Config) { c.Match.LookupCacheTTL = 0 })
	off.ok("user1", "GET", "/api/lookup/movies?term=a", "", nil)
	off.ok("user1", "GET", "/api/lookup/movies?term=a", "", nil)
	if n := off.movies.lookups.Load(); n != 2 {
		t.Errorf("with the cache off: lookups = %d, want 2", n)
	}
}

func TestMatchAddsANewFilmOnceWithSearchOffAndTheTag(t *testing.T) {
	e := newMatchEnv(t, nil)
	j := e.job("user1", store.Probed)
	path := "/api/jobs/" + j.ID + "/match"
	body := `{"app":"radarr","tmdbId":603,"qualityProfileId":4,"rootFolder":"/data/media/movies","monitored":false}`
	var got matchedJob
	e.ok("user1", "POST", path, body, &got)
	if got.State != "matched" {
		t.Fatalf("state = %q", got.State)
	}
	// Repeating the request (a double click, a retry) asks the app first and adds nothing.
	e.ok("user1", "POST", path, body, &got)
	if len(e.movies.Added) != 1 {
		t.Fatalf("Added = %+v, want one call", e.movies.Added)
	}
	a := e.movies.Added[0]
	if a.TMDBID != 603 || a.QualityProfileID != 4 || a.RootFolder != "/data/media/movies" || a.Monitored || a.Tag != "fukku" {
		t.Errorf("Add = %+v", a)
	}
	d := e.detail("user1", j)
	if d.State != "matched" || d.Target == nil || d.Target.App != "radarr" || d.Target.Title != "The Matrix" ||
		d.Target.Year != 1999 || d.Target.TitleID != e.movies.Library[2].ID {
		t.Errorf("detail = %+v %+v", d, d.Target)
	}
	if got := audits(t, e, j.ID); len(got) != 2 || got[0] != "job.matched" {
		t.Errorf("audit = %v", got)
	}
	// The stored target records what was applied.
	tg, err := e.store.Target(context.Background(), j.ID)
	if err != nil || tg.MatchedBy != "user1" || !strings.Contains(string(tg.Options), `"added":true`) {
		t.Errorf("target = %+v, %v", tg, err)
	}
	// An admin sees it too; somebody else does not.
	e.ok("admin1", "GET", "/api/jobs/"+j.ID, "", nil)
	e.refused("user2", "GET", "/api/jobs/"+j.ID, "", 404, "not_found")
}

func TestMatchUsesTheSettingsForWhatTheRequestLeavesOut(t *testing.T) {
	e := newMatchEnv(t, func(c *config.Config) { c.Radarr.MonitorAfterImport = false })
	j := e.job("user1", store.Probed)
	e.ok("user1", "POST", "/api/jobs/"+j.ID+"/match", `{"app":"radarr","tmdbId":603}`, nil)
	a := e.movies.Added[0]
	if a.QualityProfileID != 4 || a.RootFolder != "/data/media/movies" || a.Monitored {
		t.Errorf("Add = %+v", a)
	}

	// No default profile set and none chosen: the user must pick.
	e2 := newMatchEnv(t, func(c *config.Config) { c.Radarr.QualityProfile = "" })
	j2 := e2.job("user1", store.Probed)
	e2.refused("user1", "POST", "/api/jobs/"+j2.ID+"/match", `{"app":"radarr","tmdbId":603}`, 400, "bad_request")
	if len(e2.movies.Added) != 0 {
		t.Error("added without a profile")
	}
}

func TestMatchChecksProfileAndRootFolderAgainstTheApp(t *testing.T) {
	e := newMatchEnv(t, nil)
	j := e.job("user1", store.Probed)
	path := "/api/jobs/" + j.ID + "/match"
	for _, body := range []string{
		`{"app":"radarr","tmdbId":603,"rootFolder":"/etc"}`,
		`{"app":"radarr","tmdbId":603,"rootFolder":"/data/media/movies/../../.."}`,
		`{"app":"radarr","tmdbId":603,"rootFolder":"/data/media/movies/"}`,
		`{"app":"radarr","tmdbId":603,"qualityProfileId":99}`,
		`{"app":"radarr","tmdbId":0}`,
		`{"app":"radarr","tvdbId":1}`,
		`{"app":"sonarr","tmdbId":603}`,
		`{"app":"jellyfin","tmdbId":603}`,
		`{"app":"radarr","tmdbId":603,"title":"Injected","path":"/etc"}`, // unknown fields
		`not json`,
	} {
		e.refused("user1", "POST", path, body, 400, "bad_request")
	}
	if len(e.movies.Added) != 0 {
		t.Errorf("Added = %+v", e.movies.Added)
	}
	e.refused("user1", "POST", path, `{"app":"radarr","tmdbId":12345}`, 404, "title_not_found")
}

func TestMatchAnExistingFilmAddsNothing(t *testing.T) {
	e := newMatchEnv(t, nil)
	j := e.job("user1", store.Probed)
	e.ok("user1", "POST", "/api/jobs/"+j.ID+"/match", `{"app":"radarr","tmdbId":604}`, nil)
	if len(e.movies.Added) != 0 {
		t.Errorf("Added = %+v", e.movies.Added)
	}
	if d := e.detail("user1", j); d.Target == nil || d.Target.TitleID != 7 || d.State != "matched" {
		t.Errorf("detail = %+v", d)
	}
}

func TestMatchRefusals(t *testing.T) {
	e := newMatchEnv(t, nil)
	radarr := func(id int) string { return `{"app":"radarr","tmdbId":` + itoa(id) + `}` }

	// 3: the film already has a file (flow 2, M3).
	j := e.job("user1", store.Probed)
	e.refused("user1", "POST", "/api/jobs/"+j.ID+"/match", radarr(605), 409, "already_in_library")

	// 4: another active job holds the title; so does a failed one (it can be retried).
	other := e.job("user2", store.Matched)
	if err := e.store.SetTarget(context.Background(), other.ID, store.Target{App: "radarr", TitleID: 7, Title: "Library Film"}); err != nil {
		t.Fatal(err)
	}
	e.refused("user1", "POST", "/api/jobs/"+j.ID+"/match", radarr(604), 409, "title_in_use")
	// A cancelled job releases it.
	if _, err := e.store.Transition(context.Background(), other.ID, store.Matched, store.Cancelled, store.Change{}); err != nil {
		t.Fatal(err)
	}
	e.movies.Queue[7] = true
	e.refused("user1", "POST", "/api/jobs/"+j.ID+"/match", radarr(604), 409, "title_busy")
	e.movies.Queue[7] = false

	// An app that fails while it is asked.
	e.movies.Err = map[string]error{"Busy": errors.New("timeout")}
	e.refused("user1", "POST", "/api/jobs/"+j.ID+"/match", radarr(604), 502, "arr_unavailable")
	e.movies.Err = map[string]error{"Add": errors.New("500")}
	e.refused("user1", "POST", "/api/jobs/"+j.ID+"/match", radarr(603), 502, "arr_unavailable")
	e.movies.Err = map[string]error{"Lookup": errors.New("500")}
	e.refused("user1", "POST", "/api/jobs/"+j.ID+"/match", radarr(603), 502, "arr_unavailable")
	if d := e.detail("user1", j); d.State != "probed" || d.Target != nil {
		t.Errorf("a refused match changed the job: %+v", d)
	}

	// 1: wrong state; somebody else's job; no Origin.
	for _, st := range []store.State{store.Uploading, store.Probing, store.Importing} {
		w := e.job("user1", st)
		if st == store.Importing { // not reachable through the helper: walk it
			w = e.job("user1", store.AwaitingConfirmation)
			if _, err := e.store.Transition(context.Background(), w.ID, store.AwaitingConfirmation, store.Importing, store.Change{}); err != nil {
				t.Fatal(err)
			}
		}
		e.refused("user1", "POST", "/api/jobs/"+w.ID+"/match", radarr(604), 409, "wrong_state")
	}
	e.refused("user2", "POST", "/api/jobs/"+j.ID+"/match", radarr(604), 404, "not_found")
	e.refused("user1", "POST", "/api/jobs/nothex/match", radarr(604), 404, "not_found")
	r := httptest.NewRequestWithContext(t.Context(), "POST", "https://fukku.example.com/api/jobs/"+j.ID+"/match", strings.NewReader(radarr(604)))
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("Remote-User", "user1")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Errorf("no Origin: %d", rec.Code)
	}

	// 2: the app is not configured.
	off := newMatchEnv(t, func(c *config.Config) { c.Sonarr.URL = "" })
	oj := off.job("user1", store.Probed)
	off.refused("user1", "POST", "/api/jobs/"+oj.ID+"/match", `{"app":"sonarr","tvdbId":1}`, 404, "app_not_configured")
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestMatchSeries(t *testing.T) {
	e := newMatchEnv(t, nil)
	j := e.job("user1", store.Probed)
	path := "/api/jobs/" + j.ID + "/match"
	e.refused("user1", "POST", path, `{"app":"sonarr","tvdbId":1,"qualityProfileId":9,"seriesType":"movie"}`, 400, "bad_request")
	e.refused("user1", "POST", path, `{"app":"sonarr","tvdbId":1,"qualityProfileId":9,"rootFolder":"/tmp"}`, 400, "bad_request")
	e.refused("user1", "POST", path, `{"app":"sonarr","tvdbId":1}`, 400, "bad_request") // no default profile
	body := `{"app":"sonarr","tvdbId":1,"qualityProfileId":9,"seriesType":"anime"}`
	e.ok("user1", "POST", path, body, nil)
	e.ok("user1", "POST", path, body, nil)
	if len(e.series.Added) != 1 {
		t.Fatalf("Added = %+v", e.series.Added)
	}
	a := e.series.Added[0]
	if a.TVDBID != 1 || a.QualityProfileID != 9 || a.RootFolder != "/data/media/tv" || a.Type != "anime" || a.Tag != "fukku" {
		t.Errorf("Add = %+v", a)
	}
	if d := e.detail("user1", j); d.Target == nil || d.Target.App != "sonarr" || d.Target.Title != "A Show" {
		t.Errorf("detail = %+v", d)
	}

	// A show that is in the library already; another job holds it, then the app is busy.
	j2 := e.job("user1", store.Probed)
	p2 := "/api/jobs/" + j2.ID + "/match"
	e.ok("user1", "POST", p2, `{"app":"sonarr","tvdbId":2}`, nil)
	j3 := e.job("user2", store.Probed)
	e.refused("user2", "POST", "/api/jobs/"+j3.ID+"/match", `{"app":"sonarr","tvdbId":2}`, 409, "title_in_use")
	e.series.Queue[20] = true
	if _, err := e.store.Transition(context.Background(), j2.ID, store.Matched, store.Cancelled, store.Change{}); err != nil {
		t.Fatal(err)
	}
	e.refused("user2", "POST", "/api/jobs/"+j3.ID+"/match", `{"app":"sonarr","tvdbId":2}`, 409, "title_busy")
	if len(e.series.Added) != 1 {
		t.Errorf("Added = %+v", e.series.Added)
	}
}

func TestChoosingAgainReplacesTheTargetAndTheRows(t *testing.T) {
	e := newMatchEnv(t, nil)
	ctx := context.Background()
	j := e.job("user1", store.AwaitingConfirmation)
	f, err := e.store.AddFile(ctx, store.File{JobID: j.ID, Path: "files/a.mkv", Kind: store.KindVideo, Status: store.FileOK})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetTarget(ctx, j.ID, store.Target{App: "radarr", TitleID: 7, Title: "Library Film"}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.PutRows(ctx, j.ID, []store.Row{{ID: "r1", FileID: f.ID}}); err != nil {
		t.Fatal(err)
	}
	var got matchedJob
	e.ok("user1", "POST", "/api/jobs/"+j.ID+"/match", `{"app":"sonarr","tvdbId":2}`, &got)
	if got.State != "matched" {
		t.Errorf("state = %q", got.State)
	}
	if d := e.detail("user1", j); d.Target == nil || d.Target.App != "sonarr" || d.Target.TitleID != 20 {
		t.Errorf("target = %+v", d.Target)
	}
	if rows, _ := e.store.Rows(ctx, j.ID); len(rows) != 0 {
		t.Errorf("rows = %+v", rows)
	}
	// From matched, once more.
	e.ok("user1", "POST", "/api/jobs/"+j.ID+"/match", `{"app":"radarr","tmdbId":604}`, &got)
	if d := e.detail("user1", j); d.State != "matched" || d.Target.App != "radarr" {
		t.Errorf("detail = %+v %+v", d, d.Target)
	}
	// Choosing the same title that Fukku added keeps the record of having added it.
	j2 := e.job("user1", store.Probed)
	e.ok("user1", "POST", "/api/jobs/"+j2.ID+"/match", `{"app":"radarr","tmdbId":603,"qualityProfileId":4}`, nil)
	e.ok("user1", "POST", "/api/jobs/"+j2.ID+"/match", `{"app":"radarr","tmdbId":603}`, nil)
	if tg, _ := e.store.Target(ctx, j2.ID); !strings.Contains(string(tg.Options), `"added":true`) {
		t.Errorf("options = %s", tg.Options)
	}
}

func TestUnmatchReturnsToProbed(t *testing.T) {
	e := newMatchEnv(t, nil)
	j := e.job("user1", store.Probed)
	path := "/api/jobs/" + j.ID + "/match"
	e.refused("user1", "DELETE", path, "", 409, "wrong_state") // nothing to undo
	e.ok("user1", "POST", path, `{"app":"radarr","tmdbId":604}`, nil)
	e.refused("user2", "DELETE", path, "", 404, "not_found")
	var got matchedJob
	e.ok("user1", "DELETE", path, "", &got)
	if got.State != "probed" {
		t.Errorf("state = %q", got.State)
	}
	if d := e.detail("user1", j); d.Target != nil {
		t.Errorf("target = %+v", d.Target)
	}
	if got := audits(t, e, j.ID); len(got) != 2 || got[1] != "job.unmatched" {
		t.Errorf("audit = %v", got)
	}
	// From awaiting-confirmation too; the title is free again.
	w := e.job("user1", store.AwaitingConfirmation)
	if err := e.store.SetTarget(context.Background(), w.ID, store.Target{App: "radarr", TitleID: 7}); err != nil {
		t.Fatal(err)
	}
	e.ok("user1", "DELETE", "/api/jobs/"+w.ID+"/match", "", &got)
	if got.State != "probed" {
		t.Errorf("state = %q", got.State)
	}
	if jobs, _ := e.store.ActiveJobsFor(context.Background(), "radarr", 7); len(jobs) != 0 {
		t.Errorf("title still held: %+v", jobs)
	}
	// Without Origin.
	r := httptest.NewRequestWithContext(t.Context(), "DELETE", "https://fukku.example.com"+path, nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("Remote-User", "user1")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Errorf("no Origin: %d", rec.Code)
	}
}

// The client goes away after the app added the title: the match is stored, and so are its
// record and its audit entry.
func TestAMatchIsRecordedWhenTheClientGoesAway(t *testing.T) {
	e := newMatchEnv(t, nil)
	j := e.job("user1", store.Probed)
	ctx, cancel := context.WithCancel(t.Context())
	e.movies.onAdd = cancel
	r := httptest.NewRequestWithContext(ctx, "POST", "https://fukku.example.com/api/jobs/"+j.ID+"/match",
		strings.NewReader(`{"app":"radarr","tmdbId":603,"qualityProfileId":4}`))
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Remote-User", "user1")
	r.Header.Set("Remote-Groups", "users")
	r.Header.Set("Origin", "https://fukku.example.com")
	e.h.ServeHTTP(httptest.NewRecorder(), r)

	got, err := e.store.Job(context.Background(), j.ID)
	if err != nil || got.State != store.Matched {
		t.Fatalf("job = %+v, %v", got, err)
	}
	if tg, err := e.store.Target(context.Background(), j.ID); err != nil || tg.Title != "The Matrix" {
		t.Errorf("target = %+v, %v", tg, err)
	}
	if a := audits(t, e, j.ID); len(a) != 1 || a[0] != "job.matched" {
		t.Errorf("audit = %v", a)
	}
}
