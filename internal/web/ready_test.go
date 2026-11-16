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
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// readyEnv is a configuration whose local checks all pass.
type readyEnv struct {
	cfg      config.Config
	sentinel string
}

func newReadyEnv(t *testing.T) readyEnv {
	t.Helper()
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Worker.SpoolDir = filepath.Join(root, "spool")
	cfg.Paths.IntakeRoot = filepath.Join(root, "mount", "intake")
	sentinel := filepath.Join(root, "mount", ".mounted")
	cfg.Paths.Sentinels = []string{sentinel}
	for _, d := range []string{cfg.Worker.SpoolDir, cfg.Paths.IntakeRoot} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(sentinel, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	beat(t, cfg, time.Now())
	return readyEnv{cfg: cfg, sentinel: sentinel}
}

func beat(t *testing.T, cfg config.Config, at time.Time) {
	t.Helper()
	data := strconv.FormatInt(at.Unix(), 10) + "\n"
	if err := os.WriteFile(filepath.Join(cfg.Worker.SpoolDir, "worker.heartbeat"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readyz(t *testing.T, s *Server, query string) (int, Readiness) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz"+query, nil)
	req.RemoteAddr = "127.0.0.1:40000" // loopback needs no authentication (#5)
	s.Handler().ServeHTTP(rec, req)
	var got Readiness
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, got
}

func find(r Readiness, name string) (Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func newServer(cfg config.Config) *Server {
	return New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, Deps{Store: storetest.NewFake(nil)})
}

func wantFailing(t *testing.T, s *Server, name string) {
	t.Helper()
	code, got := readyz(t, s, "")
	if code != http.StatusServiceUnavailable || got.Status != "not_ready" {
		t.Fatalf("status %d %q, want 503 not_ready", code, got.Status)
	}
	c, ok := find(got, name)
	if !ok || c.OK || c.Detail == "" {
		t.Fatalf("check %s = %+v (found %v), want failing with a detail", name, c, ok)
	}
}

func TestReadyzAllPass(t *testing.T) {
	env := newReadyEnv(t)
	code, got := readyz(t, newServer(env.cfg), "")
	if code != http.StatusOK || got.Status != "ready" {
		t.Fatalf("status %d %+v", code, got)
	}
	var names []string
	for _, c := range got.Checks {
		if !c.OK {
			t.Errorf("%s failed: %s", c.Name, c.Detail)
		}
		names = append(names, c.Name)
	}
	if want := "database spool sentinels intakeRoot sameFilesystem workerHeartbeat"; strings.Join(names, " ") != want {
		t.Fatalf("checks %v, want %s", names, want)
	}
}

func TestReadyzDatabaseFails(t *testing.T) {
	env := newReadyEnv(t)
	st := storetest.NewFake(nil)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	s := New(env.cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, Deps{Store: st})
	wantFailing(t, s, "database")
	_, got := readyz(t, s, "")
	if c, _ := find(got, "database"); strings.Contains(c.Detail, "secret") {
		t.Fatalf("detail leaks the error: %q", c.Detail)
	}
}

func TestReadyzSpoolFails(t *testing.T) {
	env := newReadyEnv(t)
	env.cfg.Worker.SpoolDir = filepath.Join(t.TempDir(), "missing")
	wantFailing(t, newServer(env.cfg), "spool")
}

func TestReadyzSentinelMissing(t *testing.T) {
	env := newReadyEnv(t)
	mount := filepath.Join(t.TempDir(), "unmounted")
	env.cfg.Paths.Sentinels = []string{filepath.Join(mount, ".mounted")}
	env.cfg.Paths.IntakeRoot = filepath.Join(mount, "intake")
	s := newServer(env.cfg)
	wantFailing(t, s, "sentinels")
	_, got := readyz(t, s, "")
	if _, ok := find(got, "intakeRoot"); ok {
		t.Fatal("intakeRoot was checked although the sentinel is missing")
	}
	if _, err := os.Stat(mount); err == nil {
		t.Fatal("something was created under the missing mount")
	}
}

func TestReadyzIntakeRootFails(t *testing.T) {
	env := newReadyEnv(t)
	env.cfg.Paths.IntakeRoot = filepath.Join(t.TempDir(), "missing")
	s := newServer(env.cfg)
	wantFailing(t, s, "intakeRoot")
	if _, err := os.Stat(env.cfg.Paths.IntakeRoot); err == nil {
		t.Fatal("the intake root was created")
	}
}

func TestReadyzSameFilesystemFails(t *testing.T) {
	env := newReadyEnv(t)
	env.cfg.Paths.LibraryRoots = []string{filepath.Join(t.TempDir(), "missing")}
	env.cfg.Paths.RequireSameFilesystem = "always"
	wantFailing(t, newServer(env.cfg), "sameFilesystem")
}

func TestReadyzWorkerHeartbeatFails(t *testing.T) {
	env := newReadyEnv(t)
	beat(t, env.cfg, time.Now().Add(-time.Hour))
	wantFailing(t, newServer(env.cfg), "workerHeartbeat")

	if err := os.Remove(filepath.Join(env.cfg.Worker.SpoolDir, "worker.heartbeat")); err != nil {
		t.Fatal(err)
	}
	wantFailing(t, newServer(env.cfg), "workerHeartbeat")
}

func arrServer(t *testing.T, status int, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v3/system/status" || r.Header.Get("X-Api-Key") != "test-key" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"secret":"response body"}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestReadyzArrReachable(t *testing.T) {
	env := newReadyEnv(t)
	var calls atomic.Int32
	ts := arrServer(t, http.StatusOK, &calls)
	env.cfg.Radarr.URL = ts.URL
	env.cfg.Radarr.APIKey = "test-key"
	code, got := readyz(t, newServer(env.cfg), "")
	if c, ok := find(got, "radarr"); code != http.StatusOK || got.Status != "ready" || !ok || !c.OK {
		t.Fatalf("status %d %+v", code, got)
	}
	if _, ok := find(got, "sonarr"); ok {
		t.Fatal("sonarr is not configured but was checked")
	}
}

func TestReadyzArrDownIsDegraded(t *testing.T) {
	env := newReadyEnv(t)
	var calls atomic.Int32
	ts := arrServer(t, http.StatusInternalServerError, &calls)
	env.cfg.Sonarr.URL = ts.URL
	env.cfg.Sonarr.APIKey = "test-key"
	s := newServer(env.cfg)
	code, got := readyz(t, s, "")
	c, _ := find(got, "sonarr")
	if code != http.StatusOK || got.Status != "degraded" || c.OK || c.Detail == "" {
		t.Fatalf("status %d %+v", code, got)
	}
	if strings.Contains(c.Detail, "response body") || strings.Contains(c.Detail, "test-key") {
		t.Fatalf("detail leaks: %q", c.Detail)
	}
	// The local view, used by the container health check, leaves the apps out.
	before := calls.Load()
	code, got = readyz(t, s, "?local=1")
	if code != http.StatusOK || got.Status != "ready" || len(got.Checks) != 6 || calls.Load() != before {
		t.Fatalf("local view: %d %+v", code, got)
	}
}

func TestReadyzLocalFailureBeatsDegraded(t *testing.T) {
	env := newReadyEnv(t)
	var calls atomic.Int32
	ts := arrServer(t, http.StatusInternalServerError, &calls)
	env.cfg.Radarr.URL = ts.URL
	env.cfg.Worker.SpoolDir = filepath.Join(t.TempDir(), "missing")
	code, got := readyz(t, newServer(env.cfg), "")
	if code != http.StatusServiceUnavailable || got.Status != "not_ready" {
		t.Fatalf("status %d %q", code, got.Status)
	}
}

func TestReadyzArrTimeoutAndCache(t *testing.T) {
	env := newReadyEnv(t)
	var calls atomic.Int32
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
		<-release
	}))
	defer ts.Close()
	defer close(release)
	env.cfg.Radarr.URL = ts.URL
	env.cfg.Radarr.RequestTimeout = config.Duration(100 * time.Millisecond)
	env.cfg.Server.ReadinessCacheTTL = config.Duration(time.Minute)
	s := newServer(env.cfg)
	now := time.Now()
	s.ready.now = func() time.Time { return now }

	start := time.Now()
	code, got := readyz(t, s, "")
	c, _ := find(got, "radarr")
	if code != http.StatusOK || got.Status != "degraded" || c.OK || !strings.Contains(c.Detail, "did not answer") {
		t.Fatalf("status %d %+v", code, got)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the check did not stop at requestTimeout")
	}
	readyz(t, s, "")
	if calls.Load() != 1 {
		t.Fatalf("app called %d times inside the cache time, want 1", calls.Load())
	}
	now = now.Add(2 * time.Minute)
	readyz(t, s, "")
	if calls.Load() != 2 {
		t.Fatalf("app called %d times after the cache time, want 2", calls.Load())
	}
}
