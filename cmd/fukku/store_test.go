package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
)

// webConfig is a web configuration whose data directory does not exist yet.
func webConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.ListenAddress = "127.0.0.1:0"
	cfg.Server.DataDir = filepath.Join(t.TempDir(), "data")
	cfg.Worker.SpoolDir = filepath.Join(t.TempDir(), "spool")
	return cfg
}

// fukku web with a fresh server.dataDir creates the database.
func TestWebCreatesDatabase(t *testing.T) {
	cfg := webConfig(t)
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runWeb(ctx, cfg, slog.New(slog.NewTextHandler(logs, nil))) }()

	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(logs.String(), "web_listening") {
		select {
		case err := <-done:
			t.Fatalf("fukku web ended: %v\n%s", err, logs)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("fukku web did not start:\n%s", logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, err := os.Stat(filepath.Join(cfg.Server.DataDir, store.FileName))
	if err != nil {
		t.Fatalf("no database in a fresh data directory: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("database mode %v", st.Mode().Perm())
	}
	// A second web process on the same data directory is refused while the first runs.
	err = runWeb(ctx, cfg, slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), "server.dataDir") {
		t.Fatalf("second web process: %v, want a data-directory lock error", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("fukku web: %v", err)
	}
}

func TestWebRefusesBadDataDir(t *testing.T) {
	cfg := webConfig(t)
	// A file where the data directory should be.
	if err := os.WriteFile(cfg.Server.DataDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := runWeb(t.Context(), cfg, slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), "server.dataDir") {
		t.Fatalf("got %v, want an error naming server.dataDir", err)
	}
}

// fukku check-config validates without opening the database.
func TestCheckConfigLeavesDataDirAlone(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	file := filepath.Join(root, "fukku.yaml")
	cfg := "server:\n  dataDir: " + dataDir + "\n" +
		"auth:\n  mode: none\n" +
		"radarr:\n  url: http://radarr.example.com:7878\n"
	if err := os.WriteFile(file, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUKKU_RADARR_API_KEY", "not-a-real-key")
	var stdout, stderr syncBuffer
	if code := run([]string{"check-config", "--config", file, "--insecure-dev"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "# valid for role web") {
		t.Fatalf("output:\n%s", stdout.String())
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("check-config touched the data directory: %v", err)
	}
}

// fukku check-config warns when only admins may change the library and nobody can be one
// (#116), and still succeeds.
func TestCheckConfigWarnsWithoutAnAdmin(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "fukku.yaml")
	cfg := "server:\n  dataDir: " + filepath.Join(root, "data") + "\n" +
		"auth:\n  trustedProxies: [127.0.0.1]\n" +
		"radarr:\n  url: http://radarr.example.com:7878\n"
	if err := os.WriteFile(file, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUKKU_RADARR_API_KEY", "not-a-real-key")
	var stdout, stderr syncBuffer
	if code := run([]string{"check-config", "--config", file}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning: auth.libraryChangesRequireAdmin: ") ||
		!strings.Contains(stdout.String(), "# warning: auth.libraryChangesRequireAdmin: ") {
		t.Fatalf("stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}
