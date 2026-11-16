package flows

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/store"
)

func TestFilm(t *testing.T) {
	e := newEnv(t)
	j := e.job("radarr", filmID, vid("Some Film (2001).mkv", 100*minute))
	it := e.item(j, "Some Film (2001).mkv", arr.Quality{ID: 0, Name: "Unknown"})
	it.MovieID = 0
	e.movies.Proposed[e.folder(j)] = []arr.Row{it}
	p := e.ready(j)

	// The proposal is for the job's folder as the app sees it, and nothing else.
	if got := e.movies.ProposalCalls(); !slices.Equal(got, []string{e.folder(j)}) {
		t.Errorf("proposal calls %q, want the job folder %q", got, e.folder(j))
	}
	rc := e.movies.RecheckCalls()
	if len(rc) != 1 || len(rc[0]) != 1 {
		t.Fatalf("re-checks %+v", rc)
	}
	sent := rc[0][0]
	if sent.MovieID != filmID || sent.Path != it.Path || sent.Quality != webdl1080 {
		t.Errorf("re-checked %+v: want the film, the app's path and the quality from the probe", sent)
	}

	if p.State != StateReady || p.App != "radarr" || p.Target == nil || p.Target.TitleID != filmID || p.Episodes != nil {
		t.Errorf("proposal %+v", p)
	}
	if len(p.Qualities) != len(quals) || len(p.Languages) != len(langs) || len(p.Hints) != 0 {
		t.Errorf("pickers %+v %+v, hints %+v", p.Qualities, p.Languages, p.Hints)
	}
	rows := e.rows(j)
	if len(p.Rows) != 1 || len(rows) != 1 {
		t.Fatalf("rows %+v", p.Rows)
	}
	r := p.Rows[0]
	if r.ID != RowID(rows[0].FileID) || r.ID != rows[0].ID || r.Name != "Some Film (2001).mkv" || r.DurationMs != 100*minute {
		t.Errorf("row %+v", r)
	}
	if r.Skip || !r.Importable || r.Quality == nil || *r.Quality != Ref(webdl1080) || len(r.Rejections) != 0 || len(r.Warnings) != 0 {
		t.Errorf("row %+v", r)
	}
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), "seasonNumber") || strings.Contains(string(data), "episodes") {
		t.Errorf("a film's row has seasons or episodes: %s", data)
	}
	data, _ = json.Marshal(p)
	if strings.Contains(string(data), `"episodes"`) {
		t.Errorf("a film's proposal has episodes: %s", data)
	}

	// An edit goes through the app; the row keeps its id.
	r2 := e.edit(j, r.ID, Edit{QualityID: ptr(7)})
	if r2.ID != r.ID || r2.Quality == nil || r2.Quality.ID != 7 || len(e.movies.RecheckCalls()) != 2 {
		t.Errorf("after the edit %+v, %d re-checks", r2, len(e.movies.RecheckCalls()))
	}
	// Seasons and episodes are for shows; nothing reaches the app.
	for _, ed := range []Edit{{SeasonNumber: ptr(1)}, {EpisodeIDs: &[]int{101}}, {QualityID: ptr(99)}, {LanguageIDs: &[]int{}}, {LanguageIDs: &[]int{1, 1}}} {
		if _, err := e.p.EditRow(e.ctx, j, r.ID, ed); Code(err) != CodeBadRequest {
			t.Errorf("edit %+v: %v, want a bad request", ed, err)
		}
	}
	if _, err := e.p.Bulk(e.ctx, j, BulkEdit{RowIDs: []string{r.ID}, Shift: ptr(1)}); err == nil {
		t.Error("bulk edit of a film accepted")
	}
	if n := len(e.movies.RecheckCalls()); n != 2 {
		t.Errorf("%d re-checks after refused edits, want 2", n)
	}
}

// A quality the app found is kept; a film it matched to another title by the file name
// gets the chosen one.
func TestFilmPrematched(t *testing.T) {
	e := newEnv(t)
	j := e.job("radarr", filmID, vid("Another Film (1999) HDTV-720p.mkv", 100*minute))
	it := e.item(j, "Another Film (1999) HDTV-720p.mkv", arr.Quality{ID: 4, Name: "HDTV-720p"})
	it.MovieID, it.Rejections = 99, nil
	e.movies.Proposed[e.folder(j)] = []arr.Row{it}
	p := e.ready(j)
	sent := e.movies.RecheckCalls()[0][0]
	if sent.MovieID != filmID || sent.Quality.ID != 4 {
		t.Errorf("re-checked %+v", sent)
	}
	if r := p.Rows[0]; !r.Importable || r.Quality.ID != 4 {
		t.Errorf("row %+v", r)
	}
}

