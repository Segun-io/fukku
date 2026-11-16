//go:build probes

package probes

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/test/testenv"
)

// radarrClient is internal/arr's real client, pointed at the test Radarr (testenv has already
// refused any other address). The probes' own helpers are used only for the setup and the
// cleanup around it.
func radarrClient() arr.MovieManager {
	return arr.NewRadarr(config.Arr{
		URL:            env.Radarr.Base,
		APIKey:         config.Secret(os.Getenv("FUKKU_TEST_RADARR_KEY")),
		RequestTimeout: config.Duration(60 * time.Second),
	})
}

// TestArrClientOffline runs the client against the test Radarr for what needs no metadata
// service: status, profiles, root folders, the proposal of a staged folder, the commands.
func TestArrClientOffline(t *testing.T) {
	p := newProbe(t, "M2-client", "internal/arr client against Radarr (offline part)",
		"The arr client's Status, Profiles, RootFolders, Proposal (no title id) and Commands work against the pinned Radarr", false, "radarr")
	rc := radarrClient()

	st, err := rc.Status(p.ctx)
	p.must(err, "Status")
	p.check(st.AppName == "Radarr" && st.Version != "", fmt.Sprintf("status looks like Radarr: %+v", st))
	profiles, err := rc.Profiles(p.ctx)
	p.must(err, "Profiles")
	var fukku *arr.Profile
	for i := range profiles {
		if profiles[i].Name == "Fukku Probe" {
			fukku = &profiles[i]
		}
	}
	p.check(fukku != nil, "quality profile Fukku Probe exists (make env-up)")
	roots, err := rc.RootFolders(p.ctx)
	p.must(err, "RootFolders")
	p.check(len(roots) > 0, "Radarr has a root folder")

	dir := p.fresh(intake("M2-client"))
	src := filepath.Join(dir, bigBuckBunny.dotted()+".1080p.WEB-DL.mkv")
	p.video(src, long(bigBuckBunny.minutes, "eng"))
	rows, err := rc.Proposal(p.ctx, dir)
	p.must(err, "Proposal")
	var row *arr.Row
	for i := range rows {
		if rows[i].Path == src {
			row = &rows[i]
		}
	}
	p.check(row != nil, "the staged file is in the proposal of its folder")
	p.check(row.Size > 0 && row.Quality.Name != "" && len(row.Raw) > 0, fmt.Sprintf("the row is filled in: %+v", row.Quality))
	cmds, err := rc.Commands(p.ctx)
	p.must(err, "Commands")

	p.evidence("status", st)
	p.evidence("proposal", M{"quality": row.Quality.Name, "languages": row.Languages, "rejections": row.Rejections})
	p.observe("Radarr %s; proposal for the folder: %s, %d rejection(s); %d recent command(s)", st.Version, row.Quality.Name, len(row.Rejections), len(cmds))
	p.confirm()
}

