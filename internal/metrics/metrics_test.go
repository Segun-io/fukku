package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestEscaping(t *testing.T) {
	var b bytes.Buffer
	w := NewWriter(&b)
	w.Sample("x", 1, "a", "q\"u\\o\nte")
	if want := `x{a="q\"u\\o\nte"} 1` + "\n"; b.String() != want {
		t.Fatalf("got %q, want %q", b.String(), want)
	}
}

func TestNilSet(t *testing.T) {
	var s *Set
	s.AddBytesReceived(1)
	s.ObserveImport("radarr", ImportImported)
	var b bytes.Buffer
	s.Write(NewWriter(&b))
	if !strings.Contains(b.String(), "fukku_upload_received_bytes_total 0") {
		t.Fatalf("got %q", b.String())
	}
}