func TestSeasonWithAMisnumberedFileFixedByAnEdit(t *testing.T) {
	e := newEnv(t)
	names := []string{"Some Show - S01E01.mkv", "Some Show - S01E02.mkv", "Some Show - S01E02 (really 3).mkv"}
	var files []file
	for _, n := range names {
		files = append(files, vid(n, epRuntime))
	}
	j := e.job("sonarr", showID, files...)
	var items []arr.Row
	for _, n := range names {
		items = append(items, e.item(j, n, webdl1080))
	}
	e.series.Proposed[e.folder(j)] = items
	p := e.ready(j)
	before := ids(p.Rows)

	r1, r2, r3 := byName(p, names[0]), byName(p, names[1]), byName(p, names[2])
	if !slices.Equal(episodeIDs(r1), []int{101}) || !slices.Equal(episodeIDs(r2), []int{102}) || !slices.Equal(episodeIDs(r3), []int{102}) {
		t.Errorf("episodes from the names: %v %v %v", episodeIDs(r1), episodeIDs(r2), episodeIDs(r3))
	}
	if r1.SeasonNumber == nil || *r1.SeasonNumber != 1 || !r1.Importable {
		t.Errorf("row 1 %+v", r1)
	}
	wantCodes(t, "row 2", r2.Warnings, WarnDuplicateEpisode)
	wantCodes(t, "row 3", r3.Warnings, WarnDuplicateEpisode)
	for _, sent := range e.series.RecheckCalls()[0] {
		if sent.SeriesID != showID || sent.MovieID != 0 {
			t.Errorf("re-checked %+v without the series", sent)
		}
	}

	got := e.edit(j, r3.ID, Edit{EpisodeIDs: &[]int{103}})
	if got.ID != r3.ID || !slices.Equal(episodeIDs(got), []int{103}) || !got.Importable || len(got.Warnings) != 0 {
		t.Errorf("edited row %+v", got)
	}
	if rc := e.series.RecheckCalls(); len(rc) != 2 || len(rc[1]) != 1 || rc[1][0].Path != e.appPath(j, names[2]) || !slices.Equal(rc[1][0].EpisodeIDs, []int{103}) {
		t.Errorf("re-checks %+v", rc)
	}
	p = e.view(j)
	if !slices.Equal(ids(p.Rows), before) {
		t.Errorf("row ids %v, were %v", ids(p.Rows), before)
	}
	for _, r := range p.Rows {
		if !r.Importable {
			t.Errorf("row %s not importable: %+v", r.ID, r)
		}
	}
	var stored Edit
	for _, r := range e.rows(j) {
		if r.ID == r3.ID {
			_ = json.Unmarshal(r.Edit, &stored)
		}
	}
	if stored.EpisodeIDs == nil || !slices.Equal(*stored.EpisodeIDs, []int{103}) || stored.SeasonNumber == nil || *stored.SeasonNumber != 1 {
		t.Errorf("stored edit %+v", stored)
	}
	if p.Episodes == nil || len(*p.Episodes) != 9 || (*p.Episodes)[0].SeasonNumber == nil {
		t.Errorf("episodes for the picker %+v", p.Episodes)
	}
}

