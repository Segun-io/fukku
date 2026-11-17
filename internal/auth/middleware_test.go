package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
)

// Addresses from the documentation ranges (RFC 5737, RFC 3849).
const (
	proxyAddr     = "192.0.2.10:40000"
	untrustedAddr = "198.51.100.7:40000"
)

func testConfig() config.Config {
	cfg := config.Defaults()
	cfg.Auth.TrustedProxies = []string{"192.0.2.10", "2001:db8::/64"}
	cfg.Auth.GroupsHeader = "Remote-Groups"
	cfg.Auth.AdminGroup = "admins"
	return cfg
}

// seen is what the handler behind the middleware observed.
type seen struct {
	called  bool
	user    User
	hasUser bool
}

func newHandler(t *testing.T, cfg config.Config) (http.Handler, *seen, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	s := &seen{}
	h := Middleware(cfg, log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.called = true
		s.user, s.hasUser = FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	return h, s, &logs
}

func request(method, path, remote string, headers map[string]string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), method, "http://fukku.example.com"+path, nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func wantRefused(t *testing.T, w *httptest.ResponseRecorder, s *seen, code string) {
	t.Helper()
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", w.Code)
	}
	if s.called {
		t.Fatal("the handler ran for a refused request")
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	dec := json.NewDecoder(w.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error %+v, want code %s", body.Error, code)
	}
	if w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", w.Header())
	}
}

func TestTrustedProxyWithUser(t *testing.T) {
	h, s, _ := newHandler(t, testConfig())
	w := serve(h, request("GET", "/", proxyAddr, map[string]string{
		"Remote-User": "user1", "Remote-Email": "user1@example.com", "Remote-Groups": "users, family",
	}))
	if w.Code != http.StatusNoContent || !s.called || !s.hasUser {
		t.Fatalf("status %d, called %v, user %v", w.Code, s.called, s.hasUser)
	}
	want := User{Name: "user1", Email: "user1@example.com", Groups: []string{"users", "family"}}
	if s.user.Name != want.Name || s.user.Email != want.Email || !slices.Equal(s.user.Groups, want.Groups) || s.user.Admin {
		t.Fatalf("user %+v, want %+v", s.user, want)
	}
}

func TestUntrustedAddressIsRefused(t *testing.T) {
	cfg := testConfig()
	for _, remote := range []string{
		untrustedAddr,
		"192.0.2.11:40000",    // next to the proxy
		"[2001:db8:1::1]:443", // outside the trusted /64
		"127.0.0.1:40000",     // loopback is not trusted unless listed
		"",                    // no address recorded
		"not-an-address",
	} {
		t.Run(remote, func(t *testing.T) {
			h, s, logs := newHandler(t, cfg)
			w := serve(h, request("GET", "/", remote, map[string]string{"Remote-User": "user1"}))
			wantRefused(t, w, s, "forbidden")
			if !strings.Contains(logs.String(), `"msg":"auth_refused"`) || !strings.Contains(logs.String(), `"reason":"untrusted_address"`) {
				t.Fatalf("log %s", logs)
			}
		})
	}
}

// Identity and forwarding headers from an untrusted address change nothing: only the
// connection's address decides.
func TestSpoofedHeadersFromUntrustedAddressChangeNothing(t *testing.T) {
	cfg := testConfig()
	cfg.Auth.AllUsersAreAdmins = true
	h, s, logs := newHandler(t, cfg)
	w := serve(h, request("POST", "/api/session", untrustedAddr, map[string]string{
		"Remote-User":       "admin1",
		"Remote-Email":      "admin1@example.com",
		"Remote-Groups":     "admins",
		"X-Forwarded-For":   "192.0.2.10",
		"X-Real-IP":         "192.0.2.10",
		"Forwarded":         "for=192.0.2.10",
		"True-Client-IP":    "192.0.2.10",
		"X-Forwarded-Host":  "fukku.example.com",
		"X-Forwarded-Proto": "https",
	}))
	wantRefused(t, w, s, "forbidden")
	if !strings.Contains(logs.String(), `"remote":"198.51.100.7:40000"`) {
		t.Fatalf("the log does not name the real remote address: %s", logs)
	}
}

// From the trusted proxy, X-Forwarded-For naming an untrusted client changes nothing either.
func TestForwardedForIsIgnoredFromProxy(t *testing.T) {
	h, s, _ := newHandler(t, testConfig())
	w := serve(h, request("GET", "/", proxyAddr, map[string]string{
		"Remote-User": "user1", "X-Forwarded-For": "198.51.100.7",
	}))
	if w.Code != http.StatusNoContent || s.user.Name != "user1" {
		t.Fatalf("status %d, user %+v", w.Code, s.user)
	}
}

