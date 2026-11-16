//go:build probes

package probes

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/test/testenv"
)

// addShow adds the show fresh, with nothing monitored and search off (flow 3), and waits
// for its episodes.
func addShow(p *probe, s show) (M, []M) {
	so := env.Sonarr
	var existing []M
	p.must(so.Get(p.ctx, "/series", &existing), "series")
	for _, m := range existing {
		if int(m["tvdbId"].(float64)) == s.tvdb {
			p.must(so.Delete(p.ctx, fmt.Sprintf("/series/%d?deleteFiles=true", testenv.ID(m))), "delete series")
		}
	}
	var lookup []M
	// The lookup goes through Sonarr to its public metadata service, which can be slow.
	p.must(testenv.WaitFor(p.ctx, 4*time.Minute, 5*time.Second, "series lookup", func() (bool, error) {
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
	p.must(so.Post(p.ctx, "/series", body, &series), "add series")
	p.fresh(series["path"].(string))
	var episodes []M
	p.must(testenv.WaitFor(p.ctx, 2*time.Minute, 2*time.Second, "episodes", func() (bool, error) {
		err := so.Get(p.ctx, fmt.Sprintf("/episode?seriesId=%d", testenv.ID(series)), &episodes)
		return len(episodes) > 0, err
	}), "episodes")
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

// TestSonarr runs the Sonarr probes in order on one show: B7, B8, then B3 for episodes.
func TestSonarr(t *testing.T) {
	setup := newProbe(t, "B7-sonarr", "Sonarr-style profiles reject a not-original-language episode in the proposal",
		"With \"not original language\" scored very negatively, Manual Import proposals show a rejection for a dubbed episode and none for the original",
		true, "sonarr")
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
		c6 := newProbe(t, "C6-sonarr", "Sonarr Manual Import proposal: folder and series scoping",
			"Plan §6: `GET /manualimport?folder=<job dir>` + `seriesId` + `seasonNumber` proposes the job's files",
			true, "sonarr")
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
		p := newProbe(t, "B8-sonarr", "Sonarr Manual Import with explicit episode ids imports despite rejections",
			"Manual Import with an explicit episode id is expected to import even when the proposal lists rejections (unverified)",
			true, "sonarr")
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
		p := newProbe(t, "B3-sonarr", "Sonarr adopts an untracked upgrade in the season folder on rescan",
			"Assumed to behave like Radarr (B3) until a probe shows otherwise",
			true, "sonarr")
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
}
