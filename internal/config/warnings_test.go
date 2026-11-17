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
			if got := warned(Warnings(cfg), "auth.libraryChangesRequireAdmin"); got != c.warn {
				t.Errorf("warned %v, want %v: %+v", got, c.warn, Warnings(cfg))
			}
		})
	}
}

func warned(ws []Warning, key string) bool {
	for _, w := range ws {
		if w.Key == key {
			return true
		}
	}
	return false
}

// #133: without server.publicBaseUrl the Origin check trusts the Host header.
func TestWarningsOriginFromHost(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(*Config)
		warn   bool
	}{
		{"the default", nil, true},
		{"a public base URL", func(c *Config) { c.Server.PublicBaseURL = "https://fukku.example.com" }, false},
		{"the origin check off (it has its own log line)", func(c *Config) { c.Auth.OriginCheck = false }, false},
		{"auth.mode none", func(c *Config) { c.Auth.Mode = "none" }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := Defaults()
			if c.mutate != nil {
				c.mutate(&cfg)
			}
			if got := warned(Warnings(cfg), "server.publicBaseUrl"); got != c.warn {
				t.Errorf("warned %v, want %v", got, c.warn)
			}
		})
	}
}

func TestWarningsWideTrustedProxiesOverride(t *testing.T) {
	cfg := Defaults()
	if warned(Warnings(cfg), "auth.allowWideTrustedProxies") {
		t.Error("warned with the default")
	}
	cfg.Auth.AllowWideTrustedProxies = true
	if !warned(Warnings(cfg), "auth.allowWideTrustedProxies") {
		t.Error("no warning with the override on")
	}
}
