package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool/spooltest"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
	"github.com/segun-io/fukku/internal/worker"
)

// flow2Env is the API and the matched step of a flow 2 job, with the fakes and a live
// worker heartbeat: the film (tmdb 605, id 8) has a tracked file.
type flow2Env struct {
	t   *testing.T
	h   http.Handler
	st  *storetest.Fake
	q   *spooltest.Queue
	p   *flows.Proposals
	cfg config.Config
}

func newFlow2Env(t *testing.T, tweak func(*config.Config)) *flow2Env {
	t.Helper()
	cfg := config.Defaults()
	cfg.Auth.TrustedProxies = []string{"127.0.0.1"}
	cfg.Server.PublicBaseURL = "https://fukku.example.com"
	cfg.Paths.IntakeRoot = "/data/intake"
	cfg.Paths.LibraryRoots = []string{"/data/media/movies"}
	cfg.Paths.Mappings.Radarr = []config.PathMapping{{From: "/data/media/movies", To: "/movies"}}
	cfg.Radarr.URL = "http://radarr.example.com:7878"
	sentinel := filepath.Join(t.TempDir(), ".sentinel")
	if err := os.WriteFile(sentinel, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Paths.Sentinels = []string{sentinel}
	cfg.Worker.SpoolDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg.Worker.SpoolDir, worker.HeartbeatFile), []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tweak != nil {
		tweak(&cfg)
	}
	st := storetest.NewFake(nil)
	movies := &arrtest.FakeMovies{
		Results: []arr.Movie{{ID: 8, TMDBID: 605, Title: "Has File", Year: 2004, HasFile: true}},
		Library: []arr.Movie{{ID: 8, TMDBID: 605, Title: "Has File", Year: 2004, OriginalLanguage: "English", HasFile: true,
			File: &arr.MovieFile{ID: 21, Path: "/movies/Has File (2004)/Has File (2004) WEBDL-1080p.mkv", Size: 4000,
				Quality: arr.Quality{ID: 3, Name: "WEBDL-1080p"}, Languages: []arr.Language{{ID: 1, Name: "English"}}, RuntimeMs: 5_400_000}}},
		Queue: map[int]bool{},
	}
	q := &spooltest.Queue{}
	p := flows.NewProposals(cfg, nil, st, movies, nil, nil)
	p.SetQueue(q)
	deps := Deps{Store: st, Queue: q, Radarr: movies, Proposals: p, Imports: flows.NewEngine(p)}
	h := New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, deps).Handler()
	return &flow2Env{t: t, h: h, st: st, q: q, p: p, cfg: cfg}
}

