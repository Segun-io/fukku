package web

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
)

// Changes to the library are admin-only by default (#116): through the API a user is
// refused with 403 admin_required, an admin (who can open any job) confirms, and with
// auth.libraryChangesRequireAdmin off the user confirms too. The proposal and JobDetail
// say beforehand what is admin-only.

type gateCase struct {
	name   string
	gate   bool
	groups string // of the user who confirms user1's job
	ok     bool
}

var gateCases = []gateCase{
	{"an admin", true, "users,admins", true},
	{"a user", true, "users", false},
	{"a user with the setting off", false, "users", true},
}

func gateOn(on bool) func(*config.Config) {
	return func(c *config.Config) { c.Auth.LibraryChangesRequireAdmin = on }
}

// wantGate checks a confirmation's answer: 202, or 403 admin_required with rows (none: no
// "rows") and the job still awaiting confirmation.
func wantGate(t *testing.T, st store.Store, c gateCase, j store.Job, status int, body string, rows ...string) {
	t.Helper()
	if c.ok {
		if status != http.StatusAccepted {
			t.Fatalf("%s: %d %s", c.name, status, body)
		}
		return
	}
	var b struct {
		Error struct {
			Code string   `json:"code"`
			Rows []string `json:"rows"`
		} `json:"error"`
	}
	if status != http.StatusForbidden || json.Unmarshal([]byte(body), &b) != nil || b.Error.Code != "admin_required" || !slices.Equal(b.Error.Rows, rows) {
		t.Fatalf("%s: %d %s, want 403 admin_required with rows %v", c.name, status, body, rows)
	}
	if j2, _ := st.Job(t.Context(), j.ID); j2.State != store.AwaitingConfirmation {
		t.Fatalf("a refused confirmation moved the job to %s", j2.State)
	}
}

func TestLibraryChangesOverrideThroughTheAPI(t *testing.T) {
	for _, c := range gateCases {
		t.Run(c.name, func(t *testing.T) {
			e := newConfirmEnvWith(t, gateOn(c.gate))
			j, rows := e.ready()
			rec := e.send("user1", c.groups, "GET", "/api/jobs/"+j.ID+"/proposal", "", false)
			gated := strings.Contains(rec.Body.String(), `"notOffered":[{"offer":"override","reason":"admin_required"}]`)
			if gated == c.ok || (c.ok && !strings.Contains(rec.Body.String(), `"notOffered":[]`)) {
				t.Errorf("proposal %s", rec.Body)
			}
			rec = e.send("user1", c.groups, "POST", "/api/jobs/"+j.ID+"/confirm", `{"overrides":["`+rows[2]+`"]}`, true)
			wantGate(t, e.st, c, j, rec.Code, rec.Body.String(), rows[2])
		})
	}
	// The admin confirms the user's job as it is.
	e := newConfirmEnvWith(t, gateOn(true))
	j, rows := e.ready()
	rec := e.send("admin1", "admins", "POST", "/api/jobs/"+j.ID+"/confirm", `{"overrides":["`+rows[2]+`"]}`, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("admin1: %d %s", rec.Code, rec.Body)
	}
}

func TestLibraryChangesSeasonDubThroughTheAPI(t *testing.T) {
	setup := func(t *testing.T, gate bool) (*confirmEnv, store.Job) {
		t.Helper()
		e := newConfirmEnvWith(t, func(c *config.Config) { gateOn(gate)(c); c.Subtitles.Provider = "bazarr" })
		for _, i := range []int{0, 2} {
			e.series.Eps[11][i].FileID = 900 + i
		}
		j, _ := e.ready()
		return e, j
	}
	for _, c := range gateCases {
		t.Run(c.name, func(t *testing.T) {
			e, j := setup(t, c.gate)
			rec := e.send("user1", c.groups, "GET", "/api/jobs/"+j.ID+"/proposal", "", false)
			want := `"offers":["replace","subtitlesOnly"]`
			if !c.ok {
				want = `"offers":["subtitlesOnly"],"seasons":[{"season":1,"episodes":3,"existing":2,"new":1}],"notOffered":[{"offer":"replace","reason":"admin_required"}`
			}
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("proposal %s, want %s", rec.Body, want)
			}
			rec = e.send("user1", c.groups, "POST", "/api/jobs/"+j.ID+"/confirm", `{"seasonDub":"replace"}`, true)
			wantGate(t, e.st, c, j, rec.Code, rec.Body.String())
		})
	}
	// subtitlesOnly imports no video: a user may choose it.
	e, j := setup(t, true)
	if rec := e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/confirm", `{"seasonDub":"subtitlesOnly"}`, true); rec.Code != http.StatusAccepted {
		t.Fatalf("subtitlesOnly: %d %s", rec.Code, rec.Body)
	}
}

