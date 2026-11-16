package auth

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/segun-io/fukku/internal/config"
)

var refusedOrigin = refusal{"origin_mismatch", "origin_mismatch",
	"State-changing requests must come from this site (the Origin header does not match)."}

// OriginCheck refuses cross-site state changes when auth.originCheck is on. Every method
// except GET, HEAD and OPTIONS needs an Origin header equal to the origin of
// server.publicBaseUrl; when that setting is empty, the Origin's host must equal the
// request's Host (the scheme is not compared, because TLS usually ends at the proxy). A
// missing or "null" Origin is refused with 403 origin_mismatch. No endpoint changes state
// on GET, so those are never checked.
//
// The API never answers CORS preflights or sends Access-Control-* headers: it is same-origin
// only.
func OriginCheck(cfg config.Config, log *slog.Logger) func(http.Handler) http.Handler {
	if !cfg.Auth.OriginCheck {
		log.Warn("origin_check_off", "note", "auth.originCheck=false: cross-site requests are not refused")
		return func(next http.Handler) http.Handler { return next }
	}
	var match func(origin *url.URL, r *http.Request) bool
	if base := cfg.Server.PublicBaseURL; base != "" {
		want, ok := parseOrigin(base, true)
		if !ok {
			// Validation refuses this; refuse every state change if it gets here anyway.
			log.Error("origin_public_base_url_invalid", "publicBaseUrl", base)
		}
		match = func(origin *url.URL, _ *http.Request) bool {
			return ok && origin.Scheme == want.Scheme && origin.Host == want.Host
		}
	} else {
		match = func(origin *url.URL, r *http.Request) bool {
			host := strings.ToLower(r.Host)
			return host != "" && stripPort(origin.Host, "80", "443") == stripPort(host, "80", "443")
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			values := r.Header.Values("Origin")
			var origin *url.URL
			ok := len(values) == 1
			if ok {
				origin, ok = parseOrigin(values[0], false)
			}
			if !ok || !match(origin, r) {
				attrs := []any{"origin", strings.Join(values, ", "), "host", r.Host}
				if u, known := FromContext(r.Context()); known {
					attrs = append(attrs, "user", u.Name)
				}
				refuse(w, r, log, refusedOrigin, attrs...)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// parseOrigin parses an Origin header (scheme://host[:port], nothing else) or, with base,
// a URL whose origin is wanted (server.publicBaseUrl may have a path). The scheme and host
// come back in lower case, without the scheme's default port. "null" is never an origin.
func parseOrigin(s string, base bool) (*url.URL, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" {
		return nil, false
	}
	if !base && (u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery) {
		return nil, false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, false
	}
	defaultPort := map[string]string{"http": "80", "https": "443"}[scheme]
	return &url.URL{Scheme: scheme, Host: stripPort(strings.ToLower(u.Host), defaultPort)}, true
}

// stripPort drops the port from host:port when it is one of ports, so "example.com:443"
// and "example.com" compare equal (browsers never send a default port in Origin).
func stripPort(hostport string, ports ...string) string {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil || !slices.Contains(ports, port) {
		return hostport
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}
