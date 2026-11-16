package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

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
	score    int // the custom-format score of S01E02 (the show's profile's minimum is 0)
	clock    *webClock
}

// webClock is the store's and the server's clock: it starts at the real time and moves
// only when a test advances it.
type webClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *webClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *webClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
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
	clock := &webClock{t: time.Now()}
	st := storetest.NewFake(clock.now)
	series := &arrtest.FakeSeries{}
	series.Quals = []arr.Quality{{ID: 3, Name: "WEBDL-1080p"}}
	series.Langs = []arr.Language{{ID: 1, Name: "English"}}
	series.Library = []arr.Series{{ID: 11, Title: "Some Show", Year: 2010, QualityProfileID: 4}}
	series.Profile = []arr.Profile{{ID: 4, Name: "HD", MinFormatScore: 0}}
	series.Eps = map[int][]arr.Episode{11: {
		{ID: 101, SeasonNumber: 1, Number: 1, Title: "One", RuntimeMs: 1_320_000},
		{ID: 102, SeasonNumber: 1, Number: 2, Title: "Two", RuntimeMs: 1_320_000},
		{ID: 103, SeasonNumber: 1, Number: 3, Title: "Three", RuntimeMs: 1_320_000},
	}}
	series.Proposed = map[string][]arr.Row{}
	p := flows.NewProposals(cfg, nil, st, nil, series, nil)
	eng := flows.NewEngine(p)
	srv := New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, Deps{Store: st, Proposals: p, Imports: eng})
	srv.now = clock.now
	h := srv.Handler()
	return &confirmEnv{proposalEnv: proposalEnv{t: t, h: h, st: st, series: series, p: p}, sentinel: sentinel, eng: eng, clock: clock}
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
		it := arr.Row{Path: folder + "/" + name, Quality: arr.Quality{ID: 3, Name: "WEBDL-1080p"},
			Raw: json.RawMessage(`{"path":"` + folder + "/" + name + `"}`)}
		if name == "S01E02.mkv" && e.score != 0 {
			it.CustomFormats, it.CustomFormatScore = []string{"Not Original Language"}, e.score
		}
		items = append(items, it)
	}
	if _, err := e.st.AddFile(ctx, store.File{JobID: j.ID, Path: "files/S01E01.es.srt", Kind: store.KindSubtitle, Size: 10, Status: store.FileOK}); err != nil {
		e.t.Fatal(err)
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
	if !strings.Contains(rec.Body.String(), `"import":{"status":"pending","rows":[{"id":"`+rows[0]+`","name":"S01E01.mkv","result":""}`) ||
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

	// An admin may give up on it; the audit log says so.
	rec = e.send("admin1", "admins", "GET", "/api/jobs/"+j.ID, "", false)
	if !strings.Contains(rec.Body.String(), `"actions":["cancel"]`) {
		t.Errorf("cancel not offered to an admin: %s", rec.Body)
	}
	if rec := e.send("admin1", "admins", "POST", "/api/jobs/"+j.ID+"/cancel", ``, true); rec.Code != http.StatusOK {
		t.Fatalf("admin cancel: %d %s", rec.Code, rec.Body)
	}
	if got, _ := e.st.Job(t.Context(), j.ID); got.State != store.Cancelled {
		t.Fatalf("job %s", got.State)
	}
	log, _ := e.st.AuditLog(t.Context(), j.ID)
	if last := log[len(log)-1]; last.Action != "import.abandoned" || last.Actor != "admin1" || last.Detail["from"] != "stalled" ||
		last.Detail["app"] != "sonarr" || last.Detail["command"] != "" {
		t.Errorf("audit %+v", last)
	}
	if h, _ := e.st.History(t.Context(), j.ID); !strings.Contains(h[len(h)-1].Note, "nothing was submitted to sonarr") {
		t.Errorf("note %q", h[len(h)-1].Note)
	}
}