// TestArrClientOnline adds a film through the client (the lookup needs the internet) and runs
// the plan's import flow with it: proposal for the job folder, re-check with the film,
// import by rename, the tracked file.
func TestArrClientOnline(t *testing.T) {
	p := newProbe(t, "M2-client-online", "internal/arr client against Radarr (lookup, add, re-check, import)",
		"The arr client finds a film, adds it (search off, tagged), re-checks a proposal with it, imports by rename and reports the tracked file", true, "radarr")
	p.needMeta("movie lookup")
	rc := radarrClient()
	f := bigBuckBunny

	// Start from a library without the film.
	var existing []M
	p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/movie?tmdbId=%d", f.tmdb), &existing), "find movie")
	for _, m := range existing {
		p.must(env.Radarr.Delete(p.ctx, fmt.Sprintf("/movie/%d?deleteFiles=true&addImportExclusion=false", testenv.ID(m))), "delete movie")
	}

	var found []arr.Movie
	p.meta(testenv.WaitFor(p.ctx, 4*time.Minute, 5*time.Second, "movie lookup", func() (bool, error) {
		var err error
		found, err = rc.Lookup(p.ctx, fmt.Sprintf("tmdb:%d", f.tmdb))
		return err == nil && len(found) > 0, err
	}), "lookup")
	p.check(found[0].TMDBID == f.tmdb && found[0].ID == 0, fmt.Sprintf("lookup finds the film, not in the library: %+v", found[0]))

	profiles, err := rc.Profiles(p.ctx)
	p.must(err, "Profiles")
	profile := 0
	for _, pr := range profiles {
		if pr.Name == "Fukku Probe" {
			profile = pr.ID
		}
	}
	p.check(profile != 0, "quality profile Fukku Probe exists (make env-up)")
	movie, err := rc.Add(p.ctx, arr.AddMovie{TMDBID: f.tmdb, QualityProfileID: profile, RootFolder: "/data/media/movies", Monitored: true, Tag: "Fukku-Probe"})
	p.meta(err, "Add")
	p.fresh(movie.Path)
	p.check(movie.ID > 0 && movie.File == nil, fmt.Sprintf("added without a file: %+v", movie))
	var raw M
	p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/movie/%d", movie.ID), &raw), "movie")
	p.check(len(fmt.Sprint(raw["tags"])) > 2, "the film carries the tag")

	dir := p.fresh(intake("M2-client-online"))
	src := filepath.Join(dir, f.dotted()+".1080p.WEB-DL.mkv")
	p.video(src, long(f.minutes, "eng"))
	srcInode := inode(src)
	rows, err := rc.Proposal(p.ctx, dir)
	p.must(err, "Proposal")
	p.check(len(rows) == 1 && rows[0].Path == src, "the folder's proposal has the staged file")
	rows[0].MovieID = movie.ID
	checked, err := rc.Recheck(p.ctx, rows)
	p.must(err, "Recheck")
	p.check(len(checked) == 1 && checked[0].MovieID == movie.ID, fmt.Sprintf("re-check keeps the film: %+v", checked))

	id, err := rc.Import(p.ctx, checked, "move")
	p.must(err, "Import")
	var cmd arr.Command
	p.must(testenv.WaitFor(p.ctx, cmdTimeout, 500*time.Millisecond, "import command", func() (bool, error) {
		var err error
		cmd, err = rc.Command(p.ctx, id)
		return cmd.Status != "queued" && cmd.Status != "started", err
	}), "Command")
	p.check(cmd.Status == "completed", "import completed: "+cmd.Status+" "+cmd.Message)
	all, err := rc.Commands(p.ctx)
	p.must(err, "Commands")
	listed := false
	for _, c := range all {
		listed = listed || c.ID == id
	}
	p.check(listed, "the import is in the command list")

	got, err := rc.Movie(p.ctx, movie.ID)
	p.must(err, "Movie")
	p.check(got.File != nil, "the film has a tracked file")
	sameInode := inode(got.File.Path) == srcInode
	p.evidence("movieFile", M{"quality": got.File.Quality.Name, "runtimeMs": got.File.RuntimeMs, "sameInode": sameInode, "sourceGone": !exists(src)})
	p.observe("imported %s (%s, %d ms); same inode: %v; source gone: %v", filepath.Base(got.File.Path), got.File.Quality.Name, got.File.RuntimeMs, sameInode, !exists(src))
	p.check(sameInode && !exists(src), "the import was a rename")
	p.check(got.File.RuntimeMs > 8*60*1000 && got.File.RuntimeMs < 10*60*1000, fmt.Sprintf("mediaInfo.runTime parses (~9 min): %d ms", got.File.RuntimeMs))

	busy, err := rc.Busy(p.ctx, movie.ID)
	p.must(err, "Busy")
	p.check(!busy, "nothing is downloading")
	p.must(rc.SetMonitored(p.ctx, movie.ID, false), "SetMonitored")
	got, err = rc.Movie(p.ctx, movie.ID)
	p.must(err, "Movie")
	p.check(!got.Monitored, "the film is no longer monitored")
	p.confirm()
}

// sonarrClient is internal/arr's real client, pointed at the test Sonarr.
func sonarrClient() arr.SeriesManager {
	return arr.NewSonarr(config.Arr{
		URL:            env.Sonarr.Base,
		APIKey:         config.Secret(os.Getenv("FUKKU_TEST_SONARR_KEY")),
		RequestTimeout: config.Duration(60 * time.Second),
	})
}

