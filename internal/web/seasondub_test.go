package web

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
)

// A dub of a season already in the library (#57), through the whole chain: the proposal
// carries the question, the confirmation without a choice is refused with the rows, and each
// answer is audited. S01E01 and S01E03 have a file already; S01E03 is refused by the app as
// "not an upgrade".
func TestConfirmSeasonDub(t *testing.T) {
	setup := func(t *testing.T) (*confirmEnv, store.Job, []string) {
		t.Helper()
		e := newConfirmEnvWith(t, func(c *config.Config) { c.Subtitles.Provider = "bazarr" })
		for _, i := range []int{0, 2} {
			e.series.Eps[11][i].FileID = 900 + i
		}
		j, rows := e.ready()
		return e, j, rows
	}
	audited := func(t *testing.T, e *confirmEnv, j store.Job) map[string]store.AuditEntry {
		t.Helper()
		log, _ := e.st.AuditLog(t.Context(), j.ID)
		out := map[string]store.AuditEntry{}
		for _, a := range log {
			out[a.Action] = a
		}
		return out
	}

	t.Run("replace", func(t *testing.T) {
		e, j, rows := setup(t)
		path := "/api/jobs/" + j.ID + "/confirm"
		rec := e.send("user1", "users", "GET", "/api/jobs/"+j.ID+"/proposal", "", false)
		if !strings.Contains(rec.Body.String(), `"seasonDub":{"policy":"ask","ask":true,"offers":["replace","subtitlesOnly"],"seasons":[{"season":1,"episodes":3,"existing":2}]}`) {
			t.Errorf("proposal JSON %s", rec.Body)
		}

		rec = e.send("user1", "users", "POST", path, `{"overrides":[]}`, true)
		e.want(rec, http.StatusConflict, "season_dub_choice")
		var refusal struct {
			Error struct{ Rows []string }
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil || !slices.Equal(refusal.Error.Rows, []string{rows[0], rows[2]}) {
			t.Fatalf("body %s", rec.Body)
		}
		e.want(e.send("user1", "users", "POST", path, `{"seasonDub":"nonsense"}`, true), http.StatusBadRequest, "bad_request")
		e.want(e.send("user1", "users", "POST", path, `{"seasonDub":"merge"}`, true), http.StatusBadRequest, "bad_request")
		if log, _ := e.st.AuditLog(t.Context(), j.ID); len(log) != 0 {
			t.Fatalf("audit entries for refusals: %+v", log)
		}

		// The choice answers the app's refusal: no override is listed.
		if rec = e.send("user1", "users", "POST", path, `{"seasonDub":"replace"}`, true); rec.Code != http.StatusAccepted {
			t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
		}
		log := audited(t, e, j)
		if a := log["season.replaced"]; a.Actor != "user1" || a.Detail["season"] != "1" || a.Detail["rows"] != rows[0]+","+rows[2] {
			t.Errorf("season.replaced %+v", a)
		}
		if a := log["rejection.overridden"]; a.Detail["row"] != rows[2] {
			t.Errorf("rejection.overridden %+v", a)
		}
		stored, _ := e.st.Rows(t.Context(), j.ID)
		for _, r := range stored {
			if r.Skip {
				t.Errorf("row %s is skipped", r.ID)
			}
		}
	})

	t.Run("subtitlesOnly", func(t *testing.T) {
		e, j, _ := setup(t)
		path := "/api/jobs/" + j.ID + "/confirm"
		if rec := e.send("user1", "users", "POST", path, `{"seasonDub":"subtitlesOnly"}`, true); rec.Code != http.StatusAccepted {
			t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
		}
		if a, ok := audited(t, e, j)["season.subtitles_only"]; !ok || a.Detail["season"] != "1" {
			t.Errorf("audit %+v", a)
		}
		stored, _ := e.st.Rows(t.Context(), j.ID)
		for _, r := range stored {
			if !r.Skip || r.Result != store.ResultSkipped {
				t.Errorf("row %s: skip %v result %q", r.ID, r.Skip, r.Result)
			}
		}
		rec := e.send("user1", "users", "GET", "/api/jobs/"+j.ID, "", false)
		if !strings.Contains(rec.Body.String(), `"state":"importing"`) {
			t.Errorf("detail %s", rec.Body)
		}
	})

	t.Run("a fixed policy skips the question", func(t *testing.T) {
		e := newConfirmEnvWith(t, func(c *config.Config) { c.SeasonDub.Policy = "replace" })
		e.series.Eps[11][0].FileID = 900
		j, _ := e.ready()
		path := "/api/jobs/" + j.ID + "/confirm"
		rec := e.send("user1", "users", "GET", "/api/jobs/"+j.ID+"/proposal", "", false)
		if !strings.Contains(rec.Body.String(), `"decided":"replace"`) || !strings.Contains(rec.Body.String(), `"ask":false`) {
			t.Errorf("proposal %s", rec.Body)
		}
		e.want(e.send("user1", "users", "POST", path, `{"seasonDub":"subtitlesOnly"}`, true), http.StatusBadRequest, "bad_request")
	})
}
