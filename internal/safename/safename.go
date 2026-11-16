// Package safename cleans file names that come from users and archives.
package safename

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var (
	ErrEmpty     = errors.New("safename: empty name")
	ErrSeparator = errors.New("safename: path separator, drive letter or dot segment")
	ErrTooLong   = errors.New("safename: name too long")
)

// Clean returns one safe path element: NFC-normalised, control characters removed,
// surrounding spaces trimmed. It refuses names that are empty after cleaning, "." or "..",
// names with "/" or "\" or a drive letter, and names longer than maxBytes.
//
// Invalid UTF-8 is replaced with U+FFFD first, so the result is always valid UTF-8 and
// survives a JSON round trip unchanged. "Control characters" are the C0 and C1 controls,
// the Unicode line and paragraph separators, the byte-order mark and the bidirectional
// formatting characters (which can make "evil.exe" display as a different name). The
// returned errors wrap ErrEmpty, ErrSeparator or ErrTooLong; test them with errors.Is.
func Clean(name string, maxBytes int) (string, error) {
	s := strings.ToValidUTF8(name, string(utf8.RuneError))
	s = strings.Map(func(r rune) rune {
		if IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = norm.NFC.String(s)
	s = strings.TrimFunc(s, unicode.IsSpace)

	switch {
	case s == "":
		return "", ErrEmpty
	case s == "." || s == "..":
		return "", fmt.Errorf("%w: dot segment", ErrSeparator)
	case strings.ContainsAny(s, `/\`):
		return "", fmt.Errorf("%w: path separator", ErrSeparator)
	case HasDriveLetter(s):
		return "", fmt.Errorf("%w: drive letter", ErrSeparator)
	case len(s) > maxBytes:
		return "", fmt.Errorf("%w: %d bytes, at most %d", ErrTooLong, len(s), maxBytes)
	}
	return s, nil
}

// IsControl reports whether Clean removes r.
func IsControl(r rune) bool {
	switch {
	case unicode.IsControl(r): // C0, DEL, C1
		return true
	case r == '\u2028' || r == '\u2029': // line and paragraph separators
		return true
	case r == '\uFEFF': // byte-order mark, zero-width no-break space
		return true
	case unicode.Is(unicode.Bidi_Control, r): // LRM, RLM, ALM, embeddings, overrides, isolates
		return true
	}
	return false
}

// HasDriveLetter reports whether s starts with a Windows drive letter such as "C:".
func HasDriveLetter(s string) bool {
	return len(s) >= 2 && s[1] == ':' && ('a' <= s[0] && s[0] <= 'z' || 'A' <= s[0] && s[0] <= 'Z')
}
