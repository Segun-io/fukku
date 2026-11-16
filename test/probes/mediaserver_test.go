//go:build probes

package probes

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/mediaserver"
)

// TestMediaServerClientContract runs internal/mediaserver against the test Jellyfin: a new
// film file is refreshed through the default method (the library item, decision #22) and
// found by its path well within the minute M2 promises. It adds no title to Radarr, so it
// needs no internet.
func TestMediaServerClientContract(t *testing.T) {
	p := newProbe(t, "M2-mediaserver", "internal/mediaserver client against Jellyfin",
		"After Refresh with the default method (libraryRefresh), Find returns the item id of a new file within a minute, with the path as the server sees it",
		false, "jellyfin")
	p.must(env.Jellyfin.WaitScanIdle(p.ctx, scanTimeout), "wait for idle library")
	ms := mediaserver.NewJellyfin(config.MediaServer{
		URL:    env.Jellyfin.Base,
		APIKey: config.Secret(env.Jellyfin.Key),
		// The method is the default, so the test exercises what an operator gets.
		RefreshMethod: config.Defaults().MediaServer.RefreshMethod,
	})
	version, err := ms.Version(p.ctx)
	p.must(err, "Version")
	p.check(version != "", "the server reports a version")

	name := uniq("Probe MediaServer (2006)")
	dir := p.fresh(movies(name))
	file := filepath.Join(dir, name+" - WEBDL-1080p.mkv")
	p.video(file, short)
	id, err := ms.Find(p.ctx, file)
	p.must(err, "Find before the refresh")
	p.check(id == "", "the new file is not known before the refresh")

	start := time.Now()
	p.must(ms.Refresh(p.ctx, []string{file}), "Refresh")
	for id == "" && time.Since(start) < time.Minute {
		id, err = ms.Find(p.ctx, file)
		p.must(err, "Find")
		if id == "" {
			time.Sleep(250 * time.Millisecond)
		}
	}
	latency := time.Since(start)
	p.check(id != "", "the item is visible within a minute of Refresh")

	// A show: the episode and the show's folder (the item a title link points to).
	show := uniq("Probe MediaServer Show")
	showDir := p.fresh(shows(show))
	episode := filepath.Join(showDir, "Season 01", show+" - S01E01.mkv")
	p.must(os.MkdirAll(filepath.Dir(episode), 0o755), "season folder")
	p.video(episode, short)
	p.must(ms.Refresh(p.ctx, []string{episode}), "Refresh the show")
	var epID, seriesID string
	showStart := time.Now()
	for (epID == "" || seriesID == "") && time.Since(showStart) < time.Minute {
		epID, err = ms.Find(p.ctx, episode)
		p.must(err, "Find the episode")
		seriesID, err = ms.Find(p.ctx, showDir)
		p.must(err, "Find the show")
		if epID == "" || seriesID == "" {
			time.Sleep(250 * time.Millisecond)
		}
	}
	p.check(epID != "" && seriesID != "" && epID != seriesID, "the episode and the show's folder are both found, as different items")
	p.evidence("method", config.Defaults().MediaServer.RefreshMethod)
	p.evidence("latencySeconds", latency.Seconds())
	p.observe("Jellyfin %s; default method %s: item %s found by its path after %.1f s; a new show: its folder and its episode found as separate items after %.1f s", version,
		config.Defaults().MediaServer.RefreshMethod, id, latency.Seconds(), time.Since(showStart).Seconds())
	p.confirm()
}
