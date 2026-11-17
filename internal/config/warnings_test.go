package config

import "testing"

func TestWarningsNoAdminForLibraryChanges(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(*Config)
		warn   bool
	}{
		{"the default: nobody can be an admin", nil, true},
		{"an admin group", func(c *Config) { c.Auth.AdminGroup = "media-admins" }, false},
		{"every user an admin", func(c *Config) { c.Auth.AllUsersAreAdmins = true }, false},
		{"the setting off", func(c *Config) { c.Auth.LibraryChangesRequireAdmin = false }, false},
		{"auth.mode none: the development user is an admin", func(c *Config) { c.Auth.Mode = "none" }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := Defaults()
			if c.mutate != nil {
				c.mutate(&cfg)
			}
			w := Warnings(cfg)
			if (len(w) == 1 && w[0].Key == "auth.libraryChangesRequireAdmin") != c.warn || (!c.warn && len(w) != 0) {
				t.Errorf("warnings %+v", w)
			}
		})
	}
}
