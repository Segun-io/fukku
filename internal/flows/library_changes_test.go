package flows

import (
	"errors"
	"slices"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
)

// Changes to the library are admin-only by default (#116): for each action, an admin may
// confirm it, a user is refused with admin_required and nothing changes, and with
// auth.libraryChangesRequireAdmin off a user may confirm it too.

// who is one of the three cases of each action.
type who struct {
	name  string
	admin bool
	gate  bool // auth.libraryChangesRequireAdmin
	ok    bool // the confirmation goes through
}

var everyone = []who{
	{"an admin", true, true, true},
	{"a user", false, true, false},
	{"a user with the setting off", false, false, true},
}

func gate(on bool) func(*config.Config) {
	return func(c *config.Config) { c.Auth.LibraryChangesRequireAdmin = on }
}

// judged checks the outcome of one confirmation: through, or refused with admin_required
// and the job left as it was, with no audit entry.
func judged(t *testing.T, x *env, w who, j store.Job, err error) {
	t.Helper()
	if w.ok {
		if err != nil {
			t.Fatalf("%s: %v", w.name, err)
		}
		if s := x.jobNow(j).State; s != store.Importing {
			t.Fatalf("%s: the job is %s", w.name, s)
		}
		return
	}
	if Code(err) != CodeAdminRequired {
		t.Fatalf("%s: %v, want admin_required", w.name, err)
	}
	if s := x.jobNow(j).State; s != store.AwaitingConfirmation {
		t.Fatalf("a refused confirmation moved the job to %s", s)
	}
	if log, _ := x.st.AuditLog(x.ctx, j.ID); len(log) != 0 {
		t.Fatalf("audit entries for a refusal: %+v", log)
	}
}

func TestLibraryChangesOverride(t *testing.T) {
	for _, w := range everyone {
		t.Run(w.name, func(t *testing.T) {
			x := newImportEnv(t)
			x.cfg.Auth.LibraryChangesRequireAdmin = w.gate
			x.rebuild(x.movies, x.series)
			j := x.job("sonarr", showID, vid("Show.S01E01.mkv", epRuntime), vid("Show.S01E02.mkv", epRuntime))
			x.onDisk(j)
			x.series.Proposed[x.folder(j)] = []arr.Row{x.item(j, "Show.S01E01.mkv", webdl1080), x.item(j, "Show.S01E02.mkv", webdl1080)}
			x.series.Verdicts = map[string][]arr.Rejection{x.appPath(j, "Show.S01E02.mkv"): {{Reason: "Not an upgrade for existing episode file(s)", Permanent: true}}}
			x.ready(j)
			rows := x.rows(j)

			p, err := x.p.View(x.ctx, j, w.admin)
			if err != nil {
				t.Fatal(err)
			}
			want := []NotOffered{}
			if !w.ok {
				want = []NotOffered{{Offer: OfferOverride, Reason: ReasonAdminRequired}}
			}
			if !slices.Equal(p.NotOffered, want) {
				t.Errorf("notOffered %+v, want %+v", p.NotOffered, want)
			}
			// Without the override, every user hears needs_override first.
			if _, err := x.eng.Confirm(x.ctx, "user1", "", w.admin, j, ConfirmRequest{}); Code(err) != CodeNeedsOverride {
				t.Fatalf("without the override: %v", err)
			}
			_, err = x.eng.Confirm(x.ctx, "user1", "", w.admin, j, ConfirmRequest{Overrides: []string{rows[1].ID}})
			judged(t, x.env, w, j, err)
			if e := (*Error)(nil); errors.As(err, &e) && !slices.Equal(e.Rows, []string{rows[1].ID}) {
				t.Errorf("rows %v", e.Rows)
			}
		})
	}
	// The row left out, a user imports the rest.
	x := newImportEnv(t)
	j := x.job("sonarr", showID, vid("Show.S01E01.mkv", epRuntime), vid("Show.S01E02.mkv", epRuntime))
	x.onDisk(j)
	x.series.Proposed[x.folder(j)] = []arr.Row{x.item(j, "Show.S01E01.mkv", webdl1080), x.item(j, "Show.S01E02.mkv", webdl1080)}
	x.series.Verdicts = map[string][]arr.Rejection{x.appPath(j, "Show.S01E02.mkv"): {{Reason: "Not an upgrade for existing episode file(s)", Permanent: true}}}
	x.ready(j)
	rows := x.rows(j)
	x.edit(j, rows[1].ID, Edit{Skip: ptr(true)})
	if _, err := x.eng.Confirm(x.ctx, "user1", "", false, j, ConfirmRequest{}); err != nil {
		t.Fatalf("a new episode by a user: %v", err)
	}
}

