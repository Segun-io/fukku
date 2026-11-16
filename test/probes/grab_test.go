//go:build probes

package probes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/test/testenv"
)

// Grabs through the fake indexer (test/env/indexer) and the blackhole download clients the
// seeder configures: "grabbed" means the app fetched the release's NZB into its blackhole
// folder and wrote a grabbed event into the title's history. Nothing is downloaded; where a
// probe needs the grab imported, it puts a generated video into the blackhole's watch folder
// as the finished download.

// releaseGroup ends every generated release name.
const releaseGroup = "FUKKU"

// offer makes the fake indexer offer exactly these releases until the test ends, and starts
// from empty blackhole folders.
func offer(p *probe, a *testenv.Arr, releases ...testenv.Release) []testenv.Release {
	p.t.Helper()
	p.must(testenv.BlackholeAt(env.Data, a.Name).Empty(), "empty the blackhole folders")
	out, err := env.Indexer.Offer(p.ctx, releases...)
	p.must(err, "offer releases on the fake indexer")
	p.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = env.Indexer.Clear(ctx)
		_ = testenv.BlackholeAt(env.Data, a.Name).Empty()
	})
	return out
}

// releaseSize is a size the app's quality definition accepts for a release of that quality
// and runtime: the middle of its MB-per-minute range.
func releaseSize(p *probe, a *testenv.Arr, quality string, minutes int) int64 {
	var defs []M
	p.must(a.Get(p.ctx, "/qualitydefinition", &defs), "quality definitions")
	for _, d := range defs {
		if q, _ := d["quality"].(M); q["name"] == quality {
			lo, _ := d["minSize"].(float64)
			hi, ok := d["maxSize"].(float64)
			per := lo + 50
			if ok && hi > lo {
				per = (lo + hi) / 2
			}
			return int64(per * float64(minutes) * 1024 * 1024)
		}
	}
	p.must(fmt.Errorf("no quality %q", quality), "quality definitions")
	return 0
}

// grabCheck is what one command did with the offered releases.
type grabCheck struct {
	Step     string   `json:"step"`
	Command  string   `json:"command"`
	Requests []string `json:"indexerRequests"`
	NZBs     []string `json:"blackhole"`
	History  []string `json:"grabbedEvents"`
}

func (g grabCheck) grabbed() bool { return len(g.NZBs) > 0 || len(g.History) > 0 }

func (g grabCheck) String() string {
	return fmt.Sprintf("%s: %s; indexer requests %v; grabbed: %v (blackhole %v, new grabbed events %v)",
		g.Step, g.Command, g.Requests, g.grabbed(), g.NZBs, g.History)
}

// tryGrab runs an app command that may grab from the fake indexer. grabs lists the title's
// grabbed history events.
func tryGrab(p *probe, a *testenv.Arr, step string, cmd M, grabs func() []string) grabCheck {
	p.t.Helper()
	hole := testenv.BlackholeAt(env.Data, a.Name)
	p.must(hole.Empty(), "empty the blackhole folders")
	// The app keeps the downloads it last saw in the watch folder in its queue until it looks
	// again, and a queued copy of the release blocks a new grab: make it look now.
	_, err := a.RunCommand(p.ctx, M{"name": "RefreshMonitoredDownloads"}, cmdTimeout)
	p.must(err, "RefreshMonitoredDownloads")
	p.must(env.Indexer.ForgetRequests(p.ctx), "forget indexer requests")
	before := len(grabs())
	c, err := a.RunCommand(p.ctx, cmd, cmdTimeout)
	p.must(err, step)
	// The NZB is written before the command ends; the wait covers a slow disk.
	_ = testenv.WaitFor(p.ctx, 3*time.Second, 250*time.Millisecond, "a grab", func() (bool, error) {
		return len(hole.Grabbed()) > 0, nil
	})
	g := grabCheck{Step: step, Command: strings.TrimSpace(c.Status + " " + c.Message), NZBs: hole.Grabbed()}
	if after := grabs(); len(after) > before {
		g.History = after[before:]
	}
	reqs, err := env.Indexer.Requests(p.ctx)
	p.must(err, "indexer requests")
	for _, r := range reqs {
		app, _, _ := strings.Cut(r.Agent, "/")
		if r.Function == "get" {
			g.Requests = append(g.Requests, fmt.Sprintf("%s fetched the NZB of %v", app, r.Returned))
			continue
		}
		var q []string
		for _, k := range []string{"q", "imdbid", "tmdbid", "tvdbid", "season", "ep", "cat"} {
			if v := r.Query[k]; v != "" {
				q = append(q, k+"="+v)
			}
		}
		g.Requests = append(g.Requests, fmt.Sprintf("%s t=%s %s: %d release(s)", app, r.Function, strings.Join(q, " "), len(r.Returned)))
	}
	p.observe("%s", g)
	return g
}

