package mediaserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/mediaserver"
	"github.com/segun-io/fukku/internal/mediaserver/mediaservertest"
	"github.com/segun-io/fukku/internal/store"
)

// In these tests the app sees /movies and /tv, Fukku sees /data/media/..., and the media
// server sees /media/...: the path sent must be the server's.
const (
	appFile    = "/movies/Film (2020)/Film (2020) WEBDL-1080p.mkv"
	serverFile = "/media/movies/Film (2020)/Film (2020) WEBDL-1080p.mkv"
)

func stepConfig() config.Config {
	cfg := config.Defaults()
	cfg.MediaServer.Provider = "jellyfin"
	cfg.MediaServer.ItemWait = config.Duration(300 * time.Millisecond)
	cfg.MediaServer.ItemPollInterval = config.Duration(5 * time.Millisecond)
	cfg.Paths.Mappings.Radarr = []config.PathMapping{{From: "/data/media/movies", To: "/movies"}}
	cfg.Paths.Mappings.Sonarr = []config.PathMapping{{From: "/data/media/tv", To: "/tv"}}
	cfg.Paths.Mappings.MediaServer = []config.PathMapping{{From: "/data/media", To: "/media"}}
	return cfg
}

func movieStep(server mediaserver.MediaServer) *mediaserver.Step {
	movies := &arrtest.FakeMovies{Library: []arr.Movie{{ID: 7, Title: "Film", File: &arr.MovieFile{ID: 1, Path: appFile}}}}
	return &mediaserver.Step{Server: server, Movies: movies, Config: stepConfig()}
}

var (
	job    = store.Job{ID: "j1"}
	target = store.Target{App: "radarr", TitleID: 7}
)

func TestStepFoundAtOnce(t *testing.T) {
	fake := &mediaservertest.Fake{Pending: map[string]string{serverFile: "item-1"}}
	done, detail, err := movieStep(fake).Run(t.Context(), job, target, nil)
	if err != nil || !done || mediaserver.ItemID(detail) != "item-1" {
		t.Fatalf("Run = %v, %q, %v", done, detail, err)
	}
	if len(fake.Refreshed) != 1 || len(fake.Refreshed[0]) != 1 || fake.Refreshed[0][0] != serverFile {
		t.Errorf("refreshed %v, want the server's view %q", fake.Refreshed, serverFile)
	}
	for _, p := range fake.FindCalls {
		if p != serverFile {
			t.Errorf("Find was given %q, not the server's view", p)
		}
	}
	if link := mediaserver.TitleLink("https://media.example.com/web/#/details?id={itemId}", mediaserver.ItemID(detail)); link != "https://media.example.com/web/#/details?id=item-1" {
		t.Errorf("link = %q", link)
	}
}

func TestStepFoundLate(t *testing.T) {
	fake := &mediaservertest.Fake{Pending: map[string]string{serverFile: "item-2"}, FindsNeeded: 3}
	done, detail, err := movieStep(fake).Run(t.Context(), job, target, nil)
	if err != nil || !done || mediaserver.ItemID(detail) != "item-2" {
		t.Fatalf("Run = %v, %q, %v", done, detail, err)
	}
	if len(fake.FindCalls) != 4 {
		t.Errorf("Find calls = %d, want 4", len(fake.FindCalls))
	}
}

func TestStepNeverFoundFails(t *testing.T) {
	fake := &mediaservertest.Fake{}
	done, _, err := movieStep(fake).Run(t.Context(), job, target, nil)
	if done || !errors.Is(err, mediaserver.ErrNotVisible) {
		t.Fatalf("Run = %v, %v; want not done and ErrNotVisible", done, err)
	}
	if len(fake.FindCalls) < 2 {
		t.Errorf("it should poll: %d Find calls", len(fake.FindCalls))
	}
}

func TestStepRefreshAndFindErrors(t *testing.T) {
	boom := errors.New("boom")
	for _, method := range []string{"Refresh", "Find"} {
		fake := &mediaservertest.Fake{Items: map[string]string{serverFile: "x"}, Err: map[string]error{method: boom}}
		done, _, err := movieStep(fake).Run(t.Context(), job, target, nil)
		if done || !errors.Is(err, boom) {
			t.Errorf("%s: Run = %v, %v", method, done, err)
		}
	}
}

