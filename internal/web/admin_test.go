package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

const adminOrigin = "https://fukku.example.com"

// adminEnv is a server whose local checks pass, behind the whole chain of Handler in
// proxyHeaders mode, with every secret setting given a recognisable value.
type adminEnv struct {
	h       http.Handler
	secrets []string
}

func newAdminEnv(t *testing.T, arrStatus int) adminEnv {
	t.Helper()
	ready := newReadyEnv(t)

	// Every secret setting, found through the catalogue so that a new one is covered.
	probe := config.Defaults()
	var environ, secrets []string
	for i, l := range config.Leaves(&probe) {
		if l.Secret {
			v := "secret-value-" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + "-do-not-leak"
			environ = append(environ, l.Env+"="+v)
			secrets = append(secrets, v)
		}
	}
	if len(secrets) < 4 {
		t.Fatalf("found %d secret settings, want at least 4", len(secrets))
	}
	cfg, err := config.Load(config.Options{Environ: environ})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Worker = ready.cfg.Worker
	cfg.Paths = ready.cfg.Paths
	cfg.Auth.TrustedProxies = []string{"127.0.0.1"}
	cfg.Auth.AdminGroup = "admins"
	cfg.Server.PublicBaseURL = adminOrigin

	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(arrStatus)
		_, _ = w.Write([]byte(`{"appName":"Radarr","version":"5.2.1.8000","instanceName":"secret-value-from-app"}`))
	}))
	t.Cleanup(arr.Close)
	cfg.Radarr.URL = arr.URL
	cfg.Sonarr.URL = ""

	st := storetest.NewFake(nil)
	for _, c := range []struct {
		owner string
		to    store.State
	}{{"user1", ""}, {"user1", ""}, {"user2", ""}} {
		if _, err := st.CreateJob(t.Context(), c.owner); err != nil {
			t.Fatal(err)
		}
	}
	h := New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, Deps{Store: st}).Handler()
	return adminEnv{h: h, secrets: secrets}
}

func (e adminEnv) get(t *testing.T, path, user, groups string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, adminOrigin+path, nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Remote-User", user)
	r.Header.Set("Remote-Email", user+"@example.com")
	r.Header.Set("Remote-Groups", groups)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func TestAdminEndpointsRefuseNonAdminsWith404(t *testing.T) {
	e := newAdminEnv(t, http.StatusOK)
	for _, path := range []string{"/api/admin/status", "/api/admin/settings"} {
		rec := e.get(t, path, "user1", "users")
		if rec.Code != http.StatusNotFound {
			t.Errorf("non-admin GET %s: %d, want 404", path, rec.Code)
		}
		var b struct{ Error struct{ Code string } }
		_ = json.Unmarshal(rec.Body.Bytes(), &b)
		if b.Error.Code != "not_found" {
			t.Errorf("non-admin GET %s: code %q, want not_found", path, b.Error.Code)
		}
		// Identical to a path that does not exist.
		other := e.get(t, "/api/admin/nothing", "user1", "users")
		if rec.Body.String() != other.Body.String() {
			t.Errorf("%s answers differently from an unknown path: %q vs %q", path, rec.Body, other.Body)
		}
		if rec := e.get(t, path, "boss", "admins"); rec.Code != http.StatusOK {
			t.Errorf("admin GET %s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestAdminEndpointsCannotChangeAnything(t *testing.T) {
	e := newAdminEnv(t, http.StatusOK)
	for _, path := range []string{"/api/admin/status", "/api/admin/settings"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			r := httptest.NewRequestWithContext(t.Context(), method, adminOrigin+path, strings.NewReader("{}"))
			r.RemoteAddr = "127.0.0.1:40000"
			r.Header.Set("Origin", adminOrigin)
			r.Header.Set("Remote-User", "boss")
			r.Header.Set("Remote-Groups", "admins")
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, r)
			if rec.Code < 400 {
				t.Errorf("%s %s answered %d", method, path, rec.Code)
			}
		}
	}
}

func TestAdminResponsesNeverContainASecret(t *testing.T) {
	e := newAdminEnv(t, http.StatusOK)
	for _, path := range []string{"/api/admin/status", "/api/admin/settings"} {
		for _, who := range [][2]string{{"boss", "admins"}, {"user1", "users"}} {
			body := e.get(t, path, who[0], who[1]).Body.String()
			for _, secret := range append(e.secrets, "secret-value") {
				if strings.Contains(body, secret) {
					t.Errorf("%s for %s contains a secret value (%q): %s", path, who[0], secret, body)
				}
			}
		}
	}
}

func TestAdminSettings(t *testing.T) {
	e := newAdminEnv(t, http.StatusOK)
	rec := e.get(t, "/api/admin/settings", "boss", "admins")
	var got adminSettingsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	by := map[string]adminSettingJSON{}
	for _, s := range got.Settings {
		by[s.Key] = s
	}
	cfg := config.Defaults()
	if want := len(config.Leaves(&cfg)); len(got.Settings) != want {
		t.Errorf("%d settings, want every one of %d", len(got.Settings), want)
	}
	if s := by["radarr.apiKey"]; !s.Secret || s.Value != "[redacted]" || s.Source != "set" {
		t.Errorf("radarr.apiKey = %+v, want a redacted secret that is set", s)
	}
	if s := by["server.listenAddress"]; s.Source != "default" || s.Value != ":8080" || s.Env != "FUKKU_SERVER_LISTEN_ADDRESS" {
		t.Errorf("server.listenAddress = %+v", s)
	}
	if s := by["auth.adminGroup"]; s.Source != "set" || s.Value != "admins" {
		t.Errorf("auth.adminGroup = %+v", s)
	}
}

func TestAdminSettingsUnsetSecretIsEmptyAndDefault(t *testing.T) {
	cfg := config.Defaults()
	cfg.Auth.TrustedProxies = []string{"127.0.0.1"}
	cfg.Auth.AdminGroup = "admins"
	h := New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, Deps{Store: storetest.NewFake(nil)}).Handler()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/admin/settings", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Remote-User", "boss")
	r.Header.Set("Remote-Groups", "admins")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var got adminSettingsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, s := range got.Settings {
		if s.Secret && (s.Value != "" || s.Source != "default") {
			t.Errorf("unset secret %s = %+v", s.Key, s)
		}
	}
}

