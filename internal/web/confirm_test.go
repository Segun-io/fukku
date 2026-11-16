package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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

type confirmEnv struct {
	proposalEnv
	sentinel string
	eng      *flows.Engine
}

// newConfirmEnv is the proposal API and the confirmation behind the whole chain, with a
// sentinel file that must exist (paths.sentinels).
func newConfirmEnv(t *testing.T) *confirmEnv {
	t.Helper()
	root := t.TempDir()
	sentinel := filepath.Join(root, ".sentinel")
	if err := os.WriteFile(sentinel, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Auth.TrustedProxies = []string{"127.0.0.1"}
	cfg.Auth.AdminGroup = "admins"
	cfg.Server.PublicBaseURL = "https://fukku.example.com"
	cfg.Paths.IntakeRoot = "/data/intake"
	cfg.Paths.Sentinels = []string{sentinel}
	cfg.Sonarr.URL = "http://sonarr.example.com:8989"
	st := storetest.NewFake(nil)
	series := &arrtest.FakeSeries{}
	series.Quals = []arr.Quality{{ID: 3, Name: "WEBDL-1080p"}}
	series.Langs = []arr.Language{{ID: 1, Name: "English"}}
	series.Library = []arr.Series{{ID: 11, Title: "Some Show", Year: 2010}}
	series.Eps = map[int][]arr.Episode{11: {
		{ID: 101, SeasonNumber: 1, Number: 1, Title: "One", RuntimeMs: 1_320_000},
		{ID: 102, SeasonNumber: 1, Number: 2, Title: "Two", RuntimeMs: 1_320_000},
		{ID: 103, SeasonNumber: 1, Number: 3, Title: "Three", RuntimeMs: 1_320_000},
	}}
	series.Proposed = map[string][]arr.Row{}
	p := flows.NewProposals(cfg, nil, st, nil, series, nil)
	eng := flows.NewEngine(p)
	h := New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, Deps{Store: st, Proposals: p, Imports: eng}).Handler()
	return &confirmEnv{proposalEnv: proposalEnv{t: t, h: h, st: st, series: series, p: p}, sentinel: sentinel, eng: eng}
}

