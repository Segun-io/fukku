package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/preview"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
)

// previewed gives the flow 2 job awaiting confirmation a ready preview at -800 ms.
func (e *flow2Env) previewed(j store.Job) store.Job {
	e.t.Helper()
	if code, body := e.call("POST", "/api/jobs/"+j.ID+"/preview", `{"offsetMs": -800}`); code != http.StatusAccepted {
		e.t.Fatalf("preview: %d %s", code, body)
	}
	task := e.q.Tasks()[0]
	var a preview.Args
	if err := json.Unmarshal(task.Args, &a); err != nil {
		e.t.Fatal(err)
	}
	out := preview.Output{Request: a.Request, OK: true}
	for i, c := range a.Clips {
		out.Clips = append(out.Clips, preview.ClipFile{N: i + 1, StartMs: c.StartMs, LengthMs: c.LengthMs, Size: 100})
	}
	e.q.Complete(task.ID, out)
	if err := e.p.Previews(e.t.Context(), j); err != nil {
		e.t.Fatal(err)
	}
	return j
}

// The merge's confirmation through the API (#55): refused without the sync check, then
// accepted, with the job's import showing the merge.
func TestMergeConfirmThroughTheAPI(t *testing.T) {
	e := newFlow2Env(t, func(c *config.Config) {
		c.Paths.IntakeRoot = t.TempDir()
		c.Paths.RequireSameFilesystem = "never"
	})
	j := e.readyJob()
	confirm := "/api/jobs/" + j.ID + "/confirm"
	code, body := e.call("POST", confirm, `{"flow2": "merge"}`)
	if code != http.StatusConflict || !strings.Contains(body, `"code":"preview_required"`) {
		t.Fatalf("without a preview: %d %s", code, body)
	}

	j = e.previewed(j)
	for req, want := range map[string]string{
		`{"flow2": "merge", "merge": {"offsetMs": -700}}`:        `"code":"preview_required"`,
		`{"flow2": "merge", "merge": {"audioTracks": [7]}}`:      `"code":"preview_required"`,
		`{"flow2": "merge", "merge": {"languageTag": "ES-419"}}`: `"code":"bad_request"`,
		`{"flow2": "merge", "overrides": ["r1"]}`:                `"code":"bad_request"`,
		`{"flow2": "merge", "merge": {"tracks": [1]}}`:           `"code":"bad_request"`,
	} {
		code, body := e.call("POST", confirm, req)
		if code == http.StatusAccepted || !strings.Contains(body, want) {
			t.Errorf("%s: %d %s", req, code, body)
		}
	}
	code, body = e.call("POST", confirm, `{"flow2": "merge", "merge": {"audioTracks": [1], "trackName": "Latino"}}`)
	if code != http.StatusAccepted || !strings.Contains(body, `"state":"importing"`) {
		t.Fatalf("confirm: %d %s", code, body)
	}
	audit, _ := e.st.AuditLog(t.Context(), j.ID)
	if last := audit[len(audit)-1]; last.Action != "merge.confirmed" || last.Detail["trackName"] != "Latino" || last.Detail["offsetMs"] != "-800" {
		t.Errorf("audit %+v", last)
	}
	code, body = e.call("GET", "/api/jobs/"+j.ID, "")
	if code != http.StatusOK {
		t.Fatalf("job: %d %s", code, body)
	}
	var d struct {
		Import struct {
			Status string          `json:"status"`
			Merge  json.RawMessage `json:"merge"`
		} `json:"import"`
	}
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatal(err)
	}
	if d.Import.Status != "pending" {
		t.Errorf("import %s", body)
	}
	equalJSON(t, string(d.Import.Merge), `{"state": "pending", "refusal": "", "audioTracks": [1], "subtitleTracks": [],
	  "offsetMs": -800, "languageTag": "es-419", "trackName": "Latino", "keepOriginalDefault": true,
	  "arrLanguage": null, "name": "", "size": 0}`)
	// The preview's tasks are cancelled; nothing waits in its slots.
	for _, task := range e.q.Tasks() {
		if task.Kind == spool.KindPreview {
			t.Errorf("a preview task still waits: %+v", task)
		}
	}
	if _, ok, _ := e.q.Result(t.Context(), runner.PreviewID(j.ID, 1)); ok {
		t.Error("the preview's result is still in the spool")
	}
}

// flow2 on a job that is not flow 2 is a bad request.
func TestMergeConfirmRefusedForFlow1ThroughTheAPI(t *testing.T) {
	e := newConfirmEnv(t)
	j, _ := e.ready()
	rec := e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/confirm", `{"flow2": "merge"}`, true)
	e.want(rec, http.StatusBadRequest, "bad_request")
}
