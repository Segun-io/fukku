package uploads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	tusd "github.com/tus/tusd/v2/pkg/handler"
	expslog "golang.org/x/exp/slog"

	"github.com/segun-io/fukku/internal/store"
)

// apiError is a refusal in the API's error shape (#5):
// {"error": {"code": "...", "message": "..."}}.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.code + ": " + e.message }

func (e *apiError) body() []byte {
	b, _ := json.Marshal(map[string]any{"error": map[string]string{"code": e.code, "message": e.message}})
	return append(b, '\n')
}

// tus returns e as an error tusd sends as it is, from inside a hook.
func (e *apiError) tus() tusd.Error {
	return tusd.Error{
		ErrorCode: e.code,
		Message:   e.message,
		HTTPResponse: tusd.HTTPResponse{
			StatusCode: e.status,
			Body:       string(e.body()),
			Header:     tusd.HTTPHeader{"Content-Type": "application/json", "Cache-Control": "no-store"},
		},
	}
}

func writeError(w http.ResponseWriter, e *apiError) {
	h := w.Header()
	h.Del("Content-Length")
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(e.status)
	_, _ = w.Write(e.body()) //nolint:gosec // JSON, sent as application/json with nosniff
}

func errBadRequest(msg string) *apiError { return &apiError{http.StatusBadRequest, "bad_request", msg} }
func errNotFound(msg string) *apiError   { return &apiError{http.StatusNotFound, "not_found", msg} }

// errStorage is the answer while a sentinel is missing or the intake root cannot be
// prepared. The cause is logged, never sent: it names paths.
func errStorage() *apiError {
	return &apiError{http.StatusServiceUnavailable, "storage_unavailable", "The upload storage is not available."}
}

func errDuplicate(name string) *apiError {
	return &apiError{http.StatusConflict, "duplicate_name", fmt.Sprintf("The job already has a file named %q.", name)}
}

func errJobNotUploading(s store.State) *apiError {
	return &apiError{http.StatusConflict, "job_not_uploading", fmt.Sprintf("The job is %s and takes no more uploads.", s)}
}

// interceptor passes a response through, calling onHeader just before the status line is
// written (to add headers), and rewrites tusd's own plain-text errors into the API's error
// shape. Unwrap keeps http.ResponseController working, which tusd uses for its read
// deadlines (upload.requestIdleTimeout).
type interceptor struct {
	w        http.ResponseWriter
	onHeader func(status int, h http.Header)
	status   int
	buf      *bytes.Buffer // a tusd error being rewritten
}

func newInterceptor(w http.ResponseWriter, onHeader func(int, http.Header)) *interceptor {
	return &interceptor{w: w, onHeader: onHeader}
}

func (i *interceptor) Header() http.Header { return i.w.Header() }

func (i *interceptor) Unwrap() http.ResponseWriter { return i.w }

func (i *interceptor) WriteHeader(status int) {
	if i.status != 0 {
		return
	}
	i.status = status
	if status >= 400 && !strings.HasPrefix(i.w.Header().Get("Content-Type"), "application/json") {
		i.buf = new(bytes.Buffer)
		return
	}
	if i.onHeader != nil {
		i.onHeader(status, i.w.Header())
	}
	i.w.WriteHeader(status)
}

func (i *interceptor) Write(b []byte) (int, error) {
	if i.status == 0 {
		i.WriteHeader(http.StatusOK)
	}
	if i.buf != nil {
		return i.buf.Write(b)
	}
	return i.w.Write(b)
}

// finish writes a rewritten error. Call it after the wrapped handler has returned. tusd's
// messages for client errors are fixed texts and are passed on; a server error may carry an
// OS or store error with paths in it, so it is logged and answered with a fixed message.
func (i *interceptor) finish(log *slog.Logger) {
	if i.buf == nil {
		return
	}
	text := strings.TrimSpace(i.buf.String())
	i.buf = nil
	tusCode, msg, ok := strings.Cut(text, ": ")
	if !ok {
		msg = http.StatusText(i.status)
	}
	if i.status >= 500 {
		log.Warn("upload_tus_error", "status", i.status, "error", text)
		msg = "Internal error."
	}
	writeError(i.w, &apiError{i.status, codeForTus(i.status, tusCode), msg})
}

// discard drops a buffered error, so the caller can answer instead. Nothing has been sent.
func (i *interceptor) discard() { i.buf = nil }

// codeForTus maps tusd's error codes to the API's codes.
func codeForTus(status int, tusCode string) string {
	switch tusCode {
	case "ERR_MISMATCHED_OFFSET":
		return "offset_mismatch"
	case "ERR_MAX_SIZE_EXCEEDED":
		return "file_too_large"
	case "ERR_UPLOAD_LOCKED", "ERR_LOCK_TIMEOUT":
		return "upload_busy"
	}
	switch {
	case status == http.StatusNotFound:
		return "not_found"
	case status == http.StatusRequestEntityTooLarge:
		return "too_large"
	case status == http.StatusServiceUnavailable:
		return "storage_unavailable"
	case status >= 500:
		return "internal"
	}
	return "bad_request"
}

// tusLogger gives tusd the process's log handler, from warning level up: tusd logs every
// request at info. tusd logs through golang.org/x/exp/slog, hence the adapter.
func tusLogger(log *slog.Logger) *expslog.Logger {
	return expslog.New(expHandler{log.Handler()}).With("component", "tusd")
}

type expHandler struct{ h slog.Handler }

func (e expHandler) Enabled(ctx context.Context, l expslog.Level) bool {
	return l >= expslog.LevelWarn && e.h.Enabled(ctx, slog.Level(l))
}

func (e expHandler) Handle(ctx context.Context, r expslog.Record) error {
	rec := slog.NewRecord(r.Time, slog.Level(r.Level), r.Message, r.PC)
	r.Attrs(func(a expslog.Attr) bool {
		rec.AddAttrs(slog.Any(a.Key, a.Value.Any()))
		return true
	})
	return e.h.Handle(ctx, rec)
}

func (e expHandler) WithAttrs(attrs []expslog.Attr) expslog.Handler {
	conv := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		conv = append(conv, slog.Any(a.Key, a.Value.Any()))
	}
	return expHandler{e.h.WithAttrs(conv)}
}

func (e expHandler) WithGroup(name string) expslog.Handler { return expHandler{e.h.WithGroup(name)} }
