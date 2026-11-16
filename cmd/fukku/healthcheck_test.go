package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store/storetest"
	"github.com/segun-io/fukku/internal/web"
)

func TestHealthcheckWebFollowsReadyz(t *testing.T) {
	spool := t.TempDir()
	cfg := config.Defaults()
	cfg.Worker.SpoolDir = spool
	cfg.Paths.IntakeRoot = t.TempDir()
	// An app that is down must not fail the health check.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()
	cfg.Radarr.URL = down.URL

	ts := httptest.NewServer(web.New(cfg, slog.New(slog.DiscardHandler), fstest.MapFS{}, web.Deps{Store: storetest.NewFake(nil)}).Handler())
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.ListenAddress = u.Host

	var stderr bytes.Buffer
	if got := healthcheckWeb(cfg, &stderr); got != 1 || !strings.Contains(stderr.String(), "503") {
		t.Fatalf("no heartbeat: exit %d, stderr %q", got, stderr.String())
	}

	beat := strconv.FormatInt(time.Now().Unix(), 10) + "\n"
	if err := os.WriteFile(filepath.Join(spool, "worker.heartbeat"), []byte(beat), 0o600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if got := healthcheckWeb(cfg, &stderr); got != 0 {
		t.Fatalf("local checks pass, *arr down: exit %d, stderr %q", got, stderr.String())
	}
}
