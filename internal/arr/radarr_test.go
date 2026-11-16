package arr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
)

const testKey = "fake-credential-for-tests"

// call is one request the fake app received.
type call struct {
	Method, Path, RawQuery, Key string
	Body                        []byte
}

func (c call) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(c.Body, &m); err != nil {
		t.Fatalf("request body %q: %v", c.Body, err)
	}
	return m
}

// fakeApp answers like the pinned app: each route is "METHOD /api/v3/path" and maps to a
// recorded body in testdata (status 200 unless the route says otherwise).
type fakeApp struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string]reply
	calls  []call
}

type reply struct {
	status int
	file   string // under testdata/
	body   string // literal, when file is empty
	delay  time.Duration
}

func newApp(t *testing.T, routes map[string]reply) (*fakeApp, arr.MovieManager) {
	t.Helper()
	app := &fakeApp{t: t, routes: routes}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return app, arr.NewRadarr(cfg(srv.URL, 5*time.Second))
}

func cfg(url string, timeout time.Duration) config.Arr {
	return config.Arr{URL: url, APIKey: config.Secret(testKey), RequestTimeout: config.Duration(timeout)}
}

func (a *fakeApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	a.calls = append(a.calls, call{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Api-Key"), body})
	rep, ok := a.routes[r.Method+" "+r.URL.Path]
	a.mu.Unlock()
	if !ok {
		http.Error(w, `{"message":"no such route"}`, http.StatusNotFound)
		return
	}
	time.Sleep(rep.delay)
	data := []byte(rep.body)
	if rep.file != "" {
		var err error
		if data, err = os.ReadFile(filepath.Join("testdata", rep.file)); err != nil {
			a.t.Errorf("testdata: %v", err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if rep.status != 0 {
		w.WriteHeader(rep.status)
	}
	_, _ = w.Write(data)
}

func (a *fakeApp) last() call {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[len(a.calls)-1]
}

func (a *fakeApp) find(method, path string) []call {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []call
	for _, c := range a.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

func ok(file string) reply { return reply{file: file} }

var ctx = context.Background()

func TestStatusProfilesRootFolders(t *testing.T) {
	app, r := newApp(t, map[string]reply{
		"GET /api/v3/system/status":  ok("system_status.json"),
		"GET /api/v3/qualityprofile": ok("qualityprofile.json"),
		"GET /api/v3/rootfolder":     ok("rootfolder.json"),
	})
	st, err := r.Status(ctx)
	if err != nil || st != (arr.Status{AppName: "Radarr", Version: "6.4.4.10099"}) {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	if k := app.last().Key; k != testKey {
		t.Errorf("X-Api-Key not sent: %q", k)
	}
	ps, err := r.Profiles(ctx)
	if err != nil || len(ps) != 2 || ps[1] != (arr.Profile{ID: 4, Name: "Fukku Probe", MinFormatScore: -100}) {
		t.Fatalf("Profiles = %+v, %v", ps, err)
	}
	fs, err := r.RootFolders(ctx)
	if err != nil || len(fs) != 1 || fs[0] != (arr.RootFolder{Path: "/data/media/movies", FreeSpace: 53687091200}) {
		t.Fatalf("RootFolders = %+v, %v", fs, err)
	}
}

func TestLookup(t *testing.T) {
	app, r := newApp(t, map[string]reply{"GET /api/v3/movie/lookup": ok("movie_lookup.json")})
	ms, err := r.Lookup(ctx, "tmdb:10378")
	if err != nil || len(ms) != 2 {
		t.Fatalf("Lookup = %+v, %v", ms, err)
	}
	if q := app.last().RawQuery; q != "term=tmdb%3A10378" {
		t.Errorf("query = %q", q)
	}
	want := arr.Movie{TMDBID: 10378, Title: "Big Buck Bunny", Year: 2008, OriginalLanguage: "English"}
	if ms[0] != want {
		t.Errorf("movie = %+v, want %+v", ms[0], want)
	}
	if ms[1].ID != 0 || ms[1].File != nil {
		t.Errorf("a lookup result must have no id and no file: %+v", ms[1])
	}
}

func TestMovieTrackedFileComesFromTheMovie(t *testing.T) {
	app, r := newApp(t, map[string]reply{
		"GET /api/v3/movie/7": ok("movie.json"),
		"GET /api/v3/movie/8": ok("movie_no_file.json"),
	})
	m, err := r.Movie(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != 7 || m.TMDBID != 10378 || !m.Monitored || m.Path != "/data/media/movies/Big Buck Bunny (2008)" {
		t.Errorf("movie = %+v", m)
	}
	f := m.File
	if f == nil || f.ID != 12 || f.Size != 123456789 || f.Quality != (arr.Quality{ID: 3, Name: "WEBDL-1080p"}) ||
		f.Path != "/data/media/movies/Big Buck Bunny (2008)/Big Buck Bunny (2008) WEBDL-1080p.mkv" ||
		len(f.Languages) != 1 || f.Languages[0].Name != "English" || f.RuntimeMs != 540_000 {
		t.Errorf("file = %+v", f)
	}
	if len(app.find("GET", "/api/v3/moviefile")) != 0 {
		t.Error("the tracked file must not come from /moviefile")
	}
	m, err = r.Movie(ctx, 8)
	if err != nil || m.File != nil {
		t.Errorf("a film with no file: %+v, %v", m, err)
	}
}

func TestAddCreatesTheTagAndNeverSearches(t *testing.T) {
	app, r := newApp(t, map[string]reply{
		"GET /api/v3/movie/lookup/tmdb": ok("movie_lookup_tmdb.json"),
		"GET /api/v3/tag":               ok("tags.json"),
		"POST /api/v3/tag":              ok("tag_created.json"),
		"POST /api/v3/movie":            ok("movie_added.json"),
	})
	m, err := r.Add(ctx, arr.AddMovie{TMDBID: 10378, QualityProfileID: 4, RootFolder: "/data/media/movies", Monitored: true, Tag: "Fukku"})
	if err != nil || m.ID != 9 || m.Path == "" {
		t.Fatalf("Add = %+v, %v", m, err)
	}
	if q := app.find("GET", "/api/v3/movie/lookup/tmdb")[0].RawQuery; q != "tmdbId=10378" {
		t.Errorf("lookup query = %q", q)
	}
	if got := app.find("POST", "/api/v3/tag")[0].json(t); got["label"] != "fukku" {
		t.Errorf("tag body = %v", got)
	}
	body := app.find("POST", "/api/v3/movie")[0].json(t)
	opts, _ := body["addOptions"].(map[string]any)
	if opts["searchForMovie"] != false {
		t.Errorf("addOptions = %v: the search must be off", opts)
	}
	if body["qualityProfileId"] != float64(4) || body["rootFolderPath"] != "/data/media/movies" || body["monitored"] != true ||
		body["tmdbId"] != float64(10378) || body["title"] != "Big Buck Bunny" {
		t.Errorf("body = %v", body)
	}
	if tags, _ := body["tags"].([]any); len(tags) != 1 || tags[0] != float64(2) {
		t.Errorf("tags = %v, want the created tag 2", body["tags"])
	}
}

func TestAddSendsTheConfiguredMinimumAvailability(t *testing.T) {
	for cfgValue, want := range map[string]string{"": "released", "inCinemas": "inCinemas"} {
		app := &fakeApp{t: t, routes: map[string]reply{
			"GET /api/v3/movie/lookup/tmdb": ok("movie_lookup_tmdb.json"),
			"POST /api/v3/movie":            ok("movie_added.json"),
		}}
		srv := httptest.NewServer(app)
		c := cfg(srv.URL, time.Second)
		c.MinimumAvailability = cfgValue
		if _, err := arr.NewRadarr(c).Add(ctx, arr.AddMovie{TMDBID: 10378, QualityProfileID: 4, RootFolder: "/m"}); err != nil {
			t.Fatal(err)
		}
		srv.Close()
		if got := app.find("POST", "/api/v3/movie")[0].json(t)["minimumAvailability"]; got != want {
			t.Errorf("setting %q: minimumAvailability = %v, want %s", cfgValue, got, want)
		}
	}
}

func TestAddReusesAnExistingTag(t *testing.T) {
	app, r := newApp(t, map[string]reply{
		"GET /api/v3/movie/lookup/tmdb": ok("movie_lookup_tmdb.json"),
		"GET /api/v3/tag":               {body: `[{"id":5,"label":"fukku"}]`},
		"POST /api/v3/movie":            ok("movie_added.json"),
	})
	if _, err := r.Add(ctx, arr.AddMovie{TMDBID: 10378, QualityProfileID: 4, RootFolder: "/m", Tag: "fukku"}); err != nil {
		t.Fatal(err)
	}
	if n := len(app.find("POST", "/api/v3/tag")); n != 0 {
		t.Errorf("created the tag again (%d POSTs)", n)
	}
	if tags, _ := app.find("POST", "/api/v3/movie")[0].json(t)["tags"].([]any); len(tags) != 1 || tags[0] != float64(5) {
		t.Errorf("tags = %v", tags)
	}
}

func TestAddWithoutTagTouchesNoTags(t *testing.T) {
	app, r := newApp(t, map[string]reply{
		"GET /api/v3/movie/lookup/tmdb": ok("movie_lookup_tmdb.json"),
		"POST /api/v3/movie":            ok("movie_added.json"),
	})
	if _, err := r.Add(ctx, arr.AddMovie{TMDBID: 10378, QualityProfileID: 4, RootFolder: "/m"}); err != nil {
		t.Fatal(err)
	}
	if n := len(app.find("GET", "/api/v3/tag")); n != 0 {
		t.Errorf("%d tag requests", n)
	}
}

func TestProposalNeverSendsATitleID(t *testing.T) {
	app, r := newApp(t, map[string]reply{"GET /api/v3/manualimport": ok("manualimport.json")})
	rows, err := r.Proposal(ctx, "/data/intake/job1")
	if err != nil || len(rows) != 1 {
		t.Fatalf("Proposal = %+v, %v", rows, err)
	}
	q := app.last().RawQuery
	if q != "filterExistingFiles=false&folder=%2Fdata%2Fintake%2Fjob1" {
		t.Errorf("query = %q", q)
	}
	if strings.Contains(strings.ToLower(q), "movieid") {
		t.Error("a title id in the proposal query makes the app ignore the folder (C6)")
	}
	row := rows[0]
	if row.Path != "/data/intake/job1/Big.Buck.Bunny.2008.1080p.WEB-DL.mkv" || row.Size != 987654321 || row.MovieID != 0 ||
		row.Quality != (arr.Quality{ID: 3, Name: "WEBDL-1080p"}) || len(row.Languages) != 1 ||
		row.CustomFormatScore != -50 || len(row.CustomFormats) != 1 || row.CustomFormats[0] != "Not Original Language" {
		t.Errorf("row = %+v", row)
	}
	if len(row.Rejections) != 2 || !row.Rejections[0].Permanent || row.Rejections[1].Permanent ||
		row.Rejections[1].Reason != "Not enough free space" {
		t.Errorf("rejections = %+v", row.Rejections)
	}
}

// Radarr matches the title from the file name itself, so a proposal row can already carry
// the film (as "movie", with no "movieId" key) before any re-check.
func TestProposalRowPrematchedByTheApp(t *testing.T) {
	_, r := newApp(t, map[string]reply{"GET /api/v3/manualimport": ok("manualimport_matched.json")})
	rows, err := r.Proposal(ctx, "/data/intake/job1")
	if err != nil || len(rows) != 1 || rows[0].MovieID != 35 || len(rows[0].Rejections) != 0 {
		t.Errorf("rows = %+v, %v", rows, err)
	}
}

func TestRecheckAndImportKeepUnknownFields(t *testing.T) {
	app, r := newApp(t, map[string]reply{
		"GET /api/v3/manualimport":  ok("manualimport.json"),
		"POST /api/v3/manualimport": ok("manualimport_recheck.json"),
		"POST /api/v3/command":      ok("command_queued.json"),
	})
	rows, err := r.Proposal(ctx, "/data/intake/job1")
	if err != nil {
		t.Fatal(err)
	}
	rows[0].MovieID = 7
	checked, err := r.Recheck(ctx, rows)
	if err != nil || len(checked) != 1 {
		t.Fatalf("Recheck = %+v, %v", checked, err)
	}

	sent := app.find("POST", "/api/v3/manualimport")[0]
	var items []map[string]any
	if err := json.Unmarshal(sent.Body, &items); err != nil || len(items) != 1 {
		t.Fatalf("recheck body %s: %v", sent.Body, err)
	}
	if items[0]["movieId"] != float64(7) {
		t.Errorf("recheck must send the film: %v", items[0]["movieId"])
	}
	assertUnknownField(t, items[0])
	if items[0]["rejections"] == nil || items[0]["id"] != float64(1001) {
		t.Errorf("the app's own fields were dropped: %v", items[0])
	}

	// The fresh verdict replaces the old one; MovieID comes from the item's movie.
	if checked[0].MovieID != 7 || len(checked[0].Rejections) != 1 ||
		checked[0].Rejections[0].Reason != "Not an upgrade for existing movie file" || checked[0].CustomFormatScore != 0 {
		t.Errorf("checked = %+v", checked[0])
	}

	// Edits written over Raw: another quality, languages and release group.
	checked[0].Quality = arr.Quality{ID: 7, Name: "Bluray-1080p"}
	checked[0].Languages = []arr.Language{{ID: 2, Name: "French"}}
	checked[0].ReleaseGroup = "GRP"
	id, err := r.Import(ctx, checked, "move")
	if err != nil || id != 41 {
		t.Fatalf("Import = %d, %v", id, err)
	}
	cmd := app.find("POST", "/api/v3/command")[0].json(t)
	if cmd["name"] != "ManualImport" || cmd["importMode"] != "move" {
		t.Errorf("command = %v", cmd)
	}
	files, _ := cmd["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files = %v", cmd["files"])
	}
	f := files[0].(map[string]any)
	assertUnknownField(t, f)
	if f["movieId"] != float64(7) || f["releaseGroup"] != "GRP" {
		t.Errorf("file = %v", f)
	}
	q := f["quality"].(map[string]any)
	inner := q["quality"].(map[string]any)
	if inner["id"] != float64(7) || inner["name"] != "Bluray-1080p" || inner["source"] != "webdl" || q["revision"] == nil {
		t.Errorf("quality = %v (id and name are edited, the rest is kept)", q)
	}
	if ls, _ := f["languages"].([]any); len(ls) != 1 || ls[0].(map[string]any)["name"] != "French" {
		t.Errorf("languages = %v", f["languages"])
	}
	if f["path"] != "/data/intake/job1/Big.Buck.Bunny.2008.1080p.WEB-DL.mkv" {
		t.Errorf("path = %v", f["path"])
	}
}

func assertUnknownField(t *testing.T, item map[string]any) {
	t.Helper()
	u, _ := item["futureUnknownField"].(map[string]any)
	if keep, _ := u["keep"].([]any); len(keep) != 2 || keep[0] != "me" {
		t.Errorf("a field Fukku does not model was lost: %v", item["futureUnknownField"])
	}
}

func TestRecheckAndImportNeedRows(t *testing.T) {
	_, r := newApp(t, nil)
	if rows, err := r.Recheck(ctx, nil); err != nil || rows != nil {
		t.Errorf("Recheck(nil) = %v, %v", rows, err)
	}
	if _, err := r.Import(ctx, nil, "move"); err == nil {
		t.Error("Import with no files must fail")
	}
}

func TestRowWithoutRawIsEncoded(t *testing.T) {
	app, r := newApp(t, map[string]reply{"POST /api/v3/manualimport": {body: "[]"}})
	_, err := r.Recheck(ctx, []arr.Row{{Path: "/data/intake/x.mkv", MovieID: 3, Quality: arr.Quality{ID: 3, Name: "WEBDL-1080p"}}})
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(app.last().Body, &items); err != nil || items[0]["movieId"] != float64(3) || items[0]["path"] != "/data/intake/x.mkv" {
		t.Errorf("body = %s, %v", app.last().Body, err)
	}
}

func TestCommandAndCommands(t *testing.T) {
	_, r := newApp(t, map[string]reply{
		"GET /api/v3/command/41": ok("command_completed.json"),
		"GET /api/v3/command":    ok("commands.json"),
	})
	c, err := r.Command(ctx, 41)
	if err != nil || c != (arr.Command{ID: 41, Name: "ManualImport", Status: "completed", Result: "successful", Message: "Completed"}) {
		t.Fatalf("Command = %+v, %v", c, err)
	}
	cs, err := r.Commands(ctx)
	if err != nil || len(cs) != 2 || cs[1].Status != "failed" || cs[1].Message != "Destination already exists" {
		t.Fatalf("Commands = %+v, %v", cs, err)
	}
}

func TestBusy(t *testing.T) {
	app, r := newApp(t, map[string]reply{"GET /api/v3/queue/details": ok("queue_details_busy.json")})
	busy, err := r.Busy(ctx, 7)
	if err != nil || !busy {
		t.Fatalf("Busy = %v, %v", busy, err)
	}
	if q := app.last().RawQuery; q != "movieId=7" {
		t.Errorf("query = %q", q)
	}
	app.mu.Lock()
	app.routes["GET /api/v3/queue/details"] = reply{body: "[]"}
	app.mu.Unlock()
	if busy, err := r.Busy(ctx, 7); err != nil || busy {
		t.Errorf("Busy with an empty queue = %v, %v", busy, err)
	}
}

func TestSetMonitored(t *testing.T) {
	app, r := newApp(t, map[string]reply{"PUT /api/v3/movie/editor": {body: "[]"}})
	if err := r.SetMonitored(ctx, 7, false); err != nil {
		t.Fatal(err)
	}
	got := app.last().json(t)
	ids, _ := got["movieIds"].([]any)
	if len(ids) != 1 || ids[0] != float64(7) || got["monitored"] != false {
		t.Errorf("body = %v", got)
	}
}

// ---- errors, timeouts, the key -----------------------------------------------------------

func TestAppErrorKeepsStatusAndMessageButNotTheKey(t *testing.T) {
	cases := []struct {
		name  string
		reply reply
		want  string
	}{
		{"message", reply{status: 401, body: `{"message":"Unauthorized"}`}, "Unauthorized"},
		{"validation", reply{status: 400, file: "error_validation.json"}, "Path is not a valid folder"},
		{"plain text", reply{status: 502, body: "bad gateway"}, "bad gateway"},
		{"echoed key", reply{status: 403, body: `{"message":"invalid key ` + testKey + `"}`}, "invalid key ***"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, r := newApp(t, map[string]reply{"GET /api/v3/system/status": tc.reply})
			_, err := r.Status(ctx)
			var ae *arr.Error
			if !errors.As(err, &ae) {
				t.Fatalf("err = %v, want *arr.Error", err)
			}
			if ae.Status != tc.reply.status || arr.StatusOf(err) != tc.reply.status {
				t.Errorf("status = %d", ae.Status)
			}
			if !strings.Contains(ae.Message, tc.want) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message = %q, error = %q, want %q", ae.Message, err, tc.want)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("the key is in the error: %v", err)
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	r := arr.NewRadarr(cfg(srv.URL, 50*time.Millisecond))
	start := time.Now()
	_, err := r.Status(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("the request outlived its timeout")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Error("the key is in the error")
	}
}

func TestCallerCancelIsNotATimeout(t *testing.T) {
	_, r := newApp(t, map[string]reply{"GET /api/v3/system/status": {body: "{}", delay: time.Second}})
	c, cancel := context.WithCancel(ctx)
	cancel()
	_, err := r.Status(c)
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v", err)
	}
}

func TestUnreachableAppErrorHasNoKey(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	r := arr.NewRadarr(cfg(url, time.Second))
	_, err := r.Status(ctx)
	if err == nil || strings.Contains(err.Error(), testKey) || !errors.Is(err, arr.ErrUnreachable) {
		t.Errorf("err = %v", err)
	}
}

func TestNoURL(t *testing.T) {
	r := arr.NewRadarr(config.Arr{APIKey: testKey})
	if _, err := r.Status(ctx); err == nil || !strings.Contains(err.Error(), "no url") {
		t.Errorf("err = %v", err)
	}
}

func TestKeyAppearsInNoLogLineAndNoError(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	var errs []error
	_, r := newApp(t, map[string]reply{
		"GET /api/v3/system/status":  ok("system_status.json"),
		"GET /api/v3/movie/7":        ok("movie.json"),
		"GET /api/v3/qualityprofile": {status: 401, body: `{"message":"bad key ` + testKey + `"}`},
		"GET /api/v3/rootfolder":     {status: 500, body: "boom"},
	})
	_, e1 := r.Status(ctx)
	_, e2 := r.Movie(ctx, 7)
	_, e3 := r.Profiles(ctx)
	_, e4 := r.RootFolders(ctx)
	_, e5 := r.Command(ctx, 1) // no route: 404
	errs = append(errs, e1, e2, e3, e4, e5)
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	_, e6 := arr.NewRadarr(cfg(url, time.Second)).Status(ctx)
	errs = append(errs, e6)

	if logs.Len() == 0 {
		t.Fatal("expected debug log lines, so that this test can prove something")
	}
	if strings.Contains(logs.String(), testKey) {
		t.Errorf("the key is in the log:\n%s", logs.String())
	}
	for i, err := range errs {
		if err != nil && strings.Contains(err.Error(), testKey) {
			t.Errorf("error %d contains the key: %v", i, err)
		}
	}
	// The configuration prints without the key too.
	if s := (config.Arr{APIKey: testKey}).APIKey.String(); strings.Contains(s, testKey) {
		t.Errorf("Secret prints the key: %s", s)
	}
}

func TestQualitiesAndLanguages(t *testing.T) {
	_, r := newApp(t, map[string]reply{
		"GET /api/v3/qualitydefinition": {body: `[
			{"id": 1, "quality": {"id": 0, "name": "Unknown", "source": "unknown", "resolution": 0}, "title": "Unknown", "weight": 1},
			{"id": 9, "quality": {"id": 3, "name": "WEBDL-1080p", "source": "webdl", "resolution": 1080}, "title": "WEBDL-1080p", "weight": 15}]`},
		"GET /api/v3/language": {body: `[{"id": 1, "name": "English", "nameLower": "english"}, {"id": 3, "name": "Spanish", "nameLower": "spanish"}]`},
	})
	qs, err := r.Qualities(ctx)
	if err != nil || len(qs) != 2 || qs[1] != (arr.Quality{ID: 3, Name: "WEBDL-1080p"}) {
		t.Errorf("Qualities = %+v, %v", qs, err)
	}
	ls, err := r.Languages(ctx)
	if err != nil || len(ls) != 2 || ls[1] != (arr.Language{ID: 3, Name: "Spanish"}) {
		t.Errorf("Languages = %+v, %v", ls, err)
	}
}
