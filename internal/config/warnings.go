package config

// Warning is a valid configuration that probably does not do what the operator wants: the
// process starts, logs it (config_warning) and fukku check-config prints it.
type Warning struct {
	Key     string // the setting it is about
	Message string
}

// Warnings returns the warnings of a valid configuration, in a stable order.
func Warnings(cfg Config) []Warning {
	var out []Warning
	a := cfg.Auth
	// #116: only admins may change the library, and nobody can be one, so nobody can override
	// a rejection, replace episodes, merge or keep a second version.
	if a.LibraryChangesRequireAdmin && a.Mode != "none" && a.AdminGroup == "" && !a.AllUsersAreAdmins {
		out = append(out, Warning{
			Key: "auth.libraryChangesRequireAdmin",
			Message: "only admins may replace or change library files, and no user can be an admin: " +
				"set auth.adminGroup (or auth.allUsersAreAdmins), or turn auth.libraryChangesRequireAdmin off",
		})
	}
	// #133: without server.publicBaseUrl the Origin check compares the Origin's host with
	// the request's Host, which a page that rebinds DNS controls.
	if a.Mode == "proxyHeaders" && a.OriginCheck && cfg.Server.PublicBaseURL == "" {
		out = append(out, Warning{
			Key: "server.publicBaseUrl",
			Message: "empty: the Origin check trusts the request's Host header, which a DNS-rebinding page " +
				"controls; set server.publicBaseUrl to the address users open",
		})
	}
	if a.AllowWideTrustedProxies {
		out = append(out, Warning{
			Key:     "auth.allowWideTrustedProxies",
			Message: "on: auth.trustedProxies may hold a very wide range, which weakens the check that a request came from the proxy",
		})
	}
	return out
}
