package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

type proposalEnv struct {
	t      *testing.T
	h      http.Handler
	st     *storetest.Fake
	series *arrtest.FakeSeries
	p      *flows.Proposals
}

func newProposalEnv(t *testing.T) *proposalEnv {
	t.Helper()
	cfg := config.Defaults()
	cfg.Auth.TrustedProxies = []string{"127.0.0.1"}
	cfg.Auth.AdminGroup = "admins"
	cfg.Server.PublicBaseURL = "https://fukku.example.com"
	cfg.Paths.IntakeRoot = "/data/intake"
	cfg.Sonarr.URL = "http://sonarr.example.com:8989"
	st := storetest.NewFake(nil)
	series := &arrtest.FakeSeries{}
	series.Quals = []arr.Quality{{ID: 3, Name: "WEBDL-1080p"}, {ID: 7, Name: "Bluray-1080p"}}
	series.Langs = []arr.Language{{ID: 1, Name: "English"}}
	series.Eps = map[int][]arr.Episode{11: {
		{ID: 101, SeasonNumber: 1, Number: 1, Title: "One", RuntimeMs: 1_320_000},
		{ID: 102, SeasonNumber: 1, Number: 2, Title: "Two", RuntimeMs: 1_320_000},
		{ID: 103, SeasonNumber: 1, Number: 3, Title: "Three", RuntimeMs: 1_320_000},
	}}
	series.Proposed = map[string][]arr.Row{}
	p := flows.NewProposals(cfg, nil, st, nil, series, nil)
	h := New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, Deps{Store: st, Proposals: p}).Handler()
	return &proposalEnv{t: t, h: h, st: st, series: series, p: p}
}

