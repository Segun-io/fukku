// Command refreshscale measures how long a newly imported film takes to show up in Jellyfin
// after Fukku's default media server refresh (mediaServer.refreshMethod libraryRefresh,
// POST /Items/{libraryId}/Refresh), on generated libraries of growing size (issue #161). It
// also times the fallback full scan (mediaServer.refreshFallback, POST /Library/Refresh) and
// one Find call (the step's poll) at each size.
//
// It runs only against the throwaway Jellyfin of test/env/refreshscale/compose.yaml, which
// test/env/refresh-scale.sh starts and removes (make refresh-scale): never against the shared
// test environment or a real server.
//
//	refreshscale setup     wait for Jellyfin and complete its startup wizard
//	refreshscale measure   grow the library size by size and time the refreshes
//
// Every film is generated: the library's films are hard links to one tiny video, each in its
// own folder, as Radarr lays them out; each measured film is a copy of a short 1080p video.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/mediaserver"
	"github.com/segun-io/fukku/test/fixtures"
	"github.com/segun-io/fukku/test/testenv"
)

const (
	library  = "Movies"
	pollStep = 250 * time.Millisecond
	// The markers of the generated block in docs/compatibility.md.
	startMarker = "<!-- refresh-scale:start -->"
	endMarker   = "<!-- refresh-scale:end -->"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: refreshscale setup | refreshscale measure [flags]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	url := os.Getenv("FUKKU_TEST_JELLYFIN_URL")
	var err error
	switch os.Args[1] {
	case "setup":
		var version string
		if version, err = testenv.JellyfinStartup(ctx, url); err == nil {
			fmt.Printf("refreshscale: Jellyfin %s started, wizard done\n", version)
		}
	case "measure":
		err = measure(ctx, url, os.Getenv("FUKKU_TEST_JELLYFIN_KEY"), os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "refreshscale:", err)
		os.Exit(1)
	}
}

// run is one new film placed in the library and the wait until Fukku's Find returns it.
type run struct {
	// Seconds is from the request (refresh or scan) until Find returned the item; -1 when it
	// did not within the limit.
	Seconds float64 `json:"seconds"`
	// MediaInfo is whether the item had a runtime and streams when Find first returned it.
	MediaInfo bool `json:"mediaInfoAtFirstSight"`
	// SecondsToMediaInfo is from the request until the item had them; -1 when not within a
	// minute of being listed.
	SecondsToMediaInfo float64 `json:"secondsToMediaInfo"`
	// ScanSeconds is from the scan request until the scan task was idle again (scans only).
	ScanSeconds float64 `json:"scanSeconds,omitempty"`
}

// sizeResult is what was measured at one library size.
type sizeResult struct {
	Fillers int `json:"fillers"` // generated films placed to reach this size
	Films   int `json:"films"`   // films Jellyfin listed when the measurement started
	// FillScanSeconds is the full scan that took in the films added for this size.
	FillScanSeconds float64 `json:"fillScanSeconds"`
	Refresh         []run   `json:"libraryRefresh"`
	Scan            []run   `json:"fallbackScan"`
	// FindMedianSeconds and FindMaxSeconds are over every Find call of the runs above;
	// FindItems is how many items one Find answer carries; FindErrors counts failed calls.
	FindMedianSeconds float64 `json:"findMedianSeconds"`
	FindMaxSeconds    float64 `json:"findMaxSeconds"`
	FindItems         int     `json:"findItems"`
	FindErrors        int     `json:"findErrors"`
}

type report struct {
	At       time.Time    `json:"at"`
	Jellyfin string       `json:"jellyfin"`
	Image    string       `json:"image"`
	CPUs     int          `json:"cpus"`
	Runs     int          `json:"runsPerSize"`
	Scans    int          `json:"scansPerSize"`
	Sizes    []sizeResult `json:"sizes"`
}

