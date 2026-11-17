package mediaserver_test

import (
	"testing"

	"github.com/segun-io/fukku/internal/mediaserver"
)

// #130: the item id is one URL value, whatever characters the media server sent.
func TestTitleLinkEscapesTheItemID(t *testing.T) {
	const tmpl = "https://media.example.com/web/#/details?id={itemId}"
	for id, want := range map[string]string{
		"abc123":       "abc123",
		"a&b=c":        "a%26b%3Dc",
		"a/../b":       "a%2F..%2Fb",
		"a#b?c":        "a%23b%3Fc",
		"a b+c":        "a%20b%2Bc",
		"x\"><script>": "x%22%3E%3Cscript%3E",
		"é":            "%C3%A9",
	} {
		if got := mediaserver.TitleLink(tmpl, id); got != "https://media.example.com/web/#/details?id="+want {
			t.Errorf("id %q: %q", id, got)
		}
	}
}