func TestDoubleEpisodeSetToTwoEpisodes(t *testing.T) {
	e := newEnv(t)
	j := e.job("sonarr", showID, vid("S01E01.mkv", epRuntime), vid("S01E02.mkv", 2*epRuntime+minute))
	e.series.Proposed[e.folder(j)] = []arr.Row{e.item(j, "S01E01.mkv", webdl1080), e.item(j, "S01E02.mkv", webdl1080)}
	p := e.ready(j)
	double := byName(p, "S01E02.mkv")
	wantCodes(t, "the long file", double.Warnings, WarnDoubleEpisode)
	wantCodes(t, "the other file", byName(p, "S01E01.mkv").Warnings)
	if double.Importable {
		t.Errorf("a hinted double is importable: %+v", double)
	}
	got := e.edit(j, double.ID, Edit{EpisodeIDs: &[]int{103, 102}})
	if !slices.Equal(episodeIDs(got), []int{102, 103}) || len(got.Warnings) != 0 || !got.Importable || *got.SeasonNumber != 1 {
		t.Errorf("as a double %+v", got)
	}
	// Episodes of two seasons, or of another series, are refused.
	for _, ed := range []Edit{{EpisodeIDs: &[]int{103, 201}}, {EpisodeIDs: &[]int{999}}, {EpisodeIDs: &[]int{102, 102}}, {EpisodeIDs: &[]int{102}, SeasonNumber: ptr(2)}, {SeasonNumber: ptr(-1)}} {
		if _, err := e.p.EditRow(e.ctx, j, double.ID, ed); Code(err) != CodeBadRequest {
			t.Errorf("edit %+v: %v, want a bad request", ed, err)
		}
	}
	if n := len(e.series.RecheckCalls()); n != 2 {
		t.Errorf("%d re-checks, want 2 (refused edits reach no app)", n)
	}
}

func seasonJob(e *env, n int) (store.Job, Proposal) {
	var files []file
	var names []string
	for i := 1; i <= n; i++ {
		name := "Some.Show.S01E0" + string(rune('0'+i)) + ".mkv"
		names = append(names, name)
		files = append(files, vid(name, epRuntime))
	}
	j := e.job("sonarr", showID, files...)
	var items []arr.Row
	for _, name := range names {
		items = append(items, e.item(j, name, webdl1080))
	}
	e.series.Proposed[e.folder(j)] = items
	return j, e.ready(j)
}

func TestBulkShiftByOne(t *testing.T) {
	e := newEnv(t)
	j, p := seasonJob(e, 3)
	before := ids(p.Rows)
	rows, err := e.p.Bulk(e.ctx, j, BulkEdit{RowIDs: []string{before[2], before[0], before[1]}, Shift: ptr(1)})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(rows); !slices.Equal(got, []string{before[2], before[0], before[1]}) {
		t.Errorf("rows in the order asked: %v", got)
	}
	p = e.view(j)
	for i, want := range [][]int{{102}, {103}, {104}} {
		if got := episodeIDs(p.Rows[i]); !slices.Equal(got, want) || !p.Rows[i].Importable {
			t.Errorf("row %d: episodes %v, want %v (%+v)", i, got, want, p.Rows[i])
		}
	}
	if !slices.Equal(ids(p.Rows), before) {
		t.Errorf("ids %v, were %v", ids(p.Rows), before)
	}
	rc := e.series.RecheckCalls()
	if len(rc) != 2 || len(rc[1]) != 3 {
		t.Errorf("re-checks %d, the bulk one with %d rows, want one call with all three", len(rc), len(rc[len(rc)-1]))
	}

	// Shifting back past episode 1 is refused as a whole.
	stored := e.rows(j)
	_, err = e.p.Bulk(e.ctx, j, BulkEdit{RowIDs: before, Shift: ptr(-2)})
	if Code(err) != CodeBadRequest || !strings.Contains(err.Error(), "no episode 0") {
		t.Errorf("shift past the start: %v", err)
	}
	if after := e.rows(j); !slices.EqualFunc(after, stored, func(a, b store.Row) bool { return string(a.Proposal) == string(b.Proposal) }) {
		t.Error("a refused bulk edit changed rows")
	}
	for _, b := range []BulkEdit{
		{RowIDs: before},
		{RowIDs: before, Shift: ptr(1), SeasonNumber: ptr(1)},
		{RowIDs: nil, Shift: ptr(1)},
		{RowIDs: []string{before[0], before[0]}, Shift: ptr(1)},
		{RowIDs: before, Shift: ptr(0)},
		{RowIDs: before, SeasonNumber: ptr(-1)},
	} {
		if _, err := e.p.Bulk(e.ctx, j, b); Code(err) != CodeBadRequest {
			t.Errorf("bulk %+v: %v, want a bad request", b, err)
		}
	}
	if _, err := e.p.Bulk(e.ctx, j, BulkEdit{RowIDs: []string{"r999"}, Shift: ptr(1)}); Code(err) != CodeNotFound {
		t.Errorf("unknown row: %v", err)
	}
	if n := len(e.series.RecheckCalls()); n != 2 {
		t.Errorf("%d re-checks, want 2", n)
	}
}