func TestTrustedProxyWithoutUsableUserHeader(t *testing.T) {
	cases := map[string]struct {
		values []string
		reason string
	}{
		"missing":          {nil, "user_header_missing"},
		"empty":            {[]string{""}, "user_header_missing"},
		"repeated":         {[]string{"user1", "admin1"}, "user_header_unusable"},
		"repeated same":    {[]string{"user1", "user1"}, "user_header_unusable"},
		"too long":         {[]string{strings.Repeat("a", 257)}, "user_header_unusable"},
		"control char":     {[]string{"user1\x01"}, "user_header_unusable"},
		"tab":              {[]string{"user\t1"}, "user_header_unusable"},
		"delete char":      {[]string{"user1\x7f"}, "user_header_unusable"},
		"c1 control char":  {[]string{"user1\u0085"}, "user_header_unusable"},
		"not utf-8":        {[]string{"user\xff1"}, "user_header_unusable"},
		"other user field": {nil, "user_header_missing"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h, s, logs := newHandler(t, testConfig())
			r := request("GET", "/", proxyAddr, nil)
			for _, v := range c.values {
				r.Header.Add("Remote-User", v)
			}
			if name == "other user field" {
				r.Header.Set("X-Remote-User", "user1")
				r.Header["Remote_User"] = []string{"user1"}
			}
			wantRefused(t, serve(h, r), s, "forbidden")
			if !strings.Contains(logs.String(), `"reason":"`+c.reason+`"`) {
				t.Fatalf("log %s, want reason %s", logs, c.reason)
			}
		})
	}
	t.Run("256 bytes is accepted", func(t *testing.T) {
		h, s, _ := newHandler(t, testConfig())
		name := strings.Repeat("é", 128)
		w := serve(h, request("GET", "/", proxyAddr, map[string]string{"Remote-User": name}))
		if w.Code != http.StatusNoContent || s.user.Name != name {
			t.Fatalf("status %d", w.Code)
		}
	})
}

func TestUnusableEmailOrGroupsHeader(t *testing.T) {
	for _, c := range []struct {
		header string
		values []string
	}{
		{"Remote-Email", []string{"user1@example.com", "admin1@example.com"}},
		{"Remote-Email", []string{strings.Repeat("a", 250) + "@example.com"}},
		{"Remote-Email", []string{"user1@example.com\x00"}},
		{"Remote-Groups", []string{"users", "admins"}},
		{"Remote-Groups", []string{"users\x1badmins"}},
	} {
		h, s, _ := newHandler(t, testConfig())
		r := request("GET", "/", proxyAddr, map[string]string{"Remote-User": "user1"})
		for _, v := range c.values {
			r.Header.Add(c.header, v)
		}
		wantRefused(t, serve(h, r), s, "forbidden")
	}
}

func TestOptionalHeadersMayBeMissing(t *testing.T) {
	h, s, _ := newHandler(t, testConfig())
	w := serve(h, request("GET", "/", proxyAddr, map[string]string{"Remote-User": "user1", "Remote-Email": ""}))
	if w.Code != http.StatusNoContent || s.user.Email != "" || s.user.Groups != nil || s.user.Admin {
		t.Fatalf("status %d, user %+v", w.Code, s.user)
	}
}

func TestCustomHeaderNames(t *testing.T) {
	cfg := testConfig()
	cfg.Auth.UserHeader = "X-Auth-User"
	cfg.Auth.EmailHeader = ""
	cfg.Auth.GroupsHeader = "x-auth-groups" // header names are case-insensitive
	cfg.Auth.GroupsSeparator = "|"
	h, s, _ := newHandler(t, cfg)
	w := serve(h, request("GET", "/", proxyAddr, map[string]string{
		"X-Auth-User": "user1", "Remote-User": "admin1", "Remote-Email": "admin1@example.com",
		"X-Auth-Groups": "users|admins|",
	}))
	if w.Code != http.StatusNoContent || s.user.Name != "user1" || s.user.Email != "" ||
		!slices.Equal(s.user.Groups, []string{"users", "admins"}) || !s.user.Admin {
		t.Fatalf("status %d, user %+v", w.Code, s.user)
	}
}

func TestIPv4MappedIPv6MatchesIPv4Prefix(t *testing.T) {
	for _, c := range []struct{ trusted, remote string }{
		{"192.0.2.10", "[::ffff:192.0.2.10]:40000"},
		{"192.0.2.0/24", "[::ffff:192.0.2.99]:40000"},
		{"::ffff:192.0.2.10", "192.0.2.10:40000"},
		{"::ffff:192.0.2.0/120", "192.0.2.99:40000"},
		{"2001:db8::/64", "[2001:db8::5]:40000"},
		{"fe80::1", "[fe80::1%eth0]:40000"},
	} {
		cfg := testConfig()
		cfg.Auth.TrustedProxies = []string{c.trusted}
		h, s, _ := newHandler(t, cfg)
		w := serve(h, request("GET", "/", c.remote, map[string]string{"Remote-User": "user1"}))
		if w.Code != http.StatusNoContent || s.user.Name != "user1" {
			t.Errorf("trusted %s, remote %s: status %d", c.trusted, c.remote, w.Code)
		}
	}
	cfg := testConfig()
	cfg.Auth.TrustedProxies = []string{"192.0.2.0/24"}
	h, s, _ := newHandler(t, cfg)
	wantRefused(t, serve(h, request("GET", "/", "[::ffff:198.51.100.7]:40000", map[string]string{"Remote-User": "user1"})), s, "forbidden")
}

