//go:build probes

package probes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/test/testenv"

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

// TestMediaServerRefreshFallback is the reproduction of #84 on a library the server never
// scanned. It creates its own Jellyfin library on a new, empty folder
// and never scans it with content in it (the seeded libraries are primed by test/env/seed, so they cannot
// show the problem). With mediaServer.refreshFallback off the step must not see the film
// (libraryRefresh does nothing there); with it on, the step falls back to one full library
// scan and finds the film. The latency is taken on a library of about two hundred films.
func TestMediaServerRefreshFallback(t *testing.T) {
	p := newProbe(t, "M2-mediaserver-fallback", "Media server refresh fallback on a never-scanned library",
		"On a Jellyfin library that was never scanned, the default libraryRefresh shows nothing; with mediaServer.refreshFallback the step scans once and finds the film",
		false, "jellyfin")
	fillerScan := jellyfinFillers(p, 200)
	jf := env.Jellyfin

	// A library of its own, on a folder of its own, created without a scan.
	libName := "Probe Fresh " + runTag
	root := filepath.Join(env.Data, "media", "probe-fresh-"+strings.ToLower(runTag))
	cleanup := func(ctx context.Context) {
		_ = jf.Do(ctx, "DELETE", "/Library/VirtualFolders"+testenv.Q("name", libName, "refreshLibrary", "false"), nil, nil)
		_ = os.RemoveAll(root)
	}
	cleanup(p.ctx)
	p.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		cleanup(ctx)
	})
	p.must(os.MkdirAll(root, 0o750), "mkdir "+root)
	opts := map[string]any{"LibraryOptions": map[string]any{
		"EnableRealtimeMonitor": false, "EnablePhotos": false, "EnableInternetProviders": false,
		"SaveLocalMetadata": false, "MetadataSavers": []string{}, "DisabledLocalMetadataReaders": []string{},
		"EnableEmbeddedTitles": false, "EnableChapterImageExtraction": false, "ExtractChapterImagesDuringLibraryScan": false,
		"AutomaticRefreshIntervalDays": 0, "EnableTrickplayImageExtraction": false,
		"PathInfos": []map[string]any{{"Path": root}},
		"TypeOptions": []map[string]any{
			{"Type": "Movie", "MetadataFetchers": []string{}, "ImageFetchers": []string{}},
		},
	}}
	p.must(jf.Do(p.ctx, "POST", "/Library/VirtualFolders"+testenv.Q("name", libName, "collectionType", "movies", "refreshLibrary", "false"), opts, nil), "create the library")
	time.Sleep(3 * time.Second) // a scan the creation might have started would show by now
	p.must(jf.WaitScanIdle(p.ctx, scanTimeout), "wait for idle library")
	// The film arrives after the library exists, as an import does.
	name := uniq("Probe Fresh Film (2001)")
	file := filepath.Join(root, name, name+" - WEBDL-1080p.mkv")
	p.video(file, short)

	itemWait := 15 * time.Second
	newStep := func(fallback bool) *mediaserver.Step {
		cfg := config.Defaults()
		cfg.MediaServer.Provider = "jellyfin"
		cfg.MediaServer.RefreshFallback = fallback
		cfg.MediaServer.ItemWait = config.Duration(itemWait)
		cfg.MediaServer.ItemPollInterval = config.Duration(500 * time.Millisecond)
		return &mediaserver.Step{
			Server: mediaserver.NewJellyfin(config.MediaServer{URL: jf.Base, APIKey: config.Secret(jf.Key), RefreshMethod: cfg.MediaServer.RefreshMethod}),
			Movies: &arrtest.FakeMovies{Library: []arr.Movie{{ID: 1, Title: "Fresh", File: &arr.MovieFile{ID: 1, Path: file}}}},
			Config: cfg,
		}
	}
	job, target := store.Job{ID: "probe"}, store.Target{App: "radarr", TitleID: 1}

	// Without the fallback: libraryRefresh finds nothing, as #47 saw.
	start := time.Now()
	done, _, err := newStep(false).Run(p.ctx, job, target, nil)
	p.check(!done && errors.Is(err, mediaserver.ErrNotVisible),
		fmt.Sprintf("without the fallback the film stays invisible after itemWait (done %v, err %v)", done, err))
	p.observe("refreshFallback off: not visible after %.1f s (itemWait %s), %v", time.Since(start).Seconds(), itemWait, err)

	// With it: one full scan, then the film is listed.
	start = time.Now()
	done, detail, err := newStep(true).Run(p.ctx, job, target, nil)
	took := time.Since(start)
	p.must(err, "Run with the fallback")
	p.check(done && mediaserver.ItemID(detail) != "", "with the fallback the film is visible and the step reports its item")
	scanLatency := took - itemWait
	p.evidence("scanLatencySeconds", scanLatency.Seconds())
	p.evidence("detail", detail)
	p.observe("refreshFallback on: %q after %.1f s in all, i.e. itemWait %s plus %.1f s from the scan to the film being listed; library of about 200 films (their scan took %s)",
		detail, took.Seconds(), itemWait, scanLatency.Seconds(), fillerScan)
	p.confirm()
}
