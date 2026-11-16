// Package pathmap translates paths between how Fukku sees the file system and how another
// app (Radarr, Sonarr, the subtitle manager, the media server) sees it, using the
// paths.mappings.* settings.
package pathmap

import (
	"strings"

	"github.com/segun-io/fukku/internal/config"
)

// To maps a path as Fukku sees it to the path another app sees: the longest matching
// From prefix (whole path segments) is replaced by its To. No match: unchanged.
func To(m []config.PathMapping, path string) string {
	return translate(m, path, func(p config.PathMapping) (string, string) { return p.From, p.To })
}

// From is the reverse: a path as another app sees it to the path Fukku sees.
func From(m []config.PathMapping, path string) string {
	return translate(m, path, func(p config.PathMapping) (string, string) { return p.To, p.From })
}

func translate(m []config.PathMapping, path string, sides func(config.PathMapping) (from, to string)) string {
	best, bestTo, found := "", "", false
	for _, p := range m {
		from, to := sides(p)
		from = trimSlash(from)
		if !hasPrefix(path, from) {
			continue
		}
		if !found || len(from) > len(best) {
			best, bestTo, found = from, trimSlash(to), true
		}
	}
	if !found {
		return path
	}
	if out := bestTo + path[len(best):]; out != "" {
		return out
	}
	return "/"
}

// hasPrefix reports whether path is prefix or lies under it, on whole segments.
func hasPrefix(path, prefix string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	return len(path) == len(prefix) || path[len(prefix)] == '/'
}

// trimSlash drops trailing slashes, but keeps the root "/" as the empty prefix so that
// "/" maps every absolute path.
func trimSlash(p string) string { return strings.TrimRight(p, "/") }
