package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/flows"
)

// The confirmation (#40): the user confirms the proposal and the import starts. Rows are
// named by id; no request carries a path or a title.
//
//	POST /api/jobs/{id}/confirm  {"overrides": ["r3"]}  → 202 Job (state importing)
//
// overrides lists the rows to import despite their rejections or warnings. Errors besides
// the common ones:
//
//	409 needs_override     rows with rejections or warnings that overrides does not list:
//	                       {"error": {"code": "needs_override", "message": "…", "rows": ["r3"]}}
//	409 incomplete_rows    rows without a title or an episode (edit or skip them); "rows" as above
//	409 nothing_to_import  every row is skipped
//	409 wrong_state        the job is not awaiting confirmation (or the rows changed meanwhile)
//	400 bad_request        an override names no row of the job
//	503 storage_unavailable  a sentinel is missing, or the intake and library roots are on
//	                       different filesystems (importMode move)
//	502 arr_unavailable    the app did not answer (a show's episodes are read to judge the rows)
//
// The import and its result then show in GET /api/jobs/{id} ("import", "result").

func (s *Server) confirmRoutes(mux *http.ServeMux) {
	if s.deps.Store == nil || s.deps.Imports == nil {
		return
	}
	mux.HandleFunc("POST /api/jobs/{id}/confirm", s.confirmJob)
}

// rowsErrorBody is the error shape of a refusal about rows.
type rowsErrorBody struct {
	Error rowsError `json:"error"`
}

type rowsError struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Rows    []string `json:"rows"`
}

func (s *Server) confirmJob(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	var req flows.ConfirmRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	c, err := s.deps.Imports.Confirm(r.Context(), u.Name, j, req)
	if err != nil {
		s.confirmError(w, r, err)
		return
	}
	// The job is importing whether or not the client is still there: so are its audit entries.
	dctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	overrides := make([]string, 0, len(c.Overridden))
	for _, o := range c.Overridden {
		overrides = append(overrides, o.RowID)
	}
	s.audit(dctx, u, "import.confirmed", j.ID, map[string]string{
		"app": c.Target.App, "titleId": strconv.Itoa(c.Target.TitleID), "title": c.Target.Title,
		"rows": strings.Join(c.Rows, ","), "overrides": strings.Join(overrides, ","),
	})
	for _, o := range c.Overridden {
		s.audit(dctx, u, "rejection.overridden", j.ID, map[string]string{
			"row": o.RowID, "file": o.File,
			"rejections": strings.Join(o.Rejections, "; "), "warnings": strings.Join(o.Warnings, ","),
		})
	}
	files, uploads, err := s.parts(dctx, c.Job)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.view(u, c.Job, files, uploads))
}

func (s *Server) confirmError(w http.ResponseWriter, r *http.Request, err error) {
	var e *flows.Error
	if !errors.As(err, &e) {
		s.matchError(w, r, err)
		return
	}
	switch e.Code {
	case flows.CodeNeedsOverride, flows.CodeIncompleteRows:
		writeJSON(w, http.StatusConflict, rowsErrorBody{rowsError{Code: e.Code, Message: e.Message, Rows: e.Rows}})
	case flows.CodeNothingToImport:
		writeError(w, http.StatusConflict, e.Code, e.Message)
	case flows.CodeStorageUnavailable:
		writeError(w, http.StatusServiceUnavailable, e.Code, e.Message)
	default:
		s.matchError(w, r, err)
	}
}