func TestLibraryChangesMergeThroughTheAPI(t *testing.T) {
	for _, c := range gateCases {
		t.Run(c.name, func(t *testing.T) {
			e := newFlow2Env(t, func(cfg *config.Config) {
				gateOn(c.gate)(cfg)
				cfg.Auth.AdminGroup = "admins"
				cfg.Paths.IntakeRoot = t.TempDir()
				cfg.Paths.RequireSameFilesystem = "never"
			})
			j := e.previewed(e.readyJob())
			hdr := map[string]string{"Remote-Groups": c.groups}
			rec := e.callAs("user1", "GET", "/api/jobs/"+j.ID, "", hdr)
			var d struct {
				Flow2 struct {
					Offers     []string `json:"offers"`
					NotOffered []struct {
						Offer, Reason string
					} `json:"notOffered"`
				} `json:"flow2"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
				t.Fatal(err)
			}
			gated := slices.ContainsFunc(d.Flow2.NotOffered, func(n struct{ Offer, Reason string }) bool {
				return n.Offer == "merge" && n.Reason == "admin_required"
			})
			if gated == c.ok || slices.Contains(d.Flow2.Offers, "merge") != c.ok {
				t.Errorf("flow2 %s", rec.Body)
			}
			rec = e.callAs("user1", "POST", "/api/jobs/"+j.ID+"/confirm", `{"flow2":"merge","merge":{"audioTracks":[1]}}`, hdr)
			wantGate(t, e.st, c, j, rec.Code, rec.Body.String())
		})
	}
}

// Season-dub version is admin-only too: a user is refused with 403 before anything else is
// judged; for an admin, or with the setting off, the confirmation goes on to the usual
// checks (here versions are off, so version is not offered: 400). The placement itself is
// tested in internal/flows (TestLibraryChangesEpisodeVersions).
func TestLibraryChangesSeasonDubVersionThroughTheAPI(t *testing.T) {
	for _, c := range gateCases {
		t.Run(c.name, func(t *testing.T) {
			e := newConfirmEnvWith(t, func(cfg *config.Config) { gateOn(c.gate)(cfg); cfg.Subtitles.Provider = "bazarr" })
			e.series.Eps[11][0].FileID = 900
			j, _ := e.ready()
			rec := e.send("user1", c.groups, "POST", "/api/jobs/"+j.ID+"/confirm", `{"seasonDub":"version","version":{"label":"Español"}}`, true)
			if c.ok {
				e.want(rec, http.StatusBadRequest, "bad_request")
				return
			}
			wantGate(t, e.st, c, j, rec.Code, rec.Body.String())
		})
	}
}

// A retry through the API cannot replace a file the title got after the confirmation
// (#116): user1 confirms new episodes, the import fails title_busy while Sonarr downloads
// one of them, the download lands, user1 retries, and the job goes back to the match with
// nothing sent to Sonarr.
func TestRetryThroughTheAPIDoesNotReplaceANewFile(t *testing.T) {
	intake := t.TempDir()
	e := newConfirmEnvWith(t, func(cfg *config.Config) {
		gateOn(true)(cfg)
		cfg.Paths.IntakeRoot = intake
		cfg.Paths.RequireSameFilesystem = "never"
		cfg.Paths.Mappings.Sonarr = []config.PathMapping{{From: intake, To: "/data/intake"}}
	})
	j, rows := e.ready()
	dir := intake + "/jobs/" + j.ID + "/files"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"S01E01.mkv", "S01E02.mkv", "S01E03.mkv"} {
		if err := os.WriteFile(dir+"/"+name, make([]byte, 10), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if rec := e.send("user1", "users", "PATCH", "/api/jobs/"+j.ID+"/proposal/rows/"+rows[2], `{"skip":true}`, true); rec.Code != http.StatusOK {
		t.Fatalf("skip: %d %s", rec.Code, rec.Body)
	}
	if rec := e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/confirm", `{}`, true); rec.Code != http.StatusAccepted {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	step := func() store.Job {
		t.Helper()
		cur, err := e.st.Job(t.Context(), j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.eng.Importing(t.Context(), cur); err != nil {
			t.Fatal(err)
		}
		cur, _ = e.st.Job(t.Context(), j.ID)
		return cur
	}
	e.series.Queue = map[int]bool{11: true}
	if cur := step(); cur.State != store.Failed || cur.ErrorCode != "title_busy" {
		t.Fatalf("busy: %s %s (%s)", cur.State, cur.ErrorCode, cur.ErrorText)
	}
	e.series.Queue = nil
	e.series.Eps[11][0].FileID = 900 // the download landed
	if rec := e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/retry", "", true); rec.Code >= 300 {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	if cur := step(); cur.State != store.Probed || len(e.series.Imports) != 0 {
		t.Fatalf("after the retry: job %s (%s), %d imports", cur.State, cur.ErrorText, len(e.series.Imports))
	}
}

// A film's second version: versions.root is missing here, so a confirmation that passes the
// gate meets storage_unavailable; a user does not get that far.
func TestLibraryChangesFilmVersionThroughTheAPI(t *testing.T) {
	for _, c := range gateCases {
		t.Run(c.name, func(t *testing.T) {
			e := newFlow2Env(t, func(cfg *config.Config) {
				versionsOn(t.TempDir() + "/missing")(cfg)
				gateOn(c.gate)(cfg)
				cfg.Auth.AdminGroup = "admins"
			})
			j := e.readyPAL()
			hdr := map[string]string{"Remote-Groups": c.groups}
			rec := e.callAs("user1", "GET", "/api/jobs/"+j.ID, "", hdr)
			if got := strings.Contains(rec.Body.String(), `{"offer":"version","reason":"admin_required"}`); got == c.ok {
				t.Errorf("flow2 %s", rec.Body)
			}
			rec = e.callAs("user1", "POST", "/api/jobs/"+j.ID+"/confirm", `{"flow2":"version"}`, hdr)
			if c.ok {
				if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"storage_unavailable"`) {
					t.Fatalf("%s: %d %s", c.name, rec.Code, rec.Body)
				}
				return
			}
			wantGate(t, e.st, c, j, rec.Code, rec.Body.String())
		})
	}
}
