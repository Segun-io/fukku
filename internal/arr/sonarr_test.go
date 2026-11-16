package arr_test

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
)

func newSonarr(t *testing.T, routes map[string]reply) (*fakeApp, arr.SeriesManager) {
	t.Helper()
	app := &fakeApp{t: t, routes: routes}
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	return app, arr.NewSonarr(cfg(srv.URL, 5*time.Second))
}

func TestSonarrStatusProfilesRootFolders(t *testing.T) {
	app, s := newSonarr(t, map[string]reply{
		"GET /api/v3/system/status":  {body: `{"appName":"Sonarr","version":"4.0.20.1"}`},
		"GET /api/v3/qualityprofile": ok("qualityprofile.json"),
		"GET /api/v3/rootfolder":     {body: `[{"id":1,"path":"/data/media/tv","freeSpace":1000}]`},
	})
	st, err := s.Status(ctx)
	if err != nil || st != (arr.Status{AppName: "Sonarr", Version: "4.0.20.1"}) {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	if app.last().Key != testKey {
		t.Error("X-Api-Key not sent")
	}
	if ps, err := s.Profiles(ctx); err != nil || len(ps) != 2 {
		t.Fatalf("Profiles = %+v, %v", ps, err)
	}
	if fs, err := s.RootFolders(ctx); err != nil || len(fs) != 1 || fs[0].Path != "/data/media/tv" {
		t.Fatalf("RootFolders = %+v, %v", fs, err)
	}
}

func TestSonarrLookup(t *testing.T) {
	app, s := newSonarr(t, map[string]reply{"GET /api/v3/series/lookup": ok("series_lookup.json")})
	got, err := s.Lookup(ctx, "tvdb:170551")
	if err != nil || len(got) != 2 {
		t.Fatalf("Lookup = %+v, %v", got, err)
	}
	if q := app.last().RawQuery; q != "term=tvdb%3A170551" {
		t.Errorf("query = %q", q)
	}
	g := got[0]
	if g.ID != 0 || g.TVDBID != 170551 || g.Title != "Pioneer One" || g.Year != 2010 || g.OriginalLanguage != "English" ||
		g.Overview != "A reactor." || g.Type != "standard" || len(g.Seasons) != 3 || g.Seasons[1] != 1 {
		t.Errorf("series = %+v", g)
	}
	if got[1].ID != 0 {
		t.Errorf("a lookup result has no id: %+v", got[1])
	}
}

func TestSonarrLookupMissIsEmptyNotAnError(t *testing.T) {
	_, s := newSonarr(t, map[string]reply{"GET /api/v3/series/lookup": {body: "[]"}})
	got, err := s.Lookup(ctx, "Titulo original")
	if err != nil || len(got) != 0 {
		t.Errorf("Lookup = %+v, %v", got, err)
	}
}

func TestSonarrSeries(t *testing.T) {
	_, s := newSonarr(t, map[string]reply{"GET /api/v3/series/5": ok("series.json")})
	got, err := s.Series(ctx, 5)
	if err != nil || got.ID != 5 || got.Type != "anime" || got.Path != "/data/media/tv/Pioneer One" ||
		len(got.Seasons) != 2 || got.OriginalLanguage != "English" {
		t.Errorf("Series = %+v, %v", got, err)
	}
	if _, err := s.Series(ctx, 6); arr.StatusOf(err) != 404 {
		t.Errorf("unknown series: %v", err)
	}
}

func TestSonarrAddSearchesNothingMonitorsNothingAndTags(t *testing.T) {
	app, s := newSonarr(t, map[string]reply{
		"GET /api/v3/series/lookup": ok("series_lookup.json"),
		"GET /api/v3/tag":           ok("tags.json"),
		"POST /api/v3/tag":          ok("tag_created.json"),
		"POST /api/v3/series":       ok("series_added.json"),
	})
	got, err := s.Add(ctx, arr.AddSeries{TVDBID: 170551, QualityProfileID: 4, RootFolder: "/data/media/tv", Type: "anime", Tag: "Fukku"})
	if err != nil || got.ID != 5 || got.Path == "" {
		t.Fatalf("Add = %+v, %v", got, err)
	}
	if q := app.find("GET", "/api/v3/series/lookup")[0].RawQuery; q != "term=tvdb%3A170551" {
		t.Errorf("lookup query = %q", q)
	}
	if l := app.find("POST", "/api/v3/tag")[0].json(t)["label"]; l != "fukku" {
		t.Errorf("tag label = %v", l)
	}
	body := app.find("POST", "/api/v3/series")[0].json(t)
	opts, _ := body["addOptions"].(map[string]any)
	if opts["monitor"] != "none" || opts["searchForMissingEpisodes"] != false || opts["searchForCutoffUnmetEpisodes"] != false {
		t.Errorf("addOptions = %v", opts)
	}
	tags, _ := body["tags"].([]any)
	if body["seriesType"] != "anime" || body["qualityProfileId"] != float64(4) || body["rootFolderPath"] != "/data/media/tv" ||
		len(tags) != 1 || tags[0] != float64(2) {
		t.Errorf("body = %v", body)
	}
	// The lookup's own record went through, including what Fukku does not model.
	if body["title"] != "Pioneer One" || body["tvdbId"] != float64(170551) || body["futureUnknownField"] == nil || body["seasons"] == nil {
		t.Errorf("the lookup record was not sent back: %v", body)
	}
}

func TestSonarrAddDefaultsToStandardAndNoTag(t *testing.T) {
	app, s := newSonarr(t, map[string]reply{
		"GET /api/v3/series/lookup": ok("series_lookup.json"),
		"POST /api/v3/series":       ok("series_added.json"),
	})
	if _, err := s.Add(ctx, arr.AddSeries{TVDBID: 170551, QualityProfileID: 4, RootFolder: "/data/media/tv"}); err != nil {
		t.Fatal(err)
	}
	body := app.find("POST", "/api/v3/series")[0].json(t)
	if tags, _ := body["tags"].([]any); body["seriesType"] != "standard" || len(tags) != 0 {
		t.Errorf("body = %v", body)
	}
	if len(app.find("GET", "/api/v3/tag")) != 0 {
		t.Error("no tag configured, none should be touched")
	}
}

func TestSonarrAddNeedsTheShowInTheLookup(t *testing.T) {
	app, s := newSonarr(t, map[string]reply{"GET /api/v3/series/lookup": ok("series_lookup.json")})
	if _, err := s.Add(ctx, arr.AddSeries{TVDBID: 1}); err == nil {
		t.Error("expected an error for a show the lookup does not return")
	}
	if len(app.find("POST", "/api/v3/series")) != 0 {
		t.Error("nothing may be added")
	}
}

func TestSonarrAddAppError(t *testing.T) {
	_, s := newSonarr(t, map[string]reply{
		"GET /api/v3/series/lookup": ok("series_lookup.json"),
		"POST /api/v3/series":       {status: 400, file: "error_validation.json"},
	})
	_, err := s.Add(ctx, arr.AddSeries{TVDBID: 170551})
	if arr.StatusOf(err) != 400 || !strings.Contains(err.Error(), "Path is not a valid folder") {
		t.Errorf("err = %v", err)
	}
}

func TestSonarrEpisodes(t *testing.T) {
	app, s := newSonarr(t, map[string]reply{"GET /api/v3/episode": ok("episodes.json")})
	eps, err := s.Episodes(ctx, 5)
	if err != nil || len(eps) != 3 {
		t.Fatalf("Episodes = %+v, %v", eps, err)
	}
	if q := app.last().RawQuery; q != "seriesId=5" {
		t.Errorf("query = %q", q)
	}
	want := arr.Episode{ID: 102, SeasonNumber: 1, Number: 2, Title: "Alienation", RuntimeMs: 45 * 60_000, FileID: 77, Monitored: true}
	if eps[1] != want {
		t.Errorf("episode = %+v, want %+v", eps[1], want)
	}
	if eps[0].FileID != 0 || eps[0].Monitored || eps[2].RuntimeMs != 0 {
		t.Errorf("no file, unmonitored and unknown runtime: %+v %+v", eps[0], eps[2])
	}
}

// Sonarr scopes nothing by folder once it has a series id (C6-sonarr), so the proposal never
// sends seriesId or seasonNumber.
func TestSonarrProposalNeverSendsATitleOrSeason(t *testing.T) {
	app, s := newSonarr(t, map[string]reply{"GET /api/v3/manualimport": ok("sonarr_manualimport.json")})
	rows, err := s.Proposal(ctx, "/data/intake/job1")
	if err != nil || len(rows) != 2 {
		t.Fatalf("Proposal = %+v, %v", rows, err)
	}
	q := strings.ToLower(app.last().RawQuery)
	if q != "filterexistingfiles=false&folder=%2fdata%2fintake%2fjob1" {
		t.Errorf("query = %q", q)
	}
	if strings.Contains(q, "seriesid") || strings.Contains(q, "seasonnumber") {
		t.Error("a series id or season in the proposal query makes Sonarr ignore the folder (C6-sonarr)")
	}
	r := rows[0]
	// A row Sonarr cannot match has no series, season or episodes at all (4.0.20, M2-client-sonarr-online).
	if r.SeriesID != 0 || r.SeasonNumber != 0 || len(r.EpisodeIDs) != 0 || r.Size != 555000000 ||
		r.Languages[0].Name != "Spanish" || r.CustomFormatScore != -10000 || r.CustomFormats[0] != "Not Original Language" {
		t.Errorf("row = %+v", r)
	}
	// B7-sonarr: the score is far below any minimum but the app gives no such rejection.
	if len(r.Rejections) != 1 || r.Rejections[0].Reason != "Unknown Series" || !r.Rejections[0].Permanent {
		t.Errorf("rejections = %+v", r.Rejections)
	}
}

func TestSonarrRecheckSendsSeriesSeasonAndEpisodeIDsIncludingDouble(t *testing.T) {
	app, s := newSonarr(t, map[string]reply{
		"GET /api/v3/manualimport":  ok("sonarr_manualimport.json"),
		"POST /api/v3/manualimport": ok("sonarr_manualimport_recheck.json"),
	})
	rows, err := s.Proposal(ctx, "/data/intake/job1")
	if err != nil {
		t.Fatal(err)
	}
	rows[0].SeriesID, rows[0].SeasonNumber, rows[0].EpisodeIDs = 5, 1, []int{102} // moved to another episode
	rows[1].SeriesID, rows[1].SeasonNumber, rows[1].EpisodeIDs = 5, 1, []int{102, 103}
	checked, err := s.Recheck(ctx, rows)
	if err != nil || len(checked) != 2 {
		t.Fatalf("Recheck = %+v, %v", checked, err)
	}
	var sent []map[string]any
	if err := json.Unmarshal(app.find("POST", "/api/v3/manualimport")[0].Body, &sent); err != nil || len(sent) != 2 {
		t.Fatalf("body: %v", err)
	}
	for i, wantIDs := range [][]any{{float64(102)}, {float64(102), float64(103)}} {
		got, _ := sent[i]["episodeIds"].([]any)
		if len(got) != len(wantIDs) || sent[i]["seriesId"] != float64(5) || sent[i]["seasonNumber"] != float64(1) {
			t.Errorf("row %d sent %v", i, sent[i])
			continue
		}
		for j := range got {
			if got[j] != wantIDs[j] {
				t.Errorf("row %d episodeIds = %v", i, got)
			}
		}
		assertUnknownField(t, sent[i])
	}
	// The verdicts come back, with the series and both episodes read from the app's items.
	if checked[0].SeriesID != 5 || len(checked[0].EpisodeIDs) != 1 || len(checked[0].Rejections) != 1 ||
		checked[0].Rejections[0].Reason != "Not an upgrade for existing episode file(s)" {
		t.Errorf("checked[0] = %+v", checked[0])
	}
	if len(checked[1].EpisodeIDs) != 2 || checked[1].EpisodeIDs[1] != 103 || len(checked[1].Rejections) != 0 {
		t.Errorf("a double episode: %+v", checked[1])
	}
}

func TestSonarrImportCommandCarriesEpisodeIDs(t *testing.T) {
	app, s := newSonarr(t, map[string]reply{
		"GET /api/v3/manualimport":  ok("sonarr_manualimport.json"),
		"POST /api/v3/manualimport": ok("sonarr_manualimport_recheck.json"),
		"POST /api/v3/command":      ok("command_queued.json"),
		"GET /api/v3/command/41":    ok("command_completed.json"),
		"GET /api/v3/command":       ok("commands.json"),
	})
	rows, _ := s.Proposal(ctx, "/data/intake/job1")
	checked, err := s.Recheck(ctx, rows)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Import(ctx, checked, "copy")
	if err != nil || id != 41 {
		t.Fatalf("Import = %d, %v", id, err)
	}
	cmd := app.find("POST", "/api/v3/command")[0].json(t)
	files, _ := cmd["files"].([]any)
	if cmd["name"] != "ManualImport" || cmd["importMode"] != "copy" || len(files) != 2 {
		t.Fatalf("command = %v", cmd)
	}
	second := files[1].(map[string]any)
	if ids, _ := second["episodeIds"].([]any); len(ids) != 2 || second["seriesId"] != float64(5) {
		t.Errorf("second file = %v", second)
	}
	if c, err := s.Command(ctx, 41); err != nil || c.Status != "completed" {
		t.Errorf("Command = %+v, %v", c, err)
	}
	if cs, err := s.Commands(ctx); err != nil || len(cs) == 0 {
		t.Errorf("Commands = %+v, %v", cs, err)
	}
	if _, err := s.Import(ctx, nil, "move"); err == nil {
		t.Error("Import with no rows must fail")
	}
}

func TestSonarrBusy(t *testing.T) {
	app, s := newSonarr(t, map[string]reply{"GET /api/v3/queue/details": {body: `[{"id":3,"seriesId":5,"episodeId":101,"status":"downloading"}]`}})
	busy, err := s.Busy(ctx, 5)
	if err != nil || !busy {
		t.Fatalf("Busy = %v, %v", busy, err)
	}
	if q := app.last().RawQuery; q != "seriesId=5" {
		t.Errorf("query = %q", q)
	}
	app.mu.Lock()
	app.routes["GET /api/v3/queue/details"] = reply{body: "[]"}
	app.mu.Unlock()
	if busy, err := s.Busy(ctx, 5); err != nil || busy {
		t.Errorf("empty queue: Busy = %v, %v", busy, err)
	}
}

func TestSonarrSetEpisodesMonitored(t *testing.T) {
	app, s := newSonarr(t, map[string]reply{"PUT /api/v3/episode/monitor": {body: "[]"}})
	if err := s.SetEpisodesMonitored(ctx, []int{102, 103}, true); err != nil {
		t.Fatal(err)
	}
	got := app.last().json(t)
	ids, _ := got["episodeIds"].([]any)
	if len(ids) != 2 || ids[1] != float64(103) || got["monitored"] != true {
		t.Errorf("body = %v", got)
	}
	n := len(app.calls)
	if err := s.SetEpisodesMonitored(ctx, nil, false); err != nil || len(app.calls) != n {
		t.Errorf("no ids must be a no-op: %v", err)
	}
}

func TestSonarrAppErrorAndKey(t *testing.T) {
	_, s := newSonarr(t, map[string]reply{
		"GET /api/v3/episode":         {status: 401, body: `{"message":"bad key ` + testKey + `"}`},
		"GET /api/v3/series/lookup":   {status: 500, body: "boom"},
		"GET /api/v3/queue/details":   {status: 503},
		"PUT /api/v3/episode/monitor": {status: 400, file: "error_validation.json"},
	})
	_, e1 := s.Episodes(ctx, 5)
	_, e2 := s.Lookup(ctx, "x")
	_, e3 := s.Busy(ctx, 5)
	e4 := s.SetEpisodesMonitored(ctx, []int{1}, true)
	for i, err := range []error{e1, e2, e3, e4} {
		var ae *arr.Error
		if !errors.As(err, &ae) {
			t.Errorf("call %d: err = %v, want *arr.Error", i, err)
			continue
		}
		if strings.Contains(err.Error(), testKey) {
			t.Errorf("call %d: the key is in the error: %v", i, err)
		}
	}
	if !strings.Contains(e1.Error(), "bad key ***") || arr.StatusOf(e4) != 400 {
		t.Errorf("e1 = %v, e4 = %v", e1, e4)
	}
}
