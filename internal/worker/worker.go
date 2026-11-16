// Package worker is the worker process. It opens untrusted files (unzip, ffprobe, mkvmerge,
// preview clips) and holds no credentials and no network. It talks to the web process only
// through task and result files in the spool directory. M0 provides the heartbeat; task
// handling arrives in M1.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/segun-io/fukku/internal/config"
)

// HeartbeatFile is the name, inside the spool directory, of the worker's heartbeat.
const HeartbeatFile = "worker.heartbeat"

// Worker runs tasks from the spool directory.
type Worker struct {
	cfg config.Worker
	log *slog.Logger
	now func() time.Time
}

func New(cfg config.Worker, log *slog.Logger) *Worker {
	return &Worker{cfg: cfg, log: log, now: time.Now}
}

// Run writes a heartbeat every interval until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	if err := os.MkdirAll(w.cfg.SpoolDir, 0o750); err != nil {
		return fmt.Errorf("spool directory: %w", err)
	}
	if err := w.beat(); err != nil {
		return err
	}
	w.log.Info("worker_started", "spool", w.cfg.SpoolDir, "concurrency", w.cfg.Concurrency)
	tick := time.NewTicker(w.cfg.HeartbeatInterval.D())
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			w.log.Info("worker_stopped")
			return nil
		case <-tick.C:
			if err := w.beat(); err != nil {
				w.log.Error("heartbeat_failed", "error", err)
			}
		}
	}
}

// beat atomically replaces the heartbeat file with the current Unix time.
func (w *Worker) beat() error {
	tmp, err := os.CreateTemp(w.cfg.SpoolDir, ".heartbeat-*")
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	_, werr := tmp.WriteString(strconv.FormatInt(w.now().Unix(), 10) + "\n")
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("heartbeat: %w", err)
	}
	return os.Rename(tmp.Name(), filepath.Join(w.cfg.SpoolDir, HeartbeatFile))
}

// HeartbeatAge returns how old the worker's last heartbeat in spoolDir is.
func HeartbeatAge(spoolDir string, now time.Time) (time.Duration, error) {
	data, err := os.ReadFile(filepath.Join(spoolDir, HeartbeatFile)) //nolint:gosec // fixed name inside the configured spool
	if err != nil {
		return 0, err
	}
	sec, err := strconv.ParseInt(string(trimNewline(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("heartbeat file is malformed: %w", err)
	}
	return now.Sub(time.Unix(sec, 0)), nil
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
