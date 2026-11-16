// Package web is the web process: the HTTP API, the embedded UI and, from M1 on, uploads.
// It holds the API keys and never opens media files.
package web

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/uploads"
	"github.com/segun-io/fukku/internal/version"
)

// Server is the web process's HTTP server.
type Server struct {
	cfg  config.Config
	log  *slog.Logger
	ui   fs.FS
	deps Deps
	tus  *uploads.Handler // /api/uploads/; nil without a store
	// matcher serves the match API; it is set when the routes are built (matchRoutes).
	matcher *flows.Matcher
	http    *http.Server

	ready readiness
}

// New builds the server. ui is the built single-page app.
func New(cfg config.Config, log *slog.Logger, ui fs.FS, deps Deps) *Server {
	s := &Server{cfg: cfg, log: log, ui: ui, deps: deps}
	if deps.Store != nil {
		h, err := uploads.New(uploads.Options{Config: cfg, Log: log, Store: deps.Store, Metrics: deps.Metrics})
		if err != nil {
			log.Error("uploads_unavailable", "error", err)
		} else {
			s.tus = h
		}
	}
	s.ready.now = time.Now
	s.http = &http.Server{
		Addr:              cfg.Server.ListenAddress,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return s
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.http.Addr)
	if err != nil {
		return err
	}
	if s.tus != nil {
		s.tus.Start(ctx)
	}
	s.log.Info("web_listening", "address", ln.Addr().String(), "version", version.Version)
	errc := make(chan error, 1)
	go func() { errc <- s.http.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.http.Shutdown(shutdown); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// healthz says the process is up. Readiness is /readyz.
func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
}

// spa serves the built UI. Unknown paths without a file extension get index.html, so the
// client-side router can handle them; /api/ paths never get here (routes). It is registered
// for every method, so that /api/ can be too, and refuses all but GET and HEAD itself.
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if _, err := fs.Stat(s.ui, name); err == nil {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		if path.Ext(name) != "" && name != "index.html" {
			http.NotFound(w, r)
			return
		}
		index, err := fs.ReadFile(s.ui, "index.html")
		if err != nil {
			http.Error(w, "The UI is not built into this binary (run `make ui`).", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}
