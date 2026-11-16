package safename

import (
	"errors"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func TestClean(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
		err  error
	}{
		{"plain", "Show - S01E01.mkv", 200, "Show - S01E01.mkv", nil},
		{"spaces trimmed", "  film.mkv \u00a0", 200, "film.mkv", nil},
		{"inner spaces kept", "a  b.srt", 200, "a  b.srt", nil},
		{"nfd to nfc", "Poke\u0301mon.srt", 200, "Pok\u00e9mon.srt", nil},
		{"nfc unchanged", "Pok\u00e9mon.srt", 200, "Pok\u00e9mon.srt", nil},
		{"newline", "bad\nname.srt", 200, "badname.srt", nil},
		{"tab", "tab\tname.srt", 200, "tabname.srt", nil},
		{"escape", "esc\x1b[31m.srt", 200, "esc[31m.srt", nil},
		{"nul", "nul\x00.srt", 200, "nul.srt", nil},
		{"del and c1", "a\x7fb\u0085c.srt", 200, "abc.srt", nil},
		{"bidi override", "film\u202egpj.mkv", 200, "filmgpj.mkv", nil},
		{"bidi isolate and marks", "\u2066a\u2069\u200e\u200f.srt", 200, "a.srt", nil},
		{"line separator", "a\u2028b.srt", 200, "ab.srt", nil},
		{"bom", "\ufefffilm.mkv", 200, "film.mkv", nil},
		{"control blocks composition", "e\x00\u0301.srt", 200, "\u00e9.srt", nil},
		{"invalid utf-8", "a\xffb.srt", 200, "a\ufffdb.srt", nil},
		{"zero-width joiner kept", "a\u200db.srt", 200, "a\u200db.srt", nil},
		{"japanese", "フック.mkv", 200, "フック.mkv", nil},
		{"colon not at start", "Film: Part 2.mkv", 200, "Film: Part 2.mkv", nil},
		{"hidden file", ".mkv", 200, ".mkv", nil},
		{"exactly max", strings.Repeat("n", 196) + ".mkv", 200, strings.Repeat("n", 196) + ".mkv", nil},
		{"max counts bytes", strings.Repeat("é", 100), 200, strings.Repeat("é", 100), nil},

		{"empty", "", 200, "", ErrEmpty},
		{"only spaces", "   ", 200, "", ErrEmpty},
		{"only controls", "\x00\x01\n", 200, "", ErrEmpty},
		{"dot", ".", 200, "", ErrSeparator},
		{"dot dot", "..", 200, "", ErrSeparator},
		{"dot dot with spaces", " .. ", 200, "", ErrSeparator},
		{"dot dot with control", ".\x00.", 200, "", ErrSeparator},
		{"slash", "a/b.mkv", 200, "", ErrSeparator},
		{"backslash", `a\b.mkv`, 200, "", ErrSeparator},
		{"drive letter", "C:evil.mkv", 200, "", ErrSeparator},
		{"drive letter lower", "c:", 200, "", ErrSeparator},
		{"drive letter after cleaning", "\x01D:evil.mkv", 200, "", ErrSeparator},
		{"too long", strings.Repeat("n", 251) + ".mkv", 200, "", ErrTooLong},
		{"too long in bytes", strings.Repeat("é", 101), 200, "", ErrTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Clean(tt.in, tt.max)
			if !errors.Is(err, tt.err) || (tt.err == nil) != (err == nil) {
				t.Fatalf("Clean(%q) error %v, want %v", tt.in, err, tt.err)
			}
			if got != tt.want {
				t.Errorf("Clean(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestHasDriveLetter(t *testing.T) {
	for s, want := range map[string]bool{
		"C:": true, "z:/x": true, "C": false, "1:": false, ":C": false, "": false, "CC:": false,
	} {
		if got := HasDriveLetter(s); got != want {
			t.Errorf("HasDriveLetter(%q) = %v", s, got)
		}
	}
}

// FuzzClean checks the properties every accepted name has. Its seed corpus runs as part of
// go test; run `go test -fuzz FuzzClean ./internal/safename` to search further.
func FuzzClean(f *testing.F) {
	for _, s := range []string{
		"Show - S01E01.mkv", "Poke\u0301mon.srt", "bad\nname.srt", "nul\x00.srt", " .. ", "..",
		"C:evil", `a\b`, "a/b", "\xff\xfe", "\u202eexe.mkv", "e\x00\u0301", "\u0301\u0301", " \u0301 ",
		"\u0958", "\u1100\u1161\u11a8", strings.Repeat("é", 120), "\ufeff", "",
	} {
		f.Add(s, 200)
	}
	f.Add("abc", 1)
	f.Fuzz(func(t *testing.T, in string, maxBytes int) {
		got, err := Clean(in, maxBytes)
		if err != nil {
			if got != "" {
				t.Fatalf("Clean(%q) returned %q with error %v", in, got, err)
			}
			if !errors.Is(err, ErrEmpty) && !errors.Is(err, ErrSeparator) && !errors.Is(err, ErrTooLong) {
				t.Fatalf("Clean(%q): unexpected error %v", in, err)
			}
			return
		}
		switch {
		case got == "" || got == "." || got == "..":
			t.Fatalf("Clean(%q) = %q", in, got)
		case len(got) > maxBytes:
			t.Fatalf("Clean(%q) = %q, longer than %d bytes", in, got, maxBytes)
		case !utf8.ValidString(got):
			t.Fatalf("Clean(%q) = %q, not valid UTF-8", in, got)
		case !norm.NFC.IsNormalString(got):
			t.Fatalf("Clean(%q) = %q, not NFC", in, got)
		case strings.ContainsAny(got, `/\`) || HasDriveLetter(got):
			t.Fatalf("Clean(%q) = %q, has a separator or drive letter", in, got)
		case strings.IndexFunc(got, IsControl) >= 0:
			t.Fatalf("Clean(%q) = %q, has a control character", in, got)
		case strings.TrimFunc(got, unicode.IsSpace) != got:
			t.Fatalf("Clean(%q) = %q, has surrounding space", in, got)
		}
		again, err := Clean(got, maxBytes)
		if err != nil || again != got {
			t.Fatalf("Clean is not idempotent: %q → %q → %q, %v", in, got, again, err)
		}
	})
}