// The language warning (#39) is Fukku's own, on a new file: every user may override it.
func TestLibraryChangesLanguageOverride(t *testing.T) {
	for _, w := range everyone {
		w.ok = true
		t.Run(w.name, func(t *testing.T) {
			x := newImportEnv(t)
			x.cfg.Auth.LibraryChangesRequireAdmin = w.gate
			x.rebuild(x.movies, x.series)
			x.withProfiles(100)
			j := x.filmJob()
			v := x.view(j)
			if len(v.Rows) != 1 || !slices.ContainsFunc(v.Rows[0].Warnings, func(n Notice) bool { return n.Code == WarnLanguage }) {
				t.Fatalf("rows %+v", v.Rows)
			}
			_, err := x.eng.Confirm(x.ctx, "user1", "", w.admin, j, ConfirmRequest{Overrides: []string{v.Rows[0].ID}})
			judged(t, x.env, w, j, err)
		})
	}
}

func TestLibraryChangesSeasonDubReplace(t *testing.T) {
	for _, w := range everyone {
		t.Run(w.name, func(t *testing.T) {
			x := dubEnv(t, gate(w.gate), 101, 102)
			j := x.dubJob()
			p, err := x.p.View(x.ctx, j, w.admin)
			if err != nil {
				t.Fatal(err)
			}
			sd := p.SeasonDub
			wantOffers := []string{PolicyReplace, PolicySubtitlesOnly}
			if !w.ok {
				wantOffers = []string{PolicySubtitlesOnly}
			}
			if sd == nil || !slices.Equal(sd.Offers, wantOffers) || !sd.Ask {
				t.Fatalf("question %+v", sd)
			}
			if got := slices.Contains(sd.NotOffered, NotOffered{PolicyReplace, ReasonAdminRequired}); got == w.ok {
				t.Errorf("notOffered %+v", sd.NotOffered)
			}
			_, err = x.eng.Confirm(x.ctx, "user1", "", w.admin, j, ConfirmRequest{SeasonDub: PolicyReplace})
			judged(t, x.env, w, j, err)
		})
	}
}

// subtitlesOnly imports no video: every user may choose it.
func TestLibraryChangesSubtitlesOnlyIsOpen(t *testing.T) {
	x := dubEnv(t, nil, 101, 102)
	j := x.dubJob()
	c, err := x.eng.Confirm(x.ctx, "user1", "", false, j, ConfirmRequest{SeasonDub: PolicySubtitlesOnly})
	if err != nil {
		t.Fatal(err)
	}
	if c.SeasonDub != PolicySubtitlesOnly || c.Job.State != store.Importing {
		t.Fatalf("confirmation %+v", c)
	}
}

