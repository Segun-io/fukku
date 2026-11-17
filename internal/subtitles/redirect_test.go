package subtitles_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/subtitles"
)

// The API key goes to the configured address only: a redirect to another port is not
// followed, and the target never sees the key.
func TestRedirectIsNotFollowed(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"data":{"bazarr_version":"1.6.2"}}`))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer origin.Close()

	b := subtitles.NewBazarr(origin.URL, testKey, 5*time.Second)
	_, err := b.Version(t.Context())
	if err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Fatalf("error %v, want one saying the URL redirects", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("the redirect target was reached %d times", hits.Load())
	}
}