func measure(ctx context.Context, url, key string, args []string) error {
	fs := flag.NewFlagSet("measure", flag.ContinueOnError)
	sizesFlag := fs.String("sizes", "200,2000,10000", "library sizes to measure, in films, ascending")
	runs := fs.Int("runs", 5, "library refreshes timed at each size")
	scans := fs.Int("scans", 1, "fallback full scans timed at each size")
	limit := fs.Duration("limit", 10*time.Minute, "how long to wait for one film before giving up")
	data := fs.String("data", "/data", "the library filesystem, as Jellyfin sees it too")
	out := fs.String("out", "", "write the results as JSON here (after each size)")
	doc := fs.String("doc", "", "write the results table into this document, between the refresh-scale markers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var sizes []int
	for f := range strings.SplitSeq(*sizesFlag, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 1 || (len(sizes) > 0 && n <= sizes[len(sizes)-1]) {
			return fmt.Errorf("-sizes: want ascending positive numbers, got %q", *sizesFlag)
		}
		sizes = append(sizes, n)
	}

	j, err := testenv.NewJellyfin(url, key)
	if err != nil {
		return err
	}
	// Jellyfin can restart itself once after the wizard: wait until the key is accepted.
	if err := testenv.WaitFor(ctx, 3*time.Minute, 2*time.Second, "jellyfin with the API key", func() (bool, error) {
		err := j.Do(ctx, "GET", "/System/Info", nil, nil)
		return err == nil, err
	}); err != nil {
		return err
	}
	version, err := j.Version(ctx)
	if err != nil {
		return err
	}
	root := filepath.Join(*data, "movies")
	libraryID, err := ensureLibrary(ctx, j, root)
	if err != nil {
		return err
	}
	logf("Jellyfin %s, library %s (%s)", version, library, libraryID)

	// Fukku's own client, with the defaults an operator gets (libraryRefresh, the request
	// timeout): what the mediaServerRefresh step sends and how it looks for the item.
	msCfg := config.Defaults().MediaServer
	msCfg.Provider, msCfg.URL, msCfg.APIKey = "jellyfin", url, config.Secret(key)
	ms := mediaserver.NewJellyfin(msCfg)

	fixtureDir := filepath.Join(*data, ".fixtures")
	filler := filepath.Join(fixtureDir, "filler.mkv")
	fresh := filepath.Join(fixtureDir, "new.mkv")
	if err := os.MkdirAll(fixtureDir, 0o750); err != nil {
		return err
	}
	if err := fixtures.Generate(ctx, filler, fixtures.Video{Duration: time.Second, Width: 160, Height: 90, FrameRate: "5",
		Audio: []fixtures.Audio{{Language: "eng"}}}); err != nil {
		return fmt.Errorf("generate the filler video: %w", err)
	}
	// As the J1 probes' films: Jellyfin has no sample detection, so 10 s is enough.
	if err := fixtures.Generate(ctx, fresh, fixtures.Video{Duration: 10 * time.Second, Width: 1920, Height: 1080, FrameRate: "5",
		Audio: []fixtures.Audio{{Language: "eng", Default: true}}}); err != nil {
		return fmt.Errorf("generate the new film's video: %w", err)
	}

	rep := report{At: time.Now().UTC(), Jellyfin: version, Image: os.Getenv("FUKKU_SCALE_JELLYFIN_IMAGE"),
		CPUs: runtime.NumCPU(), Runs: *runs, Scans: *scans}
	m := &meter{j: j, ms: ms, root: root, fresh: fresh, limit: *limit}
	have := 0
	for _, size := range sizes {
		logf("size %d: placing %d films", size, size-have)
		for i := have + 1; i <= size; i++ {
			name := fmt.Sprintf("Scale Filler %05d (%d)", i, 1950+i%70)
			dir := filepath.Join(root, name)
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return err
			}
			if err := os.Link(filler, filepath.Join(dir, name+" WEBDL-1080p.mkv")); err != nil {
				return err
			}
		}
		res := sizeResult{Fillers: size}
		want := size + m.placed
		start := time.Now()
		if err := j.ScanAndWait(ctx, 3*time.Hour); err != nil {
			return fmt.Errorf("scan in the films: %w", err)
		}
		// The scan task can start late; the count is what says the films are in.
		if err := testenv.WaitFor(ctx, time.Hour, 2*time.Second, fmt.Sprintf("%d films listed", want), func() (bool, error) {
			n, err := countFilms(ctx, j)
			res.Films = n
			return n >= want, err
		}); err != nil {
			return err
		}
		if err := j.WaitScanIdle(ctx, time.Hour); err != nil {
			return err
		}
		res.FillScanSeconds = time.Since(start).Seconds()
		have = size
		logf("size %d: %d films listed, the scan took %.0f s", size, res.Films, res.FillScanSeconds)

		m.finds, m.findErrors = nil, 0
		for i := 1; i <= *runs; i++ {
			r, err := m.time(ctx, fmt.Sprintf("Scale New %05d R%d", size, i), false)
			if err != nil {
				return err
			}
			res.Refresh = append(res.Refresh, r)
			logf("size %d: libraryRefresh run %d: visible after %.2f s (runtime and streams at first sight: %v, after %.2f s)", size, i, r.Seconds, r.MediaInfo, r.SecondsToMediaInfo)
		}
		for i := 1; i <= *scans; i++ {
			r, err := m.time(ctx, fmt.Sprintf("Scale New %05d S%d", size, i), true)
			if err != nil {
				return err
			}
			res.Scan = append(res.Scan, r)
			logf("size %d: fallback scan run %d: visible after %.2f s, the scan ran %.0f s", size, i, r.Seconds, r.ScanSeconds)
		}
		res.FindMedianSeconds, res.FindMaxSeconds = median(m.finds), slices.Max(m.finds)
		res.FindErrors = m.findErrors
		if res.FindItems, err = findItems(ctx, j, filepath.Join(root, "Scale Filler 00001 (1951)", "Scale Filler 00001 (1951) WEBDL-1080p.mkv")); err != nil {
			return err
		}
		logf("size %d: one Find call: median %.3f s, max %.3f s, %d item(s) per answer, %d failed", size,
			res.FindMedianSeconds, res.FindMaxSeconds, res.FindItems, res.FindErrors)
		rep.Sizes = append(rep.Sizes, res)
		if *out != "" {
			if err := writeJSON(*out, rep); err != nil {
				return err
			}
		}
	}
	fmt.Print(render(rep))
	if *doc != "" {
		return writeDoc(*doc, render(rep))
	}
	return nil
}