// ready is a show job of user1 awaiting confirmation: S01E01, S01E02 and S01E03, the
// last one with a rejection.
func (e *confirmEnv) ready() (store.Job, []string) {
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
	for _, name := range []string{"S01E01.mkv", "S01E02.mkv", "S01E03.mkv"} {
		if _, err := e.st.AddFile(ctx, store.File{JobID: j.ID, Path: "files/" + name, Kind: store.KindVideo, Size: 10,
			Status: store.FileOK, Report: json.RawMessage(`{"durationMs": 1320000, "video": [{"width": 1920, "height": 1080}]}`)}); err != nil {
			e.t.Fatal(err)
		}
		items = append(items, arr.Row{Path: folder + "/" + name, Quality: arr.Quality{ID: 3, Name: "WEBDL-1080p"},
			Raw: json.RawMessage(`{"path":"` + folder + "/" + name + `"}`)})
	}
	e.series.Proposed[folder] = items
	e.series.Verdicts = map[string][]arr.Rejection{folder + "/S01E03.mkv": {{Reason: "Not an upgrade for existing episode file(s)", Permanent: true}}}
	if err := e.st.SetTarget(ctx, j.ID, store.Target{App: "sonarr", TitleID: 11, Title: "Some Show", Year: 2010}); err != nil {
		e.t.Fatal(err)
	}
	if j, err = e.st.Transition(ctx, j.ID, store.Probed, store.Matched, store.Change{Actor: "user1"}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.p.Matched(ctx, j); err != nil {
		e.t.Fatal(err)
	}
	rows, err := e.st.Rows(ctx, j.ID)
	if err != nil || len(rows) != 3 {
		e.t.Fatalf("rows %v %v", rows, err)
	}
	return j, []string{rows[0].ID, rows[1].ID, rows[2].ID}
}

func TestConfirmThroughTheFullChain(t *testing.T) {
	e := newConfirmEnv(t)
	j, rows := e.ready()
	path := "/api/jobs/" + j.ID + "/confirm"

	// Refusals before anything changes.
	e.want(e.send("user1", "users", "POST", path, `{"overrides":[]}`, false), http.StatusForbidden, "origin_mismatch")
	e.want(e.send("user2", "users", "POST", path, `{"overrides":[]}`, true), http.StatusNotFound, "not_found")
	e.want(e.send("user1", "users", "POST", path, `not json`, true), http.StatusBadRequest, "bad_request")
	e.want(e.send("user1", "users", "POST", path, `{"overrides":["r999"]}`, true), http.StatusBadRequest, "bad_request")
	e.want(e.send("user1", "users", "POST", path, `{"overrides":[],"paths":["/etc"]}`, true), http.StatusBadRequest, "bad_request")

	// A row with a rejection that is not listed: 409 needs_override, naming it.
	rec := e.send("user1", "users", "POST", path, `{"overrides":[]}`, true)
	e.want(rec, http.StatusConflict, "needs_override")
	var refusal struct {
		Error struct {
			Code string   `json:"code"`
			Rows []string `json:"rows"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil || !slices.Equal(refusal.Error.Rows, []string{rows[2]}) {
		t.Fatalf("needs_override body %s", rec.Body)
	}

	// A missing sentinel: 503, and nothing changes.
	if err := os.Remove(e.sentinel); err != nil {
		t.Fatal(err)
	}
	rec = e.send("user1", "users", "POST", path, `{"overrides":["`+rows[2]+`"]}`, true)
	e.want(rec, http.StatusServiceUnavailable, "storage_unavailable")
	if strings.Contains(rec.Body.String(), e.sentinel) {
		t.Errorf("the path reached the client: %s", rec.Body)
	}
	if err := os.WriteFile(e.sentinel, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// The app does not answer: 502.
	e.series.Err = map[string]error{"Episodes": errRefusedArr}
	e.want(e.send("user1", "users", "POST", path, `{"overrides":["`+rows[2]+`"]}`, true), http.StatusBadGateway, "arr_unavailable")
	e.series.Err = nil
	if j2, _ := e.st.Job(t.Context(), j.ID); j2.State != store.AwaitingConfirmation {
		t.Fatalf("a refused confirmation moved the job to %s", j2.State)
	}
	if log, _ := e.st.AuditLog(t.Context(), j.ID); len(log) != 0 {
		t.Fatalf("audit entries for refusals: %+v", log)
	}

	// Confirmed with the override: 202 and the job, importing; the audit says who and what.
	rec = e.send("user1", "users", "POST", path, `{"overrides":["`+rows[2]+`"]}`, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	var job struct {
		ID, State string
		Actions   []string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil || job.ID != j.ID || job.State != "importing" || len(job.Actions) != 0 {
		t.Fatalf("job %s", rec.Body)
	}
	log, _ := e.st.AuditLog(t.Context(), j.ID)
	var confirmed, overridden *store.AuditEntry
	for i := range log {
		switch log[i].Action {
		case "import.confirmed":
			confirmed = &log[i]
		case "rejection.overridden":
			overridden = &log[i]
		}
	}
	if confirmed == nil || confirmed.Actor != "user1" || confirmed.Detail["rows"] != strings.Join(rows, ",") || confirmed.Detail["overrides"] != rows[2] ||
		confirmed.Detail["app"] != "sonarr" || confirmed.Detail["titleId"] != "11" {
		t.Errorf("import.confirmed %+v", confirmed)
	}
	if overridden == nil || overridden.Detail["row"] != rows[2] || overridden.Detail["file"] != "S01E03.mkv" ||
		overridden.Detail["rejections"] != "Not an upgrade for existing episode file(s)" {
		t.Errorf("rejection.overridden %+v", overridden)
	}
	if n := len(log); n != 2 {
		t.Errorf("%d audit entries", n)
	}

	// JobDetail shows the import; no result yet.
	rec = e.send("user1", "users", "GET", "/api/jobs/"+j.ID, "", false)
	var d struct {
		Import *flows.ImportView `json:"import"`
		Result *flows.ResultView `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || d.Import == nil || d.Import.Status != "pending" || len(d.Import.Rows) != 3 || d.Result != nil {
		t.Fatalf("detail %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"import":{"status":"pending","rows":[{"id":"`+rows[0]+`","result":""}`) ||
		!strings.Contains(rec.Body.String(), `"result":null`) {
		t.Errorf("detail JSON %s", rec.Body)
	}

	// Twice: 409 wrong_state.
	e.want(e.send("user1", "users", "POST", path, `{"overrides":["`+rows[2]+`"]}`, true), http.StatusConflict, "wrong_state")
	// Proposal edits are over.
	e.want(e.send("user1", "users", "PATCH", "/api/jobs/"+j.ID+"/proposal/rows/"+rows[0], `{"skip":true}`, true), http.StatusConflict, "wrong_state")
}

var errRefusedArr = &arr.Error{App: "sonarr", Method: "GET", Path: "/episode", Status: 503, Message: "dial 192.0.2.1"}

func TestConfirmNothingToImport(t *testing.T) {
	e := newConfirmEnv(t)
	j, rows := e.ready()
	for _, id := range rows {
		if rec := e.send("user1", "users", "PATCH", "/api/jobs/"+j.ID+"/proposal/rows/"+id, `{"skip":true}`, true); rec.Code != http.StatusOK {
			t.Fatalf("skip: %d %s", rec.Code, rec.Body)
		}
	}
	e.want(e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/confirm", `{"overrides":[]}`, true), http.StatusConflict, "nothing_to_import")
	// An admin may confirm another user's job; here there is still nothing to import.
	e.want(e.send("admin1", "admins", "POST", "/api/jobs/"+j.ID+"/confirm", `{}`, true), http.StatusConflict, "nothing_to_import")
}

// A job stalled while importing cannot be cancelled: its command may still be running.
func TestAStalledImportCannotBeCancelled(t *testing.T) {
	e := newConfirmEnv(t)
	j, rows := e.ready()
	if rec := e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/confirm", `{"overrides":["`+rows[2]+`"]}`, true); rec.Code != http.StatusAccepted {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	if _, err := e.st.Transition(t.Context(), j.ID, store.Importing, store.Stalled, store.Change{Actor: "system", ErrorCode: "arr_unavailable"}); err != nil {
		t.Fatal(err)
	}
	rec := e.send("user1", "users", "GET", "/api/jobs/"+j.ID, "", false)
	if strings.Contains(rec.Body.String(), `"cancel"`) {
		t.Errorf("cancel offered: %s", rec.Body)
	}
	e.want(e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/cancel", ``, true), http.StatusConflict, "wrong_state")
}
