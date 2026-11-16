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

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/mediaserver"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
	"github.com/segun-io/fukku/test/fixtures"
)

// TestM3VersionGrouping runs what second versions of films (#56, decision #20) rely on
// against the pinned apps. Radarr: its parse of a version's name, the check that replaces
// "rejected as not an upgrade". Jellyfin: internal/mediaserver's keeper groups a version kept
// outside the root folder with the film's file, and groups it again after the film's file
// is replaced (an upgrade, B3e, B3f) and after it is renamed. Radarr's tracked file is
// played by the arr fake, so no title is added and no internet is needed; the files and
// Jellyfin are real.
func TestM3VersionGrouping(t *testing.T) {
	p := newProbe(t, "M3-versions", "Second versions of films: Radarr's verdict on the name, and the grouping the keeper re-applies (#56)",
		"Radarr's parse of '<film folder> - <label>' finds no quality for a language label and one for a quality word; internal/mediaserver groups a version outside the root folder with the film's file, and groups it again after the film's file is replaced and after it is renamed",
		false, "radarr", "jellyfin")
	ok := true
	name := uniq("Probe Versions (2003)")
	term := strings.TrimSuffix(name, " (2003)")

	// Radarr's verdict: the name without its extension (with ".mkv" Radarr reads WEBDL-720p).
	rc, isParser := radarrClient().(arr.NameParser)
	p.check(isParser, "the Radarr client parses names")
	verdicts := map[string]string{}
	for _, label := range []string{"Español", "Latino", "Versión extendida", "Bluray", "1080p", "DVD", "Proper"} {
		got, err := rc.Parse(p.ctx, name+" - "+label)
		p.must(err, "parse "+label)
		verdicts[label] = fmt.Sprintf("%s rev %d", got.Quality.Name, got.Revision)
		reads := got.Quality.ID != 0 || got.Resolution != 0 || got.Revision > 1 || (got.Source != "" && got.Source != "unknown")
		want := slices.Contains([]string{"Bluray", "1080p", "DVD", "Proper"}, label)
		if reads != want {
			ok = false
			p.observe("DIFFERS: %q: Radarr reads %+v", label, got)
		}
	}
	withExt, err := rc.Parse(p.ctx, name+" - Español.mkv")
	p.must(err, "parse with the extension")
	p.observe("Radarr's parse of '%s - <label>': %v; with '.mkv' added the language label reads as %s", name, verdicts, withExt.Quality.Name)
	p.evidence("verdicts", verdicts)

	// The files: the film in Radarr's root folder, the version in a versions folder outside
	// it, which is added to the Movies library for the length of the probe.
	versionsRoot := filepath.Join(env.Data, "media", "versions-m3")
	dir := p.fresh(movies(name))
	film := filepath.Join(dir, name+" WEBDL-1080p.mkv")
	p.video(film, short)
	vdir := p.fresh(filepath.Join(versionsRoot, name))
	version := filepath.Join(vdir, name+" - Español.mkv")
	p.video(version, shortSpanish)
	p.must(env.Jellyfin.AddLibraryPath(p.ctx, "Movies", versionsRoot), "add the versions folder to the Movies library")
	var primaryItem string
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*scanTimeout)
		defer cancel()
		if primaryItem != "" {
			_ = env.Jellyfin.Unmerge(ctx, primaryItem)
		}
		_ = env.Jellyfin.RemoveLibraryPath(ctx, "Movies", versionsRoot)
		_ = os.RemoveAll(versionsRoot)
		_ = os.RemoveAll(dir)
		_ = env.Jellyfin.ScanAndWait(ctx, scanTimeout)
	})
	p.must(env.Jellyfin.WaitScanIdle(p.ctx, scanTimeout), "wait for the library")

	cfg := config.Defaults()
	cfg.MediaServer = config.MediaServer{Provider: "jellyfin", URL: env.Jellyfin.Base, APIKey: config.Secret(env.Jellyfin.Key),
		RefreshMethod: config.Defaults().MediaServer.RefreshMethod, RefreshFallback: true,
		ItemWait: config.Duration(time.Minute), ItemPollInterval: config.Duration(500 * time.Millisecond)}
	st := storetest.NewFake(nil)
	movies := &arrtest.FakeMovies{Library: []arr.Movie{{ID: 1, Title: term, Year: 2003, File: &arr.MovieFile{ID: 1, Path: film}}}}
	k := mediaserver.NewKeeper(cfg, nil, st, movies, mediaserver.NewJellyfin(cfg.MediaServer))
	_, err = st.PutVersion(p.ctx, store.Version{JobID: "probe", App: "radarr", TitleID: 1, Title: term, Year: 2003, Path: version, Status: store.VersionLinked})
	p.must(err, "record")

	entries := func(stage string) int {
		list, err := env.Jellyfin.UserMovies(p.ctx, term)
		p.must(err, "user view "+stage)
		p.observe("%s: a user's library view shows %d entr(ies) %v", stage, len(list), sourcesOf(list))
		p.evidence(stage, sourcesOf(list))
		return len(list)
	}
	check := func(stage string, wantChanged bool) {
		start := time.Now()
		rec, err := k.Check(p.ctx, version, time.Minute, true)
		p.must(err, "check "+stage)
		primaryItem = rec.PrimaryItemID
		p.observe("%s: the keeper's check took %s: status %s, film item %s, version item %s, film file changed: %v",
			stage, time.Since(start).Round(100*time.Millisecond), rec.Status, rec.PrimaryItemID, rec.ItemID, !rec.PrimaryChangedAt.IsZero())
		if rec.Status != store.VersionGrouped || (wantChanged && rec.PrimaryChangedAt.IsZero()) {
			ok = false
			p.observe("DIFFERS: %s: record %+v", stage, rec)
		}
		if n := entries(stage + ", after the check"); n != 1 {
			ok = false
			p.observe("DIFFERS: %s: %d entries, want one with two versions", stage, n)
		}
	}
	check("first grouping", false)

	// An upgrade: Radarr deletes the old file and imports a new one under another name.
	p.must(os.Remove(film), "remove the film's file")
	upgraded := filepath.Join(dir, name+" Remux-1080p.mkv")
	p.video(upgraded, short)
	movies.Library[0].File = &arr.MovieFile{ID: 2, Path: upgraded}
	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "library scan")
	entries("after the upgrade and a library scan")
	check("after the upgrade", true)

	// A rename: the same file under a new name (Radarr keeps its movie-file id).
	renamed := filepath.Join(dir, name+" - Remux-1080p.mkv")
	p.must(os.Rename(upgraded, renamed), "rename the film's file")
	movies.Library[0].File = &arr.MovieFile{ID: 2, Path: renamed}
	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "library scan")
	entries("after the rename and a library scan")
	check("after the rename", true)

	log, err := st.AuditLog(p.ctx, "probe")
	p.must(err, "audit")
	var actions []string
	for _, e := range log {
		actions = append(actions, e.Action)
	}
	p.observe("audit entries %v", actions)
	if ok {
		p.confirm()
	} else {
		p.differ("second versions: see the observations marked DIFFERS")
	}
}

// shortSpanish is short with a Spanish audio track.
var shortSpanish = fixtures.Video{Duration: 10 * time.Second, Width: 1920, Height: 1080, FrameRate: "5",
	Audio: []fixtures.Audio{{Language: "spa", Default: true}}}
