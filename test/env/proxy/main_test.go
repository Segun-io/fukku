package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// rig runs a recording upstream, the proxy in front of it and its control API.
type rig struct {
	t        *testing.T
	proxy    *proxy
	public   *httptest.Server
	control  *httptest.Server
	mu       sync.Mutex
	seen     []http.Header // headers of every request the upstream received
	received chan int      // body bytes each upstream request read
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, received: make(chan int, 16)}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.seen = append(r.seen, req.Header.Clone())
		r.mu.Unlock()
		n, _ := io.Copy(io.Discard, req.Body)
		r.received <- int(n)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(up.Close)

	p, err := newProxy("http://fukku-web:8080", "http://127.0.0.1:18080/__test/signin")
	if err != nil {
		t.Fatal(err)
	}
	p.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, up.Listener.Addr().String())
	}
	r.proxy = p
	r.public = httptest.NewServer(p.publicHandler())
	r.control = httptest.NewServer(p.controlHandler())
	t.Cleanup(r.public.Close)
	t.Cleanup(r.control.Close)
	return r
}

func (r *rig) forwarded() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.seen)
}

func (r *rig) lastSeen() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen[len(r.seen)-1]
}

func post(t *testing.T, url, body string) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func (r *rig) ctl(path, body string) int {
	r.t.Helper()
	return post(r.t, r.control.URL+path, body)
}