// A fixed replace is not decided for a user: the rows that have a file stay skipped, and the
// new episodes import.
func TestLibraryChangesFixedReplaceLeavesUsersExistingRowsOut(t *testing.T) {
	x := dubEnv(t, func(c *config.Config) { c.SeasonDub.Policy = PolicyReplace }, 101, 102)
	j := x.dubJob()
	rows := x.rows(j)
	p, err := x.p.View(x.ctx, j, false)
	if err != nil {
		t.Fatal(err)
	}
	if sd := p.SeasonDub; sd == nil || sd.Decided != "" || sd.Ask || !slices.Contains(sd.NotOffered, NotOffered{PolicyReplace, ReasonAdminRequired}) {
		t.Fatalf("question %+v", sd)
	}
	if _, err := x.eng.Confirm(x.ctx, "user1", "", false, j, ConfirmRequest{SeasonDub: PolicyReplace}); Code(err) != CodeAdminRequired {
		t.Fatalf("replace asked for: %v", err)
	}
	c, err := x.eng.Confirm(x.ctx, "user1", "", false, j, ConfirmRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if c.SeasonDub != "" || !slices.Equal(c.Rows, []string{rows[2].ID}) {
		t.Fatalf("confirmation %+v", c)
	}
}

func TestLibraryChangesEpisodeVersions(t *testing.T) {
	for _, w := range everyone {
		t.Run(w.name, func(t *testing.T) {
			x := newEpvEnv(t, gate(w.gate))
			j := x.epvJob()
			p, err := x.p.View(x.ctx, j, w.admin)
			if err != nil {
				t.Fatal(err)
			}
			sd := p.SeasonDub
			if sd == nil || slices.Contains(sd.Offers, PolicyVersion) != w.ok || (sd.Version != nil) != w.ok {
				t.Fatalf("question %+v", sd)
			}
			if got := slices.Contains(sd.NotOffered, NotOffered{PolicyVersion, ReasonAdminRequired}); got == w.ok {
				t.Errorf("notOffered %+v", sd.NotOffered)
			}
			_, err = x.eng.Confirm(x.ctx, "user1", "", w.admin, j, ConfirmRequest{SeasonDub: PolicyVersion})
			judged(t, x.env, w, j, err)
		})
	}
}

func TestLibraryChangesMerge(t *testing.T) {
	for _, w := range everyone {
		t.Run(w.name, func(t *testing.T) {
			x := newMergeEnvWith(t, gate(w.gate))
			j := x.readyJob()
			_, err := x.eng.Confirm(x.ctx, "user1", "", w.admin, x.jobNow(j), mergeReq(nil))
			judged(t, x.env, w, j, err)
		})
	}
}

func TestLibraryChangesFilmVersion(t *testing.T) {
	for _, w := range everyone {
		t.Run(w.name, func(t *testing.T) {
			x := newVersionEnv(t)
			x.p.cfg.Auth.LibraryChangesRequireAdmin = w.gate
			j := x.versionJob(palMs, "spa")
			_, err := x.eng.Confirm(x.ctx, "user1", "", w.admin, j, ConfirmRequest{Flow2: OfferVersion})
			judged(t, x.env, w, j, err)
		})
	}
}

// JobDetail's flow2 as a user who may not change the library sees it.
func TestFlow2ViewForUser(t *testing.T) {
	v := &Flow2View{
		Offers:     []string{OfferMerge, OfferVersion},
		NotOffered: []NotOffered{},
		Version:    &VersionOffer{Label: "Español"},
	}
	if got := v.ForUser(true); got != v {
		t.Errorf("allowed: %+v", got)
	}
	got := v.ForUser(false)
	if len(got.Offers) != 0 || got.Version != nil || !slices.Equal(got.NotOffered, []NotOffered{
		{OfferMerge, ReasonAdminRequired}, {OfferVersion, ReasonAdminRequired},
	}) {
		t.Errorf("not allowed: %+v", got)
	}
	if len(v.Offers) != 2 || v.Version == nil || len(v.NotOffered) != 0 {
		t.Errorf("the view itself changed: %+v", v)
	}
	// What is not offered for another reason keeps it.
	w := &Flow2View{Offers: []string{OfferVersion}, NotOffered: []NotOffered{{OfferMerge, ReasonRuntimeDiffers}}}
	if got := w.ForUser(false); !slices.Equal(got.NotOffered, []NotOffered{{OfferMerge, ReasonRuntimeDiffers}, {OfferVersion, ReasonAdminRequired}}) {
		t.Errorf("not allowed: %+v", got.NotOffered)
	}
	var none *Flow2View
	if none.ForUser(false) != nil {
		t.Error("nil is not nil")
	}
	if !LibraryChangesAllowed(config.Defaults(), true) || LibraryChangesAllowed(config.Defaults(), false) {
		t.Error("the default needs an admin")
	}
}
