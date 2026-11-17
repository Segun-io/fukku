package arr_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
)

// The API key goes to the configured address only: a redirect to another port (another
// origin, as a host or scheme would be) is not followed, and the target never sees the key.
func TestRedirectIsNotFollowed(t *testing.T) {
	var hits atomic.Int32
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("X-Api-Key") != "" {
			leaked.Store(true)
		}
		_, _ = w.Write([]byte(`{"appName":"Radarr","version":"1.0"}`))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer origin.Close()

	cfg := config.Arr{URL: origin.URL, APIKey: testKey}
	for name, m := range map[string]interface {
		Status(ctx context.Context) (arr.Status, error)
	}{"radarr": arr.NewRadarr(cfg), "sonarr": arr.NewSonarr(cfg)} {
		_, err := m.Status(ctx)
		if err == nil || !strings.Contains(err.Error(), "redirects") {
			t.Errorf("%s: error %v, want one saying the URL redirects", name, err)
			continue
		}
		if strings.Contains(err.Error(), target.URL) || strings.Contains(err.Error(), string(testKey)) {
			t.Errorf("%s: the error names the target or the key: %v", name, err)
		}
	}
	if hits.Load() != 0 || leaked.Load() {
		t.Fatalf("the redirect target was reached %d times, key leaked: %v", hits.Load(), leaked.Load())
	}
}
