package mediaserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/mediaserver"
)

const apiKey = "k3y-secret-value"

type call struct {
	Method, Path, Query, Body, Auth string
}

// fakeJellyfin answers like Jellyfin 12.4 for the calls the client makes.
type fakeJellyfin struct {
	mu    sync.Mutex
	calls []call
	items []map[string]string // Id, Path of what the server lists
	fail  map[string]int      // "METHOD /path" -> status
}

func (f *fakeJellyfin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, call{r.Method, r.URL.Path, r.URL.RawQuery, string(body), r.Header.Get("Authorization")})
	f.mu.Unlock()
	if st := f.fail[r.Method+" "+r.URL.Path]; st != 0 {
		// A server that echoes the request header back in its error text.
		http.Error(w, "boom: "+r.Header.Get("Authorization"), st)
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /System/Info/Public":
		_, _ = w.Write([]byte(`{"Version":"12.4.0","ProductName":"Jellyfin Server"}`))
	case "GET /Library/VirtualFolders":
		_, _ = w.Write([]byte(`[{"Name":"Movies","Locations":["/media/movies"],"ItemId":"lib-movies"},` +
			`{"Name":"Shows","Locations":["/media/tv/"],"ItemId":"lib-shows"}]`))
	case "GET /Items":
		want := r.URL.Query().Get("Path")
		var found []map[string]string
		for _, it := range f.items {
			if it["Path"] == want {
				found = append(found, it)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": found, "TotalRecordCount": len(found)})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (f *fakeJellyfin) last() call { return f.calls[len(f.calls)-1] }

func newJellyfin(t *testing.T, method string, f *fakeJellyfin) *mediaserver.Jellyfin {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return mediaserver.NewJellyfin(config.MediaServer{URL: srv.URL + "/", APIKey: apiKey, RefreshMethod: method})
}

func TestVersion(t *testing.T) {
	f := &fakeJellyfin{}
	j := newJellyfin(t, "pathUpdate", f)
	v, err := j.Version(t.Context())
	if err != nil || v != "12.4.0" {
		t.Fatalf("Version = %q, %v", v, err)
	}
	if want := `MediaBrowser Token="` + apiKey + `"`; f.last().Auth != want {
		t.Errorf("Authorization = %q", f.last().Auth)
	}
}

func TestRefreshPathUpdateSendsTheGivenPaths(t *testing.T) {
	f := &fakeJellyfin{}
	j := newJellyfin(t, mediaserver.MethodPathUpdate, f)
	if err := j.Refresh(t.Context(), []string{"/media/movies/A (2020)/A.mkv", "/media/movies/B (2021)/B.mkv"}); err != nil {
		t.Fatal(err)
	}
	c := f.last()
	if c.Method != http.MethodPost || c.Path != "/Library/Media/Updated" {
		t.Fatalf("call = %+v", c)
	}
	var body struct{ Updates []map[string]string }
	if err := json.Unmarshal([]byte(c.Body), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Updates) != 2 || body.Updates[0]["Path"] != "/media/movies/A (2020)/A.mkv" ||
		body.Updates[1]["Path"] != "/media/movies/B (2021)/B.mkv" || body.Updates[1]["UpdateType"] != "Created" {
		t.Errorf("body = %s", c.Body)
	}
}

func TestRefreshLibraryScan(t *testing.T) {
	f := &fakeJellyfin{}
	j := newJellyfin(t, mediaserver.MethodLibraryScan, f)
	if err := j.Refresh(t.Context(), []string{"/media/movies/A/A.mkv"}); err != nil {
		t.Fatal(err)
	}
	if c := f.last(); c.Method != http.MethodPost || c.Path != "/Library/Refresh" || len(f.calls) != 1 {
		t.Errorf("calls = %+v", f.calls)
	}
}

func TestRefreshLibraryRefreshRefreshesEachLibraryItemOnce(t *testing.T) {
	f := &fakeJellyfin{}
	j := newJellyfin(t, mediaserver.MethodLibraryRefresh, f)
	err := j.Refresh(t.Context(), []string{"/media/movies/A (2020)/A.mkv", "/media/movies/B (2021)/B.mkv", "/media/tv/Show/Season 1/e1.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	var refreshes []string
	for _, c := range f.calls {
		if c.Method == http.MethodPost {
			refreshes = append(refreshes, c.Path+"?"+c.Query)
		}
	}
	if len(refreshes) != 2 {
		t.Fatalf("refreshes = %v", refreshes)
	}
	for _, r := range refreshes {
		if !strings.Contains(r, "metadataRefreshMode=Default") || !strings.Contains(r, "imageRefreshMode=Default") {
			t.Errorf("a refresh must not download metadata again: %s", r)
		}
	}
	if !strings.HasPrefix(refreshes[0], "/Items/lib-movies/Refresh?") || !strings.HasPrefix(refreshes[1], "/Items/lib-shows/Refresh?") {
		t.Errorf("refreshes = %v", refreshes)
	}
}

func TestRefreshLibraryRefreshPathOutsideEveryLibrary(t *testing.T) {
	f := &fakeJellyfin{}
	j := newJellyfin(t, mediaserver.MethodLibraryRefresh, f)
	err := j.Refresh(t.Context(), []string{"/data/media/movies/A/A.mkv"})
	if err == nil || !strings.Contains(err.Error(), "no library contains /data/media/movies/A/A.mkv") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range f.calls {
		if c.Method == http.MethodPost {
			t.Errorf("nothing may be refreshed: %+v", c)
		}
	}
}

func TestRefreshNoneDoesNothing(t *testing.T) {
	f := &fakeJellyfin{}
	j := newJellyfin(t, mediaserver.MethodNone, f)
	if err := j.Refresh(t.Context(), []string{"/media/movies/A/A.mkv"}); err != nil || len(f.calls) != 0 {
		t.Errorf("err = %v, calls = %+v", err, f.calls)
	}
}

func TestFind(t *testing.T) {
	f := &fakeJellyfin{items: []map[string]string{{"Id": "abc123", "Path": "/media/movies/A (2020)/A.mkv"}}}
	j := newJellyfin(t, mediaserver.MethodLibraryRefresh, f)
	id, err := j.Find(t.Context(), "/media/movies/A (2020)/A.mkv")
	if err != nil || id != "abc123" {
		t.Fatalf("Find = %q, %v", id, err)
	}
	id, err = j.Find(t.Context(), "/media/movies/Other/O.mkv")
	if err != nil || id != "" {
		t.Fatalf("unknown path: Find = %q, %v", id, err)
	}
}

func TestErrorsNeverCarryTheKey(t *testing.T) {
	f := &fakeJellyfin{fail: map[string]int{"POST /Library/Refresh": 401, "GET /System/Info/Public": 500}}
	j := newJellyfin(t, mediaserver.MethodLibraryScan, f)
	err := j.Refresh(t.Context(), nil)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Errorf("the error shows the key: %v", err)
	}
	if _, err = j.Version(t.Context()); err == nil || strings.Contains(err.Error(), apiKey) {
		t.Errorf("err = %v", err)
	}

	// An unreachable server: the transport error must not show the key either.
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	dead := mediaserver.NewJellyfin(config.MediaServer{URL: url, APIKey: apiKey})
	if _, err := dead.Version(t.Context()); err == nil || strings.Contains(err.Error(), apiKey) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestTitleLink(t *testing.T) {
	if got := mediaserver.TitleLink("https://media.example.com/web/#/details?id={itemId}", "abc"); got != "https://media.example.com/web/#/details?id=abc" {
		t.Errorf("link = %q", got)
	}
	if mediaserver.TitleLink("", "abc") != "" || mediaserver.TitleLink("https://x/{itemId}", "") != "" {
		t.Error("no template or no item: no link")
	}
}
