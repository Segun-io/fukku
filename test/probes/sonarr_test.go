//go:build probes

package probes

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/test/fixtures"
	"github.com/segun-io/fukku/test/testenv"
)

// addShow adds the show fresh, with nothing monitored and search off (flow 3), and waits
// for its episodes.
func addShow(p *probe, s show) (M, []M) {
	so := env.Sonarr
	p.needMeta("series lookup")
	var existing []M
	p.must(so.Get(p.ctx, "/series", &existing), "series")
	for _, m := range existing {
		if int(m["tvdbId"].(float64)) == s.tvdb {
			p.must(so.Delete(p.ctx, fmt.Sprintf("/series/%d?deleteFiles=true", testenv.ID(m))), "delete series")
		}
	}
	var lookup []M
	// The lookup goes through Sonarr to its public metadata service, which can be slow.
	p.meta(testenv.WaitFor(p.ctx, 4*time.Minute, 5*time.Second, "series lookup", func() (bool, error) {
		err := so.Get(p.ctx, "/series/lookup"+testenv.Q("term", fmt.Sprintf("tvdb:%d", s.tvdb)), &lookup)
		return err == nil && len(lookup) > 0, err
	}), "lookup")
	p.check(len(lookup) > 0, "lookup found the show")
	body := lookup[0]
	body["qualityProfileId"] = profileID(p, so)
	body["rootFolderPath"] = "/data/media/tv"
	body["seasonFolder"] = true
	body["monitored"] = true
	body["seriesType"] = "standard"
	body["addOptions"] = M{"monitor": "none", "searchForMissingEpisodes": false, "searchForCutoffUnmetEpisodes": false}
	var series M
	p.meta(so.Post(p.ctx, "/series", body, &series), "add series")
	p.fresh(series["path"].(string))
	var episodes []M
	// Sonarr fills in the episodes from its metadata service after the add; none appearing
	// means that service did not answer.
	err := testenv.WaitFor(p.ctx, 2*time.Minute, 2*time.Second, "episodes", func() (bool, error) {
		err := so.Get(p.ctx, fmt.Sprintf("/episode?seriesId=%d", testenv.ID(series)), &episodes)
		return len(episodes) > 0, err
	})
	if err != nil && len(episodes) == 0 && p.ctx.Err() == nil && testenv.StatusOf(err) == 0 {
		p.outage("no episodes after the add: "+err.Error(), "no episodes after the add")
	}
	p.must(err, "episodes")
	return series, episodes
}

func episode(eps []M, season, number int) M {
	for _, e := range eps {
		if int(e["seasonNumber"].(float64)) == season && int(e["episodeNumber"].(float64)) == number {
			return e
		}
	}
	return nil
}

func episodeFile(p *probe, seriesID int, ep M) M {
	var eps []M
	p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/episode?seriesId=%d", seriesID), &eps), "episodes")
	cur := episode(eps, int(ep["seasonNumber"].(float64)), int(ep["episodeNumber"].(float64)))
	fid := int(cur["episodeFileId"].(float64))
	if fid == 0 {
		return nil
	}
	var f M
	p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/episodefile/%d", fid), &f), "episode file")
	return f
}

