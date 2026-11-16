package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/segun-io/fukku/internal/flows"
)

// The confirmation (#40): the user confirms the proposal and the import starts. Rows are
// named by id; no request carries a path or a title.
//
//	POST /api/jobs/{id}/confirm  {"overrides": ["r3"], "seasonDub": "replace",
//	                              "subtitles": [{"fileId": 12, "language": "es", "forced": false, "hearingImpaired": true}]}
//	                              → 202 Job (state importing)
//
// overrides lists the rows to import despite their rejections or warnings (the language
// warning of #39 included). subtitles (optional) sets the language (ISO 639-1) and flags of
// subtitle files of the job for the hand-over; a file not listed keeps what its name says.
// seasonDub (optional) answers the proposal's seasonDub question (#57) for rows whose
// episodes already have a file: replace imports over them (the choice answers the app's
// "not an upgrade" refusal and the existing_file warning of those rows; the season is
// unmonitored afterwards when only a dub was imported), subtitlesOnly imports nothing and
// hands the subtitles over. With a fixed seasonDub.policy it must be empty or equal to it.
// Audit: season.replaced or season.subtitles_only per season, import.confirmed,
// rejection.overridden per overridden row, and language.overridden with the custom formats
// and scores when the row's language warning was overridden.
// Errors besides the common ones:
//
//	409 needs_override     rows with rejections or warnings that overrides does not list:
//	                       {"error": {"code": "needs_override", "message": "…", "rows": ["r3"]}}
//	409 incomplete_rows    rows without a title or an episode (edit or skip them); "rows" as above
//	409 season_dub_choice  rows whose episodes already have a file and seasonDub.policy is
//	                       ask: "rows" lists them; send "seasonDub" with one of the proposal's
//	                       seasonDub.offers ("replace", "subtitlesOnly"), or skip the rows
//	409 shared_file        with replace: the library file of these rows also holds episodes
//	                       outside the job (a double); "rows" as above; list one in overrides
//	                       to accept losing those episodes' file
//	409 nothing_to_import  every row is skipped
//	409 wrong_state        the job is not awaiting confirmation (or the rows changed meanwhile)
//	409 flow2              the film already has a file (flow 2, #52): the job's "flow2" says
//	                       what can be done instead
//	400 bad_request        an override names no row of the job; a subtitle choice names no
//	                       subtitle file of the job, a file twice, or an unknown language
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
	c, err := s.deps.Imports.Confirm(r.Context(), u.Name, u.Email, j, req)
	if err != nil {
		s.confirmError(w, r, err)
		return
	}
	// The job is importing, with its audit entries (stored in the same transaction),
	// whether or not the client is still there.
	dctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
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
	case flows.CodeNeedsOverride, flows.CodeIncompleteRows, flows.CodeSeasonDubChoice, flows.CodeSharedFile:
		writeJSON(w, http.StatusConflict, rowsErrorBody{rowsError{Code: e.Code, Message: e.Message, Rows: e.Rows}})
	case flows.CodeNothingToImport:
		writeError(w, http.StatusConflict, e.Code, e.Message)
	case flows.CodeStorageUnavailable:
		writeError(w, http.StatusServiceUnavailable, e.Code, e.Message)
	default:
		s.matchError(w, r, err)
	}
}
