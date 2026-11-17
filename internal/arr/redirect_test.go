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

// A redirect that stays on the configured origin keeps working, and carries the key there.
func TestSameOriginRedirectWorks(t *testing.T) {
	var key atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/system/status" {
			http.Redirect(w, r, "/moved/status", http.StatusTemporaryRedirect)
			return
		}
		key.Store(r.Header.Get("X-Api-Key"))
		_, _ = w.Write([]byte(`{"appName":"Radarr","version":"1.0"}`))
	}))
	defer srv.Close()
	st, err := arr.NewRadarr(config.Arr{URL: srv.URL, APIKey: testKey}).Status(ctx)
	if err != nil || st.Version != "1.0" || key.Load() != string(testKey) {
		t.Fatalf("%+v, %v, key %v", st, err, key.Load())
	}
}
