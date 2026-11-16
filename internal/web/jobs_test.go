package web

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
	"testing"
	"testing/fstest"
	"time"

	"github.com/segun-io/fukku/internal/auth"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// spyStore counts the lookups of single jobs, to show that a malformed id never reaches it.
type spyStore struct {
	store.Store
	jobCalls int
}

func (s *spyStore) Job(ctx context.Context, id string) (store.Job, error) {
	s.jobCalls++
	return s.Store.Job(ctx, id)
}

type env struct {
	t     *testing.T
	store *spyStore
	srv   *Server
	root  string
	now   time.Time
}

var (
	user1 = auth.User{Name: "user1", Email: "user1@example.com"}
	user2 = auth.User{Name: "user2", Email: "user2@example.com"}
	admin = auth.User{Name: "admin1", Email: "admin1@example.com", Admin: true}
)

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, root: t.TempDir(), now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	e.store = &spyStore{Store: storetest.NewFake(func() time.Time { return e.now })}
	cfg := config.Defaults()
	cfg.Paths.IntakeRoot = e.root
	cfg.Radarr.URL = "http://radarr.example.com:7878"
	cfg.Radarr.PublicURL = "https://radarr.example.com/"
	cfg.Sonarr.URL = "http://sonarr.example.com:8989"
	cfg.Paths.Mappings.Radarr = []config.PathMapping{{From: e.root, To: "/downloads/intake"}}
	e.srv = New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, Deps{Store: e.store})
	return e
}

// do sends a request as user (nil: no user in the context) and returns the status and body.
func (e *env) do(u *auth.User, method, target string) (int, string) {
	e.t.Helper()
	req := httptest.NewRequestWithContext(e.t.Context(), method, target, nil)
	if u != nil {
		req = req.WithContext(auth.WithUser(req.Context(), *u))
	}
	rec := httptest.NewRecorder()
	// The job routes alone: the authentication chain is tested with #5; here users are faked.
	mux := http.NewServeMux()
	e.srv.jobRoutes(mux)
	mux.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	if ct := rec.Header().Get("Content-Type"); len(body) > 0 && ct != "application/json" {
		e.t.Errorf("%s %s: Content-Type %q", method, target, ct)
	}
	return rec.Code, string(body)
}

// ok sends a request that must answer want and decodes the JSON body into out.
func (e *env) ok(u auth.User, method, target string, want int, out any) {
	e.t.Helper()
	code, body := e.do(&u, method, target)
	if code != want {
		e.t.Fatalf("%s %s: status %d, want %d: %s", method, target, code, want, body)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(body), out); err != nil {
			e.t.Fatalf("%s %s: %v: %s", method, target, err, body)
		}
	}
}

// fail sends a request that must answer the status and error code.
func (e *env) fail(u auth.User, method, target string, status int, code string) {
	e.t.Helper()
	got, body := e.do(&u, method, target)
	var b struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		e.t.Fatalf("%s %s: %v: %s", method, target, err, body)
	}
	if got != status || b.Error.Code != code || b.Error.Message == "" {
		e.t.Errorf("%s %s: %d %q (%q), want %d %q", method, target, got, b.Error.Code, b.Error.Message, status, code)
	}
}

var chain = []store.State{
	store.Uploading, store.Uploaded, store.Probing, store.Probed, store.Matched,
	store.AwaitingConfirmation, store.Importing, store.Imported, store.PostProcessing, store.Done,
}

// job creates a job owned by owner and walks it to state through the store.
func (e *env) job(owner string, state store.State) store.Job {
	e.t.Helper()
	ctx := context.Background()
	j, err := e.store.CreateJob(ctx, owner)
	if err != nil {
		e.t.Fatal(err)
	}
	e.now = e.now.Add(time.Minute)
	move := func(from, to store.State, c store.Change) {
		c.Actor = owner
		if j, err = e.store.Transition(ctx, j.ID, from, to, c); err != nil {
			e.t.Fatalf("%s -> %s: %v", from, to, err)
		}
	}
	prev := store.Uploading
	for _, st := range chain[1:] {
		if prev == state {
			break
		}
		switch state {
		case store.Failed, store.Stalled, store.Cancelled:
			if prev == store.Probing {
				c := store.Change{ErrorCode: "worker_unavailable", ErrorText: "no heartbeat in /spool"}
				if state == store.Cancelled {
					c = store.Change{}
				}
				move(prev, state, c)
				return j
			}
		}
		move(prev, st, store.Change{})
		prev = st
	}
	return j
}

