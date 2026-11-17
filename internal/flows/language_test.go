package flows

import (
	"errors"
	"slices"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/store"
)

const probeProfile = 5

// withProfiles gives both titles the quality profile of probe C8: a minimum format score
// of min, and a profile the titles do not use.
func (e *env) withProfiles(min int) {
	ps := []arr.Profile{{ID: 1, Name: "Any", MinFormatScore: 100}, {ID: probeProfile, Name: "Probe", MinFormatScore: min}}
	e.movies.Profile, e.series.Profile = ps, ps
	e.movies.Library[0].QualityProfileID = probeProfile
	e.series.Library[0].QualityProfileID = probeProfile
}

// scored is a proposal item as the apps answered in probe B7-sonarr: the dub has the custom
// format "Not Original Language" at -10000, the original has none.
func scored(it arr.Row, lang arr.Language, score int, formats ...string) arr.Row {
	it.Languages, it.CustomFormatScore, it.CustomFormats = []arr.Language{lang}, score, formats
	return it
}

func TestLanguageWarningTable(t *testing.T) {
	const notOriginal = "Not Original Language"
	for _, c := range []struct {
		name    string
		min     int
		score   int
		formats []string
		warn    bool
	}{
		{"probe B7-sonarr dub: -10000 below 0", 0, -10000, []string{notOriginal}, true},
		{"probe B7-sonarr original: 0 at 0", 0, 0, nil, false},
		{"one below the minimum", 0, -1, nil, true},
		{"above the minimum", 0, 50, []string{"Some Format"}, false},
		{"negative minimum, score above it", -100, -10, nil, false},
		{"negative minimum, score below it", -100, -10000, []string{notOriginal}, true},
		{"positive minimum, no formats at all", 10, 0, nil, true},
	} {
		for _, app := range []string{"sonarr", "radarr"} {
			t.Run(app+"/"+c.name, func(t *testing.T) {
				e := newEnv(t)
				e.withProfiles(c.min)
				var p Proposal
				if app == "sonarr" {
					j := e.job(app, showID, vid("Some.Show.S01E01.mkv", epRuntime))
					e.series.Proposed[e.folder(j)] = []arr.Row{scored(e.item(j, "Some.Show.S01E01.mkv", webdl1080), spanish, c.score, c.formats...)}
					p = e.ready(j)
				} else {
					j := e.job(app, filmID, vid("Some Film (2001).mkv", 100*minute))
					e.movies.Proposed[e.folder(j)] = []arr.Row{scored(e.item(j, "Some Film (2001).mkv", webdl1080), spanish, c.score, c.formats...)}
					p = e.ready(j)
				}
				w := p.Rows[0].Warnings
				if !c.warn {
					wantNoLanguage(t, w)
					return
				}
				var n *Notice
				for i := range w {
					if w[i].Code == WarnLanguage {
						n = &w[i]
					}
				}
				if n == nil {
					t.Fatalf("no language warning: %+v", w)
				}
				if n.Score == nil || *n.Score != c.score || n.MinFormatScore == nil || *n.MinFormatScore != c.min ||
					!slices.Equal(n.CustomFormats, append([]string{}, c.formats...)) {
					t.Errorf("warning facts %+v", n)
				}
				if p.Rows[0].Importable {
					t.Errorf("a warned row is importable")
				}
			})
		}
	}
}

func wantNoLanguage(t *testing.T, w []Notice) {
	t.Helper()
	for _, n := range w {
		if n.Code == WarnLanguage {
			t.Errorf("unexpected language warning %+v", n)
		}
	}
}

// The fixture season (#46): one dubbed episode among originals gets the warning, alone.
func TestLanguageWarningOnTheDubbedEpisodeOnly(t *testing.T) {
	e := newEnv(t)
	e.withProfiles(0)
	names := []string{"Some.Show.S01E01.mkv", "Some.Show.S01E02.mkv", "Some.Show.S01E03.mkv"}
	j := e.job("sonarr", showID, vid(names[0], epRuntime), vid(names[1], epRuntime), vid(names[2], epRuntime))
	e.series.Proposed[e.folder(j)] = []arr.Row{
		scored(e.item(j, names[0], webdl1080), english, 0),
		scored(e.item(j, names[1], webdl1080), spanish, -10000, "Not Original Language"),
		scored(e.item(j, names[2], webdl1080), english, 0),
	}
	p := e.ready(j)
	for _, r := range p.Rows {
		if r.Name == names[1] {
			if !slices.Contains(codes(r.Warnings), WarnLanguage) {
				t.Errorf("the dub has no language warning: %+v", r.Warnings)
			}
			continue
		}
		wantNoLanguage(t, r.Warnings)
	}
}

func TestLanguageWarningSettingOff(t *testing.T) {
	e := newEnv(t)
	e.withProfiles(0)
	e.p.cfg.Match.LanguageWarning = false
	j := e.job("sonarr", showID, vid("Some.Show.S01E01.mkv", epRuntime))
	e.series.Proposed[e.folder(j)] = []arr.Row{scored(e.item(j, "Some.Show.S01E01.mkv", webdl1080), spanish, -10000, "Not Original Language")}
	wantNoLanguage(t, e.ready(j).Rows[0].Warnings)
}

// The warning is computed on every read, so it follows the profile.
func TestLanguageWarningFollowsTheProfile(t *testing.T) {
	e := newEnv(t)
	e.withProfiles(0)
	j := e.job("sonarr", showID, vid("Some.Show.S01E01.mkv", epRuntime))
	e.series.Proposed[e.folder(j)] = []arr.Row{scored(e.item(j, "Some.Show.S01E01.mkv", webdl1080), spanish, -5)}
	if p := e.ready(j); !slices.Contains(codes(p.Rows[0].Warnings), WarnLanguage) {
		t.Fatalf("no warning at minimum 0: %+v", p.Rows[0].Warnings)
	}
	e.withProfiles(-10)
	wantNoLanguage(t, e.view(e.jobNow(j)).Rows[0].Warnings)
}

func TestLanguageWarningUnknownProfileAddsNothing(t *testing.T) {
	e := newEnv(t)
	e.withProfiles(0)
	e.series.Library[0].QualityProfileID = 99
	j := e.job("sonarr", showID, vid("Some.Show.S01E01.mkv", epRuntime))
	e.series.Proposed[e.folder(j)] = []arr.Row{scored(e.item(j, "Some.Show.S01E01.mkv", webdl1080), spanish, -10000)}
	wantNoLanguage(t, e.ready(j).Rows[0].Warnings)
}

func TestLanguageWarningAppDown(t *testing.T) {
	e := newEnv(t)
	e.withProfiles(0)
	j := e.job("sonarr", showID, vid("Some.Show.S01E01.mkv", epRuntime))
	e.series.Proposed[e.folder(j)] = []arr.Row{scored(e.item(j, "Some.Show.S01E01.mkv", webdl1080), spanish, -10000)}
	e.ready(j)
	e.series.Err = map[string]error{"Profiles": errors.New("connection refused")}
	if _, err := e.p.View(e.ctx, e.jobNow(j), true); err == nil {
		t.Fatal("view succeeded with the app down")
	}
	if got := e.jobNow(j).State; got != store.AwaitingConfirmation {
		t.Errorf("job is %s", got)
	}
}
