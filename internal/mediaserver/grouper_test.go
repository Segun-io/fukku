package mediaserver_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/mediaserver"
)

// A path in no library does not hold up the others: their libraries are refreshed, and
// the error names only the unlisted path.
func TestRefreshLibraryRefreshSkipsUnlistedPaths(t *testing.T) {
	f := &fakeJellyfin{}
	j := newJellyfin(t, mediaserver.MethodLibraryRefresh, f)
	err := j.Refresh(t.Context(), []string{"/elsewhere/V/V - Español.mkv", "/media/movies/A (2020)/A.mkv"})
	var unlisted *mediaserver.UnlistedError
	if !errors.As(err, &unlisted) || !slices.Equal(unlisted.Paths, []string{"/elsewhere/V/V - Español.mkv"}) ||
		!strings.Contains(err.Error(), "no library contains /elsewhere/V/V - Español.mkv") {
		t.Fatalf("err = %v", err)
	}
	var posts []string
	for _, c := range f.calls {
		if c.Method == http.MethodPost {
			posts = append(posts, c.Path)
		}
	}
	if !slices.Equal(posts, []string{"/Items/lib-movies/Refresh"}) {
		t.Errorf("refreshes %v, want the movies library", posts)
	}
}

// The Grouper side of the Jellyfin client: POST /Videos/MergeVersions with the ids, and the
// media sources of an item from GET /Items?Ids= (as Jellyfin 12.4 answers, probe B3e).
func TestJellyfinGrouper(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch r.Method + " " + r.URL.Path {
		case "POST /Videos/MergeVersions":
			w.WriteHeader(http.StatusNoContent)
		case "GET /Items":
			if r.URL.Query().Get("Ids") != "film-1" {
				_, _ = w.Write([]byte(`{"Items":[],"TotalRecordCount":0}`))
				return
			}
			_, _ = w.Write([]byte(`{"Items":[{"Id":"film-1","Path":"/media/movies/F (2020)/F (2020).mkv","MediaSources":[
				{"Id":"a","Path":"/media/movies/F (2020)/F (2020).mkv"},{"Id":"b","Path":"/media/versions/F (2020)/F (2020) - Español.mkv"}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	j := mediaserver.NewJellyfin(config.MediaServer{URL: srv.URL, APIKey: apiKey})
	if err := j.MergeVersions(t.Context(), []string{"film-1", "ver-1"}); err != nil {
		t.Fatal(err)
	}
	if err := j.MergeVersions(t.Context(), []string{"film-1"}); err == nil {
		t.Error("merging one item")
	}
	src, err := j.Sources(t.Context(), "film-1")
	if err != nil || !slices.Equal(src, []string{"/media/movies/F (2020)/F (2020).mkv", "/media/versions/F (2020)/F (2020) - Español.mkv"}) {
		t.Errorf("Sources = %v, %v", src, err)
	}
	if src, err := j.Sources(t.Context(), "gone"); err != nil || len(src) != 0 {
		t.Errorf("Sources of an unknown item = %v, %v", src, err)
	}
	if got[0] != "POST /Videos/MergeVersions?ids=film-1%2Cver-1" {
		t.Errorf("calls %v", got)
	}
}