func (e *env) addFile(j store.Job, p string, kind store.FileKind, size int64, status store.FileStatus, report string) {
	e.t.Helper()
	f := store.File{JobID: j.ID, Path: p, Kind: kind, Size: size, Status: status}
	if report != "" {
		f.Report = json.RawMessage(report)
	}
	if _, err := e.store.AddFile(context.Background(), f); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) audit(jobID string) []string {
	e.t.Helper()
	log, err := e.store.AuditLog(context.Background(), jobID)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, a := range log {
		out = append(out, a.Action+":"+a.Actor)
	}
	return out
}

func jobPath(j store.Job, action string) string {
	p := "/api/jobs/" + j.ID
	if action != "" {
		p += "/" + action
	}
	return p
}

type jobView struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Attempt   int    `json:"attempt"`
	Name      *string
	FileCount int
	Actions   []string
	Error     *struct{ Code, Message string }
}

func equalJSON(t *testing.T, got, want string) {
	t.Helper()
	var a, b bytes.Buffer
	if err := json.Indent(&a, bytes.TrimSpace([]byte(got)), "", "  "); err != nil {
		t.Fatalf("%v: %s", err, got)
	}
	if err := json.Indent(&b, []byte(want), "", "  "); err != nil {
		t.Fatalf("%v: %s", err, want)
	}
	if a.String() != b.String() {
		t.Errorf("JSON differs.\ngot:\n%s\nwant:\n%s", a.String(), b.String())
	}
}

func TestCreateJob(t *testing.T) {
	e := newEnv(t)
	code, body := e.do(&user1, http.MethodPost, "/api/jobs")
	if code != http.StatusCreated {
		t.Fatalf("status %d: %s", code, body)
	}
	var j struct{ ID string }
	_ = json.Unmarshal([]byte(body), &j)
	equalJSON(t, body, `{
		"id": "`+j.ID+`", "owner": "user1", "state": "uploading", "attempt": 1, "error": null,
		"name": null, "fileCount": 0, "totalSize": 0,
		"createdAt": "2026-01-01T12:00:00Z", "updatedAt": "2026-01-01T12:00:00Z", "expiresAt": null,
		"actions": ["cancel"]}`)
	if !jobdir.ValidID(j.ID) {
		t.Errorf("id %q", j.ID)
	}
	if got := e.audit(j.ID); len(got) != 1 || got[0] != "job.created:user1" {
		t.Errorf("audit %v", got)
	}
	stored, err := e.store.Job(t.Context(), j.ID)
	if err != nil || stored.Owner != "user1" || stored.State != store.Uploading {
		t.Errorf("stored %+v, %v", stored, err)
	}
}

func TestNoUserIs403(t *testing.T) {
	e := newEnv(t)
	j := e.job("user1", store.Uploading)
	for _, c := range [][2]string{
		{"POST", "/api/jobs"}, {"GET", "/api/jobs"}, {"GET", jobPath(j, "")}, {"POST", jobPath(j, "submit")},
		{"POST", jobPath(j, "cancel")}, {"POST", jobPath(j, "retry")}, {"DELETE", jobPath(j, "")},
	} {
		code, _ := e.do(nil, c[0], c[1])
		if code != http.StatusForbidden {
			t.Errorf("%s %s: %d", c[0], c[1], code)
		}
	}
}

