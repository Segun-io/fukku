package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/store"
)

// The match API (#37): which apps are configured, title lookups, and choosing the title a
// job is about. Titles are found on the server by id; no request carries a path or a title.
//
// Errors besides the common ones: 404 app_not_configured and title_not_found, 409
// wrong_state, already_in_library, title_in_use and title_busy, 502 arr_unavailable.

func (s *Server) matchRoutes(mux *http.ServeMux) {
	if s.deps.Store == nil {
		return
	}
	s.matcher = flows.NewMatcher(s.cfg, s.deps.Store, s.deps.Radarr, s.deps.Sonarr, s.deps.Locks, nil)
	mux.HandleFunc("GET /api/apps", s.getApps)
	mux.HandleFunc("GET /api/lookup/movies", s.lookupMovies)
	mux.HandleFunc("GET /api/lookup/series", s.lookupSeries)
	mux.HandleFunc("POST /api/jobs/{id}/match", s.matchJob)
	mux.HandleFunc("DELETE /api/jobs/{id}/match", s.unmatchJob)
}

type targetJSON struct {
	App     string `json:"app"`
	TitleID int    `json:"titleId"`
	Title   string `json:"title"`
	Year    int    `json:"year"`
}

func targetView(t store.Target) *targetJSON {
	return &targetJSON{App: t.App, TitleID: t.TitleID, Title: t.Title, Year: t.Year}
}

// matchError answers with the status of a refusal of the flow, or 500.
func (s *Server) matchError(w http.ResponseWriter, r *http.Request, err error) {
	code := flows.Code(err)
	var status int
	switch code {
	case flows.CodeBadRequest:
		status = http.StatusBadRequest
	case flows.CodeAppNotConfigured, flows.CodeTitleNotFound:
		status = http.StatusNotFound
	case flows.CodeWrongState, flows.CodeAlreadyInLibrary, flows.CodeTitleInUse, flows.CodeTitleBusy:
		status = http.StatusConflict
	case flows.CodeArrUnavailable:
		// The cause is for the log only: it may name the app's address.
		s.log.Warn("match_api", "method", r.Method, "path", r.URL.Path, "error", err)
		writeError(w, http.StatusBadGateway, code, "Radarr or Sonarr did not answer.")
		return
	default:
		s.internalError(w, r, err)
		return
	}
	msg := code
	var e *flows.Error
	if errors.As(err, &e) {
		msg = e.Message
	}
	writeError(w, status, code, msg)
}

func (s *Server) getApps(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.user(w, r); !ok {
		return
	}
	apps, errs := s.matcher.Apps(r.Context())
	for _, err := range errs {
		s.log.Warn("match_api", "method", r.Method, "path", r.URL.Path, "error", err)
	}
	writeJSON(w, http.StatusOK, apps)
}

func (s *Server) lookupMovies(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.user(w, r); !ok {
		return
	}
	hits, err := s.matcher.LookupMovies(r.Context(), r.URL.Query().Get("term"))
	if err != nil {
		s.matchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": hits})
}

func (s *Server) lookupSeries(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.user(w, r); !ok {
		return
	}
	hits, err := s.matcher.LookupSeries(r.Context(), r.URL.Query().Get("term"))
	if err != nil {
		s.matchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": hits})
}

func (s *Server) matchJob(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	var req flows.MatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	next, err := s.matcher.Match(r.Context(), u.Name, j, req)
	if err != nil {
		s.matchError(w, r, err)
		return
	}
	// The match is stored whether or not the client is still there: so are its record and
	// its audit entry.
	dctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	t, err := s.deps.Store.Target(dctx, next.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(dctx, u, "job.matched", j.ID, map[string]string{
		"app": t.App, "titleId": strconv.Itoa(t.TitleID), "title": t.Title,
	})
	s.respondJob(w, r, u, next, http.StatusOK)
}

func (s *Server) unmatchJob(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	next, err := s.matcher.Unmatch(r.Context(), u.Name, j)
	if err != nil {
		s.matchError(w, r, err)
		return
	}
	s.audit(context.WithoutCancel(r.Context()), u, "job.unmatched", j.ID, nil)
	s.respondJob(w, r, u, next, http.StatusOK)
}
