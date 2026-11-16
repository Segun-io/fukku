package web

import (
	"context"
	"net/http"
	"time"

	"github.com/segun-io/fukku/internal/auth"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/fscheck"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/version"
	"github.com/segun-io/fukku/internal/worker"
)

// The admin API (#69): health and the effective settings, read-only. Both endpoints answer
// 404 not_found to anyone who is not an administrator, exactly as for a path that does not
// exist, so a user learns nothing about them. Secrets never leave the server: settings are
// listed through config.Leaf.Value, which prints a secret as config.Secret does. No endpoint
// changes a setting (plan §11: v1 settings are file-only).
func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/status", s.adminOnly(s.adminStatus))
	mux.HandleFunc("GET /api/admin/settings", s.adminOnly(s.adminSettings))
}

func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if u, ok := auth.FromContext(r.Context()); !ok || !u.Admin {
			apiNotFound(w, r)
			return
		}
		next(w, r)
	}
}

type adminStatusJSON struct {
	Version   string      `json:"version"`
	Readiness Readiness   `json:"readiness"`
	Apps      []adminApp  `json:"apps"`
	Worker    adminWorker `json:"worker"`
	Jobs      *adminJobs  `json:"jobs"`
	Disk      adminDisk   `json:"disk"`
	Checked   time.Time   `json:"checkedAt"`
}

// adminApp is a configured app and the version it reported; Version is empty when it did
// not answer.
type adminApp struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// adminWorker: HeartbeatAgeSeconds is null when there is no readable heartbeat.
type adminWorker struct {
	HeartbeatAgeSeconds *int64 `json:"heartbeatAgeSeconds"`
	MaxAgeSeconds       int64  `json:"maxAgeSeconds"`
}

// adminJobs counts the jobs of everyone, one entry per state (zero included).
type adminJobs struct {
	Total   int            `json:"total"`
	ByState map[string]int `json:"byState"`
}

// adminDisk is the free space of the intake filesystem against the upload margin
// (upload.freeSpaceMargin). FreeBytes is null where it cannot be measured.
type adminDisk struct {
	FreeBytes   *int64 `json:"freeBytes"`
	MarginBytes int64  `json:"marginBytes"`
	AboveMargin bool   `json:"aboveMargin"`
}

var allJobStates = []store.State{
	store.Uploading, store.Uploaded, store.Probing, store.Probed, store.Matched,
	store.AwaitingConfirmation, store.Importing, store.Imported, store.PostProcessing,
	store.Done, store.Failed, store.Stalled, store.Cancelled,
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request) {
	ready, answers := s.checkReadinessApps(r.Context(), true)
	out := adminStatusJSON{
		Version:   version.Version,
		Readiness: ready,
		Apps:      make([]adminApp, 0, len(answers)),
		Checked:   s.ready.now().UTC(),
	}
	for _, a := range answers {
		out.Apps = append(out.Apps, adminApp{Name: a.check.Name, Version: a.version})
	}

	out.Worker.MaxAgeSeconds = int64(s.cfg.Worker.HeartbeatMaxAge.D() / time.Second)
	if age, err := worker.HeartbeatAge(s.cfg.Worker.SpoolDir, s.ready.now()); err == nil {
		sec := max(int64(age/time.Second), 0)
		out.Worker.HeartbeatAgeSeconds = &sec
	}

	if s.deps.Store != nil {
		jobs, err := s.countJobs(r.Context())
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		out.Jobs = jobs
	}

	out.Disk.MarginBytes = int64(s.cfg.Upload.FreeSpaceMargin)
	if free, err := fscheck.FreeBytes(s.cfg.Paths.IntakeRoot); err == nil {
		out.Disk.FreeBytes = &free
		out.Disk.AboveMargin = free >= out.Disk.MarginBytes
	}
	writeJSON(w, http.StatusOK, out)
}

// countJobs counts the jobs of every owner per state, paging through the store.
func (s *Server) countJobs(ctx context.Context) (*adminJobs, error) {
	const page = 500
	out := &adminJobs{ByState: make(map[string]int, len(allJobStates))}
	for _, st := range allJobStates {
		n := 0
		for {
			jobs, err := s.deps.Store.Jobs(ctx, store.JobFilter{States: []store.State{st}, Limit: page, Offset: n})
			if err != nil {
				return nil, err
			}
			n += len(jobs)
			if len(jobs) < page {
				break
			}
		}
		out.ByState[string(st)] = n
		out.Total += n
	}
	return out, nil
}

type adminSettingJSON struct {
	Key    string `json:"key"`
	Env    string `json:"env"`
	Secret bool   `json:"secret"`
	// Value is the effective value; for a secret it is empty (unset) or "[redacted]".
	Value string `json:"value"`
	// Source is "set" when the value differs from the default, otherwise "default".
	Source string `json:"source"`
}

type adminSettingsJSON struct {
	Settings []adminSettingJSON `json:"settings"`
}

// adminSettings lists every setting as `fukku check-config` prints it, in declaration order.
func (s *Server) adminSettings(w http.ResponseWriter, _ *http.Request) {
	cfg := s.cfg
	defaults := config.Defaults()
	leaves := config.Leaves(&cfg)
	base := config.Leaves(&defaults)
	out := adminSettingsJSON{Settings: make([]adminSettingJSON, 0, len(leaves))}
	for i, l := range leaves {
		source := "default"
		if l.Value() != base[i].Value() {
			source = "set"
		}
		out.Settings = append(out.Settings, adminSettingJSON{
			Key: l.Key, Env: l.Env, Secret: l.Secret, Value: l.Value(), Source: source,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