// do sends a request without following redirects and returns the status and headers.
func do(t *testing.T, method, url string, body io.Reader, mod func(*http.Request)) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if mod != nil {
		mod(req)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func TestUpstreamMustBeAComposeServiceName(t *testing.T) {
	for _, bad := range []string{
		"http://127.0.0.1:8080", "http://192.0.2.10:8080", "http://example.com", "http://localhost:8080",
		"http://[::1]:8080", "https://fukku-web:8080", "http://fukku-web:8080/api", "http://u:p@fukku-web", "fukku-web", "",
	} {
		if _, err := newProxy(bad, "http://x"); err == nil {
			t.Errorf("upstream %q was accepted", bad)
		}
	}
	for _, good := range []string{"http://fukku-web:8080", "http://fukku-web", "http://fukku-web:8080/"} {
		if _, err := newProxy(good, "http://x"); err != nil {
			t.Errorf("upstream %q refused: %v", good, err)
		}
	}
}

func TestUsersAndIdentityHeaders(t *testing.T) {
	r := newRig(t)
	get := func(cookie string, hdr map[string]string) http.Header {
		do(t, http.MethodGet, r.public.URL+"/api/session", nil, func(req *http.Request) {
			if cookie != "" {
				req.AddCookie(&http.Cookie{Name: userCookie, Value: cookie})
			}
			for k, v := range hdr {
				req.Header.Set(k, v)
			}
		})
		return r.lastSeen()
	}
	cases := []struct{ cookie, user, groups string }{
		{"", "user1", "users"},
		{"user2", "user2", "users"},
		{"admin1", "admin1", "users,media-admins"},
		{"nobody", "user1", "users"},
	}
	for _, c := range cases {
		h := get(c.cookie, nil)
		if h.Get("Remote-User") != c.user || h.Get("Remote-Email") != c.user+"@example.com" || h.Get("Remote-Groups") != c.groups {
			t.Errorf("cookie %q: got %q %q %q", c.cookie, h.Get("Remote-User"), h.Get("Remote-Email"), h.Get("Remote-Groups"))
		}
	}
	// Client-supplied identity never gets through.
	h := get("", map[string]string{"Remote-User": "admin1", "Remote-Email": "x@evil.example", "Remote-Groups": "media-admins"})
	if h.Get("Remote-User") != "user1" || h.Get("Remote-Groups") != "users" || h.Get("Remote-Email") != "user1@example.com" {
		t.Errorf("client identity headers leaked: %v", h)
	}
	if len(h.Values("Remote-User")) != 1 {
		t.Errorf("Remote-User repeated: %v", h.Values("Remote-User"))
	}
}

func TestLogin(t *testing.T) {
	r := newRig(t)
	status, hdr, _ := do(t, http.MethodGet, r.public.URL+"/__test/login?user=admin1", nil, nil)
	if status != http.StatusOK || !strings.Contains(hdr.Get("Set-Cookie"), userCookie+"=admin1") {
		t.Errorf("login: status %d, Set-Cookie %q", status, hdr.Get("Set-Cookie"))
	}
	if status, _, _ := do(t, http.MethodGet, r.public.URL+"/__test/login?user=root", nil, nil); status != http.StatusBadRequest {
		t.Errorf("unknown user: status %d", status)
	}
	// /__test/ is the proxy's own: unknown paths there are not forwarded.
	if status, _, _ := do(t, http.MethodGet, r.public.URL+"/__test/other", nil, nil); status != http.StatusNotFound || r.forwarded() != 0 {
		t.Errorf("/__test/other: status %d, forwarded %d", status, r.forwarded())
	}
}

func TestSessionModes(t *testing.T) {
	r := newRig(t)

	if code := r.ctl("/control/session", `{"mode":"expired-401"}`); code != http.StatusNoContent {
		t.Fatalf("control session: %d", code)
	}
	if status, _, _ := do(t, http.MethodGet, r.public.URL+"/api/session", nil, nil); status != http.StatusUnauthorized || r.forwarded() != 0 {
		t.Errorf("expired-401: status %d, forwarded %d", status, r.forwarded())
	}

	r.ctl("/control/session", `{"mode":"expired-302"}`)
	status, hdr, _ := do(t, http.MethodGet, r.public.URL+"/", nil, nil)
	if status != http.StatusFound || hdr.Get("Location") != "http://127.0.0.1:18080/__test/signin" || r.forwarded() != 0 {
		t.Errorf("expired-302: status %d, location %q, forwarded %d", status, hdr.Get("Location"), r.forwarded())
	}

	// The sign-in page works while expired, and its button restores the session.
	status, _, page := do(t, http.MethodGet, r.public.URL+"/__test/signin", nil, nil)
	if status != http.StatusOK || !strings.Contains(page, ">sign in</button>") {
		t.Errorf("sign-in page: %d %s", status, page)
	}
	if status, _, _ := do(t, http.MethodPost, r.public.URL+"/__test/signin", nil, nil); status != http.StatusSeeOther {
		t.Errorf("sign-in submit: %d", status)
	}
	if status, _, _ := do(t, http.MethodGet, r.public.URL+"/api/session", nil, nil); status != http.StatusNoContent {
		t.Errorf("after sign-in: status %d, want the upstream's 204", status)
	}

	if code := r.ctl("/control/session", `{"mode":"bogus"}`); code != http.StatusBadRequest {
		t.Errorf("bogus mode: %d", code)
	}
	r.ctl("/control/session", `{"mode":"expired-401"}`)
	r.ctl("/control/session", `{"mode":"ok"}`)
	if status, _, _ := do(t, http.MethodGet, r.public.URL+"/api/session", nil, nil); status != http.StatusNoContent {
		t.Errorf("mode ok: %d", status)
	}
}

func TestCutUpload(t *testing.T) {
	r := newRig(t)
	if code := r.ctl("/control/cut", `{"afterBytes": 1000, "times": 2}`); code != http.StatusNoContent {
		t.Fatalf("control cut: %d", code)
	}
	patch := func(path string) error {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPatch, r.public.URL+path, bytes.NewReader(make([]byte, 5000)))
		req.Header.Set("Content-Type", "application/offset+octet-stream")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}
	wait := func() int {
		select {
		case n := <-r.received:
			return n
		case <-time.After(5 * time.Second):
			t.Fatal("upstream saw nothing")
			return 0
		}
	}

	// A request that is not an upload PATCH is not cut and does not use up a cut.
	if status, _, _ := do(t, http.MethodPost, r.public.URL+"/api/uploads/", strings.NewReader("{}"), nil); status != http.StatusNoContent {
		t.Fatalf("POST /api/uploads/: %d", status)
	}
	wait()

	for i := range 2 {
		if err := patch("/api/uploads/abc"); err == nil {
			t.Errorf("cut %d: the client saw a normal response", i+1)
		}
		if got := wait(); got != 1000 {
			t.Errorf("cut %d: upstream read %d body bytes, want 1000", i+1, got)
		}
	}
	// The third one goes through in full.
	if err := patch("/api/uploads/abc"); err != nil {
		t.Fatalf("after the cuts: %v", err)
	}
	if got := wait(); got != 5000 {
		t.Errorf("upstream read %d body bytes, want 5000", got)
	}
	if h := r.lastSeen(); h.Get("Remote-User") != "user1" {
		t.Errorf("identity missing on the forwarded upload: %v", h)
	}

	for _, bad := range []string{`{}`, `{"afterBytes":-1,"times":1}`, `{"afterBytes":1,"times":0}`, `{"afterBytes":1,"times":1,"x":2}`, `nope`} {
		if code := r.ctl("/control/cut", bad); code != http.StatusBadRequest {
			t.Errorf("cut %s: %d", bad, code)
		}
	}
}

func TestResetAndControlIsNotOnThePublicPort(t *testing.T) {
	r := newRig(t)
	r.ctl("/control/session", `{"mode":"expired-401"}`)
	r.ctl("/control/cut", `{"afterBytes": 10, "times": 5}`)
	if code := r.ctl("/control/reset", ``); code != http.StatusNoContent {
		t.Fatalf("reset: %d", code)
	}
	if m := r.proxy.state.getMode(); m != modeOK {
		t.Errorf("mode after reset: %s", m)
	}
	if _, ok := r.proxy.state.takeCut(); ok {
		t.Error("cut survived reset")
	}

	// Control calls are POST only.
	if status, _, _ := do(t, http.MethodGet, r.control.URL+"/control/session", nil, nil); status != http.StatusMethodNotAllowed {
		t.Errorf("GET /control/session: %d", status)
	}
	// The public port has no control API: the path is just another path for Fukku.
	if code := post(t, r.public.URL+"/control/session", `{"mode":"expired-401"}`); code != http.StatusNoContent ||
		r.forwarded() != 1 || r.proxy.state.getMode() != modeOK {
		t.Errorf("public /control/session: status %d, forwarded %d, mode %s", code, r.forwarded(), r.proxy.state.getMode())
	}
}
