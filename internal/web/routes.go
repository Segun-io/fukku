package web

import (
	"net/http"

	"github.com/segun-io/fukku/internal/auth"
)

// routes registers every route, one line per feature; each issue adds its own line.
// Everything passes through the chain in Handler, so a handler can rely on
// auth.FromContext (except for /healthz and /readyz) and on a body limit.
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /metrics", s.metrics) // #68
	mux.HandleFunc("GET /api/session", s.session)
	s.jobRoutes(mux)      // #11
	s.matchRoutes(mux)    // #37
	s.proposalRoutes(mux) // #38
	s.confirmRoutes(mux)  // #40
	s.previewRoutes(mux)  // #53
	s.adminRoutes(mux)    // #69
	mux.Handle(uploadsPath, s.uploadsHandler())
	mux.HandleFunc("/api/", apiNotFound)
	mux.Handle("/", s.spa())
}

// Handler returns the routes behind the common chain: security headers → authentication →
// Origin check → JSON body limit → routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	h := limitJSONBody(mux)
	h = auth.OriginCheck(s.cfg, s.log)(h)
	h = auth.Middleware(s.cfg, s.log)(h)
	return securityHeaders(h)
}
