package web

import "net/http"

// uploadsHandler is /api/uploads/ (tus, package uploads). Without a store there are no
// uploads, and the endpoint says so.
func (s *Server) uploadsHandler() http.Handler {
	if s.tus != nil {
		return s.tus
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "Uploads need the database.")
	})
}