func TestStepProviderNoneIsSkipped(t *testing.T) {
	fake := &mediaservertest.Fake{}
	s := movieStep(fake)
	s.Config.MediaServer.Provider = "none"
	_, _, err := s.Run(t.Context(), job, target, nil)
	if reason, ok := flows.SkipReason(err); !ok || reason != "no media server is configured" {
		t.Fatalf("Run = %v", err)
	}
	if len(fake.Refreshed) != 0 || len(fake.FindCalls) != 0 {
		t.Error("the server must not be contacted")
	}
}

func TestStepNoTrackedFile(t *testing.T) {
	fake := &mediaservertest.Fake{}
	s := movieStep(fake)
	s.Movies = &arrtest.FakeMovies{Library: []arr.Movie{{ID: 7, Title: "Film"}}}
	if done, _, err := s.Run(t.Context(), job, target, nil); done || err == nil {
		t.Errorf("Run = %v, %v", done, err)
	}
	if len(fake.Refreshed) != 0 {
		t.Error("nothing to refresh")
	}
}

func proposal(t *testing.T, ids ...int) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(arr.Row{EpisodeIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStepSeasonWithASkippedRow(t *testing.T) {
	series := &arrtest.FakeSeries{
		Eps: map[int][]arr.Episode{3: {{ID: 31, FileID: 501}, {ID: 32, FileID: 502}, {ID: 33}}},
		Files: map[int][]arr.EpisodeFile{3: {
			{ID: 501, Path: "/tv/Show/Season 1/Show - S01E01.mkv"},
			{ID: 502, Path: "/tv/Show/Season 1/Show - S01E02.mkv"},
			{ID: 599, Path: "/tv/Show/Season 1/Show - S01E09.mkv"}, // not part of this job
		}},
	}
	rows := []store.Row{
		{ID: "r1", Proposal: proposal(t, 31), Result: store.ResultImported},
		{ID: "r2", Proposal: proposal(t, 32), Result: store.ResultImported},
		{ID: "r3", Proposal: proposal(t, 33), Skip: true, Result: store.ResultSkipped},
	}
	series.Library = []arr.Series{{ID: 3, Title: "Show", Path: "/tv/Show"}}
	e1, e2 := "/media/tv/Show/Season 1/Show - S01E01.mkv", "/media/tv/Show/Season 1/Show - S01E02.mkv"
	fake := &mediaservertest.Fake{Pending: map[string]string{"/media/tv/Show": "series-1", e1: "ep-1", e2: "ep-2"}}
	s := &mediaserver.Step{Server: fake, Series: series, Config: stepConfig()}
	done, detail, err := s.Run(t.Context(), job, store.Target{App: "sonarr", TitleID: 3}, rows)
	if err != nil || !done || mediaserver.ItemID(detail) != "series-1" {
		t.Fatalf("Run = %v, %q, %v", done, detail, err)
	}
	if got := fake.Refreshed[0]; len(got) != 2 || got[0] != e1 || got[1] != e2 {
		t.Errorf("refreshed %v", got)
	}
}

func TestItemID(t *testing.T) {
	if mediaserver.ItemID("item abc") != "abc" || mediaserver.ItemID("no media server") != "" {
		t.Error("ItemID")
	}
}

// The step is the job's Linker: its detail and mediaServer.titleLinkTemplate make the link.
func TestStepLink(t *testing.T) {
	s := movieStep(&mediaservertest.Fake{})
	s.Config.MediaServer.TitleLinkTemplate = "https://media.example.com/web/#/details?id={itemId}"
	if l, ok := s.Link("item abc123"); !ok || l != "https://media.example.com/web/#/details?id=abc123" {
		t.Errorf("Link = %q, %v", l, ok)
	}
	if _, ok := s.Link(""); ok {
		t.Error("a link without an item")
	}
	s.Config.MediaServer.TitleLinkTemplate = ""
	if _, ok := s.Link("item abc123"); ok {
		t.Error("a link without a template")
	}
}

// neverScannedJellyfin is an httptest Jellyfin like a library that was never scanned with
// content in it: refreshing the library item answers 204 and lists nothing; only a full scan
// (POST /Library/Refresh) makes the film visible.
type neverScannedJellyfin struct {
	mu      sync.Mutex
	scanned bool
	scans   int
	calls   []string
}

func (f *neverScannedJellyfin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	switch r.Method + " " + r.URL.Path {
	case "GET /Library/VirtualFolders":
		_, _ = w.Write([]byte(`[{"Name":"Movies","Locations":["/media/movies"],"ItemId":"lib-movies"}]`))
	case "GET /Items":
		items := []map[string]string{}
		if f.scanned && r.URL.Query().Get("Path") == serverFile {
			items = append(items, map[string]string{"Id": "item-scan", "Path": serverFile})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": items})
	case "POST /Library/Refresh":
		f.scanned = true
		f.scans++
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func jellyfinStep(t *testing.T, f *neverScannedJellyfin, fallback bool, method string) *mediaserver.Step {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s := movieStep(nil)
	s.Config.MediaServer.RefreshFallback = fallback
	s.Config.MediaServer.RefreshMethod = method
	s.Server = mediaserver.NewJellyfin(config.MediaServer{URL: srv.URL, APIKey: apiKey, RefreshMethod: method})
	return s
}

func TestStepFallsBackToAFullScanOnANeverScannedLibrary(t *testing.T) {
	f := &neverScannedJellyfin{}
	done, detail, err := jellyfinStep(t, f, true, "libraryRefresh").Run(t.Context(), job, target, nil)
	if err != nil || !done {
		t.Fatalf("Run = %v, %q, %v", done, detail, err)
	}
	if id := mediaserver.ItemID(detail); id != "item-scan" {
		t.Errorf("item id = %q, detail %q", id, detail)
	}
	if !strings.Contains(detail, "full library scan") {
		t.Errorf("the detail should name the fallback: %q", detail)
	}
	if f.scans != 1 {
		t.Errorf("scans = %d, want exactly 1", f.scans)
	}
	if !slices.Contains(f.calls, "POST /Items/lib-movies/Refresh") {
		t.Errorf("the library item was not refreshed first: %v", f.calls)
	}
}

func TestStepNoFallbackWhenItIsOffOrTheMethodDoesNotFit(t *testing.T) {
	for name, c := range map[string]struct {
		fallback bool
		method   string
	}{"setting off": {false, "libraryRefresh"}, "none": {true, "none"}} {
		f := &neverScannedJellyfin{}
		done, _, err := jellyfinStep(t, f, c.fallback, c.method).Run(t.Context(), job, target, nil)
		if done || !errors.Is(err, mediaserver.ErrNotVisible) {
			t.Errorf("%s: Run = %v, %v; want ErrNotVisible", name, done, err)
		}
		if f.scans != 0 {
			t.Errorf("%s: scans = %d, want 0", name, f.scans)
		}
	}
}

func TestStepFallbackThatStillShowsNothingFails(t *testing.T) {
	fake := &mediaservertest.Fake{}
	_, _, err := movieStep(fake).Run(t.Context(), job, target, nil)
	if !errors.Is(err, mediaserver.ErrNotVisible) || !strings.Contains(err.Error(), "scanned once") {
		t.Fatalf("Run = %v", err)
	}
	if fake.Scans != 1 {
		t.Errorf("scans = %d, want 1", fake.Scans)
	}
}

func TestStepFallbackWithTheFake(t *testing.T) {
	fake := &mediaservertest.Fake{Pending: map[string]string{serverFile: "item-3"}, ScanOnly: true}
	done, detail, err := movieStep(fake).Run(t.Context(), job, target, nil)
	if err != nil || !done || mediaserver.ItemID(detail) != "item-3" || fake.Scans != 1 {
		t.Fatalf("Run = %v, %q, %v (scans %d)", done, detail, err, fake.Scans)
	}
}
