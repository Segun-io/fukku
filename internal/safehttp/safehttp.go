// Package safehttp holds the HTTP client settings every client that carries a secret (an
// API key in a header) shares.
package safehttp

import (
	"errors"
	"net/http"
)

// ErrRedirect is what a request fails with when the server answers with a redirect. The
// clients send an API key in a custom header, which Go's default client keeps when it
// follows a redirect to another host, scheme or port, so none is followed: the key goes to
// the configured address and nowhere else. A redirect from a correctly configured address
// is not expected (the clients ask for exact paths); one means the URL setting is not the
// app's own address (a sign-in page of a proxy, http instead of https, a missing URL base).
var ErrRedirect = errors.New("the URL redirects; set the app's own address, including any URL base, as its url")

// NoRedirect makes c refuse every redirect with ErrRedirect, before the next request is
// sent, and returns c.
func NoRedirect(c *http.Client) *http.Client {
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrRedirect }
	return c
}
