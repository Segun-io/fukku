package web

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/segun-io/fukku/internal/metrics"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// parseMetrics reads the text format strictly: every line is a comment or
// name{labels} value, and every sample belongs to a family with a TYPE line.
func parseMetrics(t *testing.T, body string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	typed := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSuffix(body, "\n"), "\n") {
		if rest, ok := strings.CutPrefix(line, "# TYPE "); ok {
			typed[strings.Fields(rest)[0]] = true
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndex(line, " ")
		if i < 0 {
			t.Fatalf("bad line %q", line)
		}
		v, err := strconv.ParseFloat(line[i+1:], 64)
		if err != nil {
			t.Fatalf("bad value in %q", line)
		}
		series := line[:i]
		name, _, _ := strings.Cut(series, "{")
		if strings.Contains(series, "{") && !strings.HasSuffix(series, "}") {
			t.Fatalf("bad labels in %q", line)
		}
		family := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, "_bucket"), "_sum"), "_count")
		if !typed[name] && !typed[family] {
			t.Fatalf("%q has no TYPE line", line)
		}
		out[series] = v
	}
	return out
}

func metricsServer(t *testing.T, on bool) (*Server, *storetest.Fake, *metrics.Set) {
	t.Helper()
	cfg := newReadyEnv(t).cfg
	cfg.Server.Metrics = on
	cfg.Auth.TrustedProxies = []string{"192.0.2.10"}
	cfg.Auth.AdminGroup = "admins"
	st := storetest.NewFake(nil)
	m := metrics.New()
	ui := fstest.MapFS{"index.html": {Data: []byte("app")}}
	return New(cfg, slog.New(slog.DiscardHandler), ui, Deps{Store: st, Metrics: m}), st, m
}

// scrape asks for /metrics from addr; groups "-" sends no user.
func scrape(t *testing.T, s *Server, addr, groups string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	req.RemoteAddr = addr
	if groups != "-" {
		req.Header.Set("Remote-User", "user1")
		req.Header.Set("Remote-Groups", groups)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestMetricsOffIs404(t *testing.T) {
	s, _, _ := metricsServer(t, false)
	rec := scrape(t, s, "127.0.0.1:40000", "-")
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("status %d body %q, want 404 and not the UI", rec.Code, rec.Body)
	}
}

func TestMetricsAccess(t *testing.T) {
	s, _, _ := metricsServer(t, true)
	for name, c := range map[string]struct {
		addr, groups string
		want         int
	}{
		"loopback, no user":  {"127.0.0.1:40000", "-", http.StatusOK},
		"admin via proxy":    {proxyAddr, "admins", http.StatusOK},
		"user via proxy":     {proxyAddr, "users", http.StatusForbidden},
		"untrusted address":  {untrustedAddr, "admins", http.StatusForbidden},
		"unknown, no header": {untrustedAddr, "-", http.StatusForbidden},
	} {
		if got := scrape(t, s, c.addr, c.groups).Code; got != c.want {
			t.Errorf("%s: status %d, want %d", name, got, c.want)
		}
	}
}

func TestMetricsScrape(t *testing.T) {
	s, st, m := metricsServer(t, true)
	ctx := t.Context()
	j1, _ := st.CreateJob(ctx, "secret-user")
	j2, _ := st.CreateJob(ctx, "secret-user")
	if _, err := st.Transition(ctx, j2.ID, store.Uploading, store.Uploaded, store.Change{Actor: "system"}); err != nil {
		t.Fatal(err)
	}
	for i, off := range []int64{100, 250} {
		if err := st.PutUpload(ctx, store.Upload{ID: "u" + strconv.Itoa(i), JobID: j1.ID, Owner: "secret-user",
			Name: "secret-title.mkv", Size: 1000, Offset: off}); err != nil {
			t.Fatal(err)
		}
	}
	m.AddBytesReceived(4096)
	at := time.Now()
	m.ObserveTask(spool.Result{Kind: spool.KindProbe, Status: spool.StatusOK, Started: at, Finished: at.Add(2 * time.Second)})
	m.ObserveTask(spool.Result{Kind: spool.KindExtract, Status: spool.StatusError, Error: &spool.Error{Code: "timeout"},
		Started: at, Finished: at.Add(1000 * time.Second)})
	m.ObserveImport("radarr", nil)
	m.ObserveImport("sonarr", errors.New("boom"))

	rec := scrape(t, s, "127.0.0.1:40000", "-")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("status %d type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	got := parseMetrics(t, rec.Body.String())
	for series, want := range map[string]float64{
		`fukku_uploads_in_flight`:                                                        2,
		`fukku_uploads_in_flight_bytes`:                                                  350,
		`fukku_upload_received_bytes_total`:                                              4096,
		`fukku_jobs{state="uploading"}`:                                                  1,
		`fukku_jobs{state="uploaded"}`:                                                   1,
		`fukku_jobs{state="done"}`:                                                       0,
		`fukku_task_duration_seconds_bucket{kind="probe",outcome="ok",le="1"}`:           0,
		`fukku_task_duration_seconds_bucket{kind="probe",outcome="ok",le="5"}`:           1,
		`fukku_task_duration_seconds_bucket{kind="probe",outcome="ok",le="+Inf"}`:        1,
		`fukku_task_duration_seconds_sum{kind="probe",outcome="ok"}`:                     2,
		`fukku_task_duration_seconds_count{kind="extract",outcome="timeout"}`:            1,
		`fukku_task_duration_seconds_bucket{kind="extract",outcome="timeout",le="600"}`:  0,
		`fukku_task_duration_seconds_bucket{kind="extract",outcome="timeout",le="1800"}`: 1,
		`fukku_imports_total{app="radarr",outcome="ok"}`:                                 1,
		`fukku_imports_total{app="sonarr",outcome="error"}`:                              1,
		`fukku_ready{check="database"}`:                                                  1,
		`fukku_ready{check="workerHeartbeat"}`:                                           1,
	} {
		if v, ok := got[series]; !ok || v != want {
			t.Errorf("%s = %v (present %v), want %v", series, v, ok, want)
		}
	}
	for _, private := range []string{"secret-user", "secret-title", ".mkv", "user1"} {
		if strings.Contains(rec.Body.String(), private) {
			t.Errorf("the output contains %q", private)
		}
	}
}
