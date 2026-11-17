package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// The job API behind the whole chain of Handler: the authentication middleware in
// proxyHeaders mode, the Origin check and the routes.
func TestJobAPIThroughTheFullChain(t *testing.T) {
	cfg := config.Defaults()
	cfg.Auth.TrustedProxies = []string{"127.0.0.1", "::1"}
	cfg.Auth.GroupsHeader = "Remote-Groups"
	cfg.Auth.AdminGroup = "admins"
	cfg.Server.PublicBaseURL = "https://fukku.example.com"
	cfg.Paths.IntakeRoot = t.TempDir()
	cfg.Radarr.URL = "http://radarr.example.com:7878"
	h := New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, Deps{Store: storetest.NewFake(nil)}).Handler()

	send := func(remote, user, groups, method, path string, origin bool) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), method, "https://fukku.example.com"+path, nil)
		r.RemoteAddr = remote
		r.Header.Set("Remote-User", user)
		r.Header.Set("Remote-Email", user+"@example.com")
		r.Header.Set("Remote-Groups", groups)
		if origin {
			r.Header.Set("Origin", "https://fukku.example.com")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	errCode := func(rec *httptest.ResponseRecorder) string {
		var b struct{ Error struct{ Code string } }
		_ = json.Unmarshal(rec.Body.Bytes(), &b)
		return b.Error.Code
	}
	const local = "127.0.0.1:40000"

	if rec := send("[::1]:40000", "user1", "users", "POST", "/api/jobs", true); rec.Code != http.StatusCreated {
		t.Fatalf("create from ::1: %d %s", rec.Code, rec.Body)
	}
	rec := send(local, "user1", "users", "POST", "/api/jobs", true)
	var job struct{ ID, Owner string }
	_ = json.Unmarshal(rec.Body.Bytes(), &job)
	if rec.Code != http.StatusCreated || job.Owner != "user1" {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	path := "/api/jobs/" + job.ID

	if rec := send(local, "user1", "users", "GET", path, false); rec.Code != http.StatusOK {
		t.Errorf("owner GET: %d", rec.Code)
	}
	if rec := send(local, "user2", "users", "GET", path, false); rec.Code != http.StatusNotFound || errCode(rec) != "not_found" {
		t.Errorf("other user GET: %d %s", rec.Code, rec.Body)
	}
	if rec := send(local, "admin1", "users,admins", "GET", path, false); rec.Code != http.StatusOK {
		t.Errorf("admin GET: %d", rec.Code)
	}
	if rec := send(local, "user2", "users", "POST", path+"/cancel", true); rec.Code != http.StatusNotFound {
		t.Errorf("other user cancel: %d", rec.Code)
	}
	if rec := send(local, "user1", "users", "POST", "/api/jobs", false); rec.Code != http.StatusForbidden || errCode(rec) != "origin_mismatch" {
		t.Errorf("POST without Origin: %d %s", rec.Code, rec.Body)
	}
	if rec := send(local, "user1", "users", "POST", path+"/cancel", false); rec.Code != http.StatusForbidden {
		t.Errorf("cancel without Origin: %d", rec.Code)
	}
	for _, c := range [][2]string{{"GET", "/api/jobs"}, {"GET", path}, {"POST", "/api/jobs"}} {
		if rec := send("198.51.100.7:40000", "user1", "users", c[0], c[1], true); rec.Code != http.StatusForbidden {
			t.Errorf("untrusted %s %s: %d", c[0], c[1], rec.Code)
		}
	}
}