// grabbedEvents returns the sourceTitle of every grabbed event in a history list, oldest
// first, optionally only those of one episode.
func grabbedEvents(events []M, episodeID int) []string {
	out := []string{}
	for _, e := range events {
		if e["eventType"] != "grabbed" {
			continue
		}
		if episodeID != 0 && testenv.ID(M{"id": e["episodeId"]}) != episodeID {
			continue
		}
		out = append(out, fmt.Sprint(e["sourceTitle"]))
	}
	slices.Reverse(out) // the apps list the newest first
	return out
}

func movieGrabs(p *probe, id int) func() []string {
	return func() []string {
		var events []M
		p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/history/movie?movieId=%d", id), &events), "film history")
		return grabbedEvents(events, 0)
	}
}

func episodeGrabs(p *probe, seriesID, episodeID int) func() []string {
	return func() []string {
		var events []M
		p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/history/series?seriesId=%d", seriesID), &events), "series history")
		return grabbedEvents(events, episodeID)
	}
}

// noUpgradeProfile returns the id of a copy of the probe profile with upgrades not allowed,
// creating it the first time.
func noUpgradeProfile(p *probe, a *testenv.Arr) int {
	const name = "Fukku Probe No Upgrades"
	prof, err := a.FindByName(p.ctx, "/qualityprofile", name)
	p.must(err, "quality profiles")
	if prof != nil {
		return testenv.ID(prof)
	}
	var base M
	p.must(a.Get(p.ctx, fmt.Sprintf("/qualityprofile/%d", profileID(p, a)), &base), "probe profile")
	delete(base, "id")
	base["name"] = name
	base["upgradeAllowed"] = false
	var created M
	p.must(a.Post(p.ctx, "/qualityprofile", base, &created), "create "+name)
	return testenv.ID(created)
}

// ---- B6 on Radarr ------------------------------------------------------------------------

const claimB6 = "A merged file (original + dub) can be replaced by a better release that lacks the dub; unmonitoring is the protection: an unmonitored title is not upgraded by RSS or search"

// b6Film adds the film fresh with a merged file (original + dub) at WEBDL-1080p.
func b6Film(p *probe, f film) (M, int) {
	movie := addFilm(p, f)
	importFilm(p, movie, f, f.dotted()+".1080p.WEB-DL", "eng", "spa")
	return movie, testenv.ID(movie)
}

