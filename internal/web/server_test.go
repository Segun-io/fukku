package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/segun-io/fukku/internal/config"
)

// Addresses from the documentation ranges (RFC 5737).
const (
	proxyAddr     = "192.0.2.10:40000"
	untrustedAddr = "198.51.100.7:40000"
	publicHost    = "fukku.example.com"
	publicOrigin  = "https://fukku.example.com"
)

// Values that must never appear in an answer.
const (
	radarrKey = "00000000000000000000000000000001"
	sonarrKey = "00000000000000000000000000000002"
	radarrURL = "http://radarr.example.com:7878"
	sonarrURL = "http://sonarr.example.com:8989"
)

// testConfig is the defaults plus what validation needs: one trusted proxy and both apps.
func testConfig() config.Config {
	cfg := config.Defaults()
	cfg.Auth.TrustedProxies = []string{"192.0.2.10"}
	cfg.Auth.AdminGroup = "admins"
	cfg.Radarr.URL, cfg.Radarr.APIKey = radarrURL, radarrKey
	cfg.Sonarr.URL, cfg.Sonarr.APIKey = sonarrURL, sonarrKey
	return cfg
}

var testUI = fstest.MapFS{
	"index.html":    {Data: []byte("<html>app</html>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
	"favicon.svg":   {Data: []byte("<svg/>")},
}

func handler(t *testing.T, cfg config.Config, ui fstest.MapFS) http.Handler {
	t.Helper()
	if err := config.Validate(cfg, config.RoleWeb, config.ValidateOptions{InsecureDev: true}); err != nil {
		t.Fatalf("test configuration is invalid: %v", err)
	}
	return New(cfg, slog.New(slog.DiscardHandler), ui, Deps{}).Handler()
}

// fromProxy is a request as the trusted proxy forwards it for user1.
func fromProxy(method, path string, body io.Reader) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), method, "http://"+publicHost+path, body)
	r.RemoteAddr = proxyAddr
	r.Header.Set("Remote-User", "user1")
	r.Header.Set("Remote-Email", "user1@example.com")
	r.Header.Set("Remote-Groups", "users")
	if method != http.MethodGet && method != http.MethodHead {
		r.Header.Set("Origin", publicOrigin)
	}
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// wantError checks the error shape of the JSON API conventions.
func wantError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d (body %s)", w.Code, status, w.Body)
	}
	var body errorBody
	dec := json.NewDecoder(w.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("body is not the error shape: %v", err)
	}
	if body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error %+v, want code %s", body.Error, code)
	}
	if w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", w.Header())
	}
}

func TestHealthz(t *testing.T) {
	h := handler(t, testConfig(), fstest.MapFS{})
	// From anywhere, without a user.
	r := httptest.NewRequestWithContext(context.Background(), "GET", "/healthz", nil)
	r.RemoteAddr = untrustedAddr
	w := serve(h, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got["status"] != "ok" {
		t.Fatalf("body %q", w.Body)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("security headers missing")
	}
}

func TestHealthzRejectsPost(t *testing.T) {
	h := handler(t, testConfig(), fstest.MapFS{})
	if w := serve(h, fromProxy("POST", "/healthz", nil)); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", w.Code)
	}
}

func TestSPA(t *testing.T) {
	h := handler(t, testConfig(), testUI)
	cases := []struct {
		path   string
		status int
		body   string
	}{
		{"/", 200, "<html>app</html>"},
		{"/jobs/123", 200, "<html>app</html>"}, // client-side route
		{"/assets/app.js", 200, "console.log(1)"},
		{"/assets/missing.js", 404, ""},
		{"/../../etc/passwd", 307, ""}, // redirected to the cleaned route, never a file outside
	}
	for _, c := range cases {
		w := serve(h, fromProxy("GET", c.path, nil))
		if w.Code != c.status || (c.body != "" && w.Body.String() != c.body) {
			t.Errorf("%s: %d %q", c.path, w.Code, w.Body)
		}
	}
	if w := serve(h, fromProxy("POST", "/", nil)); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /: %d", w.Code)
	}
}