// meter places new films and times how long Fukku's Find takes to return them.
type meter struct {
	j          *testenv.Jellyfin
	ms         *mediaserver.Jellyfin
	root       string
	fresh      string
	limit      time.Duration
	placed     int       // films placed by the runs so far
	finds      []float64 // seconds of each Find call at this size
	findErrors int
}

// time places a new film named name, sends the default refresh (or, with scan, the
// fallback's full scan) and polls Find until it returns the item.
func (m *meter) time(ctx context.Context, name string, scan bool) (run, error) {
	if err := m.j.WaitScanIdle(ctx, time.Hour); err != nil {
		return run{}, err
	}
	name += " (2024)"
	dir := filepath.Join(m.root, name)
	file := filepath.Join(dir, name+" WEBDL-1080p.mkv")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return run{}, err
	}
	if err := copyFile(m.fresh, file); err != nil {
		return run{}, err
	}
	m.placed++
	if id, err := m.ms.Find(ctx, file); err != nil {
		return run{}, fmt.Errorf("find the new film before the refresh: %w", err)
	} else if id != "" {
		return run{}, fmt.Errorf("the new film is listed before the refresh (item %s)", id)
	}

	r := run{Seconds: -1, SecondsToMediaInfo: -1}
	start := time.Now()
	if scan {
		started, err := m.ms.Scan(ctx)
		if err != nil {
			return run{}, err
		}
		if !started {
			return run{}, errors.New("a library scan was already running")
		}
	} else if err := m.ms.Refresh(ctx, []string{file}); err != nil {
		return run{}, err
	}
	id := ""
	for id == "" && time.Since(start) < m.limit {
		t := time.Now()
		var err error
		id, err = m.ms.Find(ctx, file)
		m.finds = append(m.finds, time.Since(t).Seconds())
		if err != nil {
			if ctx.Err() != nil {
				return run{}, ctx.Err()
			}
			m.findErrors++
			logf("Find failed: %v", err)
		}
		if id == "" {
			time.Sleep(pollStep)
		}
	}
	if id != "" {
		r.Seconds = time.Since(start).Seconds()
		it, err := m.j.Item(ctx, id)
		if err != nil {
			return run{}, err
		}
		r.MediaInfo = it.HasMediaInfo()
		if err := testenv.WaitFor(ctx, time.Minute, pollStep, "runtime and streams", func() (bool, error) {
			it, err := m.j.Item(ctx, id)
			return it.HasMediaInfo(), err
		}); err == nil {
			r.SecondsToMediaInfo = time.Since(start).Seconds()
		}
	}
	if scan {
		if err := m.j.WaitScanIdle(ctx, 3*time.Hour); err != nil {
			return run{}, err
		}
		r.ScanSeconds = time.Since(start).Seconds()
	} else {
		// The refresh goes on after the new item is listed; let it end before the next run.
		time.Sleep(5 * time.Second)
	}
	return r, nil
}

