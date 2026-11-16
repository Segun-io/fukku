package flows

import (
	"errors"
	"slices"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
)

// A dub of a season already in the library (#57): the proposal offers the policies that
// apply, in seasonDub.order; the confirmation carries the choice, or a fixed
// seasonDub.policy decides.

// dubEnv is an import environment for a show whose episodes `have` (101…) already have a
// file. Every episode is monitored; the show's original language is English; a subtitle
// manager is configured, so subtitlesOnly applies. mutate changes the settings.
func dubEnv(t *testing.T, mutate func(*config.Config), have ...int) *ienv {
	t.Helper()
	x := newImportEnv(t)
	x.cfg.Subtitles.Provider = "bazarr"
	if mutate != nil {
		mutate(&x.cfg)
	}
	x.rebuild(x.movies, x.series)
	x.series.Library[0].OriginalLanguage = "English"
	eps := x.series.Eps[showID]
	for i := range eps {
		eps[i].Monitored = true
		if slices.Contains(have, eps[i].ID) {
			eps[i].FileID = 500 + eps[i].ID
		}
	}
	return x
}

// dubJob is a season job of three files (S01E01–S01E03) awaiting confirmation, whose files are
// Spanish (a dub of a show in English), and for which the app refuses to replace what it has.
func (x *ienv) dubJob() store.Job {
	x.t.Helper()
	j := x.seasonJob(3)
	x.series.Verdicts = map[string][]arr.Rejection{}
	for _, v := range x.view(j).Rows {
		if v.Episodes != nil && len(*v.Episodes) > 0 && (*v.Episodes)[0].HasFile {
			x.series.Verdicts[x.appPath(j, v.Name)] = []arr.Rejection{{Reason: "Not an upgrade for existing episode file(s)"}}
		}
	}
	for _, r := range x.rows(j) {
		x.edit(j, r.ID, Edit{LanguageIDs: &[]int{spanish.ID}})
	}
	return x.jobNow(j)
}

func (x *ienv) confirmDub(j store.Job, choice string, overrides ...string) (Confirmation, error) {
	x.t.Helper()
	return x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{Overrides: overrides, SeasonDub: choice})
}

func wantRefusal(t *testing.T, err error, code string) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	return e
}

func TestSeasonDubIsOfferedInTheConfiguredOrder(t *testing.T) {
	for _, c := range []struct {
		name    string
		mutate  func(*config.Config)
		offers  []string
		ask     bool
		decided string
	}{
		{"default order", nil, []string{"replace", "subtitlesOnly"}, true, ""},
		{"another order", func(c *config.Config) { c.SeasonDub.Order = []string{"subtitlesOnly", "replace"} }, []string{"subtitlesOnly", "replace"}, true, ""},
		{"a repeat counts once", func(c *config.Config) { c.SeasonDub.Order = []string{"replace", "replace", "subtitlesOnly"} }, []string{"replace", "subtitlesOnly"}, true, ""},
		{"no subtitle manager", func(c *config.Config) { c.Subtitles.Provider = "none" }, []string{"replace"}, true, ""},
		{"hand-over off", func(c *config.Config) { c.Subtitles.HandOverUploads = false }, []string{"replace"}, true, ""},
		{"only what the order lists", func(c *config.Config) { c.SeasonDub.Order = []string{"subtitlesOnly"} }, []string{"subtitlesOnly"}, true, ""},
		{"fixed replace", func(c *config.Config) { c.SeasonDub.Policy = "replace" }, []string{"replace", "subtitlesOnly"}, false, "replace"},
		{"fixed subtitlesOnly", func(c *config.Config) { c.SeasonDub.Policy = "subtitlesOnly" }, []string{"replace", "subtitlesOnly"}, false, "subtitlesOnly"},
		{"fixed subtitlesOnly that does not apply", func(c *config.Config) {
			c.SeasonDub.Policy = "subtitlesOnly"
			c.Subtitles.Provider = "none"
		}, []string{"replace"}, false, ""},
		{"fixed version is not offered yet", func(c *config.Config) { c.SeasonDub.Policy = "version" }, []string{"replace", "subtitlesOnly"}, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			x := dubEnv(t, c.mutate, 101, 102)
			sd := x.view(x.seasonJob(3)).SeasonDub
			if sd == nil {
				t.Fatal("no question")
			}
			if !slices.Equal(sd.Offers, c.offers) || sd.Ask != c.ask || sd.Decided != c.decided {
				t.Errorf("offers %v ask %v decided %q, want %v %v %q", sd.Offers, sd.Ask, sd.Decided, c.offers, c.ask, c.decided)
			}
		})
	}
}

