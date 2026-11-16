package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// The JSON API conventions (issue #5): everything is under /api/, same origin, JSON in UTF-8
// with Cache-Control: no-store. Errors are the HTTP status plus
//
//	{"error": {"code": "not_found", "message": "No such job."}}
//
// where code is stable (the UI translates it) and message is English, for logs and
// developers. Codes every endpoint may return: forbidden (403), origin_mismatch (403),
// not_found (404), bad_request (400), too_large (413), internal (500). Another user's
// resource answers 404, never 403.

// maxJSONBody is the largest request body outside /api/uploads/.
const maxJSONBody = 1 << 20

// uploadsPath is where uploads are mounted (tus); its bodies are not limited to maxJSONBody.
const uploadsPath = "/api/uploads/"

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorBody struct {
	Error apiError `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers with the API's error shape.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{apiError{Code: code, Message: message}})
}

// apiNotFound answers every /api/ path no route claims.
func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "No such API endpoint.")
}

// decodeJSON reads the request body into v. On failure it has already answered, with 413
// too_large above maxJSONBody or 400 bad_request otherwise, and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil && dec.More() {
		err = errors.New("more than one JSON value")
	}
	var tooLarge *http.MaxBytesError
	switch {
	case err == nil:
		return true
	case errors.As(err, &tooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("The request body is larger than %d bytes.", tooLarge.Limit))
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "The request body is not valid JSON: "+err.Error())
	}
	return false
}

// limitJSONBody caps request bodies at maxJSONBody, except for the routes mounted under
// uploadsPath. A declared length above it is refused at once; a chunked body fails while it
// is read (decodeJSON).
//
// Whether a request is an upload is decided by the pattern mux will route it to, never by
// the path alone: a cleaned or unescaped path (/api/jobs/..%2Fuploads%2Fx) can look like an
// upload while the mux, which matches escaped segments, sends it to another handler.
func limitJSONBody(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); isUploadPattern(pattern) {
			mux.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > maxJSONBody {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("The request body is larger than %d bytes.", maxJSONBody))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
		mux.ServeHTTP(w, r)
	})
}

// isUploadPattern reports whether a ServeMux pattern ("[METHOD ][HOST]/PATH") is a route
// under uploadsPath. The empty pattern (no route, or a redirect to a cleaned path) is not.
func isUploadPattern(pattern string) bool {
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		pattern = strings.TrimLeft(pattern[i+1:], " \t")
	}
	if i := strings.IndexByte(pattern, '/'); i > 0 {
		pattern = pattern[i:] // a host
	}
	return pattern+"/" == uploadsPath || strings.HasPrefix(pattern, uploadsPath)
}