var (
	specC6Sonarr = spec{"C6-sonarr", "Sonarr Manual Import proposal: folder and series scoping",
		"Plan §6: `GET /manualimport?folder=<job dir>` + `seriesId` + `seasonNumber` proposes the job's files", []string{"sonarr"}}
	specB8Sonarr = spec{"B8-sonarr", "Sonarr Manual Import with explicit episode ids imports despite rejections",
		"Manual Import with an explicit episode id is expected to import even when the proposal lists rejections (unverified)", []string{"sonarr"}}
	specC3Sonarr = spec{"C3-sonarr", "Sonarr episode monitoring calls",
		"`PUT /api/v3/episode/monitor {episodeIds, monitored}` changes exactly those episodes; the season flag is set through `PUT /api/v3/series/<id>` (spec in the plan)", []string{"sonarr"}}
	specC2Sonarr = spec{"C2-sonarr", "Sonarr re-checks Manual Import rows with edited episodeIds",
		"`POST /api/v3/manualimport` with edited `episodeIds` (a row moved by one episode, a row given two) returns a fresh decision, and a double imports as one file for two episodes (spec in the plan)", []string{"sonarr"}}
	specB5Sonarr = spec{"B5-sonarr", "Sonarr tracks existing subtitles and moves them on rename",
		"With `importExtraFiles` off, an `.srt` next to an episode file is tracked as an extra file and renamed with the episode (plan §7 B5)", []string{"sonarr"}}
	specC7Sonarr = spec{"C7-sonarr", "Sonarr's proposal for a file about twice the expected runtime",
		"Plan §8 flow 3 step 4 plans a double-episode hint for a file about twice the runtime; whether Sonarr's proposal says anything is unverified", []string{"sonarr"}}
	specC8Sonarr = spec{"C8-sonarr", "Sonarr: custom-format score, the profile's minimum and the series' original language",
		"Evidence for decision #21: a dubbed row's customFormatScore next to the profile's minFormatScore and the series' original language, and whether such a row is still imported", []string{"sonarr"}}
	specB3Sonarr = spec{"B3-sonarr", "Sonarr adopts an untracked upgrade in the season folder on rescan",
		"Assumed to behave like Radarr (B3) until a probe shows otherwise", []string{"sonarr"}}
)

