package safehttp_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/segun-io/fukku/internal/safehttp"
)

func get(t *testing.T, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Api-Key", "secret")
	c := safehttp.NoCrossOriginRedirect(&http.Client{})
	resp, err := c.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return err
}

// A redirect inside the same origin is followed, with the key.
func TestSameOriginRedirectIsFollowed(t *testing.T) {
	var key atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			key.Store(r.Header.Get("X-Api-Key"))
			return
		}
		http.Redirect(w, r, "/moved", http.StatusMovedPermanently)
	}))
	defer srv.Close()
	if err := get(t, srv.URL+"/start"); err != nil {
		t.Fatal(err)
	}
	if key.Load() != "secret" {
		t.Errorf("the key after a same-origin redirect: %v", key.Load())
	}
}

// Another port, or another host, is another origin: nothing is sent to it.
func TestOtherOriginIsRefused(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer other.Close()
	for name, target := range map[string]string{"other port": other.URL, "other host": "http://localhost:" + other.URL[len("http://127.0.0.1:"):]} {
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusFound)
		}))
		err := get(t, origin.URL)
		origin.Close()
		if !errors.Is(err, safehttp.ErrRedirect) {
			t.Errorf("%s: error %v", name, err)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("the other origin was reached %d times", hits.Load())
	}
}

// http to https on the same host is refused, with its own message.
func TestHTTPToHTTPSIsRefused(t *testing.T) {
	var hits atomic.Int32
	secure := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer secure.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secure.URL, http.StatusFound)
	}))
	defer origin.Close()
	if err := get(t, origin.URL); !errors.Is(err, safehttp.ErrRedirectHTTPS) {
		t.Errorf("error %v", err)
	}
	if hits.Load() != 0 {
		t.Errorf("the https server was reached")
	}
}

func TestRedirectLoopStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	}))
	defer srv.Close()
	if err := get(t, srv.URL+"/a"); err == nil {
		t.Error("an endless redirect was followed")
	}
}
