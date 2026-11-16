package arr_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
)

// GET /parse?title=, answered as Radarr 6.4.4 does for a name with and without a quality.
func TestRadarrParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/parse" {
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
				"revision":{"version":2,"real":0,"isRepack":false}}}}`))
		default:
			_, _ = w.Write([]byte(`{"title":"x"}`))
		}
	}))
	defer srv.Close()
	m := arr.NewRadarr(config.Arr{URL: srv.URL, APIKey: testKey})
	p, ok := m.(arr.NameParser)
	if !ok {
		t.Fatal("the Radarr client is not a NameParser")
	}
	got, err := p.Parse(t.Context(), "Film (2001) - Bluray")
	if err != nil || got.Quality.ID != 6 || got.Quality.Name != "Bluray-720p" || got.Source != "bluray" || got.Resolution != 720 || got.Revision != 1 {
		t.Errorf("Bluray: %+v, %v", got, err)
	}
	if got, err = p.Parse(t.Context(), "Film (2001) - Proper"); err != nil || got.Quality.ID != 0 || got.Revision != 2 {
		t.Errorf("Proper: %+v, %v", got, err)
	}
	if got, err = p.Parse(t.Context(), "Film (2001) - Español"); err != nil || got != (arr.Parsed{}) {
		t.Errorf("nothing parsed: %+v, %v", got, err)
	}
}
