package config

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// VersionLabelPlaceholders are the placeholders versions.labelTemplate may use: the name of
// the upload's new audio language in that language ("Español") and its ISO 639-1 code ("es").
var VersionLabelPlaceholders = []string{"{languageName}", "{languageCode}"}

var placeholder = regexp.MustCompile(`\{[^{}]*\}`)

// validateVersions checks the versions group (decision #20). The root is needed only when
// versions are enabled; it must lie outside the library roots (Radarr must never see a
// version, probes B3e and B3g) and outside the intake root.
func validateVersions(p *problems, cfg Config) {
	v := cfg.Versions
	if strings.TrimSpace(v.LabelTemplate) == "" {
		p.add("versions.labelTemplate", "must not be empty")
	}
	for _, ph := range placeholder.FindAllString(v.LabelTemplate, -1) {
		if !slices.Contains(VersionLabelPlaceholders, ph) {
			p.add("versions.labelTemplate", "unknown placeholder %s (known: %s)", ph, strings.Join(VersionLabelPlaceholders, ", "))
		}
	}
	nonNegative(p, "versions.regroupInterval", int64(v.RegroupInterval))
	if v.Root == "" {
		if v.Enabled {
			p.add("versions.root", "must be set when versions.enabled is on: a folder outside Radarr's root folders, in a media server library")
		}
		return
	}
	if !filepath.IsAbs(v.Root) || filepath.Clean(v.Root) != v.Root || v.Root == "/" {
		p.add("versions.root", "must be a clean absolute path other than /, got %q", v.Root)
		return
	}
	for _, r := range cfg.Paths.LibraryRoots {
		if r = filepath.Clean(r); Overlaps(v.Root, r) {
			p.add("versions.root", "%s overlaps the library root %s: a version must stay outside Radarr's root folders (decision #20)", v.Root, r)
		}
	}
	if in := filepath.Clean(cfg.Paths.IntakeRoot); Overlaps(v.Root, in) {
		p.add("versions.root", "%s overlaps paths.intakeRoot %s", v.Root, in)
	}
}

// Overlaps reports whether one of two clean absolute paths is the other or inside it.
func Overlaps(a, b string) bool {
	in := func(x, y string) bool { return x == y || y == "/" || strings.HasPrefix(x, y+"/") }
	return in(a, b) || in(b, a)
}
