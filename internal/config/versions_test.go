package config

import (
	"errors"
	"strings"
	"testing"
)

func versionProblems(cfg Config) string {
	var p problems
	validateVersions(&p, cfg)
	return errors.Join(p...).Error()
}

func TestValidateVersions(t *testing.T) {
	base := Defaults()
	base.Paths.IntakeRoot = "/data/intake"
	base.Paths.LibraryRoots = []string{"/data/media/movies", "/data/media/tv"}

	ok := base
	ok.Versions.Enabled, ok.Versions.Root = true, "/data/media/versions"
	var p problems
	if validateVersions(&p, ok); len(p) != 0 {
		t.Fatalf("a good configuration: %v", p)
	}
	if validateVersions(&p, base); len(p) != 0 { // off, no root: fine
		t.Fatalf("versions off: %v", p)
	}

	for name, tc := range map[string]struct {
		tweak func(*Config)
		want  string
	}{
		"enabled without a root": {func(c *Config) { c.Versions.Enabled = true }, "versions.root: must be set"},
		"relative root":          {func(c *Config) { c.Versions.Root = "versions" }, "clean absolute path"},
		"unclean root":           {func(c *Config) { c.Versions.Root = "/data/media/versions/" }, "clean absolute path"},
		"inside a library root":  {func(c *Config) { c.Versions.Root = "/data/media/movies/Versions" }, "overlaps the library root /data/media/movies"},
		"holding a library root": {func(c *Config) { c.Versions.Root = "/data/media" }, "overlaps the library root"},
		"inside the intake root": {func(c *Config) { c.Versions.Root = "/data/intake/v" }, "overlaps paths.intakeRoot"},
		"unknown placeholder":    {func(c *Config) { c.Versions.LabelTemplate = "{language}" }, "unknown placeholder {language}"},
		"negative interval":      {func(c *Config) { c.Versions.RegroupInterval = -1 }, "versions.regroupInterval"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			tc.tweak(&cfg)
			if got := versionProblems(cfg); !strings.Contains(got, tc.want) {
				t.Errorf("problems %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOverlaps(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"/a/b", "/a/b", true}, {"/a/b/c", "/a/b", true}, {"/a", "/a/b", true},
		{"/a/bc", "/a/b", false}, {"/x", "/y", false}, {"/x", "/", true},
	} {
		if got := Overlaps(tc.a, tc.b); got != tc.want {
			t.Errorf("Overlaps(%q, %q) = %t", tc.a, tc.b, got)
		}
	}
}
