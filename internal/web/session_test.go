package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/version"
)

// sessionExample is the example of the GET /api/session contract (issue #5), for the
// defaults with both apps configured.
const sessionExample = `{
  "user": {"name": "user1", "email": "user1@example.com", "admin": false},
  "version": "0.1.0",
  "authMode": "proxyHeaders",
  "insecureDev": false,
  "uiLanguages": ["en", "es"],
  "apps": {"radarr": true, "sonarr": true},
  "upload": {
    "endpoint": "/api/uploads/",
    "chunkSize": 16777216,
    "maxFileSize": 85899345920,
    "maxParallelPerUser": 3,
    "videoExtensions": [".mkv", ".mp4", ".m4v", ".avi", ".mov", ".ts", ".webm"],
    "subtitleExtensions": [".srt", ".ass", ".ssa"],
    "archiveExtensions": [".zip"],
    "largeFileHintThreshold": 21474836480
  }
}`

func compactJSON(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func getSession(t *testing.T, cfg config.Config, r *http.Request) string {
	t.Helper()
	w := serve(handler(t, cfg, testUI), r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", w.Header())
	}
	return strings.TrimSpace(w.Body.String())
}

func TestSessionMatchesContract(t *testing.T) {
	old := version.Version
	version.Version = "0.1.0"
	defer func() { version.Version = old }()

	got := getSession(t, testConfig(), fromProxy("GET", "/api/session", nil))
	if want := compactJSON(t, sessionExample); got != want {
		t.Fatalf("session\n got %s\nwant %s", got, want)
	}
	for _, secret := range []string{radarrKey, sonarrKey, radarrURL, sonarrURL, "radarr.example.com", "users"} {
		if strings.Contains(got, secret) {
			t.Errorf("the session contains %q", secret)
		}
	}
}

func TestSessionReflectsConfiguration(t *testing.T) {
	cfg := testConfig()
	cfg.Sonarr.URL, cfg.Sonarr.APIKey = "", ""
	cfg.Upload.SubtitleExtensions = nil
	cfg.Server.UILanguages = []string{"es"}
	r := fromProxy("GET", "/api/session", nil)
	r.Header.Set("Remote-Groups", "users,admins")

	var s sessionInfo
	if err := json.Unmarshal([]byte(getSession(t, cfg, r)), &s); err != nil {
		t.Fatal(err)
	}
	if !s.User.Admin || !s.Apps.Radarr || s.Apps.Sonarr || s.Upload.SubtitleExtensions == nil ||
		len(s.UILanguages) != 1 || s.InsecureDev || s.Version != version.Version {
		t.Fatalf("session %+v", s)
	}
}

func TestSessionInsecureDev(t *testing.T) {
	cfg := testConfig()
	cfg.Auth.Mode = "none"
	r := fromProxy("GET", "/api/session", nil)
	r.RemoteAddr = untrustedAddr
	got := getSession(t, cfg, r)
	if !strings.Contains(got, `"user":{"name":"dev","email":"","admin":true}`) ||
		!strings.Contains(got, `"authMode":"none","insecureDev":true`) {
		t.Fatalf("session %s", got)
	}
}
