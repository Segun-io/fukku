package mediaserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/mediaserver"
)

// The API key goes to the configured address only: a redirect to another port is not
// followed, and the target never sees the key.
func TestRedirectIsNotFollowed(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"Version":"12.4.0"}`))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer origin.Close()

	j := mediaserver.NewJellyfin(config.MediaServer{URL: origin.URL, APIKey: apiKey})
	_, err := j.Version(t.Context())
	if err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Fatalf("error %v, want one saying the URL redirects", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("the redirect target was reached %d times", hits.Load())
	}
}
