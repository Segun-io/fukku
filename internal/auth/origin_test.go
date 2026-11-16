package auth

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
)

func newOriginHandler(cfg config.Config) (http.Handler, *seen, *bytes.Buffer) {
	var logs bytes.Buffer
	s := &seen{}
	h := OriginCheck(cfg, slog.New(slog.NewJSONHandler(&logs, nil)))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	return h, s, &logs
}

// originRequest is a request to host carrying the given Origin headers.
func originRequest(method, host string, origins ...string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), method, "http://"+host+"/api/jobs", strings.NewReader("{}"))
	r.RemoteAddr = proxyAddr
	for _, o := range origins {
		r.Header.Add("Origin", o)
	}
	return r
}

func TestOriginCheckByHost(t *testing.T) {
	cfg := config.Defaults() // originCheck on, no publicBaseUrl
	pass := []struct {
		method, host, origin string
	}{
		{"POST", "fukku.example.com", "https://fukku.example.com"},
		{"PATCH", "fukku.example.com", "https://fukku.example.com"},
		{"DELETE", "fukku.example.com", "https://fukku.example.com"},
		{"PUT", "fukku.example.com", "https://fukku.example.com"},
		// The scheme is not compared: TLS usually ends at the proxy.
		{"POST", "fukku.example.com", "http://fukku.example.com"},
		{"POST", "127.0.0.1:18080", "http://127.0.0.1:18080"},
		{"POST", "FUKKU.example.com", "https://fukku.EXAMPLE.com"},
		{"POST", "fukku.example.com:443", "https://fukku.example.com"},
		{"POST", "[2001:db8::1]:8080", "http://[2001:db8::1]:8080"},
	}
	for _, c := range pass {
		h, s, _ := newOriginHandler(cfg)
		w := serve(h, originRequest(c.method, c.host, c.origin))
		if w.Code != http.StatusNoContent || !s.called {
			t.Errorf("%s to %s from %s: status %d", c.method, c.host, c.origin, w.Code)
		}
	}

	refused := []struct {
		name    string
		method  string
		origins []string
	}{
		{"cross-origin POST", "POST", []string{"https://evil.example.net"}},
		{"cross-origin PATCH", "PATCH", []string{"https://evil.example.net"}},
		{"cross-origin DELETE", "DELETE", []string{"https://evil.example.net"}},
		{"sibling host", "POST", []string{"https://fukku.example.com.evil.example.net"}},
		{"other port", "POST", []string{"https://fukku.example.com:8443"}},
		{"missing", "POST", nil},
		{"empty", "POST", []string{""}},
		{"null", "POST", []string{"null"}},
		{"repeated", "POST", []string{"https://fukku.example.com", "https://fukku.example.com"}},
		{"with a path", "POST", []string{"https://fukku.example.com/"}},
		{"with credentials", "POST", []string{"https://user@fukku.example.com"}},
		{"not http", "POST", []string{"file://fukku.example.com"}},
		{"unknown method", "PROPFIND", []string{"https://evil.example.net"}},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			h, s, logs := newOriginHandler(cfg)
			wantRefused(t, serve(h, originRequest(c.method, "fukku.example.com", c.origins...)), s, "origin_mismatch")
			if !strings.Contains(logs.String(), `"msg":"auth_refused"`) || !strings.Contains(logs.String(), `"reason":"origin_mismatch"`) {
				t.Fatalf("log %s", logs)
			}
		})
	}
}

func TestOriginNeverCheckedOnSafeMethods(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "OPTIONS"} {
		for _, origins := range [][]string{nil, {"https://evil.example.net"}, {"null"}} {
			h, s, _ := newOriginHandler(config.Defaults())
			w := serve(h, originRequest(method, "fukku.example.com", origins...))
			if w.Code != http.StatusNoContent || !s.called {
				t.Errorf("%s with %v: status %d", method, origins, w.Code)
			}
		}
	}
}

func TestOriginCheckWithPublicBaseURL(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.PublicBaseURL = "https://Fukku.example.com:443/app/"
	h, s, _ := newOriginHandler(cfg)
	// The Host the proxy forwards does not matter, only the configured origin.
	if w := serve(h, originRequest("POST", "fukku-web:8080", "https://fukku.example.com")); w.Code != http.StatusNoContent || !s.called {
		t.Fatalf("configured origin: status %d", w.Code)
	}
	for _, origin := range []string{
		"http://fukku.example.com", // the scheme is compared here
		"https://fukku-web:8080",   // matches Host, not the setting
		"https://other.example.com",
		"https://fukku.example.com:8443",
	} {
		h, s, _ := newOriginHandler(cfg)
		host := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
		wantRefused(t, serve(h, originRequest("POST", host, origin)), s, "origin_mismatch")
	}
}

func TestOriginCheckOff(t *testing.T) {
	cfg := config.Defaults()
	cfg.Auth.OriginCheck = false
	h, s, logs := newOriginHandler(cfg)
	if !strings.Contains(logs.String(), `"msg":"origin_check_off"`) {
		t.Fatalf("no start-up warning: %s", logs)
	}
	for _, origins := range [][]string{nil, {"https://evil.example.net"}, {"null"}} {
		s.called = false
		w := serve(h, originRequest("POST", "fukku.example.com", origins...))
		if w.Code != http.StatusNoContent || !s.called {
			t.Errorf("origin %v: status %d", origins, w.Code)
		}
	}
}

func TestOriginCheckInvalidPublicBaseURLFailsClosed(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.PublicBaseURL = "fukku.example.com" // no scheme: validation refuses it
	h, s, _ := newOriginHandler(cfg)
	wantRefused(t, serve(h, originRequest("POST", "fukku.example.com", "https://fukku.example.com")), s, "origin_mismatch")
}
