package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/preview"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/store"
)

func (e *flow2Env) callAs(user, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequestWithContext(e.t.Context(), method, "https://fukku.example.com"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Remote-User", user)
	r.Header.Set("Remote-Groups", "users")
	if method != http.MethodGet {
		r.Header.Set("Origin", "https://fukku.example.com")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

// readyJob is a flow 2 job awaiting confirmation whose upload is the same cut.
func (e *flow2Env) readyJob() store.Job {
	e.t.Helper()
	j := e.probedJob(5_400_400)
	if code, body := e.call("POST", "/api/jobs/"+j.ID+"/match", `{"app":"radarr","tmdbId":605}`); code != http.StatusOK {
		e.t.Fatalf("match: %d %s", code, body)
	}
	e.step(j)
	e.q.Complete(e.q.Tasks()[0].ID, media.LibraryProbeOutput{
		Path: "/data/media/movies/Has File (2004)/Has File (2004) WEBDL-1080p.mkv", OK: true, FileID: 21, TrackedSize: 4000,
		Media: &media.Probe{DurationMs: 5_400_000, Size: 4000, Video: []media.VideoTrack{{DurationMs: 5_400_000}}},
	})
	if j = e.step(j); j.State != store.AwaitingConfirmation {
		e.t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	return j
}

type previewBody struct {
	State      string `json:"state"`
	OffsetMs   *int64 `json:"offsetMs"`
	AudioTrack *int   `json:"audioTrack"`
	Error      *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Clips []struct {
		N        int    `json:"n"`
		StartMs  int64  `json:"startMs"`
		LengthMs int64  `json:"lengthMs"`
		Size     int64  `json:"size"`
		URL      string `json:"url"`
	} `json:"clips"`
}

func decodePreview(t *testing.T, body string) previewBody {
	t.Helper()
	var v previewBody
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return v
}

// The whole of #53's API: the request, the clips listed once the worker has rendered
// them, and each clip streamed with range requests.
func TestPreviewThroughTheAPI(t *testing.T) {
	intake := t.TempDir()
	e := newFlow2Env(t, func(c *config.Config) { c.Paths.IntakeRoot = intake })
	j := e.readyJob()
	base := "/api/jobs/" + j.ID + "/preview"

	code, body := e.call("GET", base, "")
	if v := decodePreview(t, body); code != http.StatusOK || v.State != "none" || v.OffsetMs != nil || v.Clips == nil {
		t.Fatalf("before: %d %s", code, body)
	}
	code, body = e.call("POST", base, `{"offsetMs": -800, "audioTrack": 1}`)
	v := decodePreview(t, body)
	if code != http.StatusAccepted || v.State != "pending" || *v.OffsetMs != -800 || *v.AudioTrack != 1 || len(v.Clips) != 0 {
		t.Fatalf("POST: %d %s", code, body)
	}
	audit, _ := e.st.AuditLog(t.Context(), j.ID)
	if last := audit[len(audit)-1]; last.Action != "preview.requested" || last.Detail["offsetMs"] != "-800" || last.Detail["audioTrack"] != "1" {
		t.Errorf("audit %+v", last)
	}
	if code, _ := e.call("GET", base+"/1", ""); code != http.StatusNotFound {
		t.Errorf("clip of a pending preview: %d", code)
	}

	// The worker renders the clips.
	tasks := e.q.Tasks()
	if len(tasks) != 1 || tasks[0].ID != runner.PreviewID(j.ID, 1) {
		t.Fatalf("tasks %+v", tasks)
	}
	var a preview.Args
	if err := json.Unmarshal(tasks[0].Args, &a); err != nil {
		t.Fatal(err)
	}
	// The worker renders them into its own directory of the job.
	dir := filepath.Join(intake, jobdir.Worker, j.ID, filepath.FromSlash(preview.Dir))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	out := preview.Output{Request: a.Request, OK: true}
	clips := map[int][]byte{}
	for i, c := range a.Clips {
		data := bytes.Repeat([]byte{byte('a' + i)}, 100+i)
		clips[i+1] = data
		if err := os.WriteFile(filepath.Join(dir, preview.ClipName(a.Request, i+1)), data, 0o600); err != nil {
			t.Fatal(err)
		}
		out.Clips = append(out.Clips, preview.ClipFile{N: i + 1, StartMs: c.StartMs, LengthMs: c.LengthMs, Size: int64(len(data))})
	}
	e.q.Complete(tasks[0].ID, out)
	if err := e.p.Previews(t.Context(), j); err != nil {
		t.Fatal(err)
	}

	code, body = e.call("GET", base, "")
	v = decodePreview(t, body)
	if code != http.StatusOK || v.State != "ready" || len(v.Clips) != 3 || v.Error != nil {
		t.Fatalf("ready: %d %s", code, body)
	}
	if c := v.Clips[1]; c.N != 2 || c.URL != base+"/2" || c.Size != 101 || c.LengthMs != 20_000 || c.StartMs != a.Clips[1].StartMs {
		t.Errorf("clip %+v", c)
	}
	rec := e.callAs("user1", "GET", base+"/2", "", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "video/mp4" || !bytes.Equal(rec.Body.Bytes(), clips[2]) ||
		rec.Header().Get("Accept-Ranges") != "bytes" || rec.Header().Get("ETag") == "" {
		t.Fatalf("clip 2: %d %v %q", rec.Code, rec.Header(), rec.Body.Bytes())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "media-src 'self'") {
		t.Errorf("CSP %q", csp)
	}
	rec = e.callAs("user1", "GET", base+"/3", "", map[string]string{"Range": "bytes=10-19"})
	if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Range") != "bytes 10-19/102" || !bytes.Equal(rec.Body.Bytes(), clips[3][10:20]) {
		t.Errorf("range: %d %v %q", rec.Code, rec.Header(), rec.Body.Bytes())
	}
	for _, n := range []string{"0", "4", "01", "-1", "x", "1.mp4"} {
		if code, _ := e.call("GET", base+"/"+n, ""); code != http.StatusNotFound {
			t.Errorf("clip %s: %d", n, code)
		}
	}

	// A file that is not the one the worker reported, or a link out of the job, is not
	// served.
	if err := os.WriteFile(filepath.Join(dir, preview.ClipName(a.Request, 1)), []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.call("GET", base+"/1", ""); code != http.StatusNotFound {
		t.Errorf("changed clip: %d", code)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, clips[3], 0o600); err != nil {
		t.Fatal(err)
	}
	clip3 := filepath.Join(dir, preview.ClipName(a.Request, 3))
	if err := os.Remove(clip3); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, clip3); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.call("GET", base+"/3", ""); code != http.StatusNotFound {
		t.Errorf("linked clip: %d", code)
	}
	// A link that stays inside, to a file of the size reported, is not served either (#113).
	clip2 := filepath.Join(dir, preview.ClipName(a.Request, 2))
	if err := os.Rename(clip2, filepath.Join(dir, "other.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.mp4", clip2); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.call("GET", base+"/2", ""); code != http.StatusNotFound {
		t.Errorf("clip linked inside: %d", code)
	}

	// Somebody else's job does not exist for them.
	for _, m := range []struct{ method, path, body string }{{"GET", base, ""}, {"POST", base, `{}`}, {"GET", base + "/2", ""}} {
		if rec := e.callAs("user2", m.method, m.path, m.body, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s as another user: %d", m.method, m.path, rec.Code)
		}
	}

	// A new offset replaces the clips.
	code, body = e.call("POST", base, `{"offsetMs": 40}`)
	if v = decodePreview(t, body); code != http.StatusAccepted || v.State != "pending" || *v.AudioTrack != 1 {
		t.Fatalf("new offset: %d %s", code, body)
	}
	if code, _ := e.call("GET", base+"/2", ""); code != http.StatusNotFound {
		t.Errorf("an earlier request's clip: %d", code)
	}
	code, body = e.call("POST", base, `{"offsetMs": 40}`)
	if code != http.StatusOK || decodePreview(t, body).State != "pending" {
		t.Errorf("same request again: %d %s", code, body)
	}

	// A failure says what failed; the technical detail is for admins.
	tasks = e.q.Tasks()
	_ = json.Unmarshal(tasks[0].Args, &a)
	e.q.Complete(tasks[0].ID, preview.Output{Request: a.Request, Refusal: preview.RefusalTimeout, Detail: "clip 1: ran past merge.previewTimeout (5m0s)"})
	if err := e.p.Previews(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	_, body = e.call("GET", base, "")
	if v = decodePreview(t, body); v.State != "failed" || v.Error == nil || v.Error.Code != "timeout" || strings.Contains(v.Error.Message, "previewTimeout") {
		t.Errorf("failed: %s", body)
	}
}

func TestPreviewRefused(t *testing.T) {
	e := newFlow2Env(t, nil)
	flow1 := e.probedJob(5_400_000)
	if err := e.st.SetTarget(t.Context(), flow1.ID, store.Target{App: "radarr", TitleID: 99}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ method, path string }{{"POST", ""}, {"GET", ""}} {
		code, body := e.call(m.method, "/api/jobs/"+flow1.ID+"/preview"+m.path, `{}`)
		if code != http.StatusConflict || !strings.Contains(body, `"code":"not_flow2"`) {
			t.Errorf("%s flow 1: %d %s", m.method, code, body)
		}
	}
	if code, _ := e.call("GET", "/api/jobs/"+flow1.ID+"/preview/1", ""); code != http.StatusNotFound {
		t.Errorf("clip of a flow 1 job: %d", code)
	}
	noTarget := e.probedJob(5_400_000)
	if code, body := e.call("GET", "/api/jobs/"+noTarget.ID+"/preview", ""); code != http.StatusConflict || !strings.Contains(body, "not_flow2") {
		t.Errorf("no target: %d %s", code, body)
	}

	j := e.readyJob()
	base := "/api/jobs/" + j.ID + "/preview"
	for body, want := range map[string]string{
		`{"offsetMs": 60001}`: `"code":"bad_request"`,
		`{"audioTrack": 4}`:   `"code":"bad_request"`,
		`{"offset": 1}`:       `"code":"bad_request"`,
		`not json`:            `"code":"bad_request"`,
	} {
		if code, got := e.call("POST", base, body); code != http.StatusBadRequest || !strings.Contains(got, want) {
			t.Errorf("POST %s: %d %s", body, code, got)
		}
	}
	if code, _ := e.call("GET", "/api/jobs/"+strings.Repeat("0", 32)+"/preview", ""); code != http.StatusNotFound {
		t.Errorf("unknown job: %d", code)
	}
	// Once the job is cancelled there is nothing to preview.
	if _, err := e.st.Transition(t.Context(), j.ID, store.AwaitingConfirmation, store.Cancelled, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	if code, body := e.call("POST", base, `{}`); code != http.StatusConflict || !strings.Contains(body, "wrong_state") {
		t.Errorf("cancelled: %d %s", code, body)
	}
}

// A merge that is not offered (a different cut) has nothing to check.
func TestPreviewMergeNotOffered(t *testing.T) {
	e := newFlow2Env(t, func(c *config.Config) { c.Merge.Enabled = false })
	j := e.readyJob()
	if code, body := e.call("POST", "/api/jobs/"+j.ID+"/preview", `{}`); code != http.StatusConflict || !strings.Contains(body, "merge_not_offered") {
		t.Errorf("merge disabled: %d %s", code, body)
	}
}

// Both fields are optional: an empty body asks for offset 0 and the default track.
func TestPreviewEmptyBody(t *testing.T) {
	e := newFlow2Env(t, nil)
	j := e.readyJob()
	code, body := e.call("POST", "/api/jobs/"+j.ID+"/preview", "")
	if v := decodePreview(t, body); code != http.StatusAccepted || v.State != "pending" || *v.OffsetMs != 0 || *v.AudioTrack != 1 {
		t.Errorf("empty body: %d %s", code, body)
	}
}