func (e *flow2Env) call(method, path, body string) (int, string) {
	e.t.Helper()
	r := httptest.NewRequestWithContext(e.t.Context(), method, "https://fukku.example.com"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Remote-User", "user1")
	r.Header.Set("Remote-Groups", "users")
	if method != http.MethodGet {
		r.Header.Set("Origin", "https://fukku.example.com")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

// probedJob is a probed job of user1 with one upload of the duration.
func (e *flow2Env) probedJob(uploadMs int64) store.Job {
	e.t.Helper()
	ctx := e.t.Context()
	j, err := e.st.CreateJob(ctx, "user1")
	if err != nil {
		e.t.Fatal(err)
	}
	if j, err = storetest.Drive(ctx, e.st, j.ID, store.Probed); err != nil {
		e.t.Fatal(err)
	}
	pr, _ := json.Marshal(media.Probe{Container: "matroska,webm", DurationMs: uploadMs, Size: 3000,
		Video: []media.VideoTrack{{Codec: "h264", Width: 1920, Height: 1080, DurationMs: uploadMs}},
		Audio: []media.AudioTrack{{Index: 1, Codec: "ac3", Language: "spa", Title: "Español (Latino)", Channels: 6, Default: true}}})
	if _, err := e.st.AddFile(ctx, store.File{JobID: j.ID, Path: "files/Has File (2004).es.mkv", Kind: store.KindVideo, Size: 3000, Status: store.FileOK, Report: pr}); err != nil {
		e.t.Fatal(err)
	}
	return j
}

func (e *flow2Env) step(j store.Job) store.Job {
	e.t.Helper()
	cur, err := e.st.Job(e.t.Context(), j.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.p.Matched(e.t.Context(), cur); err != nil {
		e.t.Fatal(err)
	}
	if cur, err = e.st.Job(e.t.Context(), j.ID); err != nil {
		e.t.Fatal(err)
	}
	return cur
}

func (e *flow2Env) flow2(j store.Job) string {
	e.t.Helper()
	code, body := e.call("GET", "/api/jobs/"+j.ID, "")
	if code != http.StatusOK {
		e.t.Fatalf("GET job: %d %s", code, body)
	}
	var d struct {
		Flow2 json.RawMessage `json:"flow2"`
	}
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		e.t.Fatal(err)
	}
	return string(d.Flow2)
}

// The whole of #52 through the API: the match of a film with a file, the library probe,
// and the comparison with what can be offered, in JobDetail.
func TestFlow2ThroughTheAPI(t *testing.T) {
	e := newFlow2Env(t, nil)
	j := e.probedJob(5_400_400)
	if code, body := e.call("POST", "/api/jobs/"+j.ID+"/match", `{"app":"radarr","tmdbId":605}`); code != http.StatusOK {
		t.Fatalf("match: %d %s", code, body)
	}
	equalJSON(t, e.flow2(j), `{"state": "pending",
	  "library": {"fileId": 21, "name": "Has File (2004) WEBDL-1080p.mkv", "size": 4000,
	              "quality": {"id": 3, "name": "WEBDL-1080p"}, "languages": [{"id": 1, "name": "English"}],
	              "runtimeMs": 5400000, "durationMs": null, "audio": [], "subtitles": []},
	  "upload": {"fileId": 1, "name": "Has File (2004).es.mkv", "size": 3000, "quality": null, "languages": [],
	             "durationMs": 5400400,
	             "audio": [{"index": 1, "codec": "ac3", "language": "spa", "title": "Español (Latino)", "channels": 6, "default": true}],
	             "subtitles": []},
	  "finding": null, "deltaMs": null, "ratio": null, "offers": [], "notOffered": []}`)

	if j = e.step(j); j.State != store.Matched {
		t.Fatalf("job %s", j.State)
	}
	tasks := e.q.Tasks()
	if len(tasks) != 1 || tasks[0].ID != runner.LibraryProbeID(j.ID, 1) ||
		string(tasks[0].Args) != `{"path":"/data/media/movies/Has File (2004)/Has File (2004) WEBDL-1080p.mkv"}` {
		t.Fatalf("tasks %+v", tasks)
	}
	e.q.Complete(tasks[0].ID, media.LibraryProbeOutput{
		Path: "/data/media/movies/Has File (2004)/Has File (2004) WEBDL-1080p.mkv", OK: true,
		Media: &media.Probe{Container: "matroska,webm", DurationMs: 5_400_030, Size: 4000,
			Video: []media.VideoTrack{{Codec: "h264", Width: 1920, Height: 1080, DurationMs: 5_400_000}},
			Audio: []media.AudioTrack{{Index: 1, Codec: "eac3", Language: "eng", Channels: 6, Default: true}}},
	})
	if j = e.step(j); j.State != store.AwaitingConfirmation {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	equalJSON(t, e.flow2(j), `{"state": "ready",
	  "library": {"fileId": 21, "name": "Has File (2004) WEBDL-1080p.mkv", "size": 4000,
	              "quality": {"id": 3, "name": "WEBDL-1080p"}, "languages": [{"id": 1, "name": "English"}],
	              "runtimeMs": 5400000, "durationMs": 5400000,
	              "audio": [{"index": 1, "codec": "eac3", "language": "eng", "channels": 6, "default": true}], "subtitles": []},
	  "upload": {"fileId": 1, "name": "Has File (2004).es.mkv", "size": 3000, "quality": null, "languages": [],
	             "durationMs": 5400400,
	             "audio": [{"index": 1, "codec": "ac3", "language": "spa", "title": "Español (Latino)", "channels": 6, "default": true}],
	             "subtitles": []},
	  "finding": "match", "deltaMs": 400, "ratio": 1.0000740740740741,
	  "offers": ["merge"], "notOffered": [{"offer": "version", "reason": "versions_disabled"}]}`)

	// The flow 1 confirmation would import the upload over the film's file: refused.
	code, body := e.call("POST", "/api/jobs/"+j.ID+"/confirm", `{}`)
	if code != http.StatusConflict || !strings.Contains(body, `"code":"flow2"`) {
		t.Errorf("confirm: %d %s", code, body)
	}
}

// With merge and second versions both turned off, the answer says that nothing can be
// offered, and why; a PAL copy and a different cut say why a merge is not offered.
func TestFlow2NothingOffered(t *testing.T) {
	for name, tc := range map[string]struct {
		uploadMs   int64
		finding    string
		notOffered string
	}{
		"same cut":  {5_400_400, "match", `[{"offer": "merge", "reason": "merge_disabled"}, {"offer": "version", "reason": "versions_disabled"}]`},
		"PAL":       {5_178_900, "pal", `[{"offer": "merge", "reason": "runtime_differs"}, {"offer": "version", "reason": "versions_disabled"}]`},
		"other cut": {5_000_000, "different", `[{"offer": "merge", "reason": "runtime_differs"}, {"offer": "version", "reason": "versions_disabled"}]`},
	} {
		t.Run(name, func(t *testing.T) {
			e := newFlow2Env(t, func(c *config.Config) { c.Merge.Enabled, c.Versions.Enabled = false, false })
			j := e.probedJob(tc.uploadMs)
			if code, body := e.call("POST", "/api/jobs/"+j.ID+"/match", `{"app":"radarr","tmdbId":605}`); code != http.StatusOK {
				t.Fatalf("match: %d %s", code, body)
			}
			e.step(j)
			task := e.q.Tasks()[0]
			e.q.Complete(task.ID, media.LibraryProbeOutput{
				Path: "/data/media/movies/Has File (2004)/Has File (2004) WEBDL-1080p.mkv", OK: true,
				Media: &media.Probe{DurationMs: 5_400_000, Video: []media.VideoTrack{{DurationMs: 5_400_000}}},
			})
			e.step(j)
			var v struct {
				State      string          `json:"state"`
				Finding    string          `json:"finding"`
				Offers     []string        `json:"offers"`
				NotOffered json.RawMessage `json:"notOffered"`
			}
			if err := json.Unmarshal([]byte(e.flow2(j)), &v); err != nil {
				t.Fatal(err)
			}
			if v.State != "ready" || v.Finding != tc.finding || v.Offers == nil || len(v.Offers) != 0 {
				t.Fatalf("flow2 %+v", v)
			}
			equalJSON(t, string(v.NotOffered), tc.notOffered)
		})
	}
}

// A job that is not flow 2 has "flow2": null; the spool is not touched for it.
func TestFlow2NullForFlow1(t *testing.T) {
	e := newFlow2Env(t, nil)
	j := e.probedJob(5_400_000)
	if got := e.flow2(j); got != "null" {
		t.Errorf("flow2 %s", got)
	}
	if err := e.st.SetTarget(e.t.Context(), j.ID, store.Target{App: "radarr", TitleID: 99}); err != nil {
		t.Fatal(err)
	}
	if got := e.flow2(j); got != "null" {
		t.Errorf("flow2 %s", got)
	}
}