func TestBulkSetSeason(t *testing.T) {
	e := newEnv(t)
	j, p := seasonJob(e, 4)
	all := ids(p.Rows)
	rows, err := e.p.Bulk(e.ctx, j, BulkEdit{RowIDs: all[:3], SeasonNumber: ptr(2)})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range [][]int{{201}, {202}, {203}} {
		if got := episodeIDs(rows[i]); !slices.Equal(got, want) || *rows[i].SeasonNumber != 2 {
			t.Errorf("row %d: %v, want %v", i, got, want)
		}
	}
	// Season 2 has no episode 4: refused, nothing changes.
	_, err = e.p.Bulk(e.ctx, j, BulkEdit{RowIDs: all[3:], SeasonNumber: ptr(2)})
	if Code(err) != CodeBadRequest || !strings.Contains(err.Error(), "Season 2 has no episode 4") {
		t.Errorf("season without the episode: %v", err)
	}
	if got := episodeIDs(e.view(j).Rows[3]); !slices.Equal(got, []int{104}) {
		t.Errorf("row 4 changed: %v", got)
	}
}

func TestFileTheAppDoesNotList(t *testing.T) {
	e := newEnv(t)
	j := e.job("sonarr", showID, vid("S01E01.mkv", epRuntime), vid("sample.mkv", minute))
	e.series.Proposed[e.folder(j)] = []arr.Row{e.item(j, "S01E01.mkv", webdl1080)}
	p := e.ready(j)
	r := byName(p, "sample.mkv")
	wantCodes(t, "unlisted", r.Warnings, WarnNotOffered)
	if !r.Skip || r.Importable || r.Quality != nil || r.Episodes == nil || len(*r.Episodes) != 0 {
		t.Errorf("unlisted row %+v", r)
	}
	if rc := e.series.RecheckCalls(); len(rc) != 1 || len(rc[0]) != 1 {
		t.Errorf("re-checked %+v: the unlisted file must not be sent", rc)
	}
	if !byName(p, "S01E01.mkv").Importable {
		t.Errorf("listed row %+v", byName(p, "S01E01.mkv"))
	}
	if _, err := e.p.EditRow(e.ctx, j, r.ID, Edit{EpisodeIDs: &[]int{102}}); Code(err) != CodeBadRequest {
		t.Errorf("editing an unlisted file: %v", err)
	}
	if got := e.edit(j, r.ID, Edit{Skip: ptr(false)}); !got.Skip {
		t.Errorf("an unlisted file must stay skipped: %+v", got)
	}
	if n := len(e.series.RecheckCalls()); n != 1 {
		t.Errorf("%d re-checks", n)
	}
}

func TestEpisodeThatAlreadyHasAFile(t *testing.T) {
	e := newEnv(t)
	e.series.Eps[showID][1].FileID = 9001 // S01E02
	j := e.job("sonarr", showID, vid("S01E01.mkv", epRuntime), vid("S01E02.mkv", epRuntime))
	e.series.Proposed[e.folder(j)] = []arr.Row{e.item(j, "S01E01.mkv", webdl1080), e.item(j, "S01E02.mkv", webdl1080)}
	p := e.ready(j)
	r := byName(p, "S01E02.mkv")
	wantCodes(t, "existing", r.Warnings, WarnExistingFile)
	if !r.Skip || r.Importable || !(*r.Episodes)[0].HasFile {
		t.Errorf("row %+v", r)
	}
	for _, row := range e.rows(j) {
		if row.ID == r.ID && !row.Skip {
			t.Error("the stored row is not skipped: the import reads Skip")
		}
	}
	// It cannot be un-skipped in M2 …
	if got := e.edit(j, r.ID, Edit{Skip: ptr(false)}); !got.Skip || got.Importable {
		t.Errorf("un-skipped %+v", got)
	}
	// … but another episode can be chosen.
	got := e.edit(j, r.ID, Edit{EpisodeIDs: &[]int{103}})
	if got.Skip || !got.Importable || len(got.Warnings) != 0 {
		t.Errorf("moved to an episode without a file: %+v", got)
	}
}