func TestAdminStatus(t *testing.T) {
	e := newAdminEnv(t, http.StatusOK)
	rec := e.get(t, "/api/admin/status", "boss", "admins")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got adminStatusJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Readiness.Status != "ready" {
		t.Errorf("readiness %q: %+v", got.Readiness.Status, got.Readiness.Checks)
	}
	if c, ok := find(got.Readiness, "radarr"); !ok || !c.OK {
		t.Errorf("radarr check = %+v", c)
	}
	if len(got.Apps) != 1 || got.Apps[0].Name != "radarr" || got.Apps[0].Version != "5.2.1.8000" {
		t.Errorf("apps = %+v, want radarr 5.2.1.8000 only (sonarr is not configured)", got.Apps)
	}
	if got.Worker.HeartbeatAgeSeconds == nil || *got.Worker.HeartbeatAgeSeconds > 60 || got.Worker.MaxAgeSeconds <= 0 {
		t.Errorf("worker = %+v", got.Worker)
	}
	if got.Jobs == nil || got.Jobs.Total != 3 || got.Jobs.ByState["uploading"] != 3 || got.Jobs.ByState["done"] != 0 {
		t.Errorf("jobs = %+v", got.Jobs)
	}
	if len(got.Jobs.ByState) != len(allJobStates) {
		t.Errorf("%d states counted, want %d", len(got.Jobs.ByState), len(allJobStates))
	}
	if got.Disk.MarginBytes != int64(config.Defaults().Upload.FreeSpaceMargin) {
		t.Errorf("disk margin = %d", got.Disk.MarginBytes)
	}
	if got.Disk.FreeBytes != nil && *got.Disk.FreeBytes < 0 {
		t.Errorf("disk free = %d", *got.Disk.FreeBytes)
	}
}

func TestAdminStatusReportsFailures(t *testing.T) {
	e := newAdminEnv(t, http.StatusInternalServerError)
	rec := e.get(t, "/api/admin/status", "boss", "admins")
	var got adminStatusJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || got.Readiness.Status != "degraded" {
		t.Errorf("%d %q, want 200 degraded", rec.Code, got.Readiness.Status)
	}
	if len(got.Apps) != 1 || got.Apps[0].Version != "" {
		t.Errorf("apps = %+v, want radarr without a version", got.Apps)
	}
}

func TestAdminStatusWithoutHeartbeat(t *testing.T) {
	ready := newReadyEnv(t)
	beat(t, ready.cfg, time.Now().Add(-time.Hour))
	cfg := ready.cfg
	cfg.Auth.TrustedProxies = []string{"127.0.0.1"}
	cfg.Auth.AdminGroup = "admins"
	h := New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, Deps{Store: storetest.NewFake(nil)}).Handler()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/admin/status", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Remote-User", "boss")
	r.Header.Set("Remote-Groups", "admins")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var got adminStatusJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// Not ready is reported in the body, with 200: the page shows it.
	if rec.Code != http.StatusOK || got.Readiness.Status != "not_ready" {
		t.Errorf("%d %q, want 200 not_ready", rec.Code, got.Readiness.Status)
	}
	if got.Worker.HeartbeatAgeSeconds == nil || *got.Worker.HeartbeatAgeSeconds < 3500 {
		t.Errorf("heartbeat age = %v, want about an hour", got.Worker.HeartbeatAgeSeconds)
	}
}

func TestCleanVersion(t *testing.T) {
	for in, want := range map[string]string{
		"5.2.1.8000":            "5.2.1.8000",
		"4.0.0-beta+build_1":    "4.0.0-beta+build_1",
		"<script>":              "",
		"":                      "",
		strings.Repeat("1", 41): "",
	} {
		if got := cleanVersion(in); got != want {
			t.Errorf("cleanVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
