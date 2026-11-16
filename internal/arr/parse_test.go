package arr_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
)

// GET /parse?title= and GET /movie?tmdbId=, answered as Radarr 6.4.4 does.
func TestRadarrParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/parse":
		case "/api/v3/movie":
			if r.URL.Query().Get("tmdbId") == "605" {
				_, _ = w.Write([]byte(`[{"id":31,"tmdbId":605,"title":"Film","year":2001,"path":"/movies/Film (2001)"}]`))
			} else {
				_, _ = w.Write([]byte(`[]`))
			}
			return
		default:
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("title") {
		case "Film (2001) - Bluray":
			_, _ = w.Write([]byte(`{"title":"Film (2001) - Bluray","parsedMovieInfo":{"movieTitles":["Film"],"year":2001,
				"quality":{"quality":{"id":6,"name":"Bluray-720p","source":"bluray","resolution":720,"modifier":"none"},
				"revision":{"version":1,"real":0,"isRepack":false}}}}`))
		case "Film (2001) - Proper":
			_, _ = w.Write([]byte(`{"parsedMovieInfo":{"quality":{"quality":{"id":0,"name":"Unknown","source":"unknown","resolution":0},
				"revision":{"version":2,"real":1,"isRepack":false}}}}`))
		case "Film (2001) - Español":
			_, _ = w.Write([]byte(`{"parsedMovieInfo":{"quality":{"quality":{"id":0,"name":"Unknown","source":"unknown","resolution":0},
				"revision":{"version":1,"real":0,"isRepack":false}}}}`))
		default: // a name Radarr cannot parse
			_, _ = w.Write([]byte(`{"title":"Zzz","customFormatScore":0}`))
		}
	}))
	defer srv.Close()
	m := arr.NewRadarr(config.Arr{URL: srv.URL, APIKey: testKey})
	p, ok := m.(arr.NameParser)
	if !ok {
		t.Fatal("the Radarr client is not a NameParser")
	}
	got, err := p.Parse(t.Context(), "Film (2001) - Bluray")
	if err != nil || !got.OK || got.Quality.ID != 6 || got.Quality.Name != "Bluray-720p" || got.Source != "bluray" || got.Resolution != 720 || got.Revision != 1 {
		t.Errorf("Bluray: %+v, %v", got, err)
	}
	if got, err = p.Parse(t.Context(), "Film (2001) - Proper"); err != nil || got.Quality.ID != 0 || got.Revision != 2 || got.Real != 1 {
		t.Errorf("Proper: %+v, %v", got, err)
	}
	if got, err = p.Parse(t.Context(), "Film (2001) - Español"); err != nil || !got.OK || got.Quality.ID != 0 || got.Revision != 1 {
		t.Errorf("Unknown: %+v, %v", got, err)
	}
	if got, err = p.Parse(t.Context(), "Zzz"); err != nil || got.OK {
		t.Errorf("nothing parsed: %+v, %v", got, err)
	}

	f, ok := m.(arr.MovieFinder)
	if !ok {
		t.Fatal("the Radarr client is not a MovieFinder")
	}
	if mv, found, err := f.MovieByTMDB(t.Context(), 605); err != nil || !found || mv.ID != 31 || mv.Path != "/movies/Film (2001)" {
		t.Errorf("MovieByTMDB(605) = %+v, %v, %v", mv, found, err)
	}
	if _, found, err := f.MovieByTMDB(t.Context(), 7); err != nil || found {
		t.Errorf("MovieByTMDB(7) = %v, %v", found, err)
	}
}
