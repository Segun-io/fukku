package probes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/segun-io/fukku/test/testenv"
)

func TestGate(t *testing.T) {
	tests := []struct {
		name     string
		rs       []Result
		exit     int
		warnings int
	}{
		{"all good", []Result{{ID: "B1", Verdict: Confirmed}, {ID: "B2", Verdict: Partial}, {ID: "B3", Verdict: Observed}}, 0, 0},
		{"differs fails", []Result{{ID: "B1", Verdict: Confirmed}, {ID: "B3", Verdict: Differs}}, 1, 0},
		{"error fails", []Result{{ID: "C1", Verdict: Error}}, 1, 0},
		{"offline skip is silent", []Result{{ID: "C1", Verdict: Skipped, Reason: "needs the internet"}}, 0, 0},
		{"upstream skip warns and passes", []Result{{ID: "C1", Verdict: Skipped, Reason: UpstreamUnavailable}}, 0, 1},
		{"upstream skip does not hide a differs", []Result{{ID: "C1", Verdict: Skipped, Reason: UpstreamUnavailable}, {ID: "B3", Verdict: Differs}}, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, ann := Gate(tt.rs, nil)
			warnings := 0
			for _, a := range ann {
				if strings.HasPrefix(a, "::warning") {
					warnings++
				}
			}
			if exit != tt.exit || warnings != tt.warnings {
				t.Errorf("exit %d, %d warnings (%v); want %d and %d", exit, warnings, ann, tt.exit, tt.warnings)
			}
		})
	}
}

func TestMetadataUnavailable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"timeout", fmt.Errorf("lookup: %w", context.DeadlineExceeded), true},
		{"500 saying so", &testenv.HTTPError{Status: 500, Body: `{"message":"Unable to communicate with SkyHook."}`}, true},
		{"wrapped 503", fmt.Errorf("x: %w", &testenv.HTTPError{Status: 503, Body: "Service Unavailable"}), true},
		{"500 about something else", &testenv.HTTPError{Status: 500, Body: "database is locked"}, false},
		{"wrong path is a real error", &testenv.HTTPError{Status: 404, Body: "NotFound: metadata"}, false},
		{"plain error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		if got := MetadataUnavailable(tt.err); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestWriteKeepsEarlierVerdict(t *testing.T) {
	dir := t.TempDir()
	if err := (Result{ID: "B3", Verdict: Differs}).Write(dir); err != nil {
		t.Fatal(err)
	}
	if err := (Result{ID: "B3", Verdict: Skipped, Reason: UpstreamUnavailable}).Write(dir); err != nil {
		t.Fatal(err)
	}
	// A second skip keeps the first earlier verdict.
	if err := (Result{ID: "B3", Verdict: Skipped, Reason: UpstreamUnavailable}).Write(dir); err != nil {
		t.Fatal(err)
	}
	rs, _, err := ReadResults(dir)
	if err != nil || len(rs) != 1 {
		t.Fatalf("%v %v", rs, err)
	}
	if rs[0].Earlier == nil || rs[0].Earlier.Verdict != Differs {
		t.Errorf("earlier verdict lost: %+v", rs[0])
	}
}