// ensureLibrary creates the Movies library on root, configured as test/env/seed configures the
// shared environment's (real-time monitoring off, no internet metadata), and returns its id.
func ensureLibrary(ctx context.Context, j *testenv.Jellyfin, root string) (string, error) {
	if id, err := j.LibraryID(ctx, root); err == nil {
		return id, nil
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", err
	}
	opts := testenv.M{"LibraryOptions": testenv.M{
		"EnableRealtimeMonitor": false, "EnablePhotos": false, "EnableInternetProviders": false,
		"SaveLocalMetadata": false, "MetadataSavers": []string{}, "DisabledLocalMetadataReaders": []string{},
		"EnableEmbeddedTitles": false, "EnableChapterImageExtraction": false, "ExtractChapterImagesDuringLibraryScan": false,
		"AutomaticRefreshIntervalDays": 0, "EnableTrickplayImageExtraction": false,
		"PathInfos":   []testenv.M{{"Path": root}},
		"TypeOptions": []testenv.M{{"Type": "Movie", "MetadataFetchers": []string{}, "ImageFetchers": []string{}}},
	}}
	q := testenv.Q("name", library, "collectionType", "movies", "refreshLibrary", "false")
	if err := j.Do(ctx, "POST", "/Library/VirtualFolders"+q, opts, nil); err != nil {
		return "", err
	}
	return j.LibraryID(ctx, root)
}

func countFilms(ctx context.Context, j *testenv.Jellyfin) (int, error) {
	var total struct {
		Count int `json:"TotalRecordCount"`
	}
	err := j.Do(ctx, "GET", "/Items"+testenv.Q("Recursive", "true", "IncludeItemTypes", "Movie", "Limit", "0"), nil, &total)
	return total.Count, err
}

// findItems sends the query of Fukku's Find (internal/mediaserver) for path and returns how
// many items the answer carries: 1 if the server filters by the path, every film if not.
func findItems(ctx context.Context, j *testenv.Jellyfin, path string) (int, error) {
	var res struct {
		Items []json.RawMessage `json:"Items"`
	}
	q := testenv.Q("Recursive", "true", "IncludeItemTypes", "Movie,Episode,Series", "Path", path, "Fields", "Path", "EnableImages", "false")
	err := j.Do(ctx, "GET", "/Items"+q, nil, &res)
	return len(res.Items), err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	return s[len(s)/2]
}

