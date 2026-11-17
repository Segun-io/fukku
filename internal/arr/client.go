package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/safehttp"
)

const defaultTimeout = 30 * time.Second

// ErrUnreachable is in the chain of an error when the request did not reach the app or
// got no answer (connection refused, name not found, connection reset). A timeout is
// context.DeadlineExceeded instead; an answer with an error status is an *Error.
var ErrUnreachable = errors.New("app unreachable")

// Error is a non-2xx answer from the app. It carries the status and the app's own message;
// it never carries the API key.
type Error struct {
	App, Method, Path string
	Status            int
	Message           string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s: %s %s: %d %s", e.App, e.Method, e.Path, e.Status, http.StatusText(e.Status))
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// StatusOf returns the HTTP status of an *Error in err's chain, or 0.
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// client is the HTTP client both apps share.
type client struct {
	app     string // radarr or sonarr, for messages
	base    string
	key     string
	timeout time.Duration
	http    *http.Client
}

func newClient(app string, cfg config.Arr) *client {
	t := cfg.RequestTimeout.D()
	if t <= 0 {
		t = defaultTimeout
	}
	return &client{
		app:     app,
		base:    strings.TrimRight(cfg.URL, "/"),
		key:     cfg.APIKey.Reveal(),
		timeout: t,
		http:    safehttp.NoCrossOriginRedirect(&http.Client{}),
	}
}

// do sends one request to /api/v3<path> and decodes the JSON answer into out (when not nil).
// The request is bounded by the configured request timeout.
func (c *client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	if c.base == "" {
		return fmt.Errorf("%s: no url configured", c.app)
	}
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%s: %s %s: encode request: %w", c.app, method, path, err)
		}
		rdr = bytes.NewReader(data)
	}
	target := c.base + "/api/v3" + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	rctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, method, target, rdr)
	if err != nil {
		return fmt.Errorf("%s: %s %s: %s", c.app, method, path, c.scrub(err.Error()))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Api-Key", c.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	data, status, err := c.send(req)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			err = ctx.Err()
		case errors.Is(rctx.Err(), context.DeadlineExceeded):
			err = fmt.Errorf("timed out after %s: %w", c.timeout, context.DeadlineExceeded)
		default:
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			err = fmt.Errorf("%w: %s", ErrUnreachable, c.scrub(err.Error()))
		}
		slog.Debug("arr request failed", "app", c.app, "method", method, "path", path, "elapsed", time.Since(start))
		return fmt.Errorf("%s: %s %s: %w", c.app, method, path, err)
	}
	slog.Debug("arr request", "app", c.app, "method", method, "path", path, "status", status, "elapsed", time.Since(start))
	if status < 200 || status > 299 {
		return &Error{App: c.app, Method: method, Path: path, Status: status, Message: c.scrub(errorMessage(data))}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s: %s %s: decode answer: %w", c.app, method, path, err)
		}
	}
	return nil
}

func (c *client) send(req *http.Request) ([]byte, int, error) {
	resp, err := c.http.Do(req) //nolint:gosec // the URL is the operator's own setting
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return data, resp.StatusCode, err
}

// scrub removes the API key from text that may echo it.
func (c *client) scrub(s string) string {
	if c.key == "" {
		return s
	}
	return strings.ReplaceAll(s, c.key, "***")
}

func (c *client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

func (c *client) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

func (c *client) put(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPut, path, nil, body, out)
}

// errorMessage extracts the app's message from an error body: {"message": ...}, a list of
// validation failures [{"errorMessage": ...}], or plain text.
func errorMessage(data []byte) string {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return ""
	}
	type msg struct {
		Message      string `json:"message"`
		ErrorMessage string `json:"errorMessage"`
	}
	var obj msg
	if json.Unmarshal(data, &obj) == nil && (obj.Message != "" || obj.ErrorMessage != "") {
		return firstNonEmpty(obj.Message, obj.ErrorMessage)
	}
	var list []msg
	if json.Unmarshal(data, &list) == nil && len(list) > 0 {
		var parts []string
		for _, e := range list {
			if m := firstNonEmpty(e.ErrorMessage, e.Message); m != "" {
				parts = append(parts, m)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "; ")
		}
	}
	s := string(data)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
