package flows

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
)

// Fixes from the review of #89.

func TestRadarrVerdict(t *testing.T) {
	unknown := arr.Parsed{OK: true, Quality: arr.Quality{Name: "Unknown"}, Source: "unknown", Revision: 1}
	if p := radarrVerdict(unknown, unknown, "x"); p != nil {
		t.Errorf("Unknown: %+v", p)
	}
	// A name Radarr cannot parse at all is not a verdict: refused, as could not be checked.
	if p := radarrVerdict(arr.Parsed{}, unknown, "x"); p == nil || p.Reason != LabelUnchecked {
		t.Errorf("nothing parsed: %+v", p)
	}
	for name, parsed := range map[string]arr.Parsed{
		"quality":    {OK: true, Quality: arr.Quality{ID: 2, Name: "DVD"}, Source: "dvd", Revision: 1},
		"resolution": {OK: true, Resolution: 720, Revision: 1},
		"source":     {OK: true, Source: "webdl", Revision: 1},
		"proper":     {OK: true, Revision: 2},
		"real":       {OK: true, Revision: 1, Real: 1},
		"repack":     {OK: true, Revision: 1, Repack: true},
	} {
		if p := radarrVerdict(parsed, unknown, "x"); p == nil || p.Reason != LabelQuality {
			t.Errorf("%s: %+v", name, p)
		}
	}
	// A film whose own name reads as a quality ("Cam (2018)" reads as CAM, probe of #89):
	// a label that adds nothing is fine, one that adds a quality is not.
	cam := arr.Parsed{OK: true, Quality: arr.Quality{ID: 25, Name: "CAM"}, Source: "cam", Revision: 1}
	if p := radarrVerdict(cam, cam, "Cam (2018) - Español"); p != nil {
		t.Errorf("the film's own quality word: %+v", p)
	}
	bluray := arr.Parsed{OK: true, Quality: arr.Quality{ID: 6, Name: "Bluray-720p"}, Source: "bluray", Resolution: 720, Revision: 1}
	if p := radarrVerdict(bluray, cam, "Cam (2018) - Bluray"); p == nil {
		t.Error("a quality the label adds to a film named like one")
	}
}

// The confirmation asks Radarr for the label's name and for the folder's name alone.
func TestVersionVerdictAgainstTheFolder(t *testing.T) {
	x := newVersionEnv(t)
	cam := arr.Parsed{OK: true, Quality: arr.Quality{ID: 25, Name: "CAM"}, Source: "cam", Revision: 1}
	x.movies.Parsed = map[string]arr.Parsed{
		"Some Film (2001)":             cam,
		"Some Film (2001) - Español":   cam,
		"Some Film (2001) - Zzz":       {}, // Radarr cannot parse it
		"Some Film (2001) - Telesync":  {OK: true, Quality: arr.Quality{ID: 26, Name: "TELESYNC"}, Source: "telesync", Revision: 1},
		"Some Film (2001) - REAL copy": {OK: true, Quality: cam.Quality, Source: "cam", Revision: 1, Real: 1},
	}
	j := x.versionJob(palMs, "spa")
	for label, want := range map[string]string{"Telesync": "Radarr reads", "Zzz": "cannot read", "REAL copy": "Radarr reads"} {
		_, err := x.confirmVersion(j, label)
		x.refused(j, err, CodeVersionLabel)
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", label, err)
		}
	}
	if _, err := x.confirmVersion(j, ""); err != nil {
		t.Fatalf("the label adds nothing to what the film's name reads: %v", err)
	}
	if !slices.Contains(x.movies.ParseCalls, "Some Film (2001)") {
		t.Errorf("parse calls %q, want the folder's name too", x.movies.ParseCalls)
	}
}

// Invisible format characters (Unicode Cf) are refused, and so is a name over 255 bytes in
// the offer itself.
func TestVersionLabelCharacters(t *testing.T) {
	cfg := config.Defaults().Versions
	for _, r := range []rune{0x200b, 0x00ad, 0x2060, 0x200d} {
		label := "Espa" + string(r) + "ñol"
		if _, p := checkLabel(label, cfg); p == nil || p.Reason != LabelInvalid {
			t.Errorf("U+%04X: %+v", r, p)
		}
	}
	x := newVersionEnv(t)
	x.cfg.Versions.LabelTemplate = "{languageName} " + strings.Repeat("x", 55) // fits the label, not the name
	long := strings.Repeat("Long Title ", 17) + "(2001)"
	x.movies.Library[0].Path = appLibRoot + "/" + long
	f := *libFile()
	f.Path = appLibRoot + "/" + long + "/" + long + ".mkv"
	x.rebuild(x.movies, x.series)
	j := x.versionJob(palMs, "spa")
	rec := newFlow2(&f, x.cfg)
	rec.Library.Probe = libProbe(filmMinutes)
	tg, _ := x.st.Target(x.ctx, j.ID)
	tg.Options, _ = withOption(tg.Options, "flow2", rec)
	if err := x.st.SetTarget(x.ctx, j.ID, tg); err != nil {
		t.Fatal(err)
	}
	v := x.view(j)
	if v.Version == nil || v.Version.Problem == nil || v.Version.Problem.Reason != LabelTooLong || v.Version.Name != "" {
		t.Fatalf("offer %+v", v.Version)
	}
}