func TestListJobs(t *testing.T) {
	e := newEnv(t)
	a := e.job("user1", store.Uploading)
	b := e.job("user2", store.Probed)
	c := e.job("admin1", store.Done)
	d := e.job("user1", store.Failed)

	ids := func(u auth.User, query string) []string {
		var out struct{ Jobs []jobView }
		e.ok(u, "GET", "/api/jobs"+query, 200, &out)
		got := []string{}
		for _, j := range out.Jobs {
			got = append(got, j.ID)
		}
		return got
	}
	same := func(name string, got []string, want ...string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	same("user, newest first", ids(user1, ""), d.ID, a.ID)
	same("other user", ids(user2, ""), b.ID)
	same("admin sees all", ids(admin, ""), d.ID, c.ID, b.ID, a.ID)
	same("admin mine", ids(admin, "?mine=true"), c.ID)
	same("user mine", ids(user1, "?mine=true"), d.ID, a.ID)
	same("mine=false", ids(admin, "?mine=false"), d.ID, c.ID, b.ID, a.ID)
	same("one state", ids(admin, "?state=probed"), b.ID)
	same("two states", ids(admin, "?state=probed&state=done"), c.ID, b.ID)
	same("limit", ids(admin, "?limit=2"), d.ID, c.ID)
	same("offset", ids(admin, "?limit=2&offset=2"), b.ID, a.ID)
	same("offset past the end", ids(admin, "?offset=10"))

	var empty struct{ Jobs []jobView }
	_, body := e.do(&user2, "GET", "/api/jobs?state=done")
	_ = json.Unmarshal([]byte(body), &empty)
	equalJSON(t, body, `{"jobs": []}`)

	for _, q := range []string{"?state=nope", "?mine=maybe", "?limit=0", "?limit=201", "?limit=x", "?offset=-1", "?offset=x"} {
		e.fail(user1, "GET", "/api/jobs"+q, 400, "bad_request")
	}
}

func TestJobDetailGolden(t *testing.T) {
	e := newEnv(t)
	j := e.job("user1", store.Probed)
	e.addFile(j, "files/Some Film (2001).mkv", store.KindVideo, 4294967296, store.FileOK, `{"container":"matroska,webm"}`)
	e.addFile(j, "files/Some Film (2001).es.srt", store.KindSubtitle, 1704, store.FileOK, "")
	e.addFile(j, "archives/Extras.zip", store.KindArchive, 1000, store.FileOK, `{"refused":null,"skipped":[{"name":"../x","reason":"traversal"}]}`)
	e.addFile(j, "files/Bonus.mkv", store.KindVideo, 500, store.FileOK, "")
	e.addFile(j, "files/Gone.exe", store.KindVideo, 77, store.FileRefused, "")
	// A refused file keeps its row but is not counted and has no probe.
	files, _ := e.store.Files(t.Context(), j.ID)
	for _, f := range files {
		switch f.Path {
		case "files/Bonus.mkv":
			f.From = "archives/Extras.zip"
			_ = e.store.UpdateFile(t.Context(), f)
		case "files/Gone.exe":
			f.Reason = "not_video"
			_ = e.store.UpdateFile(t.Context(), f)
		}
	}
	id := j.ID
	want := `{
	"id": "` + id + `", "owner": "user1", "state": "probed", "attempt": 1, "error": null,
	"name": "Extras.zip", "fileCount": 4, "totalSize": 4294970500,
	"createdAt": "2026-01-01T12:00:00Z", "updatedAt": "2026-01-01T12:01:00Z", "expiresAt": null,
	"actions": ["cancel"],
	"files": [
	  {"id": 3, "path": "archives/Extras.zip", "name": "Extras.zip", "kind": "archive", "size": 1000, "status": "ok",
	   "reason": null, "from": null, "probe": null,
	   "extraction": {"refused":null,"skipped":[{"name":"../x","reason":"traversal"}]}},
	  {"id": 4, "path": "files/Bonus.mkv", "name": "Bonus.mkv", "kind": "video", "size": 500, "status": "ok",
	   "reason": null, "from": "archives/Extras.zip", "probe": null, "extraction": null},
	  {"id": 5, "path": "files/Gone.exe", "name": "Gone.exe", "kind": "video", "size": 77, "status": "refused",
	   "reason": "not_video", "from": null, "probe": null, "extraction": null},
	  {"id": 2, "path": "files/Some Film (2001).es.srt", "name": "Some Film (2001).es.srt", "kind": "subtitle",
	   "size": 1704, "status": "ok", "reason": null, "from": null, "probe": null, "extraction": null},
	  {"id": 1, "path": "files/Some Film (2001).mkv", "name": "Some Film (2001).mkv", "kind": "video",
	   "size": 4294967296, "status": "ok", "reason": null, "from": null, "probe": {"container":"matroska,webm"},
	   "extraction": null}
	],
	"uploads": [],
	"history": [
	  {"at": "2026-01-01T12:00:00Z", "from": null, "to": "uploading", "actor": "user1", "note": ""},
	  {"at": "2026-01-01T12:01:00Z", "from": "uploading", "to": "uploaded", "actor": "user1", "note": ""},
	  {"at": "2026-01-01T12:01:00Z", "from": "uploaded", "to": "probing", "actor": "user1", "note": ""},
	  {"at": "2026-01-01T12:01:00Z", "from": "probing", "to": "probed", "actor": "user1", "note": ""}
	],
	"manualImport": [
	  {"app": "radarr", "folder": "/downloads/intake/jobs/` + id + `/files", "url": "https://radarr.example.com/wanted/missing"},
	  {"app": "sonarr", "folder": "` + e.root + `/jobs/` + id + `/files", "url": null}
	],
	"target": null, "import": null, "result": null}`
	_, body := e.do(&user1, "GET", jobPath(j, ""))
	equalJSON(t, body, want)
}

func TestManualImportOnlyWhenProbed(t *testing.T) {
	e := newEnv(t)
	for _, st := range []store.State{store.Uploading, store.Uploaded, store.Matched, store.Done, store.Failed} {
		j := e.job("user1", st)
		var d struct{ ManualImport []json.RawMessage }
		e.ok(user1, "GET", jobPath(j, ""), 200, &d)
		if len(d.ManualImport) != 0 {
			t.Errorf("%s: %d manual imports", st, len(d.ManualImport))
		}
	}
}

func TestManualImportUnconfiguredApps(t *testing.T) {
	e := newEnv(t)
	e.srv.cfg.Radarr.URL = ""
	e.srv.cfg.Sonarr.PublicURL = "https://sonarr.example.com/base"
	e.srv.cfg.Paths.Mappings.Sonarr = nil
	j := e.job("user1", store.Probed)
	var d struct {
		ManualImport []struct{ App, Folder, URL string }
	}
	e.ok(user1, "GET", jobPath(j, ""), 200, &d)
	if len(d.ManualImport) != 1 || d.ManualImport[0].App != "sonarr" ||
		d.ManualImport[0].URL != "https://sonarr.example.com/base/wanted/missing" ||
		d.ManualImport[0].Folder != filepath.Join(e.root, "jobs", j.ID, "files") {
		t.Errorf("%+v", d.ManualImport)
	}
}

func TestManualImportMappedFolder(t *testing.T) {
	e := newEnv(t)
	e.srv.cfg.Paths.IntakeRoot = "/data/intake"
	e.srv.cfg.Paths.Mappings.Radarr = []config.PathMapping{{From: "/data/intake", To: "/downloads/intake"}}
	j := e.job("user1", store.Probed)
	var d struct {
		ManualImport []struct{ App, Folder string }
	}
	e.ok(user1, "GET", jobPath(j, ""), 200, &d)
	if want := "/downloads/intake/jobs/" + j.ID + "/files"; d.ManualImport[0].Folder != want {
		t.Errorf("folder %q, want %q", d.ManualImport[0].Folder, want)
	}
}

func TestJobUploadsAndName(t *testing.T) {
	e := newEnv(t)
	j := e.job("user1", store.Uploading)
	var d struct {
		Name    *string
		Uploads []map[string]any
		Actions []string
	}
	e.ok(user1, "GET", jobPath(j, ""), 200, &d)
	if d.Name != nil || len(d.Uploads) != 0 {
		t.Errorf("empty job: %+v", d)
	}

	up := store.Upload{ID: strings.Repeat("9a", 16), JobID: j.ID, Owner: "user1", Name: "Extra.mkv", Size: 1000, Offset: 400, CreatedAt: e.now}
	if err := e.store.PutUpload(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	_, body := e.do(&user1, "GET", jobPath(j, ""))
	var got struct {
		Name    *string
		Uploads json.RawMessage
		Actions []string
	}
	_ = json.Unmarshal([]byte(body), &got)
	if got.Name == nil || *got.Name != "Extra.mkv" {
		t.Errorf("name from the upload: %v", got.Name)
	}
	equalJSON(t, string(got.Uploads), `[{"id":"`+up.ID+`","name":"Extra.mkv","size":1000,"offset":400}]`)
	if len(got.Actions) != 1 || got.Actions[0] != "cancel" {
		t.Errorf("actions with an unfinished upload: %v", got.Actions)
	}

	// The first video beats an upload; an archive beats a video.
	e.addFile(j, "files/B.mkv", store.KindVideo, 1, store.FilePending, "")
	e.ok(user1, "GET", jobPath(j, ""), 200, &d)
	if *d.Name != "B.mkv" {
		t.Errorf("name %q", *d.Name)
	}
	e.addFile(j, "archives/Z.zip", store.KindArchive, 1, store.FilePending, "")
	e.ok(user1, "GET", jobPath(j, ""), 200, &d)
	if *d.Name != "Z.zip" {
		t.Errorf("name %q", *d.Name)
	}
}

func TestJobError(t *testing.T) {
	e := newEnv(t)
	j := e.job("user1", store.Failed)
	var v jobView
	e.ok(user1, "GET", jobPath(j, ""), 200, &v)
	if v.Error == nil || v.Error.Code != "worker_unavailable" || strings.Contains(v.Error.Message, "/spool") {
		t.Errorf("owner sees %+v", v.Error)
	}
	e.ok(admin, "GET", jobPath(j, ""), 200, &v)
	if v.Error == nil || v.Error.Message != "no heartbeat in /spool" {
		t.Errorf("admin sees %+v", v.Error)
	}
	s := e.job("user1", store.Stalled)
	e.ok(user1, "GET", jobPath(s, ""), 200, &v)
	if v.Error == nil || v.State != "stalled" || len(v.Actions) != 1 || v.Actions[0] != "cancel" {
		t.Errorf("stalled %+v", v)
	}
}

func TestActionsByState(t *testing.T) {
	e := newEnv(t)
	want := map[store.State]string{
		store.Uploaded: "cancel", store.Probing: "cancel", store.Probed: "cancel", store.Matched: "cancel",
		store.AwaitingConfirmation: "cancel", store.Importing: "", store.Imported: "", store.PostProcessing: "",
		store.Done: "delete", store.Failed: "cancel,retry,delete", store.Stalled: "cancel", store.Cancelled: "delete",
	}
	for st, w := range want {
		j := e.job("user1", st)
		var v jobView
		e.ok(user1, "GET", jobPath(j, ""), 200, &v)
		if got := strings.Join(v.Actions, ","); got != w || v.Actions == nil {
			t.Errorf("%s: actions %q, want %q", st, got, w)
		}
	}
	// Uploading: submit only with a file and no unfinished upload.
	j := e.job("user1", store.Uploading)
	e.addFile(j, "files/A.mkv", store.KindVideo, 1, store.FilePending, "")
	var v jobView
	e.ok(user1, "GET", jobPath(j, ""), 200, &v)
	if got := strings.Join(v.Actions, ","); got != "cancel,submit" {
		t.Errorf("uploading with a file: %q", got)
	}
}

func TestSubmit(t *testing.T) {
	e := newEnv(t)
	j := e.job("user1", store.Uploading)
	e.fail(user1, "POST", jobPath(j, "submit"), 409, "no_files")

	e.addFile(j, "files/A.mkv", store.KindVideo, 5, store.FilePending, "")
	up := store.Upload{ID: strings.Repeat("ab", 16), JobID: j.ID, Owner: "user1", Name: "B.mkv", Size: 10, Offset: 3}
	if err := e.store.PutUpload(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	e.fail(user1, "POST", jobPath(j, "submit"), 409, "uploads_unfinished")
	if got := e.audit(j.ID); len(got) != 0 {
		t.Errorf("refusals are not audited: %v", got)
	}

	if err := e.store.DeleteUpload(t.Context(), up.ID); err != nil {
		t.Fatal(err)
	}
	var v jobView
	e.ok(user1, "POST", jobPath(j, "submit"), 200, &v)
	if v.State != "uploaded" || v.FileCount != 1 || strings.Join(v.Actions, ",") != "cancel" {
		t.Errorf("%+v", v)
	}
	if got := e.audit(j.ID); strings.Join(got, ",") != "job.submitted:user1" {
		t.Errorf("audit %v", got)
	}
	h, _ := e.store.History(t.Context(), j.ID)
	if last := h[len(h)-1]; last.To != store.Uploaded || last.Actor != "user1" {
		t.Errorf("history %+v", last)
	}
	e.fail(user1, "POST", jobPath(j, "submit"), 409, "wrong_state")

	other := e.job("user1", store.Probed)
	e.fail(user1, "POST", jobPath(other, "submit"), 409, "wrong_state")
}

func TestCancel(t *testing.T) {
	e := newEnv(t)
	for _, st := range []store.State{
		store.Uploading, store.Uploaded, store.Probing, store.Probed, store.Matched,
		store.AwaitingConfirmation, store.Failed, store.Stalled,
	} {
		j := e.job("user1", st)
		var v jobView
		e.ok(user1, "POST", jobPath(j, "cancel"), 200, &v)
		if v.State != "cancelled" || strings.Join(v.Actions, ",") != "delete" {
			t.Errorf("%s: %+v", st, v)
		}
		if got := e.audit(j.ID); strings.Join(got, ",") != "job.cancelled:user1" {
			t.Errorf("%s: audit %v", st, got)
		}
		stored, _ := e.store.Job(t.Context(), j.ID)
		if stored.ErrorCode != "" {
			t.Errorf("%s: error kept: %q", st, stored.ErrorCode)
		}
		e.fail(user1, "POST", jobPath(j, "cancel"), 409, "wrong_state")
	}
	for _, st := range []store.State{store.Importing, store.Imported, store.PostProcessing, store.Done} {
		e.fail(user1, "POST", jobPath(e.job("user1", st), "cancel"), 409, "wrong_state")
	}
}

func TestRetry(t *testing.T) {
	e := newEnv(t)
	j := e.job("user1", store.Failed) // failed while probing: resumes in uploaded
	var v jobView
	e.ok(user1, "POST", jobPath(j, "retry"), 200, &v)
	if v.State != "uploaded" || v.Attempt != 2 || v.Error != nil {
		t.Errorf("%+v", v)
	}
	if got := e.audit(j.ID); strings.Join(got, ",") != "job.retried:user1" {
		t.Errorf("audit %v", got)
	}
	e.fail(user1, "POST", jobPath(j, "retry"), 409, "wrong_state")
	for _, st := range []store.State{store.Uploading, store.Probed, store.Stalled, store.Cancelled, store.Done} {
		e.fail(user1, "POST", jobPath(e.job("user1", st), "retry"), 409, "wrong_state")
	}
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	for _, st := range []store.State{store.Uploading, store.Probed, store.Stalled, store.Importing} {
		j := e.job("user1", st)
		e.fail(user1, "DELETE", jobPath(j, ""), 409, "job_active")
		if _, err := e.store.Job(t.Context(), j.ID); err != nil {
			t.Errorf("%s: the job is gone", st)
		}
	}

	for _, st := range []store.State{store.Cancelled, store.Done, store.Failed} {
		j := e.job("user1", st)
		dir := jobdir.Dir(e.root, j.ID)
		if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "files", "A.mkv"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		e.addFile(j, "files/A.mkv", store.KindVideo, 1, store.FileOK, "")
		upID := strings.Repeat("cd", 16)
		if err := e.store.PutUpload(t.Context(), store.Upload{ID: upID, JobID: j.ID, Owner: "user1", Name: "B.mkv", Size: 9}); err != nil {
			t.Fatal(err)
		}
		incoming := filepath.Join(e.root, jobdir.Incoming)
		if err := os.MkdirAll(incoming, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{upID, upID + ".info"} {
			if err := os.WriteFile(filepath.Join(incoming, n), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		code, body := e.do(&user1, "DELETE", jobPath(j, ""))
		if code != http.StatusNoContent || body != "" {
			t.Fatalf("%s: %d %q", st, code, body)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s: the job directory is still there: %v", st, err)
		}
		for _, n := range []string{upID, upID + ".info"} {
			if _, err := os.Stat(filepath.Join(incoming, n)); !os.IsNotExist(err) {
				t.Errorf("%s: %s is still there", st, n)
			}
		}
		if _, err := e.store.Job(t.Context(), j.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: job row: %v", st, err)
		}
		if files, _ := e.store.Files(t.Context(), j.ID); len(files) != 0 {
			t.Errorf("%s: files remain", st)
		}
		if ups, _ := e.store.Uploads(t.Context(), "", j.ID); len(ups) != 0 {
			t.Errorf("%s: uploads remain", st)
		}
		audit := e.audit(j.ID)
		if len(audit) == 0 || audit[len(audit)-1] != "job.deleted:user1" {
			t.Errorf("%s: audit %v", st, audit)
		}
		e.fail(user1, "GET", jobPath(j, ""), 404, "not_found")
	}
}

func TestAuthorisation(t *testing.T) {
	e := newEnv(t)
	states := []store.State{store.Uploading, store.Failed, store.Done}
	for _, st := range states {
		j := e.job("user1", st)
		e.addFile(j, "files/A.mkv", store.KindVideo, 1, store.FilePending, "")
		for _, c := range [][2]string{
			{"GET", jobPath(j, "")}, {"POST", jobPath(j, "submit")}, {"POST", jobPath(j, "cancel")},
			{"POST", jobPath(j, "retry")}, {"DELETE", jobPath(j, "")},
		} {
			e.fail(user2, c[0], c[1], 404, "not_found")
		}
		if stored, _ := e.store.Job(t.Context(), j.ID); stored.State != st {
			t.Errorf("%s: another user changed the job to %s", st, stored.State)
		}
		if got := e.audit(j.ID); len(got) != 0 {
			t.Errorf("%s: audit %v", st, got)
		}
		e.ok(admin, "GET", jobPath(j, ""), 200, nil) // an admin sees everyone's
	}

	// An admin changes other users' jobs, as themselves.
	j := e.job("user1", store.Uploading)
	e.addFile(j, "files/A.mkv", store.KindVideo, 1, store.FilePending, "")
	var v jobView
	e.ok(admin, "POST", jobPath(j, "submit"), 200, &v)
	e.ok(admin, "POST", jobPath(j, "cancel"), 200, &v)
	e.ok(admin, "DELETE", jobPath(j, ""), 204, nil)
	if got := strings.Join(e.audit(j.ID), ","); got != "job.submitted:admin1,job.cancelled:admin1,job.deleted:admin1" {
		t.Errorf("audit %s", got)
	}
}

func TestMalformedIDIs404BeforeTheStore(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{
		"%2e%2e", "abc", strings.Repeat("A", 32), strings.Repeat("g", 32), strings.Repeat("a", 31),
		strings.Repeat("a", 33), "..%2f..%2fetc", strings.Repeat("a", 32) + "%2f..",
	} {
		for _, c := range [][2]string{
			{"GET", "/api/jobs/" + id}, {"POST", "/api/jobs/" + id + "/submit"}, {"POST", "/api/jobs/" + id + "/cancel"},
			{"POST", "/api/jobs/" + id + "/retry"}, {"DELETE", "/api/jobs/" + id},
		} {
			code, _ := e.do(&admin, c[0], c[1])
			if code != http.StatusNotFound {
				t.Errorf("%s %s: %d", c[0], c[1], code)
			}
		}
	}
	if e.store.jobCalls != 0 {
		t.Errorf("the store was asked for a malformed id %d times", e.store.jobCalls)
	}
	e.fail(admin, "GET", "/api/jobs/"+strings.Repeat("a", 32), 404, "not_found")
}