// Only some episodes of the season exist: the question counts them, and a season without
// any (or whose existing rows the user skipped) has no question.
func TestSeasonDubCountsTheEpisodesThatExist(t *testing.T) {
	x := dubEnv(t, nil, 101, 102)
	j := x.seasonJob(3)
	sd := x.view(j).SeasonDub
	if sd == nil || len(sd.Seasons) != 1 || sd.Seasons[0] != (SeasonDubSeason{Season: 1, Episodes: 3, Existing: 2}) {
		t.Fatalf("question %+v", sd)
	}
	rows := x.rows(j)
	x.edit(j, rows[0].ID, Edit{Skip: ptr(true)})
	x.edit(j, rows[1].ID, Edit{Skip: ptr(true)})
	if sd := x.view(j).SeasonDub; sd != nil {
		t.Errorf("the existing rows are skipped by the user, yet %+v", sd)
	}

	y := dubEnv(t, nil)
	if sd := y.view(y.seasonJob(2)).SeasonDub; sd != nil {
		t.Errorf("no episode exists, yet %+v", sd)
	}
	if p := y.view(y.filmJob()); p.SeasonDub != nil {
		t.Errorf("a film has no season question: %+v", p.SeasonDub)
	}
}

func TestSeasonDubChoiceIsRequiredAndChecked(t *testing.T) {
	x := dubEnv(t, nil, 101, 102)
	j := x.dubJob()
	rows := x.rows(j)

	e := wantRefusal(t, func() error { _, err := x.confirmDub(j, ""); return err }(), CodeSeasonDubChoice)
	if !slices.Equal(e.Rows, []string{rows[0].ID, rows[1].ID}) {
		t.Errorf("rows %v", e.Rows)
	}
	for _, bad := range []string{"merge", "version", "nonsense"} {
		_, err := x.confirmDub(j, bad)
		_ = wantRefusal(t, err, CodeBadRequest)
	}
	if j = x.jobNow(j); j.State != store.AwaitingConfirmation {
		t.Fatalf("a refused confirmation moved the job to %s", j.State)
	}

	// A choice where nothing exists is a mistake, not a no-op.
	y := dubEnv(t, nil)
	_, err := y.confirmDub(y.seasonJob(2), "replace")
	_ = wantRefusal(t, err, CodeBadRequest)

	// A fixed policy skips the question; another choice is refused.
	z := dubEnv(t, func(c *config.Config) { c.SeasonDub.Policy = "replace" }, 101, 102)
	jz := z.dubJob()
	_, err = z.confirmDub(jz, "subtitlesOnly")
	_ = wantRefusal(t, err, CodeBadRequest)
	if c, err := z.confirmDub(jz, ""); err != nil || c.SeasonDub != PolicyReplace {
		t.Fatalf("fixed replace: %+v, %v", c, err)
	}

	// Nothing offered (a fixed policy that does not apply): the rows with a file stay skipped
	// and the others import, as in M2.
	w := dubEnv(t, func(c *config.Config) { c.SeasonDub.Policy = "version" }, 101, 102)
	jw := w.seasonJob(3)
	c, err := w.confirmDub(jw, "")
	if err != nil || len(c.Rows) != 1 || c.SeasonDub != "" {
		t.Fatalf("version fixed: %+v, %v", c, err)
	}
}