// B6: a better release without the dub, over a merged file: Manual Import's decision, then
// RSS and searches through the fake indexer, monitored and not. C3: the monitoring editor.
func TestB6UpgradeWouldDropDub(t *testing.T) {
	p := newProbe(t, "B6", "With upgrades allowed, a better release without the dub replaces a merged file; unmonitoring stops it", claimB6, true, "radarr")
	p.alsoSkips(specC3)
	f := elephantsDream
	movie, id := b6Film(p, f)
	dir := p.fresh(intake("B6-upgrade"))
	better := filepath.Join(dir, f.dotted()+".1080p.BluRay.x264.mkv")
	p.video(better, long(f.minutes, "eng"))
	item := itemFor(proposalForMovie(p, dir, id), better)
	p.evidence("betterReleaseProposal", summary(item))
	accepted := !hasRejection(rejections(item), "upgrade", "cutoff")
	p.observe("Manual Import: a Bluray-1080p file without the dub, over the merged WEBDL-1080p: rejections %v (accepted as an upgrade: %v)", rejections(item), accepted)

	c3 := specC3.probe(t)
	c3.must(env.Radarr.Put(c3.ctx, "/movie/editor", M{"movieIds": []int{id}, "monitored": false}, nil), "movie editor")
	var m M
	c3.must(env.Radarr.Get(c3.ctx, fmt.Sprintf("/movie/%d", id), &m), "get movie")
	c3.observe("monitored after the editor call: %v", m["monitored"])
	if m["monitored"] == false {
		c3.confirm()
	} else {
		c3.differ("the movie is still monitored")
	}
	item = itemFor(proposalForMovie(p, dir, id), better)
	p.observe("after unmonitoring, Manual Import still offers it with rejections %v (Manual Import ignores monitoring)", rejections(item))
	p.must(os.RemoveAll(dir), "remove the staged file")

	minutes := f.minutes
	if rt, ok := movie["runtime"].(float64); ok && rt > 0 {
		minutes = int(rt)
	}
	rel := offer(p, env.Radarr, testenv.Release{Title: f.dotted() + ".1080p.BluRay.x264-" + releaseGroup, Category: testenv.CategoryMoviesHD,
		Size: releaseSize(p, env.Radarr, "Bluray-1080p", minutes)})[0]
	p.observe("the fake indexer offers %s (%d MB), a Bluray-1080p release without the dub", rel.Title, rel.Size>>20)

	search := M{"name": "MoviesSearch", "movieIds": []int{id}}
	p.observe("unmonitored film (movie editor), merged WEBDL-1080p file:")
	unRSS := tryGrab(p, env.Radarr, "RSS sync", M{"name": "RssSync"}, movieGrabs(p, id))
	unCutoff := tryGrab(p, env.Radarr, "Wanted, cutoff unmet: search all (CutoffUnmetMoviesSearch)", M{"name": "CutoffUnmetMoviesSearch"}, movieGrabs(p, id))
	unSearch := tryGrab(p, env.Radarr, "the film's Search button (MoviesSearch)", search, movieGrabs(p, id))

	p.observe("monitored film, merged WEBDL-1080p file (control, a fresh copy for each step so the first grab cannot block the second):")
	_, id = b6Film(p, f)
	monRSS := tryGrab(p, env.Radarr, "RSS sync", M{"name": "RssSync"}, movieGrabs(p, id))
	_, id = b6Film(p, f)
	monCutoff := tryGrab(p, env.Radarr, "Wanted, cutoff unmet: search all (CutoffUnmetMoviesSearch)", M{"name": "CutoffUnmetMoviesSearch"}, movieGrabs(p, id))
	_, id = b6Film(p, f)
	monSearch := tryGrab(p, env.Radarr, "the film's Search button (MoviesSearch)", M{"name": "MoviesSearch", "movieIds": []int{id}}, movieGrabs(p, id))

	p.observe("unmonitored film whose quality profile does not allow upgrades (a candidate protection against the forced search):")
	_, id = b6Film(p, f)
	p.must(env.Radarr.Put(p.ctx, "/movie/editor", M{"movieIds": []int{id}, "monitored": false, "qualityProfileId": noUpgradeProfile(p, env.Radarr)}, nil), "movie editor")
	noUp := tryGrab(p, env.Radarr, "the film's Search button (MoviesSearch)", M{"name": "MoviesSearch", "movieIds": []int{id}}, movieGrabs(p, id))
	p.must(env.Radarr.Delete(p.ctx, fmt.Sprintf("/movie/%d?deleteFiles=true&addImportExclusion=false", id)), "delete the film")

	p.evidence("release", rel.Title)
	p.evidence("unmonitored", []grabCheck{unRSS, unCutoff, unSearch})
	p.evidence("monitored", []grabCheck{monRSS, monCutoff, monSearch})
	p.evidence("unmonitoredNoUpgradesProfile", noUp)
	b6Verdict(p, accepted, []grabCheck{monRSS, monCutoff, monSearch}, unRSS, []grabCheck{unCutoff}, unSearch, noUp, "film")
}

// b6Verdict: every step of the monitored control must grab; RSS and the bulk searches must not grab an
// unmonitored title; the title's own search is the "forced search" of M3's exit criterion.
func b6Verdict(p *probe, accepted bool, control []grabCheck, unRSS grabCheck, unBulk []grabCheck, unSearch, noUp grabCheck, what string) {
	p.t.Helper()
	bulkGrabbed := slices.ContainsFunc(unBulk, grabCheck.grabbed)
	var missed []string
	for _, g := range control {
		if !g.grabbed() {
			missed = append(missed, g.Step)
		}
	}
	switch {
	case !accepted:
		p.differ("the better release is rejected by Manual Import; merged files are not at risk from upgrades")
	case len(missed) > 0:
		p.partial(fmt.Sprintf("the monitored control did not grab with %v, so the unmonitored results of those steps prove nothing", missed))
	case unRSS.grabbed() || bulkGrabbed:
		p.differ("an unmonitored %s was upgraded by RSS (%v) or a bulk search (%v): unmonitoring does not protect a merged file", what, unRSS.grabbed(), bulkGrabbed)
	case unSearch.grabbed():
		p.differ("unmonitoring stops RSS and the bulk searches, but the %s's own search grabs the better release without the dub: "+
			"the forced search of M3's exit criterion replaces a merged file unless something else stops it "+
			"(a quality profile without upgrades: grabbed %v)", what, noUp.grabbed())
	default:
		p.confirm()
	}
}