// send is a request through the whole chain, from the trusted proxy; non-GET requests carry
// the Origin unless origin is false.
func (e *proposalEnv) send(user, groups, method, path, body string, origin bool) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequestWithContext(e.t.Context(), method, "https://fukku.example.com"+path, nil)
	} else {
		r = httptest.NewRequestWithContext(e.t.Context(), method, "https://fukku.example.com"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Remote-User", user)
	r.Header.Set("Remote-Groups", groups)
	if origin {
		r.Header.Set("Origin", "https://fukku.example.com")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func (e *proposalEnv) want(rec *httptest.ResponseRecorder, status int, code string) {
	e.t.Helper()
	var b struct {
		Error struct{ Code, Message string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != status || b.Error.Code != code || (code != "" && b.Error.Message == "") {
		e.t.Errorf("status %d %q, want %d %q: %s", rec.Code, b.Error.Code, status, code, rec.Body)
	}
}

// job is a matched job of user1 for show 11 with three episodes; the third file is
// misnumbered (its name says E02).
func (e *proposalEnv) job() store.Job {
	e.t.Helper()
	ctx := e.t.Context()
	j, err := e.st.CreateJob(ctx, "user1")
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := storetest.Drive(ctx, e.st, j.ID, store.Probed); err != nil {
		e.t.Fatal(err)
	}
	folder := "/data/intake/jobs/" + j.ID + "/files"
	var items []arr.Row
	for _, name := range []string{"S01E01.mkv", "S01E02.mkv", "S01E02b.mkv"} {
		if _, err := e.st.AddFile(ctx, store.File{JobID: j.ID, Path: "files/" + name, Kind: store.KindVideo, Size: 10,
			Status: store.FileOK, Report: json.RawMessage(`{"durationMs": 1320000, "video": [{"width": 1920, "height": 1080}]}`)}); err != nil {
			e.t.Fatal(err)
		}
		items = append(items, arr.Row{Path: folder + "/" + name, Quality: arr.Quality{ID: 3, Name: "WEBDL-1080p"},
			Raw: json.RawMessage(`{"path":"` + folder + "/" + name + `"}`)})
	}
	e.series.Proposed[folder] = items
	if err := e.st.SetTarget(ctx, j.ID, store.Target{App: "sonarr", TitleID: 11, Title: "Some Show", Year: 2010}); err != nil {
		e.t.Fatal(err)
	}
	if j, err = e.st.Transition(ctx, j.ID, store.Probed, store.Matched, store.Change{Actor: "user1"}); err != nil {
		e.t.Fatal(err)
	}
	return j
}

func TestProposalAPIThroughTheFullChain(t *testing.T) {
	e := newProposalEnv(t)
	j := e.job()
	base := "/api/jobs/" + j.ID + "/proposal"

	// Pending until the matched step has run.
	rec := e.send("user1", "users", "GET", base, "", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"pending"`) {
		t.Fatalf("pending: %d %s", rec.Code, rec.Body)
	}
	e.want(e.send("user2", "users", "GET", base, "", false), http.StatusNotFound, "not_found")
	if err := e.p.Matched(t.Context(), j); err != nil {
		t.Fatal(err)
	}

	rec = e.send("user1", "users", "GET", base, "", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("ready: %d %s", rec.Code, rec.Body)
	}
	if rec := e.send("admin1", "admins", "GET", base, "", false); rec.Code != http.StatusOK {
		t.Errorf("admin: %d", rec.Code)
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"state", "app", "target", "rows", "hints", "episodes", "qualities", "languages"} {
		if _, ok := p[k]; !ok {
			t.Errorf("proposal has no %q: %s", k, rec.Body)
		}
	}
	if p["state"] != "ready" || p["app"] != "sonarr" {
		t.Errorf("proposal %s", rec.Body)
	}
	rows := p["rows"].([]any)
	row := rows[2].(map[string]any)
	for _, k := range []string{"id", "fileId", "name", "durationMs", "skip", "seasonNumber", "episodes", "quality",
		"languages", "customFormats", "customFormatScore", "rejections", "warnings", "importable"} {
		if _, ok := row[k]; !ok {
			t.Errorf("row has no %q: %v", k, row)
		}
	}
	rowID := row["id"].(string)
	if !strings.Contains(rec.Body.String(), `"warnings":[{"code":"duplicate_episode"}]`) {
		t.Errorf("no duplicate warning: %s", rec.Body)
	}
	rowPath := base + "/rows/" + rowID
	checks := len(e.series.RecheckCalls())

	// Edits need the Origin, a known row, known fields and episodes of this series.
	e.want(e.send("user1", "users", "PATCH", rowPath, `{"episodeIds":[103]}`, false), http.StatusForbidden, "origin_mismatch")
	e.want(e.send("user2", "users", "PATCH", rowPath, `{"episodeIds":[103]}`, true), http.StatusNotFound, "not_found")
	e.want(e.send("user1", "users", "PATCH", base+"/rows/r999", `{"episodeIds":[103]}`, true), http.StatusNotFound, "not_found")
	e.want(e.send("user1", "users", "PATCH", rowPath, `{"path":"/etc/passwd"}`, true), http.StatusBadRequest, "bad_request")
	e.want(e.send("user1", "users", "PATCH", rowPath, `{"episodeIds":[103],"path":"/x"}`, true), http.StatusBadRequest, "bad_request")
	e.want(e.send("user1", "users", "PATCH", rowPath, `{"episodeIds":[999]}`, true), http.StatusBadRequest, "bad_request")
	e.want(e.send("user1", "users", "PATCH", rowPath, `not json`, true), http.StatusBadRequest, "bad_request")
	if n := len(e.series.RecheckCalls()); n != checks {
		t.Errorf("refused edits reached the app: %d re-checks, want %d", n, checks)
	}

	rec = e.send("user1", "users", "PATCH", rowPath, `{"episodeIds":[103]}`, true)
	var edited flows.RowView
	if err := json.Unmarshal(rec.Body.Bytes(), &edited); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body)
	}
	if edited.ID != rowID || !edited.Importable || len(*edited.Episodes) != 1 || (*edited.Episodes)[0].ID != 103 {
		t.Errorf("edited %s", rec.Body)
	}
	calls := e.series.RecheckCalls()
	if len(calls) != checks+1 {
		t.Fatalf("%d re-checks, want %d", len(calls), checks+1)
	}
	// What reaches the app comes from the store: the file's path as the app sees it.
	if got := calls[len(calls)-1][0].Path; got != "/data/intake/jobs/"+j.ID+"/files/S01E02b.mkv" {
		t.Errorf("re-checked path %q", got)
	}

	// Bulk: shift the first two by one.
	ids := []string{rows[0].(map[string]any)["id"].(string), rows[1].(map[string]any)["id"].(string)}
	body, _ := json.Marshal(map[string]any{"rowIds": ids, "shift": 1})
	rec = e.send("user1", "users", "POST", base+"/bulk", string(body), true)
	var bulk struct{ Rows []flows.RowView }
	if err := json.Unmarshal(rec.Body.Bytes(), &bulk); err != nil || rec.Code != http.StatusOK || len(bulk.Rows) != 2 {
		t.Fatalf("bulk: %d %s", rec.Code, rec.Body)
	}
	if (*bulk.Rows[0].Episodes)[0].ID != 102 || (*bulk.Rows[1].Episodes)[0].ID != 103 {
		t.Errorf("shifted %s", rec.Body)
	}
	e.want(e.send("user1", "users", "POST", base+"/bulk", `{"rowIds":["`+ids[0]+`"],"shift":5}`, true), http.StatusBadRequest, "bad_request")
	e.want(e.send("user1", "users", "POST", base+"/bulk", `{"rowIds":["r999"],"shift":1}`, true), http.StatusNotFound, "not_found")
	e.want(e.send("user1", "users", "POST", base+"/bulk", `{"rowIds":["`+ids[0]+`"],"shift":1}`, false), http.StatusForbidden, "origin_mismatch")

	// The app down: 502, without its detail.
	e.series.Err = map[string]error{"Recheck": errors.New("dial tcp 192.0.2.1:8989: connection refused")}
	rec = e.send("user1", "users", "PATCH", rowPath, `{"skip":true}`, true)
	e.want(rec, http.StatusBadGateway, "arr_unavailable")
	if strings.Contains(rec.Body.String(), "192.0.2.1") {
		t.Errorf("the detail reached the client: %s", rec.Body)
	}
	e.series.Err = nil

	// Outside the proposal's states: 409.
	if _, err := e.st.Transition(t.Context(), j.ID, store.AwaitingConfirmation, store.Probed, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	e.want(e.send("user1", "users", "GET", base, "", false), http.StatusConflict, "wrong_state")
	e.want(e.send("user1", "users", "PATCH", rowPath, `{"skip":true}`, true), http.StatusConflict, "wrong_state")
	e.want(e.send("user1", "users", "POST", base+"/bulk", `{"rowIds":["`+ids[0]+`"],"shift":1}`, true), http.StatusConflict, "wrong_state")
	if rec := e.send("user1", "users", "GET", "/api/jobs/"+strings.Repeat("z", 32)+"/proposal", "", false); rec.Code != http.StatusNotFound {
		t.Error("a malformed job id is not a 404")
	}
}

// Without the proposal flow the routes do not exist.
func TestProposalRoutesNeedTheFlow(t *testing.T) {
	e := newEnv(t)
	j := e.job("user1", store.Matched)
	mux := http.NewServeMux()
	e.srv.routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/api/jobs/"+j.ID+"/proposal", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d", rec.Code)
	}
}
