package flows

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/store"
)

// What the confirmation UI (#44) needs from the engine: subtitle choices, the monitoring
// outcome, the language warning as an override, and file names on the import's rows.

func (x *ienv) subtitleFile(j store.Job) store.File {
	x.t.Helper()
	files, err := x.st.Files(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	for _, f := range files {
		if f.Kind == store.KindSubtitle {
			return f
		}
	}
	x.t.Fatal("no subtitle file")
	return store.File{}
}

// The confirmation stores the subtitle choices where the subtitles step reads them (the
// target's options, by file id), next to what the match stored; it refuses a choice for a
// file that is not a subtitle of the job, a file twice and an unknown language.
func TestConfirmStoresSubtitleChoices(t *testing.T) {
	x := newImportEnv(t)
	j := x.filmJob()
	_ = x.st.SetTarget(x.ctx, j.ID, x.target(j, `{"added":true,"monitored":true}`))
	sub := x.subtitleFile(j)
	video := x.rows(j)[0].FileID

	for name, choices := range map[string][]SubtitleChoice{
		"a video":          {{FileID: video, Language: "es"}},
		"an unknown file":  {{FileID: 9999, Language: "es"}},
		"a file twice":     {{FileID: sub.ID, Language: "es"}, {FileID: sub.ID, Language: "en"}},
		"unknown language": {{FileID: sub.ID, Language: "xx"}},
		"no language":      {{FileID: sub.ID}},
		"a name":           {{FileID: sub.ID, Language: "Spanish"}},
	} {
		if _, err := x.eng.Confirm(x.ctx, "user1", "", true, j, ConfirmRequest{Subtitles: choices}); Code(err) != CodeBadRequest {
			t.Errorf("%s: %v", name, err)
		}
	}
	if x.jobNow(j).State != store.AwaitingConfirmation {
		t.Fatal("a refused confirmation moved the job")
	}

	c, err := x.eng.Confirm(x.ctx, "user1", "", true, j, ConfirmRequest{Subtitles: []SubtitleChoice{{FileID: sub.ID, Language: "ES", HearingImpaired: true}}})
	if err != nil {
		t.Fatal(err)
	}
	want := SubtitleChoice{FileID: sub.ID, Language: "es", HearingImpaired: true}
	if len(c.Subtitles) != 1 || c.Subtitles[0] != want {
		t.Errorf("confirmation %+v", c.Subtitles)
	}
	tg, _ := x.st.Target(x.ctx, j.ID)
	if got := SubtitleChoices(tg); len(got) != 1 || got[sub.ID] != want {
		t.Errorf("stored %+v (%s)", got, tg.Options)
	}
	var opts map[string]json.RawMessage
	_ = json.Unmarshal(tg.Options, &opts)
	if string(opts["added"]) != "true" || string(opts["monitored"]) != "true" {
		t.Errorf("the match's options were lost: %s", tg.Options)
	}
	var stored map[string]map[string]any
	_ = json.Unmarshal(opts["subtitles"], &stored)
	if stored[strconv.FormatInt(sub.ID, 10)]["language"] != "es" {
		t.Errorf("not keyed by file id: %s", opts["subtitles"])
	}
	if j = x.drive(x.eng, j); j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
}

// The language warning (#39) needs an explicit override like a rejection; the confirmation
// reports its facts for the audit.
func TestLanguageWarningNeedsAnOverride(t *testing.T) {
	x := newImportEnv(t)
	x.movies.Profile = []arr.Profile{{ID: 4, Name: "HD", MinFormatScore: 0}}
	x.movies.Library[0].QualityProfileID = 4
	j := x.job("radarr", filmID, vid("Some Film (2001).mkv", 100*minute))
	x.onDisk(j)
	it := x.item(j, "Some Film (2001).mkv", webdl1080)
	it.CustomFormats, it.CustomFormatScore = []string{"Not Original Language"}, -10000
	x.movies.Proposed[x.folder(j)] = []arr.Row{it}
	x.ready(j)
	row := x.rows(j)[0].ID

	_, err := x.eng.Confirm(x.ctx, "user1", "", true, j, ConfirmRequest{})
	var refusal *Error
	if !errors.As(err, &refusal) || refusal.Code != CodeNeedsOverride || !slices.Equal(refusal.Rows, []string{row}) {
		t.Fatalf("without the override: %v", err)
	}
	c, err := x.eng.Confirm(x.ctx, "user1", "", true, j, ConfirmRequest{Overrides: []string{row}})
	if err != nil {
		t.Fatal(err)
	}
	o := c.Overridden
	if len(o) != 1 || !slices.Equal(o[0].Warnings, []string{WarnLanguage}) || o[0].Language == nil ||
		*o[0].Language.Score != -10000 || *o[0].Language.MinFormatScore != 0 || !slices.Equal(o[0].Language.CustomFormats, []string{"Not Original Language"}) {
		t.Fatalf("overridden %+v", o)
	}
	if !x.rows(j)[0].Override {
		t.Error("the row is not flagged as overridden")
	}
}

// result.monitored and result.unmonitorReason say what the imported step left.
func TestMonitoringResult(t *testing.T) {
	check := func(t *testing.T, x *ienv, j store.Job, monitored bool, reason string) {
		t.Helper()
		_, rv := x.detail(j)
		if rv == nil || rv.Monitored == nil || *rv.Monitored != monitored || rv.UnmonitorReason != reason {
			t.Errorf("result %+v, want monitored %v reason %q", rv, monitored, reason)
		}
	}
	t.Run("film as chosen: replaced by a better copy", func(t *testing.T) {
		x := newImportEnv(t)
		j := x.filmJob()
		_ = x.st.SetTarget(x.ctx, j.ID, x.target(j, `{"added":true,"monitored":true}`))
		j = x.confirm(j)
		for j.State != store.PostProcessing {
			j, _ = x.step(x.eng, j)
			x.appWorks()
			if j.State == store.Imported {
				if _, rv := x.detail(j); rv == nil || rv.Monitored != nil {
					t.Fatalf("monitored known before the step: %+v", rv)
				}
			}
		}
		check(t, x, j, true, "")
	})
	t.Run("film chosen not to be replaced", func(t *testing.T) {
		x := newImportEnv(t)
		j := x.filmJob()
		_ = x.st.SetTarget(x.ctx, j.ID, x.target(j, `{"added":true,"monitored":false}`))
		check(t, x, x.drive(x.eng, x.confirm(j)), false, UnmonitorChosen)
	})
	t.Run("film with only a dub", func(t *testing.T) {
		x := newImportEnv(t)
		j := x.filmJob()
		_ = x.st.SetTarget(x.ctx, j.ID, x.target(j, `{"added":true,"monitored":true}`))
		x.movies.Library[0].OriginalLanguage = "English"
		x.edit(j, x.rows(j)[0].ID, Edit{LanguageIDs: &[]int{spanish.ID}})
		check(t, x, x.drive(x.eng, x.confirm(j)), false, UnmonitorDubOnly)
	})
	t.Run("film in the library, left as it was", func(t *testing.T) {
		x := newImportEnv(t)
		x.movies.Library[0].Monitored = true
		check(t, x, x.drive(x.eng, x.confirm(x.filmJob())), true, "")
	})
	t.Run("show with a dub-only episode", func(t *testing.T) {
		x := newImportEnv(t)
		x.series.Library[0].OriginalLanguage = "English"
		for i := range x.series.Eps[showID] {
			x.series.Eps[showID][i].Monitored = true
		}
		j := x.seasonJob(2)
		x.edit(j, x.rows(j)[1].ID, Edit{LanguageIDs: &[]int{spanish.ID}})
		check(t, x, x.drive(x.eng, x.confirm(j)), false, UnmonitorDubOnly)
	})
	t.Run("show as Sonarr has it", func(t *testing.T) {
		x := newImportEnv(t)
		check(t, x, x.drive(x.eng, x.confirm(x.seasonJob(2))), false, "")
	})
}

// Each row of the import has its file's name, never its path.
func TestImportRowsHaveFileNames(t *testing.T) {
	x := newImportEnv(t)
	j := x.confirm(x.seasonJob(2))
	iv, _ := x.detail(j)
	if len(iv.Rows) != 2 || iv.Rows[0].Name != "Show.S01E01.mkv" || iv.Rows[1].Name != "Show.S01E02.mkv" {
		t.Fatalf("rows %+v", iv.Rows)
	}
}