// ---- B6 on Sonarr ------------------------------------------------------------------------

// b6Episode adds the show fresh with a merged S01E01 (original + dub) at WEBDL-1080p, and
// sets season 1 monitored with S01E01 monitored or not.
func b6Episode(p *probe, monitored bool) (M, int, int) {
	series, eps := addShow(p, pioneerOne)
	sid := testenv.ID(series)
	e1 := episode(eps, 1, 1)
	p.check(e1 != nil, "S01E01 exists")
	eid := testenv.ID(e1)
	dir := p.fresh(intake("B6-sonarr"))
	src := filepath.Join(dir, "Pioneer.One.S01E01.1080p.WEB-DL.mkv")
	p.video(src, long(pioneerOne.minutes, "eng", "spa"))
	item := itemFor(proposal(p, env.Sonarr, dir), src)
	p.check(item != nil, "staged episode is in the proposal")
	cmd := manualImport(p, env.Sonarr, []M{item}, M{"seriesId": sid, "episodeIds": []int{eid}}, "move")
	p.check(cmd.Status == "completed" && episodeFile(p, sid, e1) != nil, "setup import of S01E01")

	var s M
	p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/series/%d", sid), &s), "series")
	seasons, _ := s["seasons"].([]any)
	for _, x := range seasons {
		if sm, ok := x.(M); ok && sm["seasonNumber"] == float64(1) {
			sm["monitored"] = true
		}
	}
	s["monitored"] = true
	p.must(env.Sonarr.Put(p.ctx, fmt.Sprintf("/series/%d", sid), s, nil), "monitor season 1")
	p.must(env.Sonarr.Put(p.ctx, "/episode/monitor", M{"episodeIds": []int{eid}, "monitored": monitored}, nil), "episode monitoring")
	return series, sid, eid
}

