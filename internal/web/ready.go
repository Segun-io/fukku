package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/fscheck"
	"github.com/segun-io/fukku/internal/worker"
)

// Readiness statuses. Local checks decide between ready and not_ready; an unreachable
// Radarr or Sonarr only makes the status degraded and never fails the request.
const (
	statusReady    = "ready"
	statusDegraded = "degraded"
	statusNotReady = "not_ready"
)

// Check is one entry of the /readyz answer. Detail is a short sentence: no secret, no
// response body of another service.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Readiness is the /readyz answer.
type Readiness struct {
	Status string  `json:"status"`
	Checks []Check `json:"checks"`
}

type readiness struct {
	now  func() time.Time
	mu   sync.Mutex
	arrs map[string]*arrCache
}

// arrCache holds the last answer of one app. Its mutex is held while the app is asked, so
// concurrent calls share one request.
type arrCache struct {
	mu    sync.Mutex
	at    time.Time
	check Check
	set   bool
}

func (r *readiness) arr(name string) *arrCache {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.arrs == nil {
		r.arrs = map[string]*arrCache{}
	}
	if r.arrs[name] == nil {
		r.arrs[name] = &arrCache{}
	}
	return r.arrs[name]
}

// readyz reports readiness: 200 when the local checks pass (status ready, or degraded when
// Radarr or Sonarr does not answer), 503 when one fails. ?local=1 leaves out the app checks;
// the container health check uses it so a slow app cannot make it time out.
// Who may call it is #5's rule; until that lands the route is open.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	res := s.checkReadiness(r.Context(), r.URL.Query().Get("local") == "")
	status := http.StatusOK
	if res.Status == statusNotReady {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, res)
}

func (s *Server) checkReadiness(ctx context.Context, withApps bool) Readiness {
	var checks []Check
	add := func(name string, err error) bool {
		c := Check{Name: name, OK: err == nil}
		if err != nil {
			c.Detail = err.Error()
		}
		checks = append(checks, c)
		return err == nil
	}

	if s.deps.Store != nil {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.deps.Store.Ping(pctx)
		cancel()
		if err != nil {
			err = errors.New("the database does not answer")
		}
		add("database", err)
	}
	add("spool", fscheck.Writable(s.cfg.Worker.SpoolDir))
	// Never create anything under a missing mount: the intake root is only tried when the
	// sentinels are there.
	if add("sentinels", fscheck.Sentinels(s.cfg.Paths)) {
		add("intakeRoot", fscheck.Writable(s.cfg.Paths.IntakeRoot))
	}
	add("sameFilesystem", fscheck.SameFilesystem(s.cfg))
	add("workerHeartbeat", s.heartbeat())

	status := statusReady
	for _, c := range checks {
		if !c.OK {
			status = statusNotReady
		}
	}
	if withApps {
		apps := s.checkApps(ctx)
		for _, c := range apps {
			if !c.OK && status == statusReady {
				status = statusDegraded
			}
		}
		checks = append(checks, apps...)
	}
	return Readiness{Status: status, Checks: checks}
}

func (s *Server) heartbeat() error {
	age, err := worker.HeartbeatAge(s.cfg.Worker.SpoolDir, s.ready.now())
	if err != nil {
		return errors.New("there is no worker heartbeat")
	}
	if limit := s.cfg.Worker.HeartbeatMaxAge.D(); age > limit {
		return fmt.Errorf("the worker heartbeat is older than %s", s.cfg.Worker.HeartbeatMaxAge)
	}
	return nil
}

// checkApps asks the configured apps in parallel, each answer cached for
// server.readinessCacheTtl.
func (s *Server) checkApps(ctx context.Context) []Check {
	apps := []struct {
		name string
		cfg  config.Arr
	}{{"radarr", s.cfg.Radarr}, {"sonarr", s.cfg.Sonarr}}
	out := make([]Check, len(apps))
	enabled := make([]bool, len(apps))
	var wg sync.WaitGroup
	for i, a := range apps {
		if a.cfg.URL == "" {
			continue
		}
		enabled[i] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = s.cachedAppCheck(ctx, a.name, a.cfg)
		}()
	}
	wg.Wait()
	var checks []Check
	for i := range apps {
		if enabled[i] {
			checks = append(checks, out[i])
		}
	}
	return checks
}

func (s *Server) cachedAppCheck(ctx context.Context, name string, a config.Arr) Check {
	c := s.ready.arr(name)
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := s.cfg.Server.ReadinessCacheTTL.D()
	if c.set && ttl > 0 && s.ready.now().Sub(c.at) < ttl {
		return c.check
	}
	c.check = checkApp(ctx, name, a)
	c.at = s.ready.now()
	c.set = true
	return c.check
}

// checkApp calls GET <url>/api/v3/system/status with the API key. The answer's body is
// never read into the result.
func checkApp(ctx context.Context, name string, a config.Arr) Check {
	timeout := a.RequestTimeout.D()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fail := func(detail string) Check { return Check{Name: name, Detail: detail} }

	target, err := url.JoinPath(a.URL, "api/v3/system/status")
	if err != nil {
		return fail(name + " has an invalid URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fail(name + " has an invalid URL")
	}
	req.Header.Set("X-Api-Key", a.APIKey.Reveal())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fail(fmt.Sprintf("%s did not answer within %s", name, a.RequestTimeout))
		}
		return fail(name + " is unreachable")
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Sprintf("%s answered HTTP %d", name, resp.StatusCode))
	}
	return Check{Name: name, OK: true}
}
