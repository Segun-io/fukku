//go:build probes

package probes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/mediaserver"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
	"github.com/segun-io/fukku/test/testenv"
)

// caminandes is the episodes' versions probe's own show (a Creative Commons series of the
// Blender Foundation), so it never shares Pioneer One with the other Sonarr probes.
var caminandes = show{tvdb: 276116, title: "Caminandes", minutes: 2}

// TestM3EpisodeVersions runs what second versions of episodes (#58, decision #20) rely on
// against the pinned Sonarr and Jellyfin: internal/flows' EpisodeVerdict (the check before a
// version is linked) on a staged file under the version's name, Sonarr's rescan and an
// upgrade of the episode with the version next to its file, and internal/mediaserver's
// EpisodeVersionStep finding the two grouped by Jellyfin. The show is added through Sonarr's
// metadata service (online).
func TestM3EpisodeVersions(t *testing.T) {
	p := newProbe(t, "M3-episode-versions", "Second versions of episodes: Sonarr's verdict on the version's name, its rescan and an upgrade, and Jellyfin's grouping (#58)",
		"internal/flows' EpisodeVerdict passes '<series> - S01E02 - <label>.mkv' (Sonarr rejects it as not an upgrade), refuses a 'Bluray-1080p' name and a 2160p file (version_upgrade) and gives no verdict on a name Sonarr reads as another episode (version_unchecked); a label file placed next to the episode's file stays untracked through a rescan and an upgrade of the episode; internal/mediaserver's EpisodeVersionStep finds it grouped with the episode's file, before and after the upgrade",
		true, "sonarr", "jellyfin")
	ok := true
	differ := func(format string, args ...any) {
		ok = false
		p.observe("DIFFERS: "+format, args...)
	}
	series, eps := addShow(p, caminandes)
	sid := testenv.ID(series)
	// Jellyfin keeps items by path, and a version removed by an earlier run, then placed again
	// at the same path, was not listed again: each run uses a fresh series folder (the name
	// Sonarr then cannot match to the series, so the verdict assigns it, as for any job).
	_ = os.RemoveAll(series["path"].(string))
	series["path"] = p.fresh(filepath.Join(env.Data, "media", "tv", uniq(caminandes.title)))
	var moved M
	p.must(env.Sonarr.Put(p.ctx, fmt.Sprintf("/series/%d?moveFiles=false", sid), series, &moved), "a fresh series folder")
	seriesPath := moved["path"].(string)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*scanTimeout)
		defer cancel()
		_ = env.Sonarr.Delete(ctx, fmt.Sprintf("/series/%d?deleteFiles=true", sid))
		_ = os.RemoveAll(seriesPath)
		_ = os.RemoveAll(intake("sonarr-epv"))
		_ = env.Jellyfin.ScanAndWait(ctx, scanTimeout)
	})
	e1, e2 := episode(eps, 1, 1), episode(eps, 1, 2)
	p.check(e1 != nil && e2 != nil, "S01E01 and S01E02 exist")

	// The episodes' own files: WEBDL-1080p, in English.
	dir := p.fresh(intake("sonarr-epv"))
	for _, ep := range []M{e1, e2} {
		n := int(ep["episodeNumber"].(float64))
		src := filepath.Join(dir, "own", fmt.Sprintf("Caminandes.S01E%02d.1080p.WEB-DL.mkv", n))
		p.video(src, long(caminandes.minutes, "eng"))
		item := itemFor(proposal(p, env.Sonarr, filepath.Dir(src)), src)
		p.check(item != nil, "the episode's file is in the proposal")
		cmd := manualImport(p, env.Sonarr, []M{item}, M{"seriesId": sid, "episodeIds": []int{testenv.ID(ep)}}, "move")
		p.check(cmd.Status == "completed", "setup import: "+cmd.Status+" "+cmd.Message)
	}
	tracked := episodeFile(p, sid, e2)
	p.check(tracked != nil, "S01E02 has a file")
	season := filepath.Dir(filepath.Join(seriesPath, fmt.Sprint(tracked["relativePath"])))
	prefix := filepath.Base(seriesPath)

	// Sonarr's verdict, through flows.EpisodeVerdict, on a staged file under a version name.
	sc := sonarrClient()
	verdicts := map[string]string{}
	// Each case is asked as S01E02's version; want is the verdict's code ("" may be placed).
	verdict := func(episode, label string, width, height int, want string) string {
		name := fmt.Sprintf("%s - %s - %s.mkv", prefix, episode, label)
		folder := filepath.Join(dir, "stage", fmt.Sprintf("%s-%s-%dp", episode, strings.ReplaceAll(label, " ", "_"), height))
		file := filepath.Join(folder, name)
		p.video(file, longAt(caminandes.minutes, width, height, "spa"))
		vd, err := flows.EpisodeVerdict(p.ctx, sc, folder, file, sid, testenv.ID(e2), 1, 2)
		p.must(err, "verdict "+label)
		key := fmt.Sprintf("%s %s (%dp)", episode, label, height)
		verdicts[key] = vd.Code + ": " + vd.Reason
		if vd.Code == "" {
			verdicts[key] = "rejected as not an upgrade: may be placed"
		}
		if vd.Code != want {
			differ("%s: verdict %q (%s), want %q", key, vd.Code, vd.Reason, want)
		}
		return file
	}
	labelFile := verdict("S01E02", "Español", 1920, 1080, "")
	verdict("S01E02", "Bluray-1080p", 1920, 1080, flows.CodeVersionUpgrade)
	verdict("S01E02", "Español", 3840, 2160, flows.CodeVersionUpgrade)
	verdict("S01E03", "Español", 1920, 1080, flows.CodeVersionUnchecked) // Sonarr reads another episode
	p.evidence("verdicts", verdicts)
	p.observe("EpisodeVerdict on '%s - S01E02 - <label>.mkv' with S01E02's file WEBDL-1080p: %v", prefix, verdicts)

	// The version next to the episode's file: a hard link, as the placement makes it.
	version := filepath.Join(season, filepath.Base(labelFile))
	p.must(os.Link(labelFile, version), "place the version")
	rescan := func(stage string) {
		c, err := env.Sonarr.RunCommand(p.ctx, M{"name": "RescanSeries", "seriesId": sid}, cmdTimeout)
		p.must(err, "RescanSeries")
		after := episodeFile(p, sid, e2)
		var files []M
		p.must(env.Sonarr.Get(p.ctx, fmt.Sprintf("/episodefile?seriesId=%d", sid), &files), "episode files")
		var paths []string
		for _, f := range files {
			paths = append(paths, fmt.Sprint(f["relativePath"]))
		}
		adopted := strings.Contains(strings.Join(paths, "|"), "Español")
		p.observe("%s: RescanSeries %s; S01E02's file %v; Sonarr's files %v; the version on disk: %v, tracked: %v",
			stage, c.Status, after["relativePath"], paths, exists(version), adopted)
		if adopted || !exists(version) {
			differ("%s: the version was adopted or removed", stage)
		}
	}
	rescan("after placing the version")

	// Jellyfin's grouping, as the job's step records it.
	cfg := config.Defaults()
	cfg.MediaServer = config.MediaServer{Provider: "jellyfin", URL: env.Jellyfin.Base, APIKey: config.Secret(env.Jellyfin.Key),
		RefreshMethod: config.Defaults().MediaServer.RefreshMethod, RefreshFallback: true,
		ItemWait: config.Duration(time.Minute), ItemPollInterval: config.Duration(500 * time.Millisecond)}
	st := storetest.NewFake(nil)
	_, err := st.PutVersion(p.ctx, store.Version{JobID: "probe", App: "sonarr", TitleID: sid, Title: caminandes.title, Path: version,
		Label: "Español", Status: store.VersionLinked, EpisodeID: testenv.ID(e2), SeasonNumber: 1, EpisodeNumber: 2})
	p.must(err, "record")
	opts, _ := json.Marshal(M{"episodeVersions": M{"label": "Español", "versions": []M{{"path": version, "row": "r1"}}}})
	target := store.Target{App: "sonarr", TitleID: sid, Title: caminandes.title, Options: opts}
	step := &mediaserver.EpisodeVersionStep{Server: mediaserver.NewJellyfin(cfg.MediaServer), Series: sc, Store: st, Config: cfg}
	check := func(stage string, wantChanged bool) {
		start := time.Now()
		done, _, err := step.Run(p.ctx, store.Job{ID: "probe"}, target, nil)
		rec, rerr := st.VersionByPath(p.ctx, version)
		p.must(rerr, "record")
		items, ierr := env.Jellyfin.ItemsUnder(p.ctx, season, "Episode")
		p.must(ierr, "episodes")
		p.observe("%s: the step took %s: done %v (%v), status %s, episode item %s, episode's file changed: %v; Jellyfin's episodes %v",
			stage, time.Since(start).Round(100*time.Millisecond), done, err, rec.Status, rec.PrimaryItemID, !rec.PrimaryChangedAt.IsZero(), sourcesOf(items))
		p.evidence(stage, sourcesOf(items))
		if !done || err != nil || rec.Status != store.VersionGrouped || (wantChanged && rec.PrimaryChangedAt.IsZero()) {
			differ("%s: record %+v", stage, rec)
		}
	}
	check("first check", false)

	// An upgrade of the episode (a Bluray-1080p release, by Manual Import as a grab would end):
	// Sonarr replaces its own file only.
	up := filepath.Join(dir, "upgrade", "Caminandes.S01E02.1080p.BluRay.x264.mkv")
	p.video(up, long(caminandes.minutes, "eng"))
	item := itemFor(proposal(p, env.Sonarr, filepath.Dir(up)), up)
	p.check(item != nil, "the upgrade is in the proposal")
	cmd := manualImport(p, env.Sonarr, []M{item}, M{"seriesId": sid, "episodeIds": []int{testenv.ID(e2)}}, "move")
	upgraded := episodeFile(p, sid, e2)
	p.observe("upgrade of S01E02: ManualImport %s; S01E02's file now %v (was %v); season folder %v",
		cmd.Status, upgraded["relativePath"], tracked["relativePath"], listDir(season))
	if testenv.ID(upgraded) == testenv.ID(tracked) {
		differ("the upgrade did not replace the episode's file")
	}
	rescan("after the upgrade")
	check("after the upgrade", true)

	if ok {
		p.confirm()
	} else {
		p.differ("second versions of episodes: see the observations marked DIFFERS")
	}
}
