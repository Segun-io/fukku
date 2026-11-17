package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/store"
)

func versionsOn(root string) func(*config.Config) {
	return func(c *config.Config) {
		c.Versions.Enabled, c.Versions.Root = true, root
		c.MediaServer.Provider = "jellyfin"
		c.Auth.GroupsHeader = "Remote-Groups"
		c.Auth.AdminGroup = "media-admins"
	}
}

// readyPAL is a flow 2 job awaiting confirmation whose upload is a PAL speed-up of the film.
func (e *flow2Env) readyPAL() store.Job {
	e.t.Helper()
	j := e.probedJob(5_178_900)
	if code, body := e.call("POST", "/api/jobs/"+j.ID+"/match", `{"app":"radarr","tmdbId":605}`); code != http.StatusOK {
		e.t.Fatalf("match: %d %s", code, body)
	}
	e.step(j)
	e.q.Complete(e.q.Tasks()[0].ID, media.LibraryProbeOutput{
		Path: "/data/media/movies/Has File (2004)/Has File (2004) WEBDL-1080p.mkv", OK: true,
		Media: &media.Probe{DurationMs: 5_400_000, Size: 4000, Video: []media.VideoTrack{{DurationMs: 5_400_000}},
			Audio: []media.AudioTrack{{Index: 1, Codec: "eac3", Language: "eng"}}},
	})
	if j = e.step(j); j.State != store.AwaitingConfirmation {
		e.t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	return j
}

// The second version through the API (#56): JobDetail proposes it, and the confirmation's
// refusals have their own codes.
func TestVersionThroughTheAPI(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing") // not there: the storage check refuses
	e := newFlow2Env(t, versionsOn(root))
	j := e.readyPAL()
	var v struct {
		Finding string          `json:"finding"`
		Offers  []string        `json:"offers"`
		Version json.RawMessage `json:"version"`
	}
	if err := json.Unmarshal([]byte(e.flow2(j)), &v); err != nil {
		t.Fatal(err)
	}
	if v.Finding != "pal" || len(v.Offers) != 1 || v.Offers[0] != "version" {
		t.Fatalf("flow2 %+v", v)
	}
	equalJSON(t, string(v.Version), `{"label": "Español", "language": "es", "name": "Has File (2004) - Español.mkv",
	  "problem": null, "notices": ["outside_radarr", "regrouped_after_changes", "kept_on_delete"]}`)

	for body, want := range map[string]string{
		`{"flow2":"version","version":{"label":"Bluray"}}`: `"code":"version_label"`,
		`{"flow2":"version","overrides":["r1"]}`:           `"code":"bad_request"`,
		`{"flow2":"version"}`:                              `"code":"storage_unavailable"`,
	} {
		code, got := e.call("POST", "/api/jobs/"+j.ID+"/confirm", body)
		wantCode := http.StatusConflict
		switch {
		case strings.Contains(want, "bad_request"):
			wantCode = http.StatusBadRequest
		case strings.Contains(want, "storage"):
			wantCode = http.StatusServiceUnavailable
		}
		if code != wantCode || !strings.Contains(got, want) {
			t.Errorf("confirm %s: %d %s, want %d %s", body, code, got, wantCode, want)
		}
	}
	// The refusal of a label says why, as the proposal's label problem does.
	_, got := e.call("POST", "/api/jobs/"+j.ID+"/confirm", `{"flow2":"version","version":{"label":"Bluray"}}`)
	if !strings.Contains(got, `"reason":"label_forbidden"`) || !strings.Contains(got, `"token":"bluray"`) {
		t.Errorf("version_label body %s", got)
	}
	// Without a media server that groups versions, the offer is not made.
	e2 := newFlow2Env(t, func(c *config.Config) { versionsOn(root)(c); c.MediaServer.Provider = "none" })
	j2 := e2.readyPAL()
	if got := e2.flow2(j2); !strings.Contains(got, `"reason":"versions_unsupported"`) || !strings.Contains(got, `"version":null`) {
		t.Errorf("flow2 %s", got)
	}
	code, body := e2.call("POST", "/api/jobs/"+j2.ID+"/confirm", `{"flow2":"version"}`)
	if code != http.StatusConflict || !strings.Contains(body, `"code":"version_unavailable"`) {
		t.Errorf("confirm: %d %s", code, body)
	}
}

// GET /api/admin/versions lists the versions for admins, and is a 404 for anyone else.
func TestAdminVersions(t *testing.T) {
	e := newFlow2Env(t, versionsOn("/data/versions"))
	ctx := t.Context()
	if _, err := e.st.PutVersion(ctx, store.Version{JobID: "j1", App: "radarr", TitleID: 8, Title: "Has File", Year: 2004,
		Path: "/data/versions/Has File (2004)/Has File (2004) - Español.mkv", Label: "Español", Status: store.VersionGrouped,
		PrimaryPath: "/data/media/movies/Has File (2004)/Has File (2004) WEBDL-1080p.mkv"}); err != nil {
		t.Fatal(err)
	}
	// An episode's version (#58) of a special: season 0 is shown, not dropped.
	if _, err := e.st.PutVersion(ctx, store.Version{JobID: "j2", App: "sonarr", TitleID: 11, EpisodeID: 1001, SeasonNumber: 0, EpisodeNumber: 3,
		Path: "/data/media/tv/Show/Specials/Show - S00E03 - Español.mkv", Label: "Español", Status: store.VersionAdopted,
		Detail: "Sonarr tracks the version as the file of S00E03 (episode file 9)"}); err != nil {
		t.Fatal(err)
	}
	if code, _ := e.call("GET", "/api/admin/versions", ""); code != http.StatusNotFound {
		t.Errorf("a user got %d", code)
	}
	r := httptest.NewRequestWithContext(ctx, "GET", "https://fukku.example.com/api/admin/versions", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Remote-User", "admin1")
	r.Header.Set("Remote-Groups", "users,media-admins")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Versions []map[string]any `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Versions) != 2 {
		t.Fatalf("versions %s", rec.Body)
	}
	// The re-check's notices (#96) are the status and its detail.
	if ep := out.Versions[1]; ep["seasonNumber"] != float64(0) || ep["episodeNumber"] != float64(3) || ep["episodeId"] != float64(1001) ||
		ep["status"] != "adopted" || ep["detail"] != "Sonarr tracks the version as the file of S00E03 (episode file 9)" {
		t.Errorf("episode's version %v", ep)
	}
	v := out.Versions[0]
	if _, ok := v["seasonNumber"]; !ok || v["seasonNumber"] != nil || v["episodeId"] != nil {
		t.Errorf("a film's version has episode fields: %v", v)
	}
	if v["name"] != "Has File (2004) - Español.mkv" || v["status"] != "grouped" || v["primaryFile"] != "Has File (2004) WEBDL-1080p.mkv" ||
		v["groupedAt"] != nil || v["titleId"] != float64(8) {
		t.Errorf("version %v", v)
	}
}
