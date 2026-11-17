package probes

import (
	"strings"
	"testing"
)

func TestParseRun(t *testing.T) {
	out := "=== RUN   TestB3\r\n--- FAIL: TestB3 (1.00s)\r\n    --- FAIL: TestB3/sub (0.10s)\r\nFAIL\r\nFAIL\tx/probes\t2s\r\n" + RunStatusPrefix + "1\n"
	run, err := ParseRun(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if !run.Finished || run.Exit != 1 || len(run.Failed) != 2 || run.Failed[0] != "TestB3" || run.Failed[1] != "TestB3/sub" || run.Panicked || run.BuildFailed {
		t.Errorf("%+v", run)
	}
	run, _ = ParseRun(strings.NewReader("FAIL\tx/probes [build failed]\n" + RunStatusPrefix + "1\n"))
	if !run.BuildFailed {
		t.Errorf("build failure not seen: %+v", run)
	}
	run, _ = ParseRun(strings.NewReader("panic: boom\n"))
	if !run.Panicked || run.Finished {
		t.Errorf("%+v", run)
	}
}

func TestGateRun(t *testing.T) {
	acc := []Accepted{{ID: "B3", Verdict: Differs, Ref: "#20", Reason: "known"}}
	differs := Result{ID: "B3", Test: "TestB3", Verdict: Differs}
	other := Result{ID: "C1", Test: "TestC1", Verdict: Differs}
	ok := Result{ID: "B1", Test: "TestB1", Verdict: Confirmed}
	tests := []struct {
		name string
		rs   []Result
		run  Run
		exit int
		want string
	}{
		{"clean run", []Result{ok}, Run{Finished: true}, 0, ""},
		{"accepted differs only passes", []Result{ok, differs}, Run{Finished: true, Exit: 1, Failed: []string{"TestB3"}}, 0, "(accepted)"},
		{"accepted differs in a subtest passes", []Result{differs}, Run{Finished: true, Exit: 1, Failed: []string{"TestB3/sub", "TestB3"}}, 0, "(accepted)"},
		{"new differs fails", []Result{differs, other}, Run{Finished: true, Exit: 1, Failed: []string{"TestB3", "TestC1"}}, 1, "::error title=Probe C1 differs"},
		{"failed test without a result fails", []Result{ok, differs}, Run{Finished: true, Exit: 1, Failed: []string{"TestB3", "TestBoom"}}, 1, "Test TestBoom failed"},
		{"failed test whose result is confirmed fails", []Result{ok}, Run{Finished: true, Exit: 1, Failed: []string{"TestB1"}}, 1, "Test TestB1 failed"},
		{"build failure fails", []Result{differs}, Run{Finished: true, Exit: 1, BuildFailed: true}, 1, "build failed"},
		{"build failure fails even with no results", nil, Run{Finished: true, Exit: 1, BuildFailed: true}, 1, "build failed"},
		{"panic fails", []Result{differs}, Run{Finished: true, Exit: 2, Panicked: true, Failed: []string{"TestB3"}}, 1, "panicked"},
		{"non-zero exit without a failed test fails", []Result{ok}, Run{Finished: true, Exit: 1}, 1, "no test is reported as failed"},
		{"run that never finished fails", []Result{ok}, Run{}, 1, "did not finish"},
	}
	for _, tt := range tests {
		exit, ann := GateRun(tt.rs, acc, tt.run)
		joined := strings.Join(ann, "\n")
		if exit != tt.exit || !strings.Contains(joined, tt.want) {
			t.Errorf("%s: exit %d, annotations %q; want exit %d containing %q", tt.name, exit, joined, tt.exit, tt.want)
		}
	}
}
