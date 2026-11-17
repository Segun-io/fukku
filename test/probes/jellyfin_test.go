//go:build probes

package probes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/test/testenv"
)

const scanTimeout = 5 * time.Minute

func sourcesOf(items []testenv.Item) []string {
	var out []string
	for _, it := range items {
		var names []string
		for _, ms := range it.MediaSources {
			names = append(names, filepath.Base(ms.Path))
		}
		out = append(out, fmt.Sprintf("%s [%s]", it.Name, strings.Join(names, " | ")))
	}
	return out
}

// B1: film versions group only when every file is "<folder name>", optionally followed by
// " - <label>" (or another separator or a resolution), all with the same year.
func TestB1FilmVersionGrouping(t *testing.T) {
	p := newProbe(t, "B1", "Jellyfin groups film versions only with folder-name-prefixed names",
		"`{Movie Title} ({Release Year}) {Quality Full}` + `… - Español` never groups; `… ({Release Year}) - {Quality Full}` + `… - Español` groups into one item with two versions",
		false, "jellyfin")
	oldName, newName := uniq("Probe Group Old (2001)"), uniq("Probe Group New (2001)")
	oldDir, newDir := p.fresh(movies(oldName)), p.fresh(movies(newName))
	p.video(filepath.Join(oldDir, oldName+" WEBDL-1080p.mkv"), short)
	p.video(filepath.Join(oldDir, oldName+" - Español.mkv"), short)
	p.video(filepath.Join(newDir, newName+" - WEBDL-1080p.mkv"), short)
	p.video(filepath.Join(newDir, newName+" - Español.mkv"), short)
	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "library scan")

	oldItems, err := env.Jellyfin.ItemsUnder(p.ctx, oldDir, "Movie")
	p.must(err, "items (old naming)")
	newItems, err := env.Jellyfin.ItemsUnder(p.ctx, newDir, "Movie")
	p.must(err, "items (new naming)")
	p.evidence("oldNaming", sourcesOf(oldItems))
	p.evidence("newNaming", sourcesOf(newItems))
	p.observe("old naming: %d item(s) %v", len(oldItems), sourcesOf(oldItems))
	p.observe("new naming: %d item(s) %v", len(newItems), sourcesOf(newItems))

	oldGrouped := len(oldItems) == 1 && len(oldItems[0].MediaSources) == 2
	newGrouped := len(newItems) == 1 && len(newItems[0].MediaSources) == 2
	switch {
	case !oldGrouped && newGrouped:
		p.confirm()
	case oldGrouped:
		p.differ("default-style naming groups too; the naming fix is not needed")
	default:
		p.differ("the ' - ' naming does not group; second versions by naming will not work")
	}
}

// B2: Jellyfin 12.4 groups episode versions by SxxEyy, whatever follows.
func TestB2EpisodeVersionGrouping(t *testing.T) {
	p := newProbe(t, "B2", "Jellyfin 12.4 groups episode versions by SxxEyy",
		"Two files with the same strictly parsed SxxEyy in a Shows library form one episode with two versions, whatever the rest of the name",
		false, "jellyfin")
	name := uniq("Probe Versions Show")
	season := filepath.Join(p.fresh(shows(name)), "Season 01")
	p.video(filepath.Join(season, name+" - S01E01 - Pilot WEBDL-1080p.mkv"), short)
	p.video(filepath.Join(season, name+" - S01E01 - Pilot - Español.mkv"), short)
	p.video(filepath.Join(season, name+" - S01E02 - Second WEBDL-1080p.mkv"), short)
	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "library scan")

	items, err := env.Jellyfin.ItemsUnder(p.ctx, season, "Episode")
	p.must(err, "episodes")
	p.evidence("episodes", sourcesOf(items))
	p.observe("%d episode item(s): %v", len(items), sourcesOf(items))
	var e1 []testenv.Item
	for _, it := range items {
		if strings.Contains(it.Path, "S01E01") {
			e1 = append(e1, it)
		}
	}
	switch {
	case len(e1) == 1 && len(e1[0].MediaSources) == 2:
		p.confirm()
	case len(e1) == 0:
		p.must(fmt.Errorf("no S01E01 item"), "episodes")
	default:
		p.differ("S01E01 shows as %d item(s) with %d version(s); episode second versions are not possible", len(e1), len(e1[0].MediaSources))
	}
}