// Only Radarr's films get a version this way (episodes are #58's).
func TestVersionRadarrOnly(t *testing.T) {
	x := newVersionEnv(t)
	j := x.versionJob(palMs, "spa")
	tg, _ := x.st.Target(x.ctx, j.ID)
	s, err := x.p.edit(x.ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.t.App = AppSonarr
	rec, _ := Flow2Of(tg)
	_, err = x.eng.confirmVersion(x.ctx, "user1", "", s, rec, ConfirmRequest{Flow2: OfferVersion})
	if Code(err) != CodeVersionUnavailable {
		t.Errorf("a sonarr target: %v", err)
	}
}

// A record of the same name from an earlier job is never handed to another one: refused at
// the confirmation, and in the importing step if it appeared after the confirmation.
func TestVersionRecordOfAnotherJob(t *testing.T) {
	x := newVersionEnv(t)
	j := x.versionJob(palMs, "spa")
	dest := x.dest("Some Film (2001) - Español.mkv")
	old := store.Version{JobID: "earlier", App: AppRadarr, TitleID: filmID, Path: dest, Status: store.VersionMissing}
	if _, err := x.st.PutVersion(x.ctx, old); err != nil {
		t.Fatal(err)
	}
	_, err := x.confirmVersion(j, "")
	x.refused(j, err, CodeVersionExists)

	y := newVersionEnv(t)
	k := y.versionJob(palMs, "spa")
	c, err := y.confirmVersion(k, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := y.st.PutVersion(y.ctx, store.Version{JobID: "earlier", App: AppRadarr, TitleID: filmID, Path: y.dest("Some Film (2001) - Español.mkv"), Status: store.VersionGrouped}); err != nil {
		t.Fatal(err)
	}
	k, err = y.step(y.eng, c.Job)
	if err != nil {
		t.Fatal(err)
	}
	if k.State != store.Failed || k.ErrorCode != CodeVersionExists {
		t.Fatalf("job %s %s (%s)", k.State, k.ErrorCode, k.ErrorText)
	}
	recs, _ := y.st.AllVersions(y.ctx)
	if len(recs) != 1 || recs[0].JobID != "earlier" || recs[0].Status != store.VersionGrouped {
		t.Errorf("the earlier job's record changed: %+v", recs)
	}
}

// A link that fails leaves no record behind: the record of the attempt is removed.
func TestVersionFailedLinkLeavesNoRecord(t *testing.T) {
	x := newVersionEnv(t)
	j := x.versionJob(palMs, "spa")
	c, err := x.confirmVersion(j, "")
	if err != nil {
		t.Fatal(err)
	}
	// The film's folder in versions.root is a file: the link cannot be made.
	if err := os.WriteFile(x.versions+"/Some Film (2001)", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if j, err = x.step(x.eng, c.Job); err != nil {
		t.Fatal(err)
	}
	if j.State != store.Failed || j.ErrorCode != CodeVersionFailed {
		t.Fatalf("job %s %s", j.State, j.ErrorCode)
	}
	if recs, _ := x.st.AllVersions(x.ctx); len(recs) != 0 {
		t.Errorf("records left %+v", recs)
	}
	if got := x.actions(j); len(got) != 1 {
		t.Errorf("audit %v", got)
	}
}

// The record and version.linked are one write: a crash before it leaves the record placing,
// and the repeat writes both (exactly one entry).
func TestVersionLinkedAuditWithTheRecord(t *testing.T) {
	x := newVersionEnv(t)
	j := x.versionJob(palMs, "spa")
	c, err := x.confirmVersion(j, "")
	if err != nil {
		t.Fatal(err)
	}
	x.eng.st = &failLinked{Store: x.st}
	if err := x.eng.Importing(x.ctx, x.jobNow(c.Job)); err == nil {
		t.Fatal("the failed write was not reported")
	}
	recs, _ := x.st.VersionsOf(x.ctx, j.ID)
	if len(recs) != 1 || recs[0].Status != store.VersionPlacing {
		t.Fatalf("records after the crash %+v", recs)
	}
	if got := x.actions(j); len(got) != 1 {
		t.Fatalf("audit after the crash %v", got)
	}
	x.eng.st = x.st
	if j, err = x.step(x.eng, c.Job); err != nil || j.State != store.Imported {
		t.Fatalf("repeat: %s %v", j.State, err)
	}
	recs, _ = x.st.VersionsOf(x.ctx, j.ID)
	if len(recs) != 1 || recs[0].Status != store.VersionLinked {
		t.Errorf("records %+v", recs)
	}
	if got := x.actions(j); len(got) != 2 || got[1] != "version.linked" {
		t.Errorf("audit %v", got)
	}
}

// failLinked fails the write of a linked record, as a crash at that point would.
type failLinked struct{ store.Store }

func (f *failLinked) PutVersion(ctx context.Context, v store.Version, audit ...store.AuditEntry) (store.Version, error) {
	if v.Status == store.VersionLinked {
		return store.Version{}, errors.New("crash")
	}
	return f.Store.PutVersion(ctx, v, audit...)
}
