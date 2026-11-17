package flows

import (
	"testing"

	"github.com/segun-io/fukku/internal/config"
)

// Fullwidth and other compatibility characters must not get past the token and episode
// checks, which compare ASCII forms.
func TestCheckLabelRefusesCompatibilityCharacters(t *testing.T) {
	cfg := config.Defaults().Versions
	for _, label := range []string{
		"１０８０ｐ",  // fullwidth 1080p
		"ＷＥＢＤＬ",  // fullwidth WEBDL
		"Ｒｅｍｕｘ",  // fullwidth Remux
		"Ｓ０２Ｅ０５", // fullwidth S02E05
		"2x０５",   // ASCII 2x then a fullwidth episode
		"ﬁlm",    // a ligature
	} {
		if _, p := checkLabel(label, cfg); p == nil || p.Reason != LabelInvalid {
			t.Errorf("%q: problem %+v, want %s", label, p, LabelInvalid)
		}
	}
	if _, p := checkLabel("Español", cfg); p != nil {
		t.Errorf("an accented label was refused: %+v", p)
	}
}
