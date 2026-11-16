package flows

import (
	"bytes"
	"strings"
	"testing"

	"github.com/segun-io/fukku/internal/metrics"
	"github.com/segun-io/fukku/internal/store"
)

func importCounts(m *metrics.Set) string {
	var b bytes.Buffer
	m.Write(metrics.NewWriter(&b))
	var out []string
	for l := range strings.SplitSeq(b.String(), "\n") {
		if strings.HasPrefix(l, "fukku_imports_total{") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestImportMetricsSuccess(t *testing.T) {
	x := newImportEnv(t)
	m := metrics.New()
	x.eng.SetMetrics(m)
	j := x.confirm(x.filmJob())
	j, _ = x.step(x.eng, j) // submits
	x.movies.Complete(x.movies.Imports[0].CommandID)
	if j, _ = x.step(x.eng, j); j.State != store.Imported {
		t.Fatalf("job %s", j.State)
	}
	want := `fukku_imports_total{app="radarr",outcome="imported"} 1` + "\n" + `fukku_imports_total{app="radarr",outcome="submitted"} 1`
	if got := importCounts(m); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestImportMetricsFailure(t *testing.T) {
	x := newImportEnv(t)
	m := metrics.New()
	x.eng.SetMetrics(m)
	j := x.confirm(x.seasonJob(2))
	j, _ = x.step(x.eng, j)
	x.series.Fail(x.series.Imports[0].CommandID, "Disk full")
	if j, _ = x.step(x.eng, j); j.State != store.Failed {
		t.Fatalf("job %s", j.State)
	}
	want := `fukku_imports_total{app="sonarr",outcome="failed"} 1` + "\n" + `fukku_imports_total{app="sonarr",outcome="submitted"} 1`
	if got := importCounts(m); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
