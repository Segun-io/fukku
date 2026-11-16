package worker

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
)

func TestHeartbeat(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults().Worker
	cfg.SpoolDir = dir
	cfg.HeartbeatInterval = config.Duration(10 * time.Millisecond)

	w := New(cfg, slog.New(slog.DiscardHandler))
	fixed := time.Unix(1_700_000_000, 0)
	w.now = func() time.Time { return fixed }

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	age, err := HeartbeatAge(dir, fixed.Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if age != 90*time.Second {
		t.Fatalf("age %v", age)
	}
}

func TestHeartbeatMissing(t *testing.T) {
	if _, err := HeartbeatAge(t.TempDir(), time.Now()); err == nil {
		t.Fatal("want an error for a missing heartbeat")
	}
}
