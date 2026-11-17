package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/segun-io/fukku/internal/flows"
)

// The refusals of episodes' versions (#58) name their rows, as the other row refusals do; a
// film's version refusal has no rows and keeps the plain error shape.
func TestConfirmErrorOfEpisodeVersions(t *testing.T) {
	s := &Server{}
	for _, c := range []struct {
		err  *flows.Error
		rows []string
	}{
		{&flows.Error{Code: flows.CodeVersionUpgrade, Message: "m", Rows: []string{"r1"}}, []string{"r1"}},
		{&flows.Error{Code: flows.CodeVersionUnavailable, Message: "m", Rows: []string{"r2", "r3"}}, []string{"r2", "r3"}},
		{&flows.Error{Code: flows.CodeVersionExists, Message: "m", Rows: []string{"r4"}}, []string{"r4"}},
		{&flows.Error{Code: flows.CodeVersionExists, Message: "m"}, nil},
	} {
		rec := httptest.NewRecorder()
		s.confirmError(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/jobs/x/confirm", nil), c.err)
		var body struct {
			Error struct {
				Code string
				Rows []string
			}
		}
		if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error.Code != c.err.Code || !slices.Equal(body.Error.Rows, c.rows) {
			t.Errorf("%s: %d %s", c.err.Code, rec.Code, rec.Body)
		}
	}
}
