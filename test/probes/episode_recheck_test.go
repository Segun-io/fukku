//go:build probes

package probes

import (
	"context"
	"fmt"
	"log/slog"
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

// TestM3EpisodeVersionRecheck runs the keeper's re-check of episodes' versions (#96) against
// the pinned Sonarr and Jellyfin: a version placed next to S01E02's file (after Sonarr's
// verdict) is grouped; Sonarr moves the series with its files and the record follows the
// version; the episode's own file is deleted outside Sonarr (episode_file_missing); Sonarr's
// rescan adopts the version (adopted); and the series deleted in Sonarr, without its files,
// gives series_deleted. The keeper runs as the web process runs it (Keeper.Run, a short
// versions.regroupInterval). The show is added through Sonarr's metadata service (online), in
// a series folder of its own ("Probe Recheck <run>", which the suite's start-up removes).
func TestM3EpisodeVersionRecheck(t *testing.T) {
	p := newProbe(t, "M3-episode-version-recheck", "The re-check of episodes' versions against Sonarr: a move of the series, the episode's file deleted, Sonarr's adoption, the series deleted (#96)",
		"internal/mediaserver's Keeper records a placed episode's version as grouped; follows it when Sonarr moves the series with its files (the version moves with the folder); marks it episode_file_missing once the episode's file is deleted outside Sonarr, adopted once Sonarr's rescan takes it for the episode's file, and series_deleted once the series is deleted in Sonarr; it never touches the file",
		true, "sonarr", "jellyfin")
	ok := true
	differ := func(format string, args ...any) {
		ok = false
		p.observe("DIFFERS: "+format, args...)
	}
	series, eps := addShow(p, caminandes)
	sid := testenv.ID(series)
	_ = os.RemoveAll(series["path"].(string))
	series["path"] = p.fresh(shows(uniq("Probe Recheck")))
	var put M
	p.must(env.Sonarr.Put(p.ctx, fmt.Sprintf("/series/%d?moveFiles=false", sid), series, &put), "a fresh series folder")
	seriesPath := put["path"].(string)
	movedPath := shows(uniq("Probe Recheck Moved"))
	deleted := false
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*scanTimeout)
		defer cancel()
		if !deleted {
			_ = env.Sonarr.Delete(ctx, fmt.Sprintf("/series/%d?deleteFiles=true", sid))
		}
		_ = os.RemoveAll(seriesPath)
		_ = os.RemoveAll(movedPath)
		_ = os.RemoveAll(intake("sonarr-epv-recheck"))
		_ = env.Jellyfin.ScanAndWait(ctx, scanTimeout)
	})
	e2 := episode(eps, 1, 2)
	p.check(e2 != nil, "S01E02 exists")

	// S01E02's own file: WEBDL-1080p, in English.
	dir := p.fresh(intake("sonarr-epv-recheck"))
	src := filepath.Join(dir, "own", "Caminandes.S01E02.1080p.WEB-DL.mkv")
	p.video(src, long(caminandes.minutes, "eng"))
	item := itemFor(proposal(p, env.Sonarr, filepath.Dir(src)), src)
	p.check(item != nil, "the episode's file is in the proposal")
	cmd := manualImport(p, env.Sonarr, []M{item}, M{"seriesId": sid, "episodeIds": []int{testenv.ID(e2)}}, "move")
	p.check(cmd.Status == "completed", "setup import: "+cmd.Status+" "+cmd.Message)
	tracked := episodeFile(p, sid, e2)
	p.check(tracked != nil, "S01E02 has a file")
	own := filepath.Join(seriesPath, fmt.Sprint(tracked["relativePath"]))
	season := filepath.Dir(own)

	// The version, as the placement makes it: Sonarr's verdict on the staged file, then a hard
	// link next to the episode's file.
	sc := sonarrClient()
	name := fmt.Sprintf("%s - S01E02 - Español.mkv", filepath.Base(seriesPath))
	staged := filepath.Join(dir, "stage", name)
	p.video(staged, long(caminandes.minutes, "spa"))
	vd, err := flows.EpisodeVerdict(p.ctx, sc, filepath.Dir(staged), staged, sid, testenv.ID(e2), 1, 2)
	p.must(err, "verdict")
	p.check(vd.Code == "", "Sonarr rejects the version as not an upgrade: "+vd.Reason)
	version := filepath.Join(season, name)
	p.must(os.Link(staged, version), "place the version")
	// The re-check asks the same verdict of the file where it is (the season folder's proposal).
	vd, err = flows.EpisodeVerdict(p.ctx, sc, season, version, sid, testenv.ID(e2), 1, 2)
	p.must(err, "verdict in place")
	p.observe("Sonarr's verdict on the placed version, in the season folder: %q %s", vd.Code, vd.Reason)
	if vd.Code != "" {
		differ("in place, Sonarr does not reject the version as not an upgrade: %s %s", vd.Code, vd.Reason)
	}

	cfg := config.Defaults()
	cfg.Paths.LibraryRoots = []string{filepath.Join(env.Data, "media", "tv")}
	cfg.MediaServer = config.MediaServer{Provider: "jellyfin", URL: env.Jellyfin.Base, APIKey: config.Secret(env.Jellyfin.Key),
		RefreshMethod: config.Defaults().MediaServer.RefreshMethod, RefreshFallback: true,
		ItemWait: config.Duration(time.Minute), ItemPollInterval: config.Duration(500 * time.Millisecond)}
	cfg.Versions.RegroupInterval = config.Duration(3 * time.Second)
	st := storetest.NewFake(nil)
	_, err = st.PutVersion(p.ctx, store.Version{JobID: "probe-recheck", App: "sonarr", TitleID: sid, Title: caminandes.title, Path: version,
		Label: "Español", Status: store.VersionLinked, EpisodeID: testenv.ID(e2), SeasonNumber: 1, EpisodeNumber: 2})
	p.must(err, "record")
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	keeper := func() context.CancelFunc {
		k := mediaserver.NewKeeper(cfg, log, st, nil, mediaserver.NewJellyfin(cfg.MediaServer), nil)
		k.SetSeries(sc)
		ctx, cancel := context.WithCancel(p.ctx)
		done := make(chan struct{})
		go func() { _ = k.Run(ctx); close(done) }()
		return func() { cancel(); <-done }
	}
	statuses := map[string]string{}
	// await waits for the record (at path) to reach status (and, with under, to be grouped with an
	// episode file inside that folder), and records what it was.
	await := func(stage, path, status string, timeout time.Duration, under ...string) store.Version {
		start := time.Now()
		var rec store.Version
		err := testenv.WaitFor(p.ctx, timeout, time.Second, stage, func() (bool, error) {
			var err error
			rec, err = st.VersionByPath(p.ctx, path)
			return err == nil && rec.Status == status && (len(under) == 0 || strings.HasPrefix(rec.PrimaryPath, under[0]+"/")), nil
		})
		statuses[stage] = rec.Status + ": " + rec.Detail
		p.observe("%s: after %s the record is %s (%s) at %s; on disk: %v", stage, time.Since(start).Round(time.Second), rec.Status, rec.Detail, filepath.Base(filepath.Dir(filepath.Dir(rec.Path))), exists(path))
		if err != nil {
			differ("%s: status %q (%s), want %s", stage, rec.Status, rec.Detail, status)
		}
		return rec
	}
	stop := keeper()
	await("placed", version, store.VersionGrouped, 3*time.Minute)

	// Sonarr moves the series with its files: the whole folder moves, the version with it.
	put["path"] = movedPath
	p.must(env.Sonarr.Put(p.ctx, fmt.Sprintf("/series/%d?moveFiles=true", sid), put, &put), "move the series")
	movedVersion := filepath.Join(movedPath, filepath.Base(season), name)
	p.must(testenv.WaitFor(p.ctx, cmdTimeout, time.Second, "the move", func() (bool, error) {
		return exists(filepath.Join(movedPath, filepath.Base(season), filepath.Base(own))) && !exists(seriesPath), nil
	}), "the move")
	p.observe("Sonarr moved the series to %s: the version moved with it: %v; the season folder %v", filepath.Base(movedPath), exists(movedVersion), listDir(filepath.Dir(movedVersion)))
	if !exists(movedVersion) {
		differ("the version did not move with the series' folder")
	}
	// Grouped again once Jellyfin lists the moved files as one episode.
	rec := await("series moved", movedVersion, store.VersionGrouped, 3*time.Minute, movedPath)
	if rec.Path != movedVersion {
		differ("the record did not follow the version: %s", rec.Path)
	}
	own = filepath.Join(movedPath, filepath.Base(season), filepath.Base(own))

	// The episode's own file deleted outside Sonarr: Sonarr still lists it.
	p.must(os.Remove(own), "delete the episode's file")
	await("episode file deleted", movedVersion, store.VersionEpisodeFileMissing, time.Minute)

	// Sonarr's rescan drops the missing file and takes the version for the episode's file.
	c, err := env.Sonarr.RunCommand(p.ctx, M{"name": "RescanSeries", "seriesId": sid}, cmdTimeout)
	p.must(err, "RescanSeries")
	after := episodeFile(p, sid, e2)
	var adoptedPath string
	if after != nil {
		adoptedPath = fmt.Sprint(after["relativePath"])
	}
	p.observe("RescanSeries %s: S01E02's file is now %q", c.Status, adoptedPath)
	if !strings.Contains(adoptedPath, "Español") {
		differ("Sonarr's rescan did not adopt the version: S01E02's file %q", adoptedPath)
	}
	rec = await("rescan", movedVersion, store.VersionAdopted, time.Minute)
	if !exists(movedVersion) {
		differ("the adopted version is gone")
	}
	stop()

	// The series deleted in Sonarr, without its files. (The record is set back to grouped:
	// an adopted version is checked again only hourly.)
	p.must(env.Sonarr.Delete(p.ctx, fmt.Sprintf("/series/%d?deleteFiles=false", sid)), "delete the series")
	deleted = true
	rec.Status, rec.Detail = store.VersionGrouped, ""
	_, err = st.PutVersion(p.ctx, rec)
	p.must(err, "reset the record")
	stop = keeper()
	rec = await("series deleted", movedVersion, store.VersionSeriesDeleted, time.Minute)
	stop()
	if !exists(movedVersion) {
		differ("the version was removed with the series deleted without its files")
	}
	log2, _ := st.AuditLog(p.ctx, "probe-recheck")
	var actions []string
	for _, e := range log2 {
		actions = append(actions, e.Action)
	}
	p.observe("audit: %v", actions)
	p.evidence("statuses", statuses)
	p.evidence("audit", actions)

	if ok {
		p.confirm()
	} else {
		p.differ("the re-check of episodes' versions: see the observations marked DIFFERS")
	}
}
