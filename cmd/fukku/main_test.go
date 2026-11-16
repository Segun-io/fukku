package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/worker"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fukku combined runs the web process and the same worker code in one process: a task
// submitted to the spool is run and its result comes back, while the web process serves.
func TestCombined(t *testing.T) {
	cfg := config.Defaults()
	cfg.Mode = "combined"
	cfg.Server.ListenAddress = "127.0.0.1:0"
	cfg.Worker.SpoolDir = filepath.Join(t.TempDir(), "spool")
	cfg.Paths.IntakeRoot = t.TempDir()
	cfg.Worker.PollInterval = config.Duration(5 * time.Millisecond)
	const job = "0123456789abcdef0123456789abcdef"
	if err := os.MkdirAll(jobdir.Dir(cfg.Paths.IntakeRoot, job), 0o750); err != nil {
		t.Fatal(err)
	}

	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runCombined(ctx, cfg, slog.New(slog.NewTextHandler(logs, nil)), map[string]worker.Handler{
			spool.KindProbe: func(_ context.Context, root *os.Root, _ json.RawMessage) (any, error) {
				return root.Name(), nil
			},
		})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()

	q, err := spool.Open(cfg.Worker.SpoolDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.Close() }()
	if err := q.Submit(ctx, spool.Task{ID: "t-1", Kind: spool.KindProbe, Job: job}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var r spool.Result
	for {
		var ok bool
		if r, ok, err = q.Result(ctx, "t-1"); err != nil {
			t.Fatal(err)
		} else if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no result; log:\n%s", logs)
		}
		time.Sleep(5 * time.Millisecond)
	}
	var root string
	if err := json.Unmarshal(r.Output, &root); err != nil || r.Status != spool.StatusOK || root != jobdir.Dir(cfg.Paths.IntakeRoot, job) {
		t.Fatalf("result %+v (%v)", r, err)
	}

	addr := regexp.MustCompile(`msg=web_listening address=(\S+)`)
	for addr.FindStringSubmatch(logs.String()) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the web process is not listening; log:\n%s", logs)
		}
		time.Sleep(5 * time.Millisecond)
	}
	url := "http://" + addr.FindStringSubmatch(logs.String())[1] + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz %d", resp.StatusCode)
	}
}