// B6-sonarr: the same for one episode of a show on Sonarr.
func TestB6SonarrUpgradeWouldDropDub(t *testing.T) {
	p := newProbe(t, "B6-sonarr", "Sonarr: a better release without the dub replaces a merged episode; unmonitoring the episode stops it", claimB6, true, "sonarr")
	series, sid, eid := b6Episode(p, false)
	minutes := pioneerOne.minutes
	if rt, ok := series["runtime"].(float64); ok && rt > 0 {
		minutes = int(rt)
	}
	rel := offer(p, env.Sonarr, testenv.Release{Title: "Pioneer.One.S01E01.1080p.BluRay.x264-" + releaseGroup, Category: testenv.CategoryTVHD,
		Size: releaseSize(p, env.Sonarr, "Bluray-1080p", minutes)})[0]
	p.observe("the fake indexer offers %s (%d MB), a Bluray-1080p release without the dub", rel.Title, rel.Size>>20)

	p.observe("S01E01 unmonitored (series and season 1 monitored), merged WEBDL-1080p file:")
	unRSS := tryGrab(p, env.Sonarr, "RSS sync", M{"name": "RssSync"}, episodeGrabs(p, sid, eid))
	unCutoff := tryGrab(p, env.Sonarr, "Wanted, cutoff unmet: search all (CutoffUnmetEpisodeSearch)", M{"name": "CutoffUnmetEpisodeSearch"}, episodeGrabs(p, sid, eid))
	unSeason := tryGrab(p, env.Sonarr, "the season's Search button (SeasonSearch)", M{"name": "SeasonSearch", "seriesId": sid, "seasonNumber": 1}, episodeGrabs(p, sid, eid))
	unSeries := tryGrab(p, env.Sonarr, "the series' Search button (SeriesSearch)", M{"name": "SeriesSearch", "seriesId": sid}, episodeGrabs(p, sid, eid))
	unSearch := tryGrab(p, env.Sonarr, "the episode's Search button (EpisodeSearch)", M{"name": "EpisodeSearch", "episodeIds": []int{eid}}, episodeGrabs(p, sid, eid))

	p.observe("S01E01 monitored, merged WEBDL-1080p file (control, a fresh copy for each step):")
	_, sid, eid = b6Episode(p, true)
	monRSS := tryGrab(p, env.Sonarr, "RSS sync", M{"name": "RssSync"}, episodeGrabs(p, sid, eid))
	_, sid, eid = b6Episode(p, true)
	monCutoff := tryGrab(p, env.Sonarr, "Wanted, cutoff unmet: search all (CutoffUnmetEpisodeSearch)", M{"name": "CutoffUnmetEpisodeSearch"}, episodeGrabs(p, sid, eid))
	_, sid, eid = b6Episode(p, true)
	monSeason := tryGrab(p, env.Sonarr, "the season's Search button (SeasonSearch)", M{"name": "SeasonSearch", "seriesId": sid, "seasonNumber": 1}, episodeGrabs(p, sid, eid))
	_, sid, eid = b6Episode(p, true)
	monSeries := tryGrab(p, env.Sonarr, "the series' Search button (SeriesSearch)", M{"name": "SeriesSearch", "seriesId": sid}, episodeGrabs(p, sid, eid))
	_, sid, eid = b6Episode(p, true)
	monSearch := tryGrab(p, env.Sonarr, "the episode's Search button (EpisodeSearch)", M{"name": "EpisodeSearch", "episodeIds": []int{eid}}, episodeGrabs(p, sid, eid))

	p.observe("S01E01 unmonitored, the series' quality profile without upgrades:")
	_, sid, eid = b6Episode(p, false)
	var s M
	p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/series/%d", sid), &s), "series")
	s["qualityProfileId"] = noUpgradeProfile(p, env.Sonarr)
	p.must(env.Sonarr.Put(p.ctx, fmt.Sprintf("/series/%d", sid), s, nil), "series profile")
	noUp := tryGrab(p, env.Sonarr, "the episode's Search button (EpisodeSearch)", M{"name": "EpisodeSearch", "episodeIds": []int{eid}}, episodeGrabs(p, sid, eid))
	p.must(env.Sonarr.Delete(p.ctx, fmt.Sprintf("/series/%d?deleteFiles=true", sid)), "delete the series")

	p.evidence("release", rel.Title)
	p.evidence("unmonitored", []grabCheck{unRSS, unCutoff, unSeason, unSeries, unSearch})
	control := []grabCheck{monRSS, monCutoff, monSeason, monSeries, monSearch}
	p.evidence("monitored", control)
	p.evidence("unmonitoredNoUpgradesProfile", noUp)
	b6Verdict(p, true, control, unRSS, []grabCheck{unCutoff, unSeason, unSeries}, unSearch, noUp, "episode")
}

// ---- B3f ---------------------------------------------------------------------------------

// B3f: an upgrade grabbed by search, then imported from the blackhole's watch folder by
// Radarr's completed-download handling (the path of every grab, by search or RSS), with and
// without a label file in the film folder.
func TestB3fUpgradeBySearch(t *testing.T) {
	p := newProbe(t, "B3f", "Radarr: an upgrade grabbed by search and imported as a download, with a label file", claimB3x, true, "radarr")
	f := tearsOfSteel
	scenarios := []struct {
		name  string
		setup func() b3Film
	}{
		{"no label file (control)", func() b3Film {
			movie := addFilm(p, f)
			mf := importFilm(p, movie, f, f.dotted()+".1080p.WEB-DL", "eng")
			folder := movie["path"].(string)
			return b3Film{movie: movie, id: testenv.ID(movie), folder: folder, tracked: filepath.Join(folder, fmt.Sprint(mf["relativePath"]))}
		}},
		{"label file in the film folder", func() b3Film { return b3Start(p, f) }},
	}
	var rows []M
	allImported := true
	for _, sc := range scenarios {
		row := b3fScenario(p, f, sc.name, sc.setup())
		rows = append(rows, row)
		allImported = allImported && row["imported"] == true
	}
	p.evidence("scenarios", rows)
	if !allImported {
		p.partial("a grabbed download was not imported within 4 minutes")
		return
	}
	p.observed()
}