// B4 (Jellyfin side): -trailer files and extras/ folders are extras, not versions.
func TestB4JellyfinExtras(t *testing.T) {
	p := newProbe(t, "B4-jellyfin", "Jellyfin treats -trailer, -other and extras/ as extras, not versions",
		"Files Radarr skips (`-trailer`, `-other`, `extras/`) are not usable as versions: Jellyfin shows them as extras",
		false, "jellyfin")
	name := uniq("Probe Extras (2002)")
	dir := p.fresh(movies(name))
	p.video(filepath.Join(dir, name+" - WEBDL-1080p.mkv"), short)
	p.video(filepath.Join(dir, name+" - Español-trailer.mkv"), short)
	p.video(filepath.Join(dir, name+" - Español-other.mkv"), short)
	p.video(filepath.Join(dir, "extras", name+" - Español.mkv"), short)
	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "library scan")

	items, err := env.Jellyfin.ItemsUnder(p.ctx, dir, "Movie")
	p.must(err, "items")
	p.evidence("movies", sourcesOf(items))
	p.observe("%d movie item(s): %v", len(items), sourcesOf(items))
	if len(items) != 1 {
		p.differ("expected one film, got %d items: extras were taken as films", len(items))
		return
	}
	it := items[0]
	trailers, special, err := env.Jellyfin.Extras(p.ctx, it.ID)
	p.must(err, "extras")
	p.evidence("trailers", trailers)
	p.evidence("specialFeatures", special)
	p.observe("versions %d; trailers %v; special features %v", len(it.MediaSources), trailers, special)
	if len(it.MediaSources) == 1 && len(trailers)+len(special) == 3 {
		p.confirm()
	} else {
		p.differ("expected one version and three extras, got %d version(s), trailers %v, special features %v", len(it.MediaSources), trailers, special)
	}
}

// J1: POST /Library/Media/Updated makes a new file visible without a full scan, with
// real-time monitoring off. The plan lists the effect as unverified; M2 needs it within a minute.
func TestJ1MediaUpdated(t *testing.T) {
	p := newProbe(t, "J1", "Jellyfin picks up a path reported to /Library/Media/Updated",
		"Unverified in the plan. M2 expects a new film visible in the media server within a minute",
		false, "jellyfin")
	// Settle any scan from earlier probes, so a running scan cannot be what finds the file.
	p.must(env.Jellyfin.WaitScanIdle(p.ctx, scanTimeout), "wait for idle library")
	name := uniq("Probe Updated (2003)")
	dir := p.fresh(movies(name))
	file := filepath.Join(dir, name+" - WEBDL-1080p.mkv")
	p.video(file, short)
	before, err := env.Jellyfin.ItemsUnder(p.ctx, dir, "Movie")
	p.must(err, "items before")
	p.check(len(before) == 0, "the new film is not known before the report")

	for _, target := range []struct{ name, path string }{{"file", file}} {
		start := time.Now()
		p.must(env.Jellyfin.Do(p.ctx, "POST", "/Library/Media/Updated",
			testenv.M{"Updates": []testenv.M{{"Path": target.path, "UpdateType": "Created"}}}, nil), "Media/Updated")
		var found []testenv.Item
		err := testenv.WaitFor(p.ctx, 3*time.Minute, 2*time.Second, "item to appear", func() (bool, error) {
			var err error
			found, err = env.Jellyfin.ItemsUnder(p.ctx, dir, "Movie")
			return len(found) > 0, err
		})
		latency := time.Since(start).Round(time.Second)
		p.evidence("reportedPath", target.name)
		p.evidence("latencySeconds", latency.Seconds())
		var cfg struct {
			LibraryMonitorDelay int `json:"LibraryMonitorDelay"`
		}
		if env.Jellyfin.Do(p.ctx, "GET", "/System/Configuration", nil, &cfg) == nil {
			p.evidence("libraryMonitorDelaySeconds", cfg.LibraryMonitorDelay)
			p.observe("server LibraryMonitorDelay is %d s", cfg.LibraryMonitorDelay)
		}
		if err != nil {
			p.observe("reporting the %s path: the item did not appear within 3 min", target.name)
			p.differ("Media/Updated with the file path does not make the file visible; fall back to a library scan")
			return
		}
		p.observe("reporting the %s path: item visible after %s", target.name, latency)
		if cfg.LibraryMonitorDelay > 0 && latency >= time.Duration(cfg.LibraryMonitorDelay)*time.Second*9/10 {
			p.partial(fmt.Sprintf("it works, but only after the server's LibraryMonitorDelay (%d s): M2's \"within a minute\" holds only with a lower delay", cfg.LibraryMonitorDelay))
			return
		}
	}
	p.observed()
}