// replace: the dub is imported over the existing files, an override of the app's "not an
// upgrade" refusal; the season is unmonitored afterwards, exactly that season; and the audit
// says which season was replaced.
func TestSeasonDubReplace(t *testing.T) {
	x := dubEnv(t, nil, 101, 102)
	// Season 2 is monitored and untouched; one episode of season 1 is not in the upload.
	j := x.dubJob()
	rows := x.rows(j)
	if v := x.view(j); v.Rows[0].Importable || len(v.Rows[0].Rejections) != 1 {
		t.Fatalf("the existing row before the choice: %+v", v.Rows[0])
	}

	// The refusal of the app is answered by the choice, not by an override; a rejection that
	// is about something else still needs one.
	other := x.appPath(j, "Show.S01E02.mkv")
	x.series.Verdicts[other] = append(x.series.Verdicts[other], arr.Rejection{Reason: "Sample", Permanent: true})
	x.edit(j, rows[1].ID, Edit{LanguageIDs: &[]int{spanish.ID}})
	e := wantRefusal(t, func() error { _, err := x.confirmDub(j, "replace"); return err }(), CodeNeedsOverride)
	if !slices.Equal(e.Rows, []string{rows[1].ID}) {
		t.Fatalf("rows %v", e.Rows)
	}
	c, err := x.confirmDub(j, "replace", rows[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	j = c.Job
	if c.SeasonDub != PolicyReplace || !slices.Equal(c.Seasons, []int{1}) || len(c.Rows) != 3 {
		t.Fatalf("confirmation %+v", c)
	}
	var replaced []store.AuditEntry
	for _, a := range c.audit("user1", "") {
		if a.Action == "season.replaced" {
			replaced = append(replaced, a)
		}
	}
	if len(replaced) != 1 || replaced[0].Detail["season"] != "1" || replaced[0].Detail["titleId"] != "11" {
		t.Errorf("season.replaced entries %+v", replaced)
	}
	if got := results(x.rows(j)); !slices.Equal(got, []string{"", "", ""}) {
		t.Errorf("rows %v", got)
	}
	for _, r := range x.rows(j) {
		if r.Skip {
			t.Errorf("row %s is skipped", r.ID)
		}
	}

	if j = x.drive(x.eng, j); j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if len(x.series.Imports) != 1 || len(x.series.Imports[0].Rows) != 3 {
		t.Fatalf("imports %+v", x.series.Imports)
	}
	eps := x.series.Eps[showID]
	for _, ep := range eps[:3] {
		if ep.FileID == 0 || ep.FileID < 9000 {
			t.Errorf("episode %d still has its old file: %d", ep.ID, ep.FileID)
		}
	}
	// Exactly season 1 is unmonitored, through its flag.
	if flags := x.series.SeasonFlags[showID]; len(flags) != 1 || flags[1] {
		t.Errorf("season flags %v", flags)
	}
	for _, ep := range eps {
		if want := ep.SeasonNumber != 1; ep.Monitored != want {
			t.Errorf("episode %d monitored %v, want %v", ep.ID, ep.Monitored, want)
		}
	}
	_, rv := x.detail(j)
	if rv.Monitored == nil || *rv.Monitored || rv.UnmonitorReason != UnmonitorDubOnly ||
		rv.SeasonDub == nil || rv.SeasonDub.Policy != PolicyReplace || !slices.Equal(rv.SeasonDub.Seasons, []int{1}) {
		t.Errorf("result %+v", rv)
	}
}

// With merge.unmonitorAfterDubOnlyImport off, or a file that is not only a dub, the season
// stays monitored.
func TestSeasonDubReplaceUnmonitorsOnlyAfterADub(t *testing.T) {
	for name, c := range map[string]struct {
		mutate func(*config.Config)
		dub    bool
	}{
		"setting off":      {func(c *config.Config) { c.Merge.UnmonitorAfterDubOnlyImport = false }, true},
		"original present": {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			x := dubEnv(t, c.mutate, 101)
			j := x.seasonJob(2)
			if c.dub {
				for _, r := range x.rows(j) {
					x.edit(j, r.ID, Edit{LanguageIDs: &[]int{spanish.ID}})
				}
			}
			cf, err := x.confirmDub(j, "replace", x.rows(j)[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if j = x.drive(x.eng, cf.Job); j.State != store.Done {
				t.Fatalf("job %s (%s)", j.State, j.ErrorText)
			}
			if len(x.series.SeasonFlags) != 0 {
				t.Errorf("season flags %v", x.series.SeasonFlags)
			}
		})
	}
}

// A failed season-flag call is the monitoring step's failure: the app is asked again.
func TestSeasonDubReplaceRetriesTheSeasonFlag(t *testing.T) {
	x := dubEnv(t, nil, 101)
	j := x.dubJob()
	c, err := x.confirmDub(j, "replace")
	if err != nil {
		t.Fatal(err)
	}
	x.series.Err = map[string]error{"SetSeasonMonitored": errRefused}
	for range 3 {
		_, _ = x.step(x.eng, c.Job)
		x.appWorks()
	}
	if j = x.jobNow(j); j.State != store.Stalled || j.ErrorCode != CodeArrUnavailable {
		t.Fatalf("job %s (%s)", j.State, j.ErrorCode)
	}
	x.series.Err = nil
	if j = x.drive(x.eng, j); j.State != store.Done || x.series.SeasonFlags[showID][1] {
		t.Fatalf("job %s, flags %v", j.State, x.series.SeasonFlags)
	}
}

// subtitlesOnly: nothing is imported, the rows are skipped, the app is not asked to import
// or to change monitoring, and the post-processing steps still run (they hand the subtitles
// over); the result says so.
func TestSeasonDubSubtitlesOnly(t *testing.T) {
	x := dubEnv(t, nil, 101, 102)
	step := &recordingStep{name: "subtitles"}
	x.eng.AddPostStep(step)
	j := x.dubJob()
	rows := x.rows(j)
	c, err := x.confirmDub(j, "subtitlesOnly")
	if err != nil {
		t.Fatal(err)
	}
	if c.SeasonDub != PolicySubtitlesOnly || len(c.Rows) != 0 {
		t.Fatalf("confirmation %+v", c)
	}
	var audited []string
	for _, a := range c.audit("user1", "") {
		audited = append(audited, a.Action)
	}
	if !slices.Contains(audited, "season.subtitles_only") || slices.Contains(audited, "season.replaced") {
		t.Errorf("audit %v", audited)
	}
	j = x.drive(x.eng, c.Job)
	if j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if len(x.series.Imports) != 0 || len(x.series.SeasonFlags) != 0 {
		t.Errorf("imports %d, season flags %v", len(x.series.Imports), x.series.SeasonFlags)
	}
	for _, ep := range x.series.Eps[showID] {
		if !ep.Monitored || (ep.FileID != 0 && ep.FileID != 500+ep.ID) {
			t.Errorf("episode %+v changed", ep)
		}
	}
	if got := results(x.rows(j)); !slices.Equal(got, []string{store.ResultSkipped, store.ResultSkipped, store.ResultSkipped}) {
		t.Errorf("results %v", got)
	}
	if step.count() != 1 {
		t.Errorf("the post-processing step ran %d times", step.count())
	}
	if only, ok := SubtitlesOnlyRows(mustTarget(t, x, j)); !ok || !slices.Equal(only, []string{rows[0].ID, rows[1].ID}) {
		t.Errorf("rows for the subtitles %v %v", only, ok)
	}
	iv, rv := x.detail(j)
	if iv.Status != ImportDone || rv.Monitored != nil || rv.SeasonDub == nil || rv.SeasonDub.Policy != PolicySubtitlesOnly {
		t.Errorf("import %+v result %+v", iv, rv)
	}
}

func mustTarget(t *testing.T, x *ienv, j store.Job) store.Target {
	t.Helper()
	tg, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tg
}

// With a fixed policy the question is skipped and the choice is made at the confirmation.
func TestSeasonDubFixedPolicyNeedsNoChoice(t *testing.T) {
	x := dubEnv(t, func(c *config.Config) { c.SeasonDub.Policy = "subtitlesOnly" }, 101)
	j := x.dubJob()
	if sd := x.view(j).SeasonDub; sd == nil || sd.Ask || sd.Decided != PolicySubtitlesOnly {
		t.Fatalf("question %+v", sd)
	}
	c, err := x.confirmDub(j, "")
	if err != nil || c.SeasonDub != PolicySubtitlesOnly {
		t.Fatalf("confirm: %+v, %v", c, err)
	}
	if j = x.drive(x.eng, c.Job); j.State != store.Done || len(x.series.Imports) != 0 {
		t.Fatalf("job %s, %d imports", j.State, len(x.series.Imports))
	}
}

// A season the upload fully has no file for: no question, the normal import, and no season
// flag is touched even with replace fixed.
func TestSeasonDubDoesNotTouchAnImportWithoutExistingFiles(t *testing.T) {
	x := dubEnv(t, func(c *config.Config) { c.SeasonDub.Policy = "replace" }, 201)
	j := x.seasonJob(2)
	if sd := x.view(j).SeasonDub; sd != nil {
		t.Fatalf("question %+v", sd)
	}
	if j = x.drive(x.eng, x.confirm(j)); j.State != store.Done || len(x.series.SeasonFlags) != 0 {
		t.Fatalf("job %s, flags %v", j.State, x.series.SeasonFlags)
	}
}
