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
	return out
}
