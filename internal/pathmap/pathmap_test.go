package pathmap

import (
	"testing"

	"github.com/segun-io/fukku/internal/config"
)

func TestTo(t *testing.T) {
	m := []config.PathMapping{
		{From: "/data", To: "/mnt/data"},
		{From: "/data/intake", To: "/downloads/intake"},
		{From: "/", To: "/host"},
	}
	for _, c := range []struct {
		name string
		m    []config.PathMapping
		in   string
		want string
	}{
		{"longest prefix wins", m, "/data/intake/jobs/x", "/downloads/intake/jobs/x"},
		{"shorter prefix", m, "/data/library/film.mkv", "/mnt/data/library/film.mkv"},
		{"the prefix itself", m, "/data/intake", "/downloads/intake"},
		{"whole segments only", m[:2], "/data/intake2/x", "/mnt/data/intake2/x"},
		{"no sibling match", []config.PathMapping{{From: "/data/in", To: "/x"}}, "/data/intake/x", "/data/intake/x"},
		{"root mapping", m[2:], "/etc/x", "/host/etc/x"},
		{"no match", m[:2], "/other/x", "/other/x"},
		{"no mapping", nil, "/data/x", "/data/x"},
		{"trailing slashes", []config.PathMapping{{From: "/data/", To: "/mnt/"}}, "/data/x", "/mnt/x"},
		{"to root", []config.PathMapping{{From: "/data", To: "/"}}, "/data", "/"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := To(c.m, c.in); got != c.want {
				t.Errorf("To(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestFrom(t *testing.T) {
	m := []config.PathMapping{
		{From: "/data/intake", To: "/downloads/intake"},
		{From: "/data", To: "/downloads"},
	}
	for in, want := range map[string]string{
		"/downloads/intake/jobs/x": "/data/intake/jobs/x",
		"/downloads/other":         "/data/other",
		"/downloads/intakes":       "/data/intakes",
		"/elsewhere":               "/elsewhere",
	} {
		if got := From(m, in); got != want {
			t.Errorf("From(%q) = %q, want %q", in, got, want)
		}
	}
}
