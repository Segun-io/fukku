// Package testenv talks to the local integration environment (test/env): Radarr, Sonarr,
// Bazarr and Jellyfin started from pinned images, and the fake indexer built from this
// repository, with fixed fake API keys. It is used by the
// seeder and the behaviour probes; it must never be pointed at a real instance.
package testenv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Env is the environment as seen from the tools container.
type Env struct {
	Radarr, Sonarr *Arr
	Bazarr         *Client
	BazarrKey      string // the test Bazarr's API key, for clients that are not testenv.Client
	Jellyfin       *Jellyfin
	Indexer        *Indexer
	JellyfinDB     string // path of jellyfin.db
	Data           string // the shared library filesystem, /data
	Results        string // where probes write their results
}

// FromEnv reads FUKKU_TEST_* variables (set by test/env/compose.yaml for the tools service).
func FromEnv() (*Env, error) {
	get := func(k string) (string, error) {
		v := os.Getenv(k)
		if v == "" {
			return "", fmt.Errorf("%s is not set: run this inside the test environment (make probes)", k)
		}
		return v, nil
	}
	vals := map[string]string{}
	var errs []error
	for _, k := range []string{"RADARR_URL", "RADARR_KEY", "SONARR_URL", "SONARR_KEY", "BAZARR_URL", "BAZARR_KEY",
		"JELLYFIN_URL", "JELLYFIN_KEY", "JELLYFIN_DB", "INDEXER_URL", "INDEXER_KEY", "DATA", "RESULTS"} {
		v, err := get("FUKKU_TEST_" + k)
		errs = append(errs, err)
		vals[k] = v
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	for _, k := range []string{"RADARR_URL", "SONARR_URL", "BAZARR_URL", "JELLYFIN_URL", "INDEXER_URL"} {
		if err := mustBeLocal(vals[k]); err != nil {
			return nil, err
		}
	}
	return &Env{
		Radarr:     &Arr{Client: newClient(vals["RADARR_URL"], "X-Api-Key", vals["RADARR_KEY"]), Name: "radarr"},
		Sonarr:     &Arr{Client: newClient(vals["SONARR_URL"], "X-Api-Key", vals["SONARR_KEY"]), Name: "sonarr"},
		Bazarr:     newClient(vals["BAZARR_URL"], "X-API-KEY", vals["BAZARR_KEY"]),
		BazarrKey:  vals["BAZARR_KEY"],
		Jellyfin:   &Jellyfin{Client: newClient(vals["JELLYFIN_URL"], "Authorization", `MediaBrowser Token="`+vals["JELLYFIN_KEY"]+`"`), Key: vals["JELLYFIN_KEY"]},
		Indexer:    &Indexer{Client: newClient(vals["INDEXER_URL"], "X-Api-Key", vals["INDEXER_KEY"]), Key: vals["INDEXER_KEY"]},
		JellyfinDB: vals["JELLYFIN_DB"],
		Data:       vals["DATA"],
		Results:    vals["RESULTS"],
	}, nil
}

// mustBeLocal refuses anything but the compose service names, so a stray variable cannot
// point the probes (which create and delete titles) at a real instance.
func mustBeLocal(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	switch u.Hostname() {
	case "radarr", "sonarr", "bazarr", "jellyfin", "indexer", "127.0.0.1", "localhost":
		return nil
	}
	return fmt.Errorf("refusing %s: the test tools only talk to the local test environment", raw)
}

// Client is a small JSON HTTP client with one authentication header.
type Client struct {
	Base       string
	authHeader string
	authValue  string
	http       *http.Client
}

// NewAnonymous returns a client without authentication (for Jellyfin's startup wizard).
func NewAnonymous(base string) *Client {
	if err := mustBeLocal(base); err != nil {
		panic(err)
	}
	return newClient(base, "", "")
}

func newClient(base, header, value string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), authHeader: header, authValue: value, http: &http.Client{Timeout: 60 * time.Second}}
}

// HTTPError is a non-2xx response.
type HTTPError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *HTTPError) Error() string {
	body := e.Body
	if len(body) > 600 {
		body = body[:600] + "…"
	}
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, body)
}

// StatusOf returns the HTTP status of err, or 0.
func StatusOf(err error) int {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

// Do sends body as JSON (unless it is an io.Reader with contentType set) and decodes the
// response into out when out is not nil.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	return c.DoRaw(ctx, method, path, "", body, out, nil)
}

// DoRaw is Do with an explicit content type and extra headers.
func (c *Client) DoRaw(ctx context.Context, method, path, contentType string, body, out any, headers map[string]string) error {
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case io.Reader:
		r = b
	default:
		data, err := json.Marshal(b)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, r)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if c.authValue != "" {
		req.Header.Set(c.authHeader, c.authValue)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &HTTPError{Method: method, Path: path, Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decode: %w", method, path, err)
		}
	}
	return nil
}

// WaitFor calls check every interval until it returns true or an error, or timeout passes.
func WaitFor(ctx context.Context, timeout, interval time.Duration, what string, check func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		ok, err := check()
		if ok && err == nil {
			return nil
		}
		last = err
		if time.Now().After(deadline) {
			if last != nil {
				return fmt.Errorf("timed out after %s waiting for %s: %w", timeout, what, last)
			}
			return fmt.Errorf("timed out after %s waiting for %s", timeout, what)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Q builds a query string.
func Q(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return "?" + v.Encode()
}