// TestArrClientSonarrOnline runs the plan's flow 3 import with the Sonarr client: lookup, add
// (search off, nothing monitored, tagged), the folder's proposal without a series id, a
// re-check that sets one episode, one that moves the row to another episode, one that sets
// two episodes, import by rename and the episodes' file ids. It also confirms the spec calls
// (PUT /episode/monitor, re-check with edited episodeIds) that #18 lists.
func TestArrClientSonarrOnline(t *testing.T) {
	p := newProbe(t, "M2-client-sonarr-online", "internal/arr client against Sonarr (lookup, add, re-check, import, monitoring)",
		"The arr client finds a show, adds it (search off, nothing monitored, tagged), proposes a folder without a series id, re-checks rows to one episode, another episode and two episodes, imports by rename and reports episode files",
		true, "sonarr")
	p.needMeta("series lookup")
	sc := sonarrClient()
	s := pioneerOne

	// Start from a library without the show.
	var existing []M
	p.must(env.Sonarr.Get(p.ctx, "/series", &existing), "series")
	for _, m := range existing {
		if int(m["tvdbId"].(float64)) == s.tvdb {
			p.must(env.Sonarr.Delete(p.ctx, fmt.Sprintf("/series/%d?deleteFiles=true", testenv.ID(m))), "delete series")
		}
	}

	var found []arr.Series
	p.meta(testenv.WaitFor(p.ctx, 4*time.Minute, 5*time.Second, "series lookup", func() (bool, error) {
		var err error
		found, err = sc.Lookup(p.ctx, fmt.Sprintf("tvdb:%d", s.tvdb))
		return err == nil && len(found) > 0, err
	}), "lookup")
	p.check(found[0].TVDBID == s.tvdb && found[0].ID == 0, fmt.Sprintf("lookup finds the show, not in the library: %+v", found[0]))

	profiles, err := sc.Profiles(p.ctx)
	p.must(err, "Profiles")
	profile := 0
	for _, pr := range profiles {
		if pr.Name == "Fukku Probe" {
			profile = pr.ID
		}
	}
	p.check(profile != 0, "quality profile Fukku Probe exists (make env-up)")
	series, err := sc.Add(p.ctx, arr.AddSeries{TVDBID: s.tvdb, QualityProfileID: profile, RootFolder: "/data/media/tv", Type: "standard", Tag: "Fukku-Probe"})
	p.meta(err, "Add")
	p.fresh(series.Path)
	p.check(series.ID > 0 && series.Type == "standard", fmt.Sprintf("added: %+v", series))
	var raw M
	p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/series/%d", series.ID), &raw), "series")
	p.check(len(fmt.Sprint(raw["tags"])) > 2, "the show carries the tag")

	var eps []arr.Episode
	err = testenv.WaitFor(p.ctx, 2*time.Minute, 2*time.Second, "episodes", func() (bool, error) {
		var err error
		eps, err = sc.Episodes(p.ctx, series.ID)
		return len(eps) > 0, err
	})
	if err != nil && len(eps) == 0 && p.ctx.Err() == nil && testenv.StatusOf(err) == 0 {
		p.outage("no episodes after the add: "+err.Error(), "no episodes after the add")
	}
	p.must(err, "Episodes")
	pick := func(season, number int) arr.Episode {
		for _, e := range eps {
			if e.SeasonNumber == season && e.Number == number {
				return e
			}
		}
		p.check(false, fmt.Sprintf("S%02dE%02d exists", season, number))
		return arr.Episode{}
	}
	e1, e2, e3 := pick(1, 1), pick(1, 2), pick(1, 3)
	anyMonitored := false
	for _, e := range eps {
		anyMonitored = anyMonitored || e.Monitored
	}
	p.check(!anyMonitored, "nothing is monitored after the add")
	busy, err := sc.Busy(p.ctx, series.ID)
	p.must(err, "Busy")
	p.check(!busy, "nothing is downloading")

	dir := p.fresh(intake("M2-client-sonarr-online"))
	one := filepath.Join(dir, "Pioneer.One.S01E01.1080p.WEB-DL.mkv")
	two := filepath.Join(dir, "Pioneer.One.S01E02.1080p.WEB-DL.mkv")
	p.video(one, long(s.minutes, "eng"))
	p.video(two, long(s.minutes, "eng"))
	inodeOne := inode(one)
	rows, err := sc.Proposal(p.ctx, dir)
	p.must(err, "Proposal")
	var r1, r2 *arr.Row
	for i := range rows {
		switch rows[i].Path {
		case one:
			r1 = &rows[i]
		case two:
			r2 = &rows[i]
		}
	}
	p.check(r1 != nil && r2 != nil, "the folder's proposal has both staged files")
	proposedSeries, proposedEpisodes := r1.SeriesID, r1.EpisodeIDs

	// The first row is set to S01E01, the second to S01E02 and S01E03 (a double).
	r1.SeriesID, r1.SeasonNumber, r1.EpisodeIDs = series.ID, 1, []int{e1.ID}
	r2.SeriesID, r2.SeasonNumber, r2.EpisodeIDs = series.ID, 1, []int{e2.ID, e3.ID}
	checked, err := sc.Recheck(p.ctx, []arr.Row{*r1, *r2})
	p.must(err, "Recheck")
	p.check(len(checked) == 2 && checked[0].SeriesID == series.ID && len(checked[0].EpisodeIDs) == 1 && checked[0].EpisodeIDs[0] == e1.ID,
		fmt.Sprintf("re-check sets one episode: %+v", checked))
	p.check(len(checked) == 2 && len(checked[1].EpisodeIDs) == 2, "re-check sets two episodes")
	p.observe("verified shapes (Sonarr 4.0.20): the proposal already matches a row by its file name (series id %d, episode ids %v); a re-check answers with seriesId and full episode objects (series id %d, episode ids %v and %v) and without size or name; episodeFileId and the shared file of a double come back on GET /episode",
		proposedSeries, proposedEpisodes, checked[0].SeriesID, checked[0].EpisodeIDs, checked[1].EpisodeIDs)

	// Move the first row to another episode (S01E03 alone) and re-check again.
	moved := checked[0]
	moved.EpisodeIDs = []int{e3.ID}
	again, err := sc.Recheck(p.ctx, []arr.Row{moved})
	p.must(err, "Recheck (moved)")
	p.check(len(again) == 1 && len(again[0].EpisodeIDs) == 1 && again[0].EpisodeIDs[0] == e3.ID,
		fmt.Sprintf("re-check moves the row to another episode: %+v", again))
	// Back to S01E01 for the import.
	back := again[0]
	back.EpisodeIDs = []int{e1.ID}
	fixed, err := sc.Recheck(p.ctx, []arr.Row{back})
	p.must(err, "Recheck (back)")
	p.check(len(fixed) == 1, "re-check returns the row")
	checked[0] = fixed[0]

	id, err := sc.Import(p.ctx, checked, "move")
	p.must(err, "Import")
	var cmd arr.Command
	p.must(testenv.WaitFor(p.ctx, cmdTimeout, 500*time.Millisecond, "import command", func() (bool, error) {
		var err error
		cmd, err = sc.Command(p.ctx, id)
		return cmd.Status != "queued" && cmd.Status != "started", err
	}), "Command")
	p.check(cmd.Status == "completed", "import completed: "+cmd.Status+" "+cmd.Message)

	eps, err = sc.Episodes(p.ctx, series.ID)
	p.must(err, "Episodes")
	withFile := map[int]int{}
	for _, e := range eps {
		if e.FileID != 0 {
			withFile[e.ID] = e.FileID
		}
	}
	p.check(withFile[e1.ID] != 0 && withFile[e2.ID] != 0 && withFile[e3.ID] != 0, fmt.Sprintf("episodeFileId is set for all three episodes: %v", withFile))
	p.check(withFile[e2.ID] == withFile[e3.ID], "the double episode's two episodes share one file")
	p.check(!exists(one) && !exists(two), "the sources are gone (moved)")
	var file M
	p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/episodefile/%d", withFile[e1.ID]), &file), "episode file")
	sameInode := inode(filepath.Join(series.Path, fmt.Sprint(file["relativePath"]))) == inodeOne
	p.check(sameInode, "the import was a rename")

	// The spec calls: monitoring.
	p.must(sc.SetEpisodesMonitored(p.ctx, []int{e1.ID, e2.ID}, true), "SetEpisodesMonitored")
	eps, err = sc.Episodes(p.ctx, series.ID)
	p.must(err, "Episodes")
	monitored := map[int]bool{}
	for _, e := range eps {
		monitored[e.ID] = e.Monitored
	}
	p.check(monitored[e1.ID] && monitored[e2.ID] && !monitored[e3.ID], fmt.Sprintf("only the two episodes are monitored: %v", monitored))
	p.must(sc.SetEpisodesMonitored(p.ctx, []int{e1.ID, e2.ID}, false), "SetEpisodesMonitored (off)")
	eps, err = sc.Episodes(p.ctx, series.ID)
	p.must(err, "Episodes")
	for _, e := range eps {
		p.check(!e.Monitored, fmt.Sprintf("episode %d is unmonitored again", e.ID))
	}
	p.evidence("import", M{"status": cmd.Status, "files": withFile, "sameInode": sameInode})
	p.observe("added %s, re-checked one, moved and double rows, imported %d episodes by rename; same inode: %v", series.Title, len(withFile), sameInode)
	p.confirm()
}
