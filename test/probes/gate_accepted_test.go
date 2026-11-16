package probes

import (
	"strings"
	"testing"
)

func TestGateAccepted(t *testing.T) {
	acc := []Accepted{{ID: "B3", Verdict: Differs, Ref: "#20", Reason: "known"}}
	tests := []struct {
		name string
		rs   []Result
		exit int
		want string
	}{
		{"accepted differs warns", []Result{{ID: "B3", Verdict: Differs}}, 0, "(accepted)"},
		{"other differs still fails", []Result{{ID: "B3", Verdict: Differs}, {ID: "C1", Verdict: Differs}}, 1, "::error title=Probe C1"},
		{"accepted probe now an error fails", []Result{{ID: "B3", Verdict: Error}}, 1, "::error"},
		{"accepted probe now passes", []Result{{ID: "B3", Verdict: Confirmed}}, 0, "can be removed"},
		{"accepted probe skipped is silent", []Result{{ID: "B3", Verdict: Skipped, Reason: "x"}}, 0, ""},
	}
	for _, tt := range tests {
		exit, ann := Gate(tt.rs, acc)
		joined := strings.Join(ann, "\n")
		if exit != tt.exit || !strings.Contains(joined, tt.want) {
			t.Errorf("%s: exit %d, annotations %q; want exit %d containing %q", tt.name, exit, joined, tt.exit, tt.want)
		}
	}
}

func TestAcceptedFileIsValid(t *testing.T) {
	acc, err := ReadAccepted("accepted.json")
	if err != nil || len(acc) == 0 {
		t.Fatalf("accepted.json: %v (%d entries)", err, len(acc))
	}
}