func b3fScenario(p *probe, f film, name string, s b3Film) M {
	p.t.Helper()
	before := movieFile(p, s.id)
	p.observe("%s: before, tracked file %v; records %v; folder %v", name, before["relativePath"], movieFileRecords(p, s.id), listDir(s.folder))
	minutes := f.minutes
	if rt, ok := s.movie["runtime"].(float64); ok && rt > 0 {
		minutes = int(rt)
	}
	rel := offer(p, env.Radarr, testenv.Release{Title: f.dotted() + ".1080p.BluRay.REMUX.AVC-" + releaseGroup, Category: testenv.CategoryMoviesHD,
		Size: releaseSize(p, env.Radarr, "Remux-1080p", minutes)})[0]
	g := tryGrab(p, env.Radarr, "the film's Search button (MoviesSearch)", M{"name": "MoviesSearch", "movieIds": []int{s.id}}, movieGrabs(p, s.id))
	if !g.grabbed() {
		p.differ("%s: the search did not grab the Remux upgrade, so its import could not be observed", name)
		return M{"scenario": name, "grab": g}
	}

	// The download "finishes": the release's folder with a generated video appears in the
	// watch folder, older than the client's scan grace period.
	hole := testenv.BlackholeAt(env.Data, "radarr")
	dl := filepath.Join(hole.Watch, rel.Title)
	video := filepath.Join(dl, rel.Title+".mkv")
	p.video(video, long(f.minutes, "eng"))
	old := time.Now().Add(-time.Hour)
	p.must(os.Chtimes(video, old, old), "age the download")
	p.must(os.Chtimes(dl, old, old), "age the download")
	history := func() []M {
		var events []M
		p.must(env.Radarr.Get(p.ctx, fmt.Sprintf("/history/movie?movieId=%d", s.id), &events), "history")
		return events
	}
	var queue M
	start := time.Now()
	imported := testenv.WaitFor(p.ctx, 4*time.Minute, 5*time.Second, "the download to be imported", func() (bool, error) {
		if _, err := env.Radarr.RunCommand(p.ctx, M{"name": "RefreshMonitoredDownloads"}, cmdTimeout); err != nil {
			return false, err
		}
		_ = env.Radarr.Get(p.ctx, fmt.Sprintf("/queue?movieIds=%d&includeUnknownMovieItems=true", s.id), &queue)
		return slices.ContainsFunc(history(), func(e M) bool {
			return e["eventType"] == "downloadFolderImported" && e["sourceTitle"] == rel.Title
		}), nil
	}) == nil
	took := time.Since(start).Round(time.Second)
	// Radarr removes the replaced file after the import; give it time.
	oldGone := testenv.WaitFor(p.ctx, 10*time.Second, 250*time.Millisecond, "the replaced file to be removed", func() (bool, error) {
		return !exists(s.tracked), nil
	}) == nil

	var types []string
	for _, e := range slices.Backward(history()) {
		types = append(types, fmt.Sprintf("%v (%v)", e["eventType"], e["sourceTitle"]))
	}
	var queueState []string
	if recs, ok := queue["records"].([]any); ok {
		for _, r := range recs {
			if q, ok := r.(M); ok {
				queueState = append(queueState, fmt.Sprintf("%v: %v/%v %v", q["title"], q["trackedDownloadStatus"], q["trackedDownloadState"], q["statusMessages"]))
			}
		}
	}
	after := movieFile(p, s.id)
	recs := movieFileRecords(p, s.id)
	remuxTracked := strings.Contains(fmt.Sprint(after["relativePath"]), "Remux")
	p.observe("%s: the finished download was imported by completed-download handling: %v (after %s; last queue state %v); film history, oldest first: %v",
		name, imported, took, queueState, types)
	label := ""
	if s.label != "" {
		label = "; the label file is " + map[bool]string{true: "still on disk", false: "gone"}[exists(s.label)]
	}
	p.observe("%s: after the import, the replaced tracked file was removed: %v; the film's tracked file is now %v (the Remux: %v); records %v; folder %v%s",
		name, oldGone, after["relativePath"], remuxTracked, recs, listDir(s.folder), label)
	return M{"scenario": name, "grab": g, "imported": imported, "replacedFileRemoved": oldGone, "trackedAfter": after["relativePath"], "remuxIsTracked": remuxTracked,
		"records": recs, "folder": listDir(s.folder), "historyEvents": types, "queue": queueState, "watchFolder": listDir(hole.Watch)}
}