// J2: POST /Videos/MergeVersions groups two films; does the grouping survive a full scan?
func TestJ2MergeVersionsPersistence(t *testing.T) {
	p := newProbe(t, "J2", "Jellyfin MergeVersions survives a library rescan",
		"Unverified in the plan (second versions of films are grouped with it, decision #20)",
		false, "jellyfin")
	name := uniq("Probe Merge (2004)")
	dir := p.fresh(movies(name))
	p.video(filepath.Join(dir, name+" WEBDL-1080p.mkv"), short)
	p.video(filepath.Join(dir, name+" Español.mkv"), short)
	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "library scan")
	items, err := env.Jellyfin.ItemsUnder(p.ctx, dir, "Movie")
	p.must(err, "items")
	p.observe("before merge: %d item(s) %v", len(items), sourcesOf(items))
	p.check(len(items) == 2, "the two files must start as two films")

	p.must(env.Jellyfin.Do(p.ctx, "POST", "/Videos/MergeVersions"+testenv.Q("ids", items[0].ID+","+items[1].ID), nil, nil), "MergeVersions")
	after := func(stage string) int {
		list, err := env.Jellyfin.ItemsUnder(p.ctx, dir, "Movie")
		p.must(err, "items "+stage)
		maxSources := 0
		for _, it := range list {
			maxSources = max(maxSources, len(it.MediaSources))
		}
		p.observe("%s: %d item(s) %v", stage, len(list), sourcesOf(list))
		p.evidence(stage, sourcesOf(list))
		return maxSources
	}
	merged := after("after merge")
	p.check(merged == 2, "MergeVersions should give one item two versions")

	p.must(env.Jellyfin.ScanAndWait(p.ctx, scanTimeout), "rescan")
	afterScan := after("after full scan")
	// A metadata refresh of the folder re-resolves the files: the harder case.
	p.must(env.Jellyfin.Do(p.ctx, "POST", "/Items/"+items[0].ID+"/Refresh"+testenv.Q("metadataRefreshMode", "FullRefresh", "replaceAllMetadata", "true"), nil, nil), "item refresh")
	time.Sleep(5 * time.Second)
	p.must(env.Jellyfin.WaitScanIdle(p.ctx, scanTimeout), "refresh")
	afterRefresh := after("after full metadata refresh")
	p.evidence("survivesScan", afterScan == 2)
	p.evidence("survivesRefresh", afterRefresh == 2)
	p.observed()
}

// ---- J1 alternatives (issue #49: evidence for decision #22) ------------------------------

// refreshRun is one placement of a new film file and the wait until Jellyfin lists it.
type refreshRun struct {
	Seconds float64 `json:"seconds"` // trigger sent until the item is listed; -1: not within the limit
	// MediaInfoAtFirstSight is whether the item already had a runtime and streams when listed;
	// SecondsToMediaInfo is the time from the trigger until it did (-1: not within a minute).
	MediaInfoAtFirstSight bool    `json:"mediaInfoAtFirstSight"`
	SecondsToMediaInfo    float64 `json:"secondsToMediaInfo"`
	// ScanSeconds is the time until the library scan task was idle again (full scans only).
	ScanSeconds float64 `json:"scanSeconds,omitempty"`
}

// refreshSeq numbers the films the J1 alternatives place.
var refreshSeq int

const (
	fillerFilms = 200
	pollEvery   = 250 * time.Millisecond
)

