// Package metrics is a minimal Prometheus text exposition (format 0.0.4): counters, one
// histogram family and a writer for gauges that the web process reads from the database at
// scrape time. It is written by hand rather than taking prometheus/client_golang, which
// would add a dozen modules for what is a handful of series. Labels only ever carry values
// from fixed sets (task kind, outcome, app, job state, check name): never a file name, a
// title or a user.
package metrics

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/segun-io/fukku/internal/spool"
)

// TaskBuckets are the upper bounds, in seconds, of the task duration histogram.
var TaskBuckets = []float64{0.1, 0.5, 1, 5, 15, 30, 60, 120, 300, 600, 1800}

// Set holds the series that the process updates as things happen. Every method is safe for
// concurrent use and for a nil *Set, so code that has no metrics passes nil.
type Set struct {
	bytesReceived atomic.Int64

	mu      sync.Mutex
	tasks   map[[2]string]*hist
	imports map[[2]string]int64
}

type hist struct {
	counts []int64 // per bucket, not cumulative
	inf    int64
	sum    float64
}

// New returns an empty Set.
func New() *Set {
	return &Set{tasks: map[[2]string]*hist{}, imports: map[[2]string]int64{}}
}

// AddBytesReceived counts upload bytes read from clients.
func (s *Set) AddBytesReceived(n int64) {
	if s != nil && n > 0 {
		s.bytesReceived.Add(n)
	}
}

// ObserveTask records one finished worker task. Kind and outcome are reduced to a fixed set.
func (s *Set) ObserveTask(res spool.Result) {
	if s == nil {
		return
	}
	kind := res.Kind
	if kind != spool.KindProbe && kind != spool.KindExtract {
		kind = "other"
	}
	outcome := "ok"
	if res.Status != spool.StatusOK {
		outcome = "error"
		if res.Error != nil && (res.Error.Code == "timeout" || res.Error.Code == "cancelled") {
			outcome = res.Error.Code
		}
	}
	d := max(res.Finished.Sub(res.Started), 0).Seconds()
	s.mu.Lock()
	defer s.mu.Unlock()
	k := [2]string{kind, outcome}
	h := s.tasks[k]
	if h == nil {
		h = &hist{counts: make([]int64, len(TaskBuckets))}
		s.tasks[k] = h
	}
	if i := slices.IndexFunc(TaskBuckets, func(b float64) bool { return d <= b }); i >= 0 {
		h.counts[i]++
	} else {
		h.inf++
	}
	h.sum += d
}

// Import outcomes: the import command was sent to the app, every file arrived, or the job
// failed in the import.
const (
	ImportSubmitted = "submitted"
	ImportImported  = "imported"
	ImportFailed    = "failed"
)

// ObserveImport counts one step of the import engine for an app ("radarr" or "sonarr").
func (s *Set) ObserveImport(app, outcome string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.imports[[2]string{app, outcome}]++
	s.mu.Unlock()
}

// Write writes the Set's own series.
func (s *Set) Write(w *Writer) {
	if s == nil {
		s = New()
	}
	w.Family("fukku_upload_received_bytes_total", "counter", "Bytes of upload data received since the process started.")
	w.Sample("fukku_upload_received_bytes_total", float64(s.bytesReceived.Load()))

	s.mu.Lock()
	defer s.mu.Unlock()
	w.Family("fukku_task_duration_seconds", "histogram", "Worker task durations by kind and outcome.")
	for _, k := range sortedKeys(s.tasks) {
		h := s.tasks[k]
		base := []string{"kind", k[0], "outcome", k[1]}
		var cum int64
		for i, b := range TaskBuckets {
			cum += h.counts[i]
			w.Sample("fukku_task_duration_seconds_bucket", float64(cum), append(slices.Clone(base), "le", formatFloat(b))...)
		}
		cum += h.inf
		w.Sample("fukku_task_duration_seconds_bucket", float64(cum), append(slices.Clone(base), "le", "+Inf")...)
		w.Sample("fukku_task_duration_seconds_sum", h.sum, base...)
		w.Sample("fukku_task_duration_seconds_count", float64(cum), base...)
	}
	w.Family("fukku_imports_total", "counter", "Imports by app and outcome (submitted, imported, failed).")
	for _, k := range sortedKeys(s.imports) {
		w.Sample("fukku_imports_total", float64(s.imports[k]), "app", k[0], "outcome", k[1])
	}
}

func sortedKeys[V any](m map[[2]string]V) [][2]string {
	keys := make([][2]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b [2]string) int {
		return strings.Compare(a[0]+"\x00"+a[1], b[0]+"\x00"+b[1])
	})
	return keys
}

// Writer writes the text exposition format.
type Writer struct {
	w   io.Writer
	err error
}

// NewWriter returns a Writer on w; Err reports the first write error.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Err is the first error a write returned.
func (w *Writer) Err() error { return w.err }

func (w *Writer) printf(format string, a ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprintf(w.w, format, a...)
	}
}

// Family writes the HELP and TYPE lines of a metric family (typ: counter, gauge, histogram).
func (w *Writer) Family(name, typ, help string) {
	w.printf("# HELP %s %s\n# TYPE %s %s\n", name, strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(help), name, typ)
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// Sample writes one sample; labels are name, value pairs.
func (w *Writer) Sample(name string, value float64, labels ...string) {
	var b strings.Builder
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(labels[i] + `="` + labelEscaper.Replace(labels[i+1]) + `"`)
		}
		b.WriteByte('}')
	}
	w.printf("%s %s\n", b.String(), formatFloat(value))
}

func formatFloat(f float64) string {
	if math.IsInf(f, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
