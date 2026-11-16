package config

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/language"
)

// MaxTrackName is the longest merge.trackName (and track name of a merge task), in bytes.
const MaxTrackName = 256

// CanonicalLanguageTag reports whether tag is a BCP 47 language tag in its canonical form,
// and returns that form. The merge (#54) gives the tag to mkvmerge and then verifies that
// the added tracks carry it, so only tags mkvmerge keeps as they are can be used: no
// underscores ("en_US", which mkvmerge refuses), no deprecated or extended forms that it
// rewrites ("iw" becomes "he", "zh-yue" "yue", "sgn-BE-FR" "sfb"), and the usual letter
// case ("es-419", not "ES-419"). canonical is "" when tag does not parse at all, or when
// it is a regional sign language ("sgn-BR", which mkvmerge stores as "bzs" but the tag
// library keeps): such a language must be named by its own code.
func CanonicalLanguageTag(tag string) (canonical string, ok bool) {
	if tag == "" || len(tag) > 64 {
		return "", false
	}
	t, err := language.Parse(tag)
	if err != nil {
		return "", false
	}
	canonical = t.String()
	if strings.HasPrefix(canonical, "sgn-") { // the tag library keeps it, mkvmerge rewrites it
		return "", false
	}
	return canonical, canonical == tag && !strings.Contains(tag, "_")
}

// ValidTrackName reports whether name can be a track name: UTF-8 text of at most
// MaxTrackName bytes, without control characters. Empty is allowed (no name).
func ValidTrackName(name string) bool {
	return len(name) <= MaxTrackName && utf8.ValidString(name) && !strings.ContainsFunc(name, unicode.IsControl)
}

// validateMergeTrack checks what the merge gives every added track, and how it checks them.
func validateMergeTrack(p *problems, mg Merge) {
	if mg.CoverageTolerance <= 0 || mg.CoverageTolerance.D() > time.Minute {
		p.add("merge.coverageTolerance", "must be more than 0 and at most 1m")
	}
	if mg.LanguageTag != "" { // empty is refused with the other merge settings
		if c, ok := CanonicalLanguageTag(mg.LanguageTag); !ok && c != "" {
			p.add("merge.languageTag", "%q is not in canonical BCP 47 form, which mkvmerge keeps: use %q", mg.LanguageTag, c)
		} else if !ok {
			p.add("merge.languageTag", "%q is not a BCP 47 tag such as es-419 (a sign language by its own code, e.g. bzs, not sgn-BR)", mg.LanguageTag)
		}
	}
	if !ValidTrackName(mg.TrackName) {
		p.add("merge.trackName", "must be UTF-8 text of at most %d bytes, without control characters", MaxTrackName)
	}
}
