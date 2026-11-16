// Package auth authenticates requests (auth.mode) and carries the user in the context.
package auth

import "context"

// User is the signed-in user of a request.
type User struct {
	Name   string
	Email  string
	Groups []string
	Admin  bool // member of auth.adminGroup, or auth.allUsersAreAdmins
}

type ctxKey struct{}

// WithUser returns ctx carrying u. The middleware uses it; tests use it to fake a user.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// FromContext returns the user of the request. ok is false only on routes outside the
// middleware (/healthz, /readyz).
func FromContext(ctx context.Context) (u User, ok bool) {
	u, ok = ctx.Value(ctxKey{}).(User)
	return u, ok
}