func logf(format string, args ...any) {
	fmt.Printf("%s %s\n", time.Now().UTC().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func writeJSON(path string, rep report) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// seconds is a list of runs' times as "min / median / max s", or says how many runs did not
// see the film within the limit.
func seconds(runs []run) string {
	var s []float64
	for _, r := range runs {
		if r.Seconds < 0 {
			return fmt.Sprintf("not visible within the limit in %d of %d runs", countIf(runs, func(r run) bool { return r.Seconds < 0 }), len(runs))
		}
		s = append(s, r.Seconds)
	}
	if len(s) == 0 {
		return "not measured"
	}
	return fmt.Sprintf("%.1f / %.1f / %.1f s", slices.Min(s), median(s), slices.Max(s))
}

func countIf(runs []run, f func(run) bool) int {
	n := 0
	for _, r := range runs {
		if f(r) {
			n++
		}
	}
	return n
}

// render is the results table of docs/compatibility.md (the block between the markers).
func render(rep report) string {
	var b strings.Builder
	image := rep.Image
	if i := strings.Index(image, "@"); i > 0 {
		image = image[:i]
	}
	fmt.Fprintf(&b, "\nRun of %s UTC against Jellyfin %s (`%s`), %d library refreshes and %d fallback scan(s) per size, "+
		"Find polled every %s, on %d CPUs with the library on a local disk.\n\n",
		rep.At.Format("2006-01-02 15:04"), rep.Jellyfin, image, rep.Runs, rep.Scans, pollStep, rep.CPUs)
	b.WriteString("| Films | `libraryRefresh`: visible after (min / median / max) | Runtime and streams at first sight | Runtime and streams after (median) | " +
		"Fallback scan: visible after (min / median / max) | Fallback scan ran | One `Find` call (median / max) | Items per `Find` answer | Scanning in the generated films |\n")
	b.WriteString("| ---: | --- | --- | --- | --- | --- | --- | ---: | --- |\n")
	for _, s := range rep.Sizes {
		var scanRan []float64
		for _, r := range s.Scan {
			scanRan = append(scanRan, r.ScanSeconds)
		}
		ran := "not measured"
		if len(scanRan) > 0 {
			ran = fmt.Sprintf("%.0f s", median(scanRan))
		}
		find := fmt.Sprintf("%.3f / %.3f s", s.FindMedianSeconds, s.FindMaxSeconds)
		if s.FindErrors > 0 {
			find += fmt.Sprintf(", %d failed", s.FindErrors)
		}
		var info []float64
		for _, r := range s.Refresh {
			info = append(info, r.SecondsToMediaInfo)
		}
		infoAfter := fmt.Sprintf("%.1f s", median(info))
		if slices.Contains(info, -1) {
			infoAfter += " (-1: not within a minute)"
		}
		fmt.Fprintf(&b, "| %d | %s | %d of %d | %s | %s | %s | %s | %d | %.0f s (%d new) |\n",
			s.Films, seconds(s.Refresh), countIf(s.Refresh, func(r run) bool { return r.MediaInfo }), len(s.Refresh), infoAfter,
			seconds(s.Scan), ran, find, s.FindItems, s.FillScanSeconds, s.Fillers-prevFillers(rep, s))
	}
	b.WriteString("\n")
	return b.String()
}

// prevFillers is the number of generated films placed before size s.
func prevFillers(rep report, s sizeResult) int {
	prev := 0
	for _, x := range rep.Sizes {
		if x.Fillers < s.Fillers {
			prev = x.Fillers
		}
	}
	return prev
}

func writeDoc(path, block string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	i, k := strings.Index(text, startMarker), strings.Index(text, endMarker)
	if i < 0 || k < i {
		return fmt.Errorf("%s has no %s … %s block", path, startMarker, endMarker)
	}
	return os.WriteFile(path, []byte(text[:i+len(startMarker)]+"\n"+block+text[k:]), 0o644)
}
