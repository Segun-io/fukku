package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A JSON body above 1 MiB is refused with 413 too_large, through the whole chain.
func TestJSONBodyAboveLimit(t *testing.T) {
	h := handler(t, testConfig(), testUI)
	body := `{"name":"` + strings.Repeat("a", maxJSONBody) + `"}`
	r := fromProxy("POST", "/api/session", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	wantError(t, serve(h, r), http.StatusRequestEntityTooLarge, "too_large")
}

// echoJSON decodes a body as a handler would.
var echoJSON = func() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Name string `json:"name"`
		}
		if decodeJSON(w, r, &v) {
			writeJSON(w, http.StatusOK, v)
		}
	})
	return limitJSONBody(mux)
}()

// A chunked body (no declared length) fails while it is read.
func TestChunkedJSONBodyAboveLimit(t *testing.T) {
	body := `{"name":"` + strings.Repeat("a", maxJSONBody) + `"}`
	r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/jobs", io.MultiReader(strings.NewReader(body)))
	r.ContentLength = -1
	wantError(t, serve(echoJSON, r), http.StatusRequestEntityTooLarge, "too_large")
}

func TestJSONBodyWithinLimit(t *testing.T) {
	body := `{"name":"` + strings.Repeat("a", maxJSONBody-20) + `"}`
	w := serve(echoJSON, httptest.NewRequestWithContext(context.Background(), "POST", "/api/jobs", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}

func TestBadJSONBody(t *testing.T) {
	for _, body := range []string{"", "{", `{"name":1}`, `{"other":"x"}`, `{} {}`} {
		wantError(t, serve(echoJSON, httptest.NewRequestWithContext(context.Background(), "POST", "/api/jobs", strings.NewReader(body))), http.StatusBadRequest, "bad_request")
	}
}

// limitMux routes like the real server will once uploads (#10) and jobs (#11) are in, and
// records which handler ran and how many body bytes it could read.
type limitMux struct {
	http.Handler
	route string
	read  int64
}

func newLimitMux() *limitMux {
	m := &limitMux{}
	mux := http.NewServeMux()
	record := func(route string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			m.route = route
			n, err := io.Copy(io.Discard, r.Body)
			m.read = n
			if err != nil {
				writeError(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
	mux.HandleFunc("/api/uploads/", record("uploads"))
	mux.HandleFunc("POST /api/jobs/{id}/files", record("jobs"))
	mux.HandleFunc("POST /api/jobs/{id}", record("jobs"))
	mux.HandleFunc("/api/", apiNotFound)
	m.Handler = limitJSONBody(mux)
	return m
}

// bigRequest is a POST of three times the limit, with a declared length or chunked.
func bigRequest(method, target string, chunked bool) *http.Request {
	size := 3 * maxJSONBody
	var body io.Reader = bytes.NewReader(make([]byte, size))
	if chunked {
		body = io.MultiReader(body)
	}
	r := httptest.NewRequestWithContext(context.Background(), method, target, body)
	if chunked {
		r.ContentLength = -1
	}
	return r
}

// Uploads are not limited: tus chunks are far larger than 1 MiB.
func TestUploadBodiesAreNotLimited(t *testing.T) {
	for _, target := range []string{
		"/api/uploads/",
		"/api/uploads/0123456789abcdef0123456789abcdef",
		"/api/uploads/x%2F..%2F..%2Fjobs", // still the uploads route
	} {
		for _, chunked := range []bool{false, true} {
			m := newLimitMux()
			w := serve(m, bigRequest("PATCH", target, chunked))
			if w.Code != http.StatusNoContent || m.route != "uploads" || m.read != 3*maxJSONBody {
				t.Errorf("%s (chunked %v): status %d, route %q, read %d", target, chunked, w.Code, m.route, m.read)
			}
		}
	}
}

// Only the uploads route is exempt. Paths that look like uploads once cleaned or unescaped,
// but that the mux routes elsewhere (or redirects), stay limited.
func TestBodyLimitCannotBeEscapedByPath(t *testing.T) {
	for _, target := range []string{
		"/api/uploadsx",
		"/api/jobs/..%2Fuploads%2Fx/files",
		"/api/jobs/..%2Fuploads%2Fx",
		"/api/jobs/%2E%2E/uploads/x",
		"/api/jobs/%2e%2e%2Fuploads%2Fx/files",
		"/api/jobs/x%2F..%2F..%2Fuploads%2F/files",
		"/api/jobs/x%2F..%2F..%2Fuploads%2F",
		"/api/uploads/../jobs/x",
		"/api/jobs/../uploads/x",
	} {
		for _, chunked := range []bool{false, true} {
			m := newLimitMux()
			w := serve(m, bigRequest("POST", target, chunked))
			if m.route == "uploads" || m.read > maxJSONBody {
				t.Errorf("%s (chunked %v): reached %q and read %d bytes", target, chunked, m.route, m.read)
			}
			if w.Code < 300 {
				t.Errorf("%s (chunked %v): status %d", target, chunked, w.Code)
			}
		}
	}
	// The jobs route itself is reached through the escaped id, and limited there.
	m := newLimitMux()
	wantError(t, serve(m, bigRequest("POST", "/api/jobs/..%2Fuploads%2Fx/files", true)), http.StatusRequestEntityTooLarge, "too_large")
	if m.route != "jobs" {
		t.Fatalf("route %q", m.route)
	}
}

func TestIsUploadPattern(t *testing.T) {
	for pattern, want := range map[string]bool{
		"/api/uploads/":                 true,
		"/api/uploads":                  true,
		"PATCH /api/uploads/{id}":       true,
		"POST  /api/uploads/":           true,
		"HEAD example.com/api/uploads/": true,
		"":                              false,
		"/":                             false,
		"/api/":                         false,
		"/api/uploadsx":                 false,
		"POST /api/jobs/{id}/files":     false,
		"POST /api/jobs/uploads/":       false,
	} {
		if got := isUploadPattern(pattern); got != want {
			t.Errorf("%q: %v, want %v", pattern, got, want)
		}
	}
}