// D. An importing job whose command never ends can be cancelled by an admin once it has
// been importing for longer than server.jobStuckAfter, and by nobody before. E. The
// cancellation is audited as import.abandoned with the app's command id, and the job's
// history says that the app may still finish it.
func TestAStuckImportCanBeAbandoned(t *testing.T) {
	e := newConfirmEnv(t)
	j, rows := e.ready()
	if rec := e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/confirm", `{"overrides":["`+rows[2]+`"]}`, true); rec.Code != http.StatusAccepted {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	// Submitted: the app knows the command, which never ends.
	if err := e.st.PutImport(t.Context(), store.Import{JobID: j.ID, CommandID: 4321, SubmittedAt: e.clock.now(), Baseline: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	path := "/api/jobs/" + j.ID
	offered := func(user, groups string) bool {
		rec := e.send(user, groups, "GET", path, "", false)
		if rec.Code != http.StatusOK {
			t.Fatalf("detail: %d %s", rec.Code, rec.Body)
		}
		return strings.Contains(rec.Body.String(), `"cancel"`)
	}

	// Not stuck yet: nobody may cancel it.
	e.clock.advance(59 * time.Minute)
	for _, who := range [][2]string{{"user1", "users"}, {"admin1", "admins"}} {
		if offered(who[0], who[1]) {
			t.Errorf("cancel offered to %s before server.jobStuckAfter", who[0])
		}
		e.want(e.send(who[0], who[1], "POST", path+"/cancel", ``, true), http.StatusConflict, "wrong_state")
	}

	// Stuck: an admin may, its user still may not.
	e.clock.advance(2 * time.Minute)
	if offered("user1", "users") {
		t.Error("cancel offered to the user")
	}
	e.want(e.send("user1", "users", "POST", path+"/cancel", ``, true), http.StatusConflict, "wrong_state")
	if !offered("admin1", "admins") {
		t.Error("cancel not offered to an admin past server.jobStuckAfter")
	}
	if rec := e.send("admin1", "admins", "POST", path+"/cancel", ``, true); rec.Code != http.StatusOK {
		t.Fatalf("admin cancel: %d %s", rec.Code, rec.Body)
	}
	if got, _ := e.st.Job(t.Context(), j.ID); got.State != store.Cancelled {
		t.Fatalf("job %s", got.State)
	}
	log, _ := e.st.AuditLog(t.Context(), j.ID)
	last := log[len(log)-1]
	if last.Action != "import.abandoned" || last.Actor != "admin1" || last.Detail["from"] != "importing" ||
		last.Detail["to"] != "cancelled" || last.Detail["app"] != "sonarr" || last.Detail["command"] != "4321" {
		t.Errorf("audit %+v", last)
	}
	h, _ := e.st.History(t.Context(), j.ID)
	if ev := h[len(h)-1]; ev.From != store.Importing || ev.Actor != "admin1" || !strings.Contains(ev.Note, "sonarr may still finish ManualImport command 4321") {
		t.Errorf("event %+v", ev)
	}
}

// An import submitted whose command id was not stored yet is audited as such.
func TestAbandonedImportWithoutACommandID(t *testing.T) {
	e := newConfirmEnv(t)
	j, rows := e.ready()
	if rec := e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/confirm", `{"overrides":["`+rows[2]+`"]}`, true); rec.Code != http.StatusAccepted {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	if err := e.st.PutImport(t.Context(), store.Import{JobID: j.ID, SubmittedAt: e.clock.now()}); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(2 * time.Hour)
	if rec := e.send("admin1", "admins", "POST", "/api/jobs/"+j.ID+"/cancel", ``, true); rec.Code != http.StatusOK {
		t.Fatalf("admin cancel: %d %s", rec.Code, rec.Body)
	}
	log, _ := e.st.AuditLog(t.Context(), j.ID)
	if last := log[len(log)-1]; last.Action != "import.abandoned" || last.Detail["command"] != "unknown" {
		t.Errorf("audit %+v", last)
	}
	if h, _ := e.st.History(t.Context(), j.ID); !strings.Contains(h[len(h)-1].Note, "its id is not known") {
		t.Errorf("note %q", h[len(h)-1].Note)
	}
}

// The language warning is overridden like a rejection, with an audit entry of its own; the
// subtitle choices of the body are validated and stored.
func TestConfirmLanguageOverrideAndSubtitles(t *testing.T) {
	e := newConfirmEnv(t)
	e.score = -10000
	j, rows := e.ready()
	path := "/api/jobs/" + j.ID + "/confirm"
	rec := e.send("user1", "users", "POST", path, `{"overrides":["`+rows[2]+`"]}`, true)
	e.want(rec, http.StatusConflict, "needs_override")
	if !strings.Contains(rec.Body.String(), `"rows":["`+rows[1]+`"]`) {
		t.Fatalf("the language warning is not named: %s", rec.Body)
	}
	files, _ := e.st.Files(t.Context(), j.ID)
	var sub int64
	for _, f := range files {
		if f.Kind == store.KindSubtitle {
			sub = f.ID
		}
	}
	both := `"overrides":["` + rows[1] + `","` + rows[2] + `"]`
	e.want(e.send("user1", "users", "POST", path, `{`+both+`,"subtitles":[{"fileId":1,"language":"es"}]}`, true), http.StatusBadRequest, "bad_request")
	e.want(e.send("user1", "users", "POST", path, `{`+both+`,"subtitles":[{"fileId":`+strconv.FormatInt(sub, 10)+`,"language":"zz"}]}`, true), http.StatusBadRequest, "bad_request")
	e.want(e.send("user1", "users", "POST", path, `{`+both+`,"subtitles":[{"fileId":`+strconv.FormatInt(sub, 10)+`,"lang":"es"}]}`, true), http.StatusBadRequest, "bad_request")

	body := `{` + both + `,"subtitles":[{"fileId":` + strconv.FormatInt(sub, 10) + `,"language":"es","forced":true,"hearingImpaired":false}]}`
	if rec := e.send("user1", "users", "POST", path, body, true); rec.Code != http.StatusAccepted {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	tg, _ := e.st.Target(t.Context(), j.ID)
	if c := flows.SubtitleChoices(tg)[sub]; c.Language != "es" || !c.Forced || c.HearingImpaired {
		t.Errorf("stored choice %+v", c)
	}
	log, _ := e.st.AuditLog(t.Context(), j.ID)
	var lang *store.AuditEntry
	actions := []string{}
	for i := range log {
		actions = append(actions, log[i].Action)
		if log[i].Action == "language.overridden" {
			lang = &log[i]
		}
		if log[i].Action == "import.confirmed" && !strings.Contains(log[i].Detail["subtitles"], ":es forced=true") {
			t.Errorf("import.confirmed %+v", log[i].Detail)
		}
	}
	if lang == nil || lang.Detail["row"] != rows[1] || lang.Detail["score"] != "-10000" || lang.Detail["minFormatScore"] != "0" ||
		lang.Detail["customFormats"] != "Not Original Language" {
		t.Errorf("language.overridden %+v (entries %v)", lang, actions)
	}
	if n := len(actions); n != 4 { // confirmed, two rejection.overridden, one language.overridden
		t.Errorf("audit %v", actions)
	}
}