// Sonarr matches files to a show in its library by their names before Fukku says which
// show it is. A file matched to the chosen show keeps its episodes; one matched to another
// show keeps the season and numbers the app parsed; one the app could not parse has none.
func TestProposalArrivesPrematched(t *testing.T) {
	e := newEnv(t)
	j := e.job("sonarr", showID,
		vid("a Some.Show.S01E01.mkv", epRuntime), vid("b Other.Name.S01E02.mkv", epRuntime), vid("c Pilot.mkv", epRuntime))
	a := e.item(j, "a Some.Show.S01E01.mkv", webdl1080)
	a.SeriesID, a.SeasonNumber, a.EpisodeIDs, a.Rejections = showID, 1, []int{101}, nil
	b := e.item(j, "b Other.Name.S01E02.mkv", webdl1080)
	b.SeriesID, b.SeasonNumber, b.EpisodeIDs = otherShow, 1, []int{902}
	b.Raw = overlay(b.Raw, json.RawMessage(`{"seriesId":12,"episodes":[{"id":902,"seasonNumber":1,"episodeNumber":3,"seriesId":12}]}`))
	c := e.item(j, "c Pilot.mkv", webdl1080)
	e.series.Proposed[e.folder(j)] = []arr.Row{c, b, a} // the app's order is not the files'
	p := e.ready(j)

	sent := e.series.RecheckCalls()[0]
	if len(sent) != 3 {
		t.Fatalf("re-checked %d rows", len(sent))
	}
	for _, s := range sent {
		if s.SeriesID != showID {
			t.Errorf("re-checked %s with series %d", s.Path, s.SeriesID)
		}
	}
	if got := episodeIDs(byName(p, "a Some.Show.S01E01.mkv")); !slices.Equal(got, []int{101}) {
		t.Errorf("matched to the show: %v", got)
	}
	// The app parsed S01E03 for its other show, whatever the name says.
	if got := episodeIDs(byName(p, "b Other.Name.S01E02.mkv")); !slices.Equal(got, []int{103}) {
		t.Errorf("matched to another show: %v", got)
	}
	r := byName(p, "c Pilot.mkv")
	if len(episodeIDs(r)) != 0 || r.SeasonNumber != nil || r.Importable {
		t.Errorf("unparsed %+v", r)
	}
	if !byName(p, "a Some.Show.S01E01.mkv").Importable || !byName(p, "b Other.Name.S01E02.mkv").Importable {
		t.Errorf("rows %+v", p.Rows)
	}
}

func TestRuntimeMismatchHintsDVDOrder(t *testing.T) {
	e := newEnv(t)
	j := e.job("sonarr", showID,
		vid("S01E01.mkv", 30*minute), vid("S01E02.mkv", 31*minute), vid("S01E03.mkv", epRuntime), vid("S01E04.mkv", 15*minute))
	var items []arr.Row
	for _, n := range []string{"S01E01.mkv", "S01E02.mkv", "S01E03.mkv", "S01E04.mkv"} {
		items = append(items, e.item(j, n, webdl1080))
	}
	e.series.Proposed[e.folder(j)] = items
	p := e.ready(j)
	wantCodes(t, "hints", p.Hints, HintDVDOrder)
	for i, want := range []string{WarnRuntimeMismatch, WarnRuntimeMismatch, "", WarnRuntimeMismatch} {
		if want == "" {
			wantCodes(t, "row 3", p.Rows[i].Warnings)
		} else {
			wantCodes(t, "row", p.Rows[i].Warnings, want)
		}
	}
	// Skipping rows does not change their runtimes: the hint stays.
	for _, r := range p.Rows[:2] {
		e.edit(j, r.ID, Edit{Skip: ptr(true)})
	}
	if p := e.view(j); len(p.Hints) != 1 {
		t.Errorf("skipping does not change the runtimes: hints %v", p.Hints)
	}

	e2 := newEnv(t)
	j2 := e2.job("sonarr", showID, vid("S01E01.mkv", 30*minute), vid("S01E02.mkv", epRuntime), vid("S01E03.mkv", epRuntime))
	e2.series.Proposed[e2.folder(j2)] = []arr.Row{e2.item(j2, "S01E01.mkv", webdl1080), e2.item(j2, "S01E02.mkv", webdl1080), e2.item(j2, "S01E03.mkv", webdl1080)}
	if p := e2.ready(j2); len(p.Hints) != 0 {
		t.Errorf("one mismatch in three: hints %v", p.Hints)
	}
}

