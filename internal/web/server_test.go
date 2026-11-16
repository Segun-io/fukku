package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/segun-io/fukku/internal/config"
)

func newTestServer(ui fstest.MapFS) *httptest.Server {
	s := New(config.Defaults(), slog.New(slog.DiscardHandler), ui, Deps{})
	return httptest.NewServer(s.Handler())
}

type result struct {
	StatusCode int
	Header     http.Header
}

func get(t *testing.T, url string) (result, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return result{resp.StatusCode, resp.Header}, string(body)
}

func TestHealthz(t *testing.T) {
	ts := newTestServer(fstest.MapFS{})
	defer ts.Close()
	resp, body := get(t, ts.URL+"/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(body), &got); err != nil || got["status"] != "ok" {
		t.Fatalf("body %q", body)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("security headers missing")
	}
}

func TestHealthzRejectsPost(t *testing.T) {
	ts := newTestServer(fstest.MapFS{})
	defer ts.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestSPA(t *testing.T) {
	ts := newTestServer(fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
		"favicon.svg":   {Data: []byte("<svg/>")},
	})
	defer ts.Close()

	cases := []struct {
		path   string
		status int
		body   string
	}{
		{"/", 200, "<html>app</html>"},
		{"/jobs/123", 200, "<html>app</html>"}, // client-side route
		{"/assets/app.js", 200, "console.log(1)"},
		{"/assets/missing.js", 404, ""},
		{"/api/unknown", 404, ""},
		{"/../../etc/passwd", 200, "<html>app</html>"}, // cleaned to a route, never a file outside
	}
	for _, c := range cases {
		resp, body := get(t, ts.URL+c.path)
		if resp.StatusCode != c.status || (c.body != "" && body != c.body) {
			t.Errorf("%s: %d %q", c.path, resp.StatusCode, body)
		}
	}
}

func TestSPANotBuilt(t *testing.T) {
	ts := newTestServer(fstest.MapFS{".gitkeep": {}})
	defer ts.Close()
	resp, _ := get(t, ts.URL+"/")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