func TestAdmin(t *testing.T) {
	cases := []struct {
		name       string
		adminGroup string
		allAdmins  bool
		groups     string
		want       bool
	}{
		{"by group", "admins", false, "users,admins", true},
		{"group is matched exactly", "admins", false, "users,Admins,admins-old", false},
		{"not in the group", "admins", false, "users", false},
		{"no admin group", "", false, "users,admins", false},
		{"empty group is never admin", "", false, ",,", false},
		{"all users are admins", "", true, "", true},
		{"all users are admins with a group", "admins", true, "users", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Auth.AdminGroup = c.adminGroup
			cfg.Auth.AllUsersAreAdmins = c.allAdmins
			h, s, _ := newHandler(t, cfg)
			serve(h, request("GET", "/", proxyAddr, map[string]string{"Remote-User": "user1", "Remote-Groups": c.groups}))
			if !s.called || s.user.Admin != c.want {
				t.Fatalf("called %v, admin %v, want %v", s.called, s.user.Admin, c.want)
			}
		})
	}
}

func TestModeNone(t *testing.T) {
	cfg := config.Defaults()
	cfg.Auth.Mode = "none"
	h, s, logs := newHandler(t, cfg)
	if !strings.Contains(logs.String(), `"msg":"auth_insecure_dev"`) || !strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Fatalf("no start-up warning: %s", logs)
	}
	w := serve(h, request("DELETE", "/api/jobs/x", untrustedAddr, map[string]string{"Remote-User": "user1"}))
	if w.Code != http.StatusNoContent || (s.user.Name != "dev" || !s.user.Admin || s.user.Email != "" || s.user.Groups != nil) {
		t.Fatalf("status %d, user %+v", w.Code, s.user)
	}
}

func TestUnsupportedModeRefusesEverything(t *testing.T) {
	cfg := testConfig()
	cfg.Auth.Mode = "oidc"
	h, s, _ := newHandler(t, cfg)
	wantRefused(t, serve(h, request("GET", "/", proxyAddr, map[string]string{"Remote-User": "user1"})), s, "forbidden")
}

func TestHealthzIsOpen(t *testing.T) {
	for _, method := range []string{"GET", "HEAD"} {
		h, s, _ := newHandler(t, testConfig())
		w := serve(h, request(method, "/healthz", untrustedAddr, nil))
		if w.Code != http.StatusNoContent || s.hasUser {
			t.Fatalf("%s: status %d, user %v", method, w.Code, s.hasUser)
		}
	}
	// Only GET and HEAD of exactly /healthz.
	for _, r := range []*http.Request{
		request("POST", "/healthz", untrustedAddr, nil),
		request("GET", "/healthz/", untrustedAddr, nil),
		request("GET", "/healthz/../api/session", untrustedAddr, nil),
	} {
		h, s, _ := newHandler(t, testConfig())
		wantRefused(t, serve(h, r), s, "forbidden")
	}
}

func TestReadyz(t *testing.T) {
	cfg := testConfig()
	for _, remote := range []string{"127.0.0.1:40000", "127.0.0.2:40000", "[::1]:40000", "[::ffff:127.0.0.1]:40000"} {
		h, s, _ := newHandler(t, cfg)
		w := serve(h, request("GET", "/readyz", remote, nil))
		if w.Code != http.StatusNoContent || s.hasUser {
			t.Errorf("%s: status %d", remote, w.Code)
		}
	}
	t.Run("untrusted address", func(t *testing.T) {
		h, s, _ := newHandler(t, cfg)
		wantRefused(t, serve(h, request("GET", "/readyz", untrustedAddr, nil)), s, "forbidden")
	})
	t.Run("loopback POST", func(t *testing.T) {
		h, s, _ := newHandler(t, cfg)
		wantRefused(t, serve(h, request("POST", "/readyz", "127.0.0.1:40000", nil)), s, "forbidden")
	})
	t.Run("not an admin", func(t *testing.T) {
		h, s, logs := newHandler(t, cfg)
		wantRefused(t, serve(h, request("GET", "/readyz", proxyAddr, map[string]string{"Remote-User": "user1"})), s, "forbidden")
		if !strings.Contains(logs.String(), `"reason":"not_admin"`) || !strings.Contains(logs.String(), `"user":"user1"`) {
			t.Fatalf("log %s", logs)
		}
	})
	t.Run("admin", func(t *testing.T) {
		h, s, _ := newHandler(t, cfg)
		w := serve(h, request("GET", "/readyz", proxyAddr, map[string]string{"Remote-User": "admin1", "Remote-Groups": "admins"}))
		if w.Code != http.StatusNoContent || !s.hasUser || !s.user.Admin {
			t.Fatalf("status %d, user %+v", w.Code, s.user)
		}
	})
}

func TestWithUserFromContext(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
	if _, ok := FromContext(r.Context()); ok {
		t.Fatal("a user without the middleware")
	}
	u := User{Name: "user1", Admin: true}
	got, ok := FromContext(WithUser(r.Context(), u))
	if !ok || got.Name != "user1" || !got.Admin {
		t.Fatalf("got %+v, %v", got, ok)
	}
}