func TestAppDownStallsAndResumes(t *testing.T) {
	for _, method := range []string{"Proposal", "Episodes", "Recheck"} {
		t.Run(method, func(t *testing.T) {
			e := newEnv(t)
			j := e.job("sonarr", showID, vid("S01E01.mkv", epRuntime))
			e.series.Proposed[e.folder(j)] = []arr.Row{e.item(j, "S01E01.mkv", webdl1080)}
			e.series.Err = map[string]error{method: errRefused, "Status": errRefused}
			j = e.matched(j)
			if j.State != store.Stalled || j.ErrorCode != CodeArrUnavailable || j.Resume != store.Matched {
				t.Fatalf("job %+v", j)
			}
			if len(e.rows(j)) != 0 {
				t.Error("rows stored")
			}
			if p := e.view(j); p.State != StatePending || p.Target == nil || len(p.Rows) != 0 {
				t.Errorf("view %+v", p)
			}
			if ok, err := e.p.AppAnswers(e.ctx, j); ok || err != nil {
				t.Errorf("answers while down: %v %v", ok, err)
			}
			e.series.Err = nil
			e.p.now = func() time.Time { return time.Now().Add(time.Hour) } // past the backoff
			if ok, err := e.p.AppAnswers(e.ctx, j); !ok || err != nil {
				t.Errorf("answers when back: %v %v", ok, err)
			}
			// The runner moves it back (runner.ResumeWhen); the step then finishes.
			if _, err := e.st.Transition(e.ctx, j.ID, store.Stalled, store.Matched, store.Change{Actor: "system"}); err != nil {
				t.Fatal(err)
			}
			if p := e.ready(j); len(p.Rows) != 1 || !p.Rows[0].Importable {
				t.Errorf("after resuming %+v", p)
			}
		})
	}
}

func TestAppRefusalFails(t *testing.T) {
	e := newEnv(t)
	j := e.job("radarr", filmID, vid("Film.mkv", 100*minute))
	e.movies.Err = map[string]error{"Proposal": &arr.Error{App: "radarr", Method: "GET", Path: "/manualimport", Status: 400, Message: "folder does not exist"}}
	j = e.matched(j)
	if j.State != store.Failed || j.ErrorCode != CodeProposalFailed || j.Resume != store.Matched || !strings.Contains(j.ErrorText, "folder does not exist") {
		t.Fatalf("job %+v", j)
	}
	if p := e.view(j); p.State != StatePending {
		t.Errorf("view %+v", p)
	}
}

func TestUnconfiguredAppStalls(t *testing.T) {
	e := newEnv(t)
	e.p = NewProposals(e.cfg, nil, e.st, e.movies, nil, nil)
	j := e.matched(e.job("sonarr", showID, vid("S01E01.mkv", epRuntime)))
	if j.State != store.Stalled || j.ErrorCode != CodeArrUnavailable {
		t.Fatalf("job %+v", j)
	}
	if ok, _ := e.p.AppAnswers(e.ctx, j); ok {
		t.Error("an app that is not configured answers")
	}
}

