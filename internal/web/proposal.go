package web

import (
	"net/http"

	"github.com/segun-io/fukku/internal/flows"
)

// The proposal API (#38): the rows the app proposes for a matched job, and the user's
// edits, each re-checked through the app. Rows are addressed by id; no request carries a
// path or a title: both come from the store.
//
//	GET   /api/jobs/{id}/proposal                → 200 flows.Proposal
//	PATCH /api/jobs/{id}/proposal/rows/{rowId}   flows.Edit → 200 flows.RowView
//	POST  /api/jobs/{id}/proposal/bulk           flows.BulkEdit → 200 {"rows": [flows.RowView]}
//
// Errors: 409 wrong_state, 404 not_found, 400 bad_request, 502 arr_unavailable.
//
// The proposal is the user's: what only an admin may confirm (#116,
// auth.libraryChangesRequireAdmin) is listed in notOffered (the offer "override": of a
// rejection by the app or of existing_file) and in
// seasonDub.notOffered (replace, version) with the reason admin_required.

func (s *Server) proposalRoutes(mux *http.ServeMux) {
	if s.deps.Store == nil || s.deps.Proposals == nil {
		return
	}
	mux.HandleFunc("GET /api/jobs/{id}/proposal", s.getProposal)
	mux.HandleFunc("PATCH /api/jobs/{id}/proposal/rows/{rowId}", s.editProposalRow)
	mux.HandleFunc("POST /api/jobs/{id}/proposal/bulk", s.bulkEditProposal)
}

func (s *Server) getProposal(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	p, err := s.deps.Proposals.View(r.Context(), j, u.Admin)
	if err != nil {
		s.matchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) editProposalRow(w http.ResponseWriter, r *http.Request) {
	_, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	var e flows.Edit
	if !decodeJSON(w, r, &e) {
		return
	}
	row, err := s.deps.Proposals.EditRow(r.Context(), j, r.PathValue("rowId"), e)
	if err != nil {
		s.matchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) bulkEditProposal(w http.ResponseWriter, r *http.Request) {
	_, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	var b flows.BulkEdit
	if !decodeJSON(w, r, &b) {
		return
	}
	rows, err := s.deps.Proposals.Bulk(r.Context(), j, b)
	if err != nil {
		s.matchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}