// timeRefresh places a fresh film file n times, calls trigger after each, and measures how
// long Jellyfin takes to list the item. It stops early when an item is not listed within limit.
func timeRefresh(p *probe, label string, n int, limit time.Duration, fullScan bool, trigger func(dir, file string) error) []refreshRun {
	p.t.Helper()
	var runs []refreshRun
	for i := 1; i <= n; i++ {
		p.must(env.Jellyfin.WaitScanIdle(p.ctx, scanTimeout), "wait for idle library")
		refreshSeq++ // names must never repeat: Jellyfin keeps items by path
		name := uniq(fmt.Sprintf("Probe %s %d (2005)", label, refreshSeq))
		term := name[:strings.Index(name, " (")]
		dir := p.fresh(movies(name))
		file := filepath.Join(dir, name+" - WEBDL-1080p.mkv")
		p.video(file, short)
		known, err := env.Jellyfin.FindMovie(p.ctx, term, dir)
		p.must(err, "look for the film before the trigger")
		p.check(known == nil, "the new film is not known before the trigger")

		start := time.Now()
		p.must(trigger(dir, file), label+" trigger")
		var it *testenv.Item
		err = testenv.WaitFor(p.ctx, limit, pollEvery, "item to appear", func() (bool, error) {
			var err error
			it, err = env.Jellyfin.FindMovie(p.ctx, term, dir)
			return it != nil, err
		})
		run := refreshRun{Seconds: -1, SecondsToMediaInfo: -1}
		if err != nil {
			p.observe("%s run %d: not listed within %s", label, i, limit)
			runs = append(runs, run)
			return runs
		}
		run.Seconds = time.Since(start).Seconds()
		run.MediaInfoAtFirstSight = it.HasMediaInfo()
		if run.MediaInfoAtFirstSight {
			run.SecondsToMediaInfo = run.Seconds
		} else if testenv.WaitFor(p.ctx, time.Minute, pollEvery, "media info", func() (bool, error) {
			it, err = env.Jellyfin.FindMovie(p.ctx, term, dir)
			return it != nil && it.HasMediaInfo(), err
		}) == nil {
			run.SecondsToMediaInfo = time.Since(start).Seconds()
		}
		if fullScan {
			p.must(env.Jellyfin.WaitScanIdle(p.ctx, 15*time.Minute), "wait for the scan to end")
			run.ScanSeconds = time.Since(start).Seconds()
		}
		runs = append(runs, run)
	}
	return runs
}

// describeRuns is the min, median and max of the runs, and how many items were complete at
// first sight.
func describeRuns(runs []refreshRun) string {
	var secs []float64
	complete := 0
	for _, r := range runs {
		if r.Seconds < 0 {
			return fmt.Sprintf("%d run(s), the item did not appear within the limit", len(runs))
		}
		secs = append(secs, r.Seconds)
		if r.MediaInfoAtFirstSight {
			complete++
		}
	}
	sort.Float64s(secs)
	out := fmt.Sprintf("%d run(s): min %.1f s, median %.1f s, max %.1f s; runtime and streams present at first sight in %d of %d",
		len(secs), secs[0], secs[len(secs)/2], secs[len(secs)-1], complete, len(runs))
	var info []float64
	for _, r := range runs {
		info = append(info, r.SecondsToMediaInfo)
	}
	sort.Float64s(info)
	if complete < len(runs) {
		out += fmt.Sprintf(" (runtime and streams after min %.1f s, median %.1f s, max %.1f s; -1: not within a minute)", info[0], info[len(info)/2], info[len(info)-1])
	}
	if runs[0].ScanSeconds > 0 {
		var total float64
		for _, r := range runs {
			total += r.ScanSeconds
		}
		out += fmt.Sprintf("; the scan task ran %.0f s on average", total/float64(len(runs)))
	}
	return out
}

// jellyfinFillers adds n small films to the Movies library and scans them in, so the refresh
// timings are taken on a library of a realistic size. It returns the time the scan took.
func jellyfinFillers(p *probe, n int) time.Duration {
	p.t.Helper()
	p.must(env.Jellyfin.WaitScanIdle(p.ctx, scanTimeout), "wait for idle library")
	first := ""
	var dirs []string
	for i := 1; i <= n; i++ {
		name := uniq(fmt.Sprintf("Probe Filler %03d (%d)", i, 1980+i%40))
		dir := p.fresh(movies(name))
		dirs = append(dirs, dir)
		file := filepath.Join(dir, name+" - WEBDL-1080p.mkv")
		if first == "" {
			p.video(file, short)
			first = file
		} else {
			p.must(os.Link(first, file), "hard link filler")
		}
	}
	p.t.Cleanup(func() {
		for _, d := range dirs {
			_ = os.RemoveAll(d)
		}
		ctx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		_ = env.Jellyfin.ScanAndWait(ctx, scanTimeout)
	})
	start := time.Now()
	p.must(env.Jellyfin.ScanAndWait(p.ctx, 15*time.Minute), "scan in the filler films")
	return time.Since(start).Round(time.Second)
}

