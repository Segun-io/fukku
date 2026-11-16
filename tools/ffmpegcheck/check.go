package main

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// version is an FFmpeg release number. "7.1" (no patch) is 7.1.0.
type version struct{ major, minor, patch int }

func (v version) String() string {
	if v.patch == 0 {
		return fmt.Sprintf("%d.%d", v.major, v.minor)
	}
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

func (v version) less(o version) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

func (v version) sameLine(o version) bool { return v.major == o.major && v.minor == o.minor }

var versionRE = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?$`)

func parseVersion(s string) (version, error) {
	m := versionRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return version{}, fmt.Errorf("not a release version: %q", s)
	}
	v := version{}
	v.major, _ = strconv.Atoi(m[1])
	v.minor, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		v.patch, _ = strconv.Atoi(m[3])
	}
	return v, nil
}

var tarballRE = regexp.MustCompile(`ffmpeg-(\d+\.\d+(?:\.\d+)?)\.tar\.xz`)

// releasesFromIndex returns the release versions named by tarballs in ffmpeg.org's release
// index (an HTML directory listing), sorted ascending and without duplicates.
func releasesFromIndex(index string) []version {
	seen := map[version]bool{}
	var out []version
	for _, m := range tarballRE.FindAllStringSubmatch(index, -1) {
		v, err := parseVersion(m[1])
		if err != nil || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].less(out[j]) })
	return out
}

var pinRE = regexp.MustCompile(`(?m)^ARG FFMPEG_VERSION=(\S+)\s*$`)

// pinFromDockerfile reads the pinned version from the FFMPEG_VERSION build argument.
func pinFromDockerfile(dockerfile string) (version, error) {
	m := pinRE.FindStringSubmatch(dockerfile)
	if m == nil {
		return version{}, errors.New("no `ARG FFMPEG_VERSION=` line found")
	}
	return parseVersion(m[1])
}

// result is the outcome of comparing the pin with the releases.
type result struct {
	Pinned     version
	LatestLine version   // newest release in the pin's line (equal to Pinned when current)
	NewerLines []version // newest release of each later line, ascending
	Behind     bool
}

// evaluate compares the pin with the release list. Behind means a newer release exists in the
// pin's own line (an update to take); NewerLines lists later lines (a decision to make).
func evaluate(pinned version, releases []version) (result, error) {
	r := result{Pinned: pinned, LatestLine: pinned}
	found := false
	newest := map[[2]int]version{}
	for _, v := range releases {
		if v == pinned {
			found = true
		}
		switch {
		case v.sameLine(pinned):
			if r.LatestLine.less(v) {
				r.LatestLine = v
			}
		case pinned.less(v):
			k := [2]int{v.major, v.minor}
			if cur, ok := newest[k]; !ok || cur.less(v) {
				newest[k] = v
			}
		}
	}
	if !found {
		return r, fmt.Errorf("pinned version %s is not in the upstream release list", pinned)
	}
	for _, v := range newest {
		r.NewerLines = append(r.NewerLines, v)
	}
	sort.Slice(r.NewerLines, func(i, j int) bool { return r.NewerLines[i].less(r.NewerLines[j]) })
	r.Behind = r.Pinned.less(r.LatestLine)
	return r, nil
}
