// Package safehttp holds the HTTP client settings every client that carries a secret (an
// API key in a header) shares.
package safehttp

import (
	"errors"
	"net/http"
	"net/url"
)

// The clients send an API key in a custom header, which Go's default client keeps when it
// follows a redirect to another host, scheme or port. So a redirect is followed only when
// it stays on the origin (scheme, host and port) the request was made to, which keeps
// working a proxy that adds a trailing slash or moves a path inside the app; any other
// redirect fails the request before it is sent, and the key goes nowhere else.
var (
	// ErrRedirect: the URL redirects to another origin (a sign-in page of a proxy, say).
	ErrRedirect = errors.New("the URL redirects to another address; set the app's own address, including any URL base, as its url")
	// ErrRedirectHTTPS: the URL is http and redirects to https on the same host.
	ErrRedirectHTTPS = errors.New("the URL redirects from http to https; use the https URL as the url setting")
	errTooMany       = errors.New("the URL redirects too many times; set the app's own address as its url")
)

const maxRedirects = 10

// NoCrossOriginRedirect makes c follow same-origin redirects only, and returns c.
func NoCrossOriginRedirect(c *http.Client) *http.Client {
	c.CheckRedirect = checkRedirect
	return c
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errTooMany
	}
	first := via[0].URL
	switch {
	case sameOrigin(first, req.URL):
		return nil
	case first.Scheme == "http" && req.URL.Scheme == "https" && first.Hostname() == req.URL.Hostname():
		return ErrRedirectHTTPS
	}
	return ErrRedirect
}

func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && a.Hostname() == b.Hostname() && port(a) == port(b)
}

func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}
