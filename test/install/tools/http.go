package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// api is a small JSON client with fixed headers, for the apps' own APIs.
type api struct {
	base    string
	headers map[string]string
	http    *http.Client
}

func newAPI(base string, headers map[string]string) *api {
	return &api{base: strings.TrimRight(base, "/"), headers: headers, http: &http.Client{Timeout: 60 * time.Second}}
}

// do sends the request (body as JSON when not nil) and decodes a JSON answer into out when
// given. A status that is not 2xx is an error.
func (a *api) do(ctx context.Context, method, path string, body, out any) error {
	status, raw, err := a.raw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("%s %s: %d %s", method, path, status, truncate(string(raw), 400))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return nil
}

func (a *api) raw(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range a.headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	return res.StatusCode, raw, err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// waitFor calls check every two seconds until it succeeds or the time is up.
func waitFor(ctx context.Context, what string, timeout time.Duration, check func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := check()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		time.Sleep(2 * time.Second)
	}
}

type obj = map[string]any
