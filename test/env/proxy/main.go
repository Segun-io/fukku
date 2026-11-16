// Command proxy is the test reverse proxy of the Fukku dev stack (make dev-up). It stands in
// for the authenticating reverse proxy of a real deployment: it signs requests in as one of
// three fixed users by setting Remote-User, Remote-Email and Remote-Groups, and a control API
// makes it misbehave on demand (expired sessions, cut uploads) for tests. The contract
// (addresses, users, control API) is owned by issue #30 and summarised in test/env/compose.yaml.
// It is for local tests only.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Header names the product's defaults expect (auth.userHeader and friends).
const (
	headerUser   = "Remote-User"
	headerEmail  = "Remote-Email"
	headerGroups = "Remote-Groups"
	// userCookie picks the signed-in user; GET /__test/login?user=<name> sets it.
	userCookie  = "fukku_test_user"
	defaultUser = "user1"
)

type testUser struct {
	Name   string
	Groups string
}

var users = map[string]testUser{
	"user1":  {"user1", "users"},
	"user2":  {"user2", "users"},
	"admin1": {"admin1", "users,media-admins"},
}

// A compose service name: one DNS label, so the proxy can only ever reach a container of the
// stack and never an address typed by mistake.
var serviceName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := newFlags()
	if err := fs.set.Parse(args); err != nil {
		return 2
	}
	if *fs.health != "" {
		return healthcheck(*fs.health)
	}
	p, err := newProxy(*fs.upstream, *fs.signin)
	if err != nil {
		log.Printf("proxy: %v", err)
		return 1
	}

	pub := &http.Server{Addr: *fs.listen, Handler: p.publicHandler(), ReadHeaderTimeout: 10 * time.Second}
	ctl := &http.Server{Addr: *fs.control, Handler: p.controlHandler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 2)
	for _, s := range []*http.Server{pub, ctl} {
		go func() { errc <- s.ListenAndServe() }()
	}
	log.Printf("proxy: public %s, control %s, upstream %s", *fs.listen, *fs.control, *fs.upstream)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := 0
	select {
	case err := <-errc:
		log.Printf("proxy: %v", err)
		code = 1
	case <-ctx.Done():
	}
	_ = pub.Close()
	_ = ctl.Close()
	return code
}

func healthcheck(addr string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// checkUpstream accepts only http://<compose service name>[:port].
func checkUpstream(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("upstream %q must look like http://fukku-web:8080", raw)
	}
	if !serviceName.MatchString(u.Hostname()) || u.Hostname() == "localhost" {
		return nil, fmt.Errorf("upstream host %q is not a compose service name (a single lower-case DNS label); the test proxy never forwards anywhere else", u.Hostname())
	}
	return u, nil
}

// Session modes of the control API.
const (
	modeOK         = "ok"
	modeExpired401 = "expired-401"
	modeExpired302 = "expired-302"
)

type proxy struct {
	upstream  *url.URL
	signinURL string
	rp        *httputil.ReverseProxy
	state     state
	// dial connects to the upstream; tests replace it to reach a server that is not a
	// compose service.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

func newProxy(upstream, signinURL string) (*proxy, error) {
	u, err := checkUpstream(upstream)
	if err != nil {
		return nil, err
	}
	p := &proxy{upstream: u, signinURL: signinURL, state: newState(), dial: (&net.Dialer{}).DialContext}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(u)
			r.SetXForwarded()
			// Whatever the client sent, identity comes from this proxy only.
			setIdentity(r.Out.Header, r.In)
		},
		Transport: &http.Transport{
			DialContext:     func(ctx context.Context, n, a string) (net.Conn, error) { return p.dial(ctx, n, a) },
			MaxIdleConns:    10,
			IdleConnTimeout: 30 * time.Second,
		},
		FlushInterval: -1, // stream bodies and responses; uploads are large
	}
	return p, nil
}

func setIdentity(h http.Header, in *http.Request) {
	h.Del(headerUser)
	h.Del(headerEmail)
	h.Del(headerGroups)
	u := userOf(in)
	h.Set(headerUser, u.Name)
	h.Set(headerEmail, u.Name+"@example.com")
	h.Set(headerGroups, u.Groups)
}

func userOf(r *http.Request) testUser {
	if c, err := r.Cookie(userCookie); err == nil {
		if u, ok := users[c.Value]; ok {
			return u
		}
	}
	return users[defaultUser]
}

func (p *proxy) publicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__test/login", p.login)
	mux.HandleFunc("GET /__test/signin", p.signinPage)
	mux.HandleFunc("POST /__test/signin", p.signinSubmit)
	// Anything else under /__test/ belongs to the proxy and is never forwarded.
	mux.HandleFunc("/__test/", http.NotFound)
	mux.HandleFunc("/", p.forward)
	return mux
}

func (p *proxy) login(w http.ResponseWriter, r *http.Request) {
	u, ok := users[r.URL.Query().Get("user")]
	if !ok {
		http.Error(w, "unknown user; use user1, user2 or admin1", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: userCookie, Value: u.Name, Path: "/", SameSite: http.SameSiteLaxMode})
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, "{\"user\":%q,\"email\":%q,\"groups\":%q}\n", u.Name, u.Name+"@example.com", u.Groups)
}

const signinHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Test sign-in</title></head>
<body><h1>Test sign-in</h1>
<form method="post" action="/__test/signin"><button type="submit">sign in</button></form>
</body></html>
`

func (p *proxy) signinPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, signinHTML)
}

func (p *proxy) signinSubmit(w http.ResponseWriter, r *http.Request) {
	p.state.setMode(modeOK)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (p *proxy) forward(w http.ResponseWriter, r *http.Request) {
	switch p.state.getMode() {
	case modeExpired401:
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	case modeExpired302:
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, p.signinURL, http.StatusFound)
		return
	}
	if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/uploads/") {
		if after, ok := p.state.takeCut(); ok {
			p.cutUpload(w, r, after)
			return
		}
	}
	p.rp.ServeHTTP(w, r)
}

// cutUpload forwards the request head and the first after bytes of the body to the upstream,
// then closes the client and upstream connections, as a network failure would.
func (p *proxy) cutUpload(w http.ResponseWriter, r *http.Request, after int64) {
	up, err := p.dial(r.Context(), "tcp", p.upstream.Host)
	if err != nil {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = up.Close() }()

	h := r.Header.Clone()
	for _, k := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "Transfer-Encoding", "Upgrade", "Te", "Trailer", "Content-Length", "Expect"} {
		h.Del(k)
	}
	setIdentity(h, r)
	head := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n", r.Method, r.URL.RequestURI(), p.upstream.Host)
	if r.ContentLength >= 0 {
		head += fmt.Sprintf("Content-Length: %d\r\n", r.ContentLength)
	}
	if _, err := io.WriteString(up, head); err != nil {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	if err := h.Write(up); err != nil {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	if _, err := io.WriteString(up, "\r\n"); err != nil {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	_, _ = io.CopyN(up, r.Body, after)

	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	// Reset rather than finish politely: the sender must see a broken connection.
	if tc, ok := client.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = client.Close()
	// Give the upstream a moment to read the bytes before its connection goes away.
	time.Sleep(50 * time.Millisecond)
}

func (p *proxy) controlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("POST /control/session", p.controlSession)
	mux.HandleFunc("POST /control/cut", p.controlCut)
	mux.HandleFunc("POST /control/reset", func(w http.ResponseWriter, _ *http.Request) {
		p.state.reset()
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}
