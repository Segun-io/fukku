package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/segun-io/fukku/internal/config"
)

// maxIdentityBytes is the longest user name or email the middleware accepts.
const maxIdentityBytes = 256

// refusal is why a request was turned away: a stable reason for the log (auth_refused) and
// an English message for the answer.
type refusal struct {
	code    string // the API error code: forbidden or origin_mismatch
	reason  string
	message string
}

var (
	refusedUntrusted = refusal{"forbidden", "untrusted_address",
		"Requests must come through the authenticating reverse proxy."}
	refusedNoUser = refusal{"forbidden", "user_header_missing",
		"The reverse proxy did not send a user."}
	refusedBadUser = refusal{"forbidden", "user_header_unusable",
		"The reverse proxy sent an unusable user header."}
	refusedBadEmail = refusal{"forbidden", "email_header_unusable",
		"The reverse proxy sent an unusable email header."}
	refusedBadGroups = refusal{"forbidden", "groups_header_unusable",
		"The reverse proxy sent an unusable groups header."}
	refusedNotAdmin = refusal{"forbidden", "not_admin",
		"Only an admin may see this."}
	refusedMode = refusal{"forbidden", "auth_mode_unsupported",
		"This authentication mode is not available."}
)

// Middleware authenticates every request according to auth.mode and puts the user into the
// request context (FromContext). Requests that cannot be authenticated get 403 forbidden and
// are logged as auth_refused.
//
// In proxyHeaders mode a request is accepted only when the connection comes from one of
// auth.trustedProxies and carries exactly one usable auth.userHeader. Only the connection's
// own remote address counts: X-Forwarded-For and similar headers never do, so nothing a
// client sends can make it look like the proxy.
//
// Two routes need no user: GET /healthz from anywhere, and GET /readyz from a loopback
// address (the container's health check). /readyz from anywhere else needs an admin. GET
// /metrics follows the same rule as /readyz (a scraper on the host, or an admin).
func Middleware(cfg config.Config, log *slog.Logger) func(http.Handler) http.Handler {
	var authenticate func(r *http.Request, addr netip.Addr) (User, *refusal)
	switch a := cfg.Auth; a.Mode {
	case "proxyHeaders":
		authenticate = newProxyHeaders(a, log).authenticate
	case "none":
		log.Warn("auth_insecure_dev",
			"note", "auth.mode=none: every request is the admin user dev, with no sign-in; for development only, never expose it")
		authenticate = func(*http.Request, netip.Addr) (User, *refusal) {
			return User{Name: "dev", Admin: true}, nil
		}
	default:
		// Validation refuses other modes; fail closed if one gets here anyway.
		log.Error("auth_mode_unsupported", "mode", a.Mode)
		authenticate = func(*http.Request, netip.Addr) (User, *refusal) { return User{}, &refusedMode }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			addr := remoteAddr(r)
			// /readyz and /metrics share one rule: loopback needs no user, anyone else an admin.
			readyz := isProbe(r, "/readyz") || isProbe(r, "/metrics")
			if isProbe(r, "/healthz") || (readyz && addr.IsLoopback()) {
				next.ServeHTTP(w, r)
				return
			}
			u, ref := authenticate(r, addr)
			if ref == nil && readyz && !u.Admin {
				ref = &refusedNotAdmin
			}
			if ref != nil {
				var attrs []any
				if u.Name != "" {
					attrs = append(attrs, "user", u.Name)
				}
				refuse(w, r, log, *ref, attrs...)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
		})
	}
}

// remoteAddr is the address of the connection the request came on, with an IPv4-mapped IPv6
// address turned into IPv4 and any zone dropped. It is the zero Addr when the server did not
// record a usable address. Headers such as X-Forwarded-For never take part.
func remoteAddr(r *http.Request) netip.Addr {
	var a netip.Addr
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		a = ap.Addr()
	} else if a, err = netip.ParseAddr(r.RemoteAddr); err != nil {
		return netip.Addr{}
	}
	return a.Unmap().WithZone("")
}

// isProbe reports whether r is a GET (or HEAD) of exactly path.
func isProbe(r *http.Request, path string) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == path
}

type proxyHeaders struct {
	trusted []netip.Prefix
	cfg     config.Auth
}

func newProxyHeaders(a config.Auth, log *slog.Logger) *proxyHeaders {
	p := &proxyHeaders{cfg: a}
	for _, s := range a.TrustedProxies {
		pre, err := config.ParsePrefix(s)
		if err != nil {
			// Validation refuses this; skipping it can only refuse more requests.
			log.Error("auth_trusted_proxy_invalid", "entry", s, "error", err)
			continue
		}
		p.trusted = append(p.trusted, pre)
	}
	return p
}

func (p *proxyHeaders) isTrusted(a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	return slices.ContainsFunc(p.trusted, func(pre netip.Prefix) bool { return pre.Contains(a) })
}

func (p *proxyHeaders) authenticate(r *http.Request, addr netip.Addr) (User, *refusal) {
	if !p.isTrusted(addr) {
		return User{}, &refusedUntrusted
	}
	name, present, ok := identityHeader(r.Header, p.cfg.UserHeader, maxIdentityBytes)
	switch {
	case !present:
		return User{}, &refusedNoUser
	case !ok:
		return User{}, &refusedBadUser
	}
	u := User{Name: name}
	if p.cfg.EmailHeader != "" {
		email, _, ok := identityHeader(r.Header, p.cfg.EmailHeader, maxIdentityBytes)
		if !ok {
			return User{}, &refusedBadEmail
		}
		u.Email = email
	}
	if p.cfg.GroupsHeader != "" {
		// Group lists can be long, so only the request's own header size limit applies.
		groups, _, ok := identityHeader(r.Header, p.cfg.GroupsHeader, 0)
		if !ok {
			return User{}, &refusedBadGroups
		}
		for g := range strings.SplitSeq(groups, p.cfg.GroupsSeparator) {
			if g = strings.TrimSpace(g); g != "" {
				u.Groups = append(u.Groups, g)
			}
		}
	}
	u.Admin = p.cfg.AllUsersAreAdmins || p.cfg.AdminGroup != "" && slices.Contains(u.Groups, p.cfg.AdminGroup)
	return u, nil
}

// identityHeader reads a header set by the proxy. present is false when it is missing or
// empty. ok is false when it is unusable: repeated (a proxy that appends instead of
// replacing would let a client add its own), longer than limit bytes (0: no limit), not
// UTF-8 or with control characters. A missing header is usable.
func identityHeader(h http.Header, name string, limit int) (v string, present, ok bool) {
	values := h.Values(name)
	switch {
	case len(values) == 0:
		return "", false, true
	case len(values) > 1:
		return "", true, false
	}
	v = values[0]
	if v == "" {
		return "", false, true
	}
	if (limit > 0 && len(v) > limit) || !utf8.ValidString(v) || strings.ContainsFunc(v, unicode.IsControl) {
		return "", true, false
	}
	return v, true, true
}

// refuse answers 403 with the API's error shape and logs auth_refused.
func refuse(w http.ResponseWriter, r *http.Request, log *slog.Logger, ref refusal, attrs ...any) {
	log.Warn("auth_refused", append([]any{
		"reason", ref.reason, "remote", r.RemoteAddr, "method", r.Method, "path", r.URL.Path,
	}, attrs...)...)
	writeError(w, http.StatusForbidden, ref.code, ref.message)
}

// writeError writes the error shape of the JSON API conventions; internal/web has the same
// for handlers, and a test there checks the two agree.
func writeError(w http.ResponseWriter, status int, code, message string) {
	type apiError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]apiError{"error": {code, message}})
}