func TestSPANotBuilt(t *testing.T) {
	h := handler(t, testConfig(), fstest.MapFS{".gitkeep": {}})
	if w := serve(h, fromProxy("GET", "/", nil)); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", w.Code)
	}
}

func TestUnknownAPIPath(t *testing.T) {
	h := handler(t, testConfig(), testUI)
	for _, method := range []string{"GET", "POST", "DELETE"} {
		wantError(t, serve(h, fromProxy(method, "/api/unknown", nil)), http.StatusNotFound, "not_found")
	}
	wantError(t, serve(h, fromProxy("GET", "/api/", nil)), http.StatusNotFound, "not_found")
}

// An untrusted address gets 403 everywhere but /healthz, whatever headers it sends.
func TestUntrustedAddressGets403(t *testing.T) {
	h := handler(t, testConfig(), testUI)
	for _, path := range []string{"/", "/api/session", "/assets/app.js", "/favicon.svg", "/jobs/123", "/api/unknown", "/readyz"} {
		for _, spoof := range []bool{false, true} {
			r := fromProxy("GET", path, nil)
			r.RemoteAddr = untrustedAddr
			if !spoof {
				r.Header.Del("Remote-User")
			} else {
				r.Header.Set("Remote-Groups", "admins")
				r.Header.Set("X-Forwarded-For", "192.0.2.10")
				r.Header.Set("X-Real-IP", "192.0.2.10")
				r.Header.Set("Forwarded", "for=192.0.2.10")
			}
			w := serve(h, r)
			wantError(t, w, http.StatusForbidden, "forbidden")
			if strings.Contains(w.Body.String(), "app") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("%s: %q, headers %v", path, w.Body, w.Header())
			}
		}
	}
}

func TestTrustedAddressWithoutUserGets403(t *testing.T) {
	h := handler(t, testConfig(), testUI)
	for _, path := range []string{"/", "/api/session", "/assets/app.js"} {
		r := fromProxy("GET", path, nil)
		r.Header.Del("Remote-User")
		wantError(t, serve(h, r), http.StatusForbidden, "forbidden")
	}
}

// Over a real connection, the address the middleware sees is the connection's: loopback
// here, which this configuration does not trust.
func TestRealConnectionAddressDecides(t *testing.T) {
	ts := httptest.NewServer(handler(t, testConfig(), testUI))
	defer ts.Close()
	get := func(path string) int {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Remote-User", "user1")
		req.Header.Set("X-Forwarded-For", "192.0.2.10")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := get("/"); code != http.StatusForbidden {
		t.Errorf("/ from loopback: %d", code)
	}
	if code := get("/healthz"); code != http.StatusOK {
		t.Errorf("/healthz: %d", code)
	}
}

func TestCrossOriginStateChangeGets403(t *testing.T) {
	h := handler(t, testConfig(), testUI)
	for _, origin := range []string{"https://evil.example.net", "null", ""} {
		r := fromProxy("POST", "/api/session", strings.NewReader("{}"))
		r.Header.Del("Origin")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		wantError(t, serve(h, r), http.StatusForbidden, "origin_mismatch")
	}
	// Same origin passes the check and reaches the routes (no POST route there yet: 404).
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		wantError(t, serve(h, fromProxy(method, "/api/session", strings.NewReader("{}"))), http.StatusNotFound, "not_found")
	}
}

// The API is same-origin only: no answer carries CORS headers, preflights included.
func TestNoCORSHeaders(t *testing.T) {
	h := handler(t, testConfig(), testUI)
	reqs := []*http.Request{
		fromProxy("GET", "/api/session", nil),
		fromProxy("OPTIONS", "/api/session", nil),
		fromProxy("OPTIONS", "/", nil),
		fromProxy("POST", "/api/unknown", nil),
	}
	for _, r := range reqs {
		r.Header.Set("Origin", "https://evil.example.net")
		r.Header.Set("Access-Control-Request-Method", "POST")
		w := serve(h, r)
		for k := range w.Header() {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("%s %s: answer has %s", r.Method, r.URL.Path, k)
			}
		}
	}
}