// TestSonarr runs the Sonarr probes in order on one show: B7, B8, then B3 for episodes.
func TestSonarr(t *testing.T) {
	setup := newProbe(t, "B7-sonarr", "Sonarr-style profiles reject a not-original-language episode in the proposal",
		"With \"not original language\" scored very negatively, Manual Import proposals show a rejection for a dubbed episode and none for the original",
		true, "sonarr")
	setup.alsoSkips(specC6Sonarr, specB8Sonarr, specB3Sonarr, specC3Sonarr, specC2Sonarr, specB5Sonarr, specC7Sonarr, specC8Sonarr)
	series, eps := addShow(setup, pioneerOne)
	sid := testenv.ID(series)
	e1, e2 := episode(eps, 1, 1), episode(eps, 1, 2)
	setup.check(e1 != nil && e2 != nil, "S01E01 and S01E02 exist")

	dir := setup.fresh(intake("sonarr"))
	dub := filepath.Join(dir, "Pioneer.One.S01E01.1080p.WEB-DL.SPANISH.mkv")
	orig := filepath.Join(dir, "Pioneer.One.S01E02.1080p.WEB-DL.mkv")
	setup.video(dub, long(pioneerOne.minutes, "spa"))
	setup.video(orig, long(pioneerOne.minutes, "eng"))
	items := proposal(setup, env.Sonarr, dir)
	scoped := proposal(setup, env.Sonarr, dir, "seriesId", fmt.Sprint(sid), "seasonNumber", "1")
	{
		c6 := specC6Sonarr.probe(t)
		byFolder, withID := itemFor(items, orig) != nil, itemFor(scoped, orig) != nil
		c6.evidence("folderOnlyFindsFile", byFolder)
		c6.evidence("folderPlusSeriesIdFindsFile", withID)
		c6.observe("Sonarr: folder only finds the files: %v; folder + seriesId + seasonNumber finds them: %v", byFolder, withID)
		if byFolder && withID {
			c6.confirm()
		} else {
			c6.differ("folder %v, folder+seriesId %v: scope Sonarr proposals like Radarr's (folder, then re-check)", byFolder, withID)
		}
	}
	sd, so := summary(itemFor(items, dub)), summary(itemFor(items, orig))
	setup.evidence("dub", sd)
	setup.evidence("original", so)
	setup.observe("dub: languages %v, formats %v, score %v, rejections %v", sd["languages"], sd["customFormats"], sd["customFormatScore"], sd["rejections"])
	setup.observe("original: languages %v, formats %v, score %v, rejections %v", so["languages"], so["customFormats"], so["customFormatScore"], so["rejections"])
	dubRejected := len(sd["rejections"].([]string)) > 0
	origClean := len(so["rejections"].([]string)) == 0
	switch {
	case dubRejected && origClean:
		setup.confirm()
	case !dubRejected:
		setup.differ("the dubbed episode has no rejection: the UI will not see a language problem to warn about")
	default:
		setup.differ("the original-language episode is rejected too: %v", so["rejections"])
	}

	t.Run("B8", func(t *testing.T) {
		p := specB8Sonarr.probe(t)
		// The custom-format score gives no rejection (B7-sonarr), so use "not an upgrade":
		// import the dub, then a lower-quality copy over it.
		cmd := manualImport(p, env.Sonarr, []M{itemFor(items, dub)}, M{"seriesId": sid, "episodeIds": []int{testenv.ID(e1)}}, "move")
		before := episodeFile(p, sid, e1)
		p.check(cmd.Status == "completed" && before != nil, "setup import of S01E01")
		wdir := p.fresh(intake("sonarr-worse"))
		worse := filepath.Join(wdir, "Pioneer.One.S01E01.720p.HDTV.x264.mkv")
		p.video(worse, longAt(pioneerOne.minutes, 1280, 720, "eng"))
		item := itemFor(proposal(p, env.Sonarr, wdir), worse)
		rs := rejections(item)
		p.evidence("proposal", summary(item))
		p.check(len(rs) > 0, "the worse copy must carry a rejection")
		cmd = manualImport(p, env.Sonarr, []M{item}, M{"seriesId": sid, "episodeIds": []int{testenv.ID(e1)}}, "move")
		f := episodeFile(p, sid, e1)
		imported := f != nil && testenv.ID(f) != testenv.ID(before)
		p.evidence("command", M{"status": cmd.Status, "result": cmd.Result, "message": cmd.Message})
		if f != nil {
			p.evidence("episodeFile", f["relativePath"])
		}
		p.observe("rejections %v; ManualImport %s/%s; S01E01 file now %v; replaced: %v", rs, cmd.Status, cmd.Result, f["relativePath"], imported)
		if imported {
			p.confirm()
		} else {
			p.differ("the explicit import did not replace the episode file: the override flow must change")
		}
	})

	t.Run("B3", func(t *testing.T) {
		p := specB3Sonarr.probe(t)
		item := itemFor(items, orig)
		cmd := manualImport(p, env.Sonarr, []M{item}, M{"seriesId": sid, "episodeIds": []int{testenv.ID(e2)}}, "move")
		tracked := episodeFile(p, sid, e2)
		p.check(cmd.Status == "completed" && tracked != nil, "setup import of S01E02")
		folder := filepath.Dir(filepath.Join(series["path"].(string), fmt.Sprint(tracked["relativePath"])))
		title := fmt.Sprint(e2["title"])
		losing := filepath.Join(folder, fmt.Sprintf("Pioneer One - S01E02 - %s - Español.mkv", title))
		p.video(losing, long(pioneerOne.minutes, "spa"))
		rescan := func() {
			c, err := env.Sonarr.RunCommand(p.ctx, M{"name": "RescanSeries", "seriesId": sid}, cmdTimeout)
			p.must(err, "RescanSeries")
			p.check(c.Status == "completed", "RescanSeries completed")
		}
		prop := proposal(p, env.Sonarr, folder)
		p.evidence("losingProposal", summary(itemFor(prop, losing)))
		rescan()
		after := episodeFile(p, sid, e2)
		losingAdopted := strings.Contains(fmt.Sprint(after["relativePath"]), "Español")
		p.observe("label without quality token: rejections %v; adopted on rescan: %v", rejections(itemFor(prop, losing)), losingAdopted)

		winning := filepath.Join(folder, fmt.Sprintf("Pioneer One - S01E02 - %s Bluray-1080p.mkv", title))
		p.video(winning, long(pioneerOne.minutes, "eng"))
		prop = proposal(p, env.Sonarr, folder)
		p.evidence("winningProposal", summary(itemFor(prop, winning)))
		rescan()
		after = episodeFile(p, sid, e2)
		winningAdopted := strings.Contains(fmt.Sprint(after["relativePath"]), "Bluray-1080p")
		p.evidence("afterRescan", M{"relativePath": after["relativePath"], "folder": listDir(folder)})
		p.observe("Bluray-1080p name: rejections %v; adopted on rescan: %v; folder %v", rejections(itemFor(prop, winning)), winningAdopted, listDir(folder))
		switch {
		case losingAdopted:
			p.differ("a label without a quality token was adopted: episode second versions are not safe")
		case winningAdopted:
			p.confirm()
		default:
			p.differ("Sonarr did not adopt an upgrade-named file on rescan: it does not behave like Radarr here")
		}
	})

	// ---- gaps found in M0 (issue #18): monitoring, re-check, subtitles, runtime, language score ----

	// stage puts a generated file in a fresh intake folder and returns the folder and file.
	stage := func(p *probe, label, name string, v fixtures.Video) (string, string) {
		d := p.fresh(intake("sonarr-" + label))
		f := filepath.Join(d, name)
		p.video(f, v)
		return d, f
	}
	episodeMonitored := func(p *probe) map[int]bool {
		var list []M
		p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/episode?seriesId=%d", sid), &list), "episodes")
		out := map[int]bool{}
		for _, e := range list {
			out[testenv.ID(e)], _ = e["monitored"].(bool)
		}
		return out
	}
	monitoredIDs := func(m map[int]bool) []int {
		var out []int
		for id, on := range m {
			if on {
				out = append(out, id)
			}
		}
		slices.Sort(out)
		return out
	}
	epNum := func(n int) M {
		e := episode(eps, 1, n)
		setup.check(e != nil, fmt.Sprintf("S01E%02d exists", n))
		return e
	}

	t.Run("C3-sonarr", func(t *testing.T) {
		p := specC3Sonarr.probe(t)
		a, b, c := testenv.ID(epNum(3)), testenv.ID(epNum(4)), testenv.ID(epNum(5))
		before := monitoredIDs(episodeMonitored(p))
		p.must(env.Sonarr.Put(p.ctx, "/episode/monitor", M{"episodeIds": []int{a, b}, "monitored": true}, nil), "episode/monitor on")
		on := monitoredIDs(episodeMonitored(p))
		p.must(env.Sonarr.Put(p.ctx, "/episode/monitor", M{"episodeIds": []int{a}, "monitored": false}, nil), "episode/monitor off")
		off := monitoredIDs(episodeMonitored(p))
		exact := slices.Equal(on, slices.Sorted(slices.Values([]int{a, b}))) && slices.Equal(off, []int{b})
		p.evidence("episodes", M{"monitoredBefore": before, "afterOn": on, "afterOff": off, "ids": []int{a, b, c}})
		p.observe("monitored before %v; after PUT /episode/monitor on %v (asked for %d and %d): %v; after switching %d off: %v; changes exactly the listed episodes: %v", before, on, a, b, on, a, off, exact)

		// The season flag through PUT /series/<id>.
		var s M
		p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/series/%d", sid), &s), "series")
		seasons, _ := s["seasons"].([]any)
		for _, x := range seasons {
			if sm, ok := x.(M); ok && int(sm["seasonNumber"].(float64)) == 1 {
				sm["monitored"] = true
			}
		}
		p.must(env.Sonarr.Put(p.ctx, fmt.Sprintf("/series/%d", sid), s, nil), "PUT /series")
		var s2 M
		p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/series/%d", sid), &s2), "series")
		var flag any
		for _, x := range s2["seasons"].([]any) {
			if sm, ok := x.(M); ok && int(sm["seasonNumber"].(float64)) == 1 {
				flag = sm["monitored"]
			}
		}
		afterSeason := monitoredIDs(episodeMonitored(p))
		p.evidence("season", M{"seasonOneMonitored": flag, "episodesMonitoredAfter": len(afterSeason), "episodesTotal": len(eps)})
		p.observe("season 1 set monitored through PUT /series/<id>: the flag reads %v; episodes monitored afterwards %d of %d (before the call: %v)", flag, len(afterSeason), len(eps), off)
		// restore: nothing monitored, as the other probes expect
		for _, x := range s2["seasons"].([]any) {
			if sm, ok := x.(M); ok {
				sm["monitored"] = false
			}
		}
		p.must(env.Sonarr.Put(p.ctx, fmt.Sprintf("/series/%d", sid), s2, nil), "PUT /series (restore)")
		var all []int
		for id := range episodeMonitored(p) {
			all = append(all, id)
		}
		p.must(env.Sonarr.Put(p.ctx, "/episode/monitor", M{"episodeIds": all, "monitored": false}, nil), "episode/monitor all off")
		if exact && flag == true {
			p.confirm()
		} else {
			p.differ("monitoring calls did not behave as the plan assumes: exact %v, season flag %v", exact, flag)
		}
	})

	t.Run("C7-sonarr", func(t *testing.T) {
		p := specC7Sonarr.probe(t)
		e := epNum(4)
		var ep M
		p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/episode/%d", testenv.ID(e)), &ep), "episode")
		dir, file := stage(p, "c7", "Pioneer.One.S01E04.1080p.WEB-DL.mkv", long(2*pioneerOne.minutes, "eng"))
		row := itemFor(proposal(p, env.Sonarr, dir), file)
		p.check(row != nil, "the staged file is in the proposal")
		var keys []string
		for k := range row {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		rechecked := reprocess(p, env.Sonarr, []M{row}, M{"seriesId": sid, "seasonNumber": 1, "episodeIds": []int{testenv.ID(e)}})
		p.check(len(rechecked) == 1, "re-check returns one row")
		p.evidence("proposal", summary(row))
		p.evidence("rechecked", summary(rechecked[0]))
		p.observe("episode runtime in Sonarr: %v min; the file is %d min long; proposal rejections %v, after the re-check for the episode %v; the row's fields %v: none gives the file's runtime",
			ep["runtime"], 2*pioneerOne.minutes, rejections(row), rejections(rechecked[0]), keys)
		if len(rejections(rechecked[0])) == 0 {
			p.observe("Sonarr says nothing about a file twice the expected runtime: the double-episode hint must come from Fukku's own ffprobe duration and the episode runtime")
		}
		p.observed()
	})

	t.Run("C2-sonarr", func(t *testing.T) {
		p := specC2Sonarr.probe(t)
		e3, e4 := epNum(3), epNum(4)
		// A row moved by one episode.
		dir, file := stage(p, "c2-shift", "Pioneer.One.S01E03.1080p.WEB-DL.mkv", long(pioneerOne.minutes, "eng"))
		rows := proposal(p, env.Sonarr, dir)
		row := itemFor(rows, file)
		p.check(row != nil, "the staged file is in the proposal")
		original := episodeNumbers(row)
		moved := reprocess(p, env.Sonarr, []M{row}, M{"seriesId": sid, "seasonNumber": 1, "episodeIds": []int{testenv.ID(e4)}})
		p.check(len(moved) == 1, "re-check returns one row")
		p.evidence("shift", M{"proposed": original, "rechecked": episodeNumbers(moved[0]), "rejections": rejections(moved[0])})
		p.observe("proposal for %s: episodes %v; re-checked with episodeIds [%d] (S01E04): episodes %v, seriesId %v, rejections %v",
			filepath.Base(file), original, testenv.ID(e4), episodeNumbers(moved[0]), moved[0]["seriesId"], rejections(moved[0]))
		cmd := manualImport(p, env.Sonarr, moved, M{"seriesId": sid, "episodeIds": []int{testenv.ID(e4)}}, "move")
		f3, f4 := episodeFile(p, sid, e3), episodeFile(p, sid, e4)
		p.observe("imported with the shifted id (%s): S01E03 has a file: %v; S01E04 has a file: %v (%v)", cmd.Status, f3 != nil, f4 != nil, f4["relativePath"])

		// A row given two episodes (a double).
		dir, file = stage(p, "c2-double", "Pioneer.One.S01E05.1080p.WEB-DL.mkv", long(2*pioneerOne.minutes, "eng"))
		row = itemFor(proposal(p, env.Sonarr, dir), file)
		p.check(row != nil, "the staged double is in the proposal")
		e5, e6 := epNum(5), epNum(6)
		double := reprocess(p, env.Sonarr, []M{row}, M{"seriesId": sid, "seasonNumber": 1, "episodeIds": []int{testenv.ID(e5), testenv.ID(e6)}})
		p.check(len(double) == 1, "re-check returns one row")
		p.evidence("double", M{"proposed": episodeNumbers(row), "rechecked": episodeNumbers(double[0]), "rejections": rejections(double[0])})
		p.observe("a file twice the runtime, proposed for episodes %v, re-checked with two ids (S01E05, S01E06): episodes %v, rejections %v", episodeNumbers(row), episodeNumbers(double[0]), rejections(double[0]))
		cmd = manualImport(p, env.Sonarr, double, M{"seriesId": sid, "episodeIds": []int{testenv.ID(e5), testenv.ID(e6)}}, "move")
		f5, f6 := episodeFile(p, sid, e5), episodeFile(p, sid, e6)
		same := f5 != nil && f6 != nil && testenv.ID(f5) == testenv.ID(f6)
		p.evidence("doubleImport", M{"status": cmd.Status, "sameFile": same, "file": f5["relativePath"]})
		p.observe("import of the double (%s/%s): S01E05 and S01E06 point to the same file: %v (%v)", cmd.Status, cmd.Result, same, f5["relativePath"])
		if same && len(episodeNumbers(double[0])) == 2 && len(episodeNumbers(moved[0])) == 1 {
			p.confirm()
		} else {
			p.differ("a re-check with edited episodeIds, or a double import, did not behave as the plan assumes")
		}
	})

	t.Run("B5-sonarr", func(t *testing.T) {
		p := specB5Sonarr.probe(t)
		e := epNum(3)
		dir, file := stage(p, "b5", "Pioneer.One.S01E03.1080p.WEB-DL.mkv", long(pioneerOne.minutes, "eng"))
		row := itemFor(proposal(p, env.Sonarr, dir), file)
		p.check(row != nil, "the staged file is in the proposal")
		cmd := manualImport(p, env.Sonarr, []M{row}, M{"seriesId": sid, "episodeIds": []int{testenv.ID(e)}}, "move")
		f := episodeFile(p, sid, e)
		p.check(cmd.Status == "completed" && f != nil, "setup import")
		folder := filepath.Dir(filepath.Join(series["path"].(string), fmt.Sprint(f["relativePath"])))
		base := strings.TrimSuffix(filepath.Base(fmt.Sprint(f["relativePath"])), ".mkv")
		p.must(os.WriteFile(filepath.Join(folder, base+".en.srt"), []byte(fixtures.SRT(time.Duration(pioneerOne.minutes)*time.Minute, "eng")), 0o640), "write srt")
		c, err := env.Sonarr.RunCommand(p.ctx, M{"name": "RescanSeries", "seriesId": sid}, cmdTimeout)
		p.must(err, "RescanSeries")
		p.check(c.Status == "completed", "RescanSeries completed")
		// Radarr lists its extra files at /extrafile; ask Sonarr the same way.
		var extras []M
		extraErr := env.Sonarr.Get(p.ctx, fmt.Sprintf("/extrafile?seriesId=%d", sid), &extras)
		var tracked []string
		for _, x := range extras {
			tracked = append(tracked, fmt.Sprint(x["relativePath"]))
		}
		p.observe("with importExtraFiles off, after a rescan: GET /extrafile?seriesId= answers HTTP %d, extra files listed %v (Sonarr 4.0.20 has no such endpoint if the status is 404, so tracking is judged by the rename below)", testenv.StatusOf(extraErr), tracked)

		var naming M
		p.must(env.Sonarr.Get(p.ctx, "/config/naming", &naming), "naming")
		old := fmt.Sprint(naming["standardEpisodeFormat"])
		naming["standardEpisodeFormat"] = strings.Replace(old, "{Quality Full}", "- {Quality Full}", 1)
		p.must(env.Sonarr.Put(p.ctx, "/config/naming", naming, nil), "set naming")
		defer func() {
			naming["standardEpisodeFormat"] = old
			_ = env.Sonarr.Put(p.ctx, "/config/naming", naming, nil)
		}()
		rn, err := env.Sonarr.RunCommand(p.ctx, M{"name": "RenameFiles", "seriesId": sid, "files": []int{testenv.ID(f)}}, cmdTimeout)
		p.must(err, "RenameFiles")
		files := listDir(folder)
		p.evidence("subtitle", M{"extrafileEndpointHTTP": testenv.StatusOf(extraErr), "tracked": tracked, "filesAfterRename": files, "format": naming["standardEpisodeFormat"]})
		p.observe("rename with format %q (%s): folder now %v", naming["standardEpisodeFormat"], rn.Status, files)
		srtFollowed := false
		for _, name := range files {
			if strings.HasSuffix(name, ".en.srt") && !strings.HasPrefix(name, base) {
				srtFollowed = true
			}
		}
		if srtFollowed {
			p.confirm()
		} else {
			p.differ("the subtitle did not follow the rename: it is not tracked, or not renamed with the episode")
		}
	})

	t.Run("C8-sonarr", func(t *testing.T) {
		p := specC8Sonarr.probe(t)
		var prof M
		p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/qualityprofile/%d", profileID(p, env.Sonarr)), &prof), "quality profile")
		var s M
		p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/series/%d", sid), &s), "series")
		orig, _ := s["originalLanguage"].(M)
		var scored []string
		for _, fi := range prof["formatItems"].([]any) {
			if fm, ok := fi.(M); ok && fmt.Sprint(fm["score"]) != "0" {
				scored = append(scored, fmt.Sprintf("%v=%v", fm["name"], fm["score"]))
			}
		}
		e := epNum(1)
		oldFile := episodeFile(p, sid, e)
		dir, file := stage(p, "c8", "Pioneer.One.S01E01.1080p.WEB-DL.SPANISH.mkv", long(pioneerOne.minutes, "spa"))
		row := itemFor(proposal(p, env.Sonarr, dir), file)
		p.check(row != nil, "the staged dub is in the proposal")
		sum := summary(row)
		cmd := manualImport(p, env.Sonarr, []M{row}, M{"seriesId": sid, "episodeIds": []int{testenv.ID(e)}}, "move")
		newFile := episodeFile(p, sid, e)
		imported := newFile != nil && (oldFile == nil || testenv.ID(newFile) != testenv.ID(oldFile))
		p.evidence("profile", M{"name": prof["name"], "minFormatScore": prof["minFormatScore"], "cutoffFormatScore": prof["cutoffFormatScore"], "upgradeAllowed": prof["upgradeAllowed"], "scored": scored})
		p.evidence("originalLanguage", orig["name"])
		p.evidence("dub", sum)
		p.observe("quality profile %v: minFormatScore %v, cutoffFormatScore %v, scored custom formats %v; the series' original language %v", prof["name"], prof["minFormatScore"], prof["cutoffFormatScore"], scored, orig["name"])
		p.observe("a Spanish dub row: languages %v, formats %v, customFormatScore %v, rejections %v; imported over S01E01 by Manual Import (%s): %v", sum["languages"], sum["customFormats"], sum["customFormatScore"], sum["rejections"], cmd.Status, imported)
		p.observed()
	})
}

// episodeNumbers is the episode numbers of a proposal row, e.g. [3] or [5 6].
func episodeNumbers(row M) []int {
	var out []int
	raw, _ := row["episodes"].([]any)
	for _, x := range raw {
		if em, ok := x.(M); ok {
			out = append(out, int(em["episodeNumber"].(float64)))
		}
	}
	return out
}
