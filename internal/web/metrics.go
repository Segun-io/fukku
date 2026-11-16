package web

import (
	"bytes"
	"net/http"

	"github.com/segun-io/fukku/internal/metrics"
	"github.com/segun-io/fukku/internal/store"
)

const metricsPage = 1000

// metrics serves /metrics in the Prometheus text format when server.metrics is on, and is a
// plain 404 when it is off (so it never falls through to the UI). Who may read it is the
// authentication middleware's rule for /readyz: a loopback address, or an admin.
//
// Gauges that the database knows (uploads in flight, jobs by state) are read at scrape time;
// counters and the task duration histogram are kept by the process (package metrics). No
// label carries a user, a file name or a title.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Server.Metrics {
		http.NotFound(w, r)
		return
	}
	var buf bytes.Buffer
	mw := metrics.NewWriter(&buf)

	if st := s.deps.Store; st != nil {
		ctx := r.Context()
		if ups, err := st.Uploads(ctx, "", ""); err != nil {
			s.log.Warn("metrics_uploads_failed", "error", err)
		} else {
			var bytesIn int64
			for _, u := range ups {
				bytesIn += u.Offset
			}
			mw.Family("fukku_uploads_in_flight", "gauge", "Unfinished uploads.")
			mw.Sample("fukku_uploads_in_flight", float64(len(ups)))
			mw.Family("fukku_uploads_in_flight_bytes", "gauge", "Bytes received so far by unfinished uploads.")
			mw.Sample("fukku_uploads_in_flight_bytes", float64(bytesIn))
		}
		if counts, err := s.jobCounts(r); err != nil {
			s.log.Warn("metrics_jobs_failed", "error", err)
		} else {
			mw.Family("fukku_jobs", "gauge", "Jobs by state.")
			for _, state := range allStates {
				mw.Sample("fukku_jobs", float64(counts[state]), "state", string(state))
			}
		}
	}

	s.deps.Metrics.Write(mw)

	ready := s.checkReadiness(r.Context(), true)
	mw.Family("fukku_ready", "gauge", "1 when the readiness check passes, else 0.")
	for _, c := range ready.Checks {
		v := 0.0
		if c.OK {
			v = 1
		}
		mw.Sample("fukku_ready", v, "check", c.Name)
	}

	if mw.Err() != nil {
		http.Error(w, "metrics unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) jobCounts(r *http.Request) (map[store.State]int, error) {
	counts := map[store.State]int{}
	seen := map[string]bool{}
	for off := 0; ; off += metricsPage {
		page, err := s.deps.Store.Jobs(r.Context(), store.JobFilter{Limit: metricsPage, Offset: off})
		if err != nil {
			return nil, err
		}
		for _, j := range page {
			if !seen[j.ID] { // a job that moved between pages can show up twice
				seen[j.ID] = true
				counts[j.State]++
			}
		}
		if len(page) < metricsPage {
			return counts, nil
		}
	}
}