func TestNoTargetGoesBackToProbed(t *testing.T) {
	e := newEnv(t)
	j := e.job("radarr", filmID, vid("Film.mkv", 100*minute))
	if err := e.st.ClearTarget(e.ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if j = e.matched(j); j.State != store.Probed {
		t.Errorf("job %+v", j)
	}
}

func TestViewStates(t *testing.T) {
	e := newEnv(t)
	j := e.job("radarr", filmID, vid("Film.mkv", 100*minute))
	if p := e.view(j); p.State != StatePending || p.App != "radarr" || p.Target.TitleID != filmID {
		t.Errorf("matched: %+v", p)
	}
	if _, err := e.p.EditRow(e.ctx, j, "r1", Edit{}); Code(err) != CodeWrongState {
		t.Errorf("edit while matched: %v", err)
	}
	e.movies.Proposed[e.folder(j)] = []arr.Row{e.item(j, "Film.mkv", webdl1080)}
	e.ready(j)
	if _, err := e.st.Transition(e.ctx, j.ID, store.AwaitingConfirmation, store.Importing, store.Change{Actor: "user1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.p.View(e.ctx, j); Code(err) != CodeWrongState {
		t.Errorf("view while importing: %v", err)
	}
	if _, err := e.p.EditRow(e.ctx, j, RowID(1), Edit{}); Code(err) != CodeWrongState {
		t.Errorf("edit while importing: %v", err)
	}
}

func TestEditRecheckFailureKeepsTheRow(t *testing.T) {
	e := newEnv(t)
	j, p := seasonJob(e, 2)
	before := e.rows(j)
	e.series.Err = map[string]error{"Recheck": errors.New("timed out")}
	_, err := e.p.EditRow(e.ctx, j, p.Rows[0].ID, Edit{EpisodeIDs: &[]int{105}})
	if Code(err) != CodeArrUnavailable || !strings.Contains(err.Error(), "sonarr") {
		t.Errorf("re-check failure: %v", err)
	}
	if after := e.rows(j); !slices.EqualFunc(after, before, func(a, b store.Row) bool {
		return string(a.Proposal) == string(b.Proposal) && string(a.Edit) == string(b.Edit)
	}) {
		t.Error("a failed re-check changed the row")
	}
	e.series.Err = map[string]error{"Episodes": errors.New("timed out")}
	if _, err := e.p.View(e.ctx, j); Code(err) != CodeArrUnavailable {
		t.Errorf("view without episodes: %v", err)
	}
}

func TestLanguageHook(t *testing.T) {
	e := newEnv(t)
	var seen []string
	e.p.Language = func(_ context.Context, target store.Target, r arr.Row) []string {
		seen = append(seen, r.Path)
		if target.TitleID == showID && slices.ContainsFunc(r.Languages, func(l arr.Language) bool { return l.ID == spanish.ID }) {
			return []string{"language"}
		}
		return nil
	}
	j, p := seasonJob(e, 2)
	if len(seen) != 2 || len(p.Rows[0].Warnings) != 0 {
		t.Errorf("hook saw %v; row %+v", seen, p.Rows[0])
	}
	got := e.edit(j, p.Rows[0].ID, Edit{LanguageIDs: &[]int{spanish.ID}})
	wantCodes(t, "dub", got.Warnings, "language")
	if got.Importable {
		t.Errorf("a warned row is importable: %+v", got)
	}
	if rc := e.series.RecheckCalls(); rc[len(rc)-1][0].Languages[0] != spanish {
		t.Errorf("re-checked %+v", rc[len(rc)-1][0])
	}
}

func TestParseEpisodes(t *testing.T) {
	for name, want := range map[string][]epRef{
		"Some.Show.S01E02.1080p.WEB-DL.mkv":    {{1, 2}},
		"some_show_s02e10.mkv":                 {{2, 10}},
		"Some Show - S01E02-E03 - Title.mkv":   {{1, 2}, {1, 3}},
		"Some.Show.S01E02E03.mkv":              {{1, 2}, {1, 3}},
		"Some Show 1x05.mkv":                   {{1, 5}},
		"Some Show (2010) 1920x1080 Pilot.mp4": nil,
		"Pilot.mkv":                            nil,
		"Seasons.Episode.mkv":                  nil,
	} {
		if got := parseEpisodes(name); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}

func TestDefaultQuality(t *testing.T) {
	all := append(slices.Clone(quals), arr.Quality{ID: 18, Name: "WEBDL-2160p"}, arr.Quality{ID: 8, Name: "WEBRip-1080p"}, arr.Quality{ID: 9, Name: "DVD"})
	for _, tc := range []struct {
		w, h   int
		source string
		want   int // quality id; -1 none
	}{
		{1920, 1080, "WEBDL", 3},
		{1920, 800, "WEBDL", 3},
		{3840, 2160, "WEBDL", 18},
		{1920, 1080, "WEBRIP", 8},
		{1920, 1080, "BLURAY", 7},
		{1280, 720, "HDTV", 4},
		{720, 576, "HDTV", 1},
		{720, 576, "DVD", 9},
		{1280, 720, "BLURAY", -1}, // the app has no Bluray-720p
		{0, 0, "WEBDL", -1},
	} {
		q, ok := defaultQuality(probeWith(tc.w, tc.h), tc.source, all)
		if (tc.want < 0) == ok || (ok && q.ID != tc.want) {
			t.Errorf("%dx%d %s: %+v %v, want %d", tc.w, tc.h, tc.source, q, ok, tc.want)
		}
	}
}