// TestJ1RefreshAlternatives measures four ways to make a new film visible (J1a to J1d), each
// on a library of about two hundred films, several runs each, with real-time monitoring off.
// The existing J1 probe is the baseline: the file path reported to /Library/Media/Updated.
func TestJ1RefreshAlternatives(t *testing.T) {
	const claim = "Unverified in the plan. Decision #22: probe whether the item becomes visible without waiting for LibraryMonitorDelay (J1: file path, 60 s)"
	delay, err := env.Jellyfin.LibraryMonitorDelay(t.Context())
	if err != nil {
		t.Fatalf("read LibraryMonitorDelay: %v", err)
	}
	libraryID, err := env.Jellyfin.LibraryID(t.Context(), filepath.Join(env.Data, "media", "movies"))
	if err != nil {
		t.Fatalf("find the Movies library: %v", err)
	}

	p := newProbe(t, "J1a", "Jellyfin: reporting the film's folder to /Library/Media/Updated", claim, false, "jellyfin")
	scan := jellyfinFillers(p, fillerFilms)
	var total struct {
		Count int `json:"TotalRecordCount"`
	}
	p.must(env.Jellyfin.Do(p.ctx, "GET", "/Items"+testenv.Q("Recursive", "true", "IncludeItemTypes", "Movie", "Limit", "0"), nil, &total), "count films")
	version, err := env.Jellyfin.Version(p.ctx)
	p.must(err, "server version")
	p.observe("library of %d films; server %s; LibraryMonitorDelay %d s; scanning in %d new films took %s", total.Count, version, delay, fillerFilms, scan)
	p.evidence("films", total.Count)
	p.evidence("libraryMonitorDelaySeconds", delay)

	runs := timeRefresh(p, "J1a", 3, 3*time.Minute, false, func(dir, _ string) error { return env.Jellyfin.MediaUpdated(p.ctx, dir) })
	p.evidence("runs", runs)
	p.observe("folder path reported, delay %d s: %s", delay, describeRuns(runs))
	p.observed()

	t.Run("J1b", func(t *testing.T) {
		p := newProbe(t, "J1b", "Jellyfin: refreshing the library's folder item (POST /Items/{id}/Refresh)", claim, false, "jellyfin")
		for _, recursive := range []bool{false, true} {
			label := map[bool]string{false: "without recursion", true: "with recursion"}[recursive]
			runs := timeRefresh(p, "J1b", 5, 3*time.Minute, false, func(_, _ string) error {
				return env.Jellyfin.RefreshItem(p.ctx, libraryID, recursive)
			})
			p.evidence("runs "+label, runs)
			p.observe("refresh of the library item %s: %s", label, describeRuns(runs))
		}
		p.observed()
	})

	t.Run("J1c", func(t *testing.T) {
		p := newProbe(t, "J1c", "Jellyfin: /Library/Media/Updated (file path) with a lower LibraryMonitorDelay", claim, false, "jellyfin")
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := env.Jellyfin.SetLibraryMonitorDelay(ctx, delay); err != nil {
				t.Errorf("restore LibraryMonitorDelay to %d: %v", delay, err)
			}
		})
		for _, d := range []int{1, 5, 15, 30} {
			p.must(env.Jellyfin.SetLibraryMonitorDelay(p.ctx, d), fmt.Sprintf("set LibraryMonitorDelay %d", d))
			runs := timeRefresh(p, "J1c", 3, 3*time.Minute, false, func(_, file string) error { return env.Jellyfin.MediaUpdated(p.ctx, file) })
			p.evidence(fmt.Sprintf("runs with delay %d", d), runs)
			p.observe("file path reported, LibraryMonitorDelay %d s: %s", d, describeRuns(runs))
		}
		p.observed()
	})

	t.Run("J1d", func(t *testing.T) {
		p := newProbe(t, "J1d", "Jellyfin: a full POST /Library/Refresh (upper bound)", claim, false, "jellyfin")
		runs := timeRefresh(p, "J1d", 3, 10*time.Minute, true, func(_, _ string) error {
			return env.Jellyfin.Do(p.ctx, "POST", "/Library/Refresh", nil, nil)
		})
		p.evidence("runs", runs)
		p.observe("full library refresh on a library of about %d films: %s", total.Count, describeRuns(runs))
		p.observed()
	})
}
