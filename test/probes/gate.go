package probes

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/segun-io/fukku/test/testenv"
)

// Gate decides how CI treats a run: it fails (exit code 1) on any differs or error verdict,
// and passes a probe skipped because the upstream metadata service was unavailable with a
// warning. It returns GitHub Actions annotations, one per finding.
//
// accepted lists recorded differences: a differs or error verdict that matches an entry is
// reported as a warning, any other still fails, and an entry whose probe now passes is
// reported as one that can be removed.
func Gate(rs []Result, accepted []Accepted) (exit int, annotations []string) {
	acc := map[string]Accepted{}
	for _, a := range accepted {
		acc[a.ID] = a
	}
	for _, r := range rs {
		a, isAccepted := acc[r.ID]
		switch {
		case isAccepted && r.Verdict == a.Verdict:
			annotations = append(annotations, fmt.Sprintf("::warning title=Probe %s %s (accepted)::%s: %s", r.ID, r.Verdict, oneLine(a.Ref), oneLine(a.Reason)))
		case isAccepted && (r.Verdict == Confirmed || r.Verdict == Partial || r.Verdict == Observed):
			annotations = append(annotations, fmt.Sprintf("::warning title=Probe %s can be removed from the accepted list::it is now %s (accepted %s: %s)", r.ID, r.Verdict, a.Verdict, oneLine(a.Ref)))
		case r.Verdict == Differs || r.Verdict == Error:
			exit = 1
			annotations = append(annotations, fmt.Sprintf("::error title=Probe %s %s::%s", r.ID, r.Verdict, oneLine(r.Title)))
		case r.Verdict == Skipped && strings.HasPrefix(r.Reason, UpstreamUnavailable):
			annotations = append(annotations, fmt.Sprintf("::warning title=Probe %s skipped::%s: %s", r.ID, r.Reason, oneLine(r.Title)))
		}
	}
	return exit, annotations
}

// Accepted is a recorded difference: the probe's verdict is a known finding, tracked
// elsewhere, so CI reports it as a warning instead of failing.
type Accepted struct {
	ID      string  `json:"id"`
	Verdict Verdict `json:"verdict"`
	Ref     string  `json:"ref"`
	Reason  string  `json:"reason"`
}

// ReadAccepted loads the accepted list (a JSON array); a missing file is an empty list.
func ReadAccepted(path string) ([]Accepted, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Accepted
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, a := range out {
		if a.ID == "" || a.Ref == "" || a.Reason == "" || (a.Verdict != Differs && a.Verdict != Error) {
			return nil, fmt.Errorf("%s: entry %q needs an id, a verdict of differs or error, a ref and a reason", path, a.ID)
		}
	}
	return out, nil
}

func oneLine(s string) string {
	return strings.NewReplacer("%", "%25", "\n", " ", "\r", " ").Replace(s)
}

// Run is what run-probes.sh recorded about the go test run that wrote the results: the exit
// status and the failed tests, read from the test output (see ParseRun).
type Run struct {
	Exit        int
	Finished    bool     // the exit status line was found: go test ran to its end
	Failed      []string // names of the tests (and subtests) that failed
	Panicked    bool     // a test panicked
	BuildFailed bool     // the probe package did not build or set up
}

// RunStatusPrefix starts the last line run-probes.sh appends to the go test output.
const RunStatusPrefix = "go test exit status: "

var failedTest = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)

// ParseRun reads the go test -v output that run-probes.sh recorded. It is deliberately
// forgiving about the format: any failed test line, panic or build failure counts.
func ParseRun(r io.Reader) (Run, error) {
	var run Run
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case strings.HasPrefix(line, RunStatusPrefix):
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, RunStatusPrefix)))
			if err != nil {
				return run, fmt.Errorf("bad exit status line %q", line)
			}
			run.Exit, run.Finished = n, true
		case strings.HasPrefix(line, "panic:"):
			run.Panicked = true
		case strings.Contains(line, "[build failed]"), strings.Contains(line, "[setup failed]"):
			run.BuildFailed = true
		default:
			if m := failedTest.FindStringSubmatch(line); m != nil {
				run.Failed = append(run.Failed, m[1])
			}
		}
	}
	return run, sc.Err()
}

// sameTest reports whether a failed test belongs to the probe test: the same test, or one
// is a subtest of the other.
func sameTest(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// GateRun is Gate plus the go test run behind the results. A test that failed because its
// probe recorded a differs or error verdict is judged by Gate (accepted or not); any other
// failure fails the gate: a test that failed without such a result, a panic, a build failure,
// a non-zero exit with no failed test, or a run that never recorded its exit status.
func GateRun(rs []Result, accepted []Accepted, run Run) (exit int, annotations []string) {
	exit, annotations = Gate(rs, accepted)
	fail := func(title, msg string) {
		exit = 1
		annotations = append(annotations, fmt.Sprintf("::error title=%s::%s", title, oneLine(msg)))
	}
	switch {
	case !run.Finished:
		fail("go test did not finish", "no exit status was recorded: the run was killed or never started")
	case run.BuildFailed:
		fail("go test build failed", "the probe package did not build (see the go test output)")
	}
	if run.Panicked {
		fail("go test panicked", "a probe panicked (see the go test output)")
	}
	for _, name := range run.Failed {
		covered := false
		for _, r := range rs {
			if (r.Verdict == Differs || r.Verdict == Error) && r.Test != "" && sameTest(r.Test, name) {
				covered = true
				break
			}
		}
		if !covered {
			fail("Test "+name+" failed", "it failed without writing a differs or error result: a crash or a broken test, not a finding")
		}
	}
	if run.Finished && run.Exit != 0 && len(run.Failed) == 0 && !run.BuildFailed && !run.Panicked {
		fail("go test failed", fmt.Sprintf("it exited with status %d but no test is reported as failed", run.Exit))
	}
	return exit, annotations
}

// metadataKeywords mark a 5xx body as "the app could not reach its metadata service".
var metadataKeywords = []string{"unable to communicate", "skyhook", "radarrapi", "metadata", "timed out", "timeout", "unavailable", "upstream"}

// MetadataUnavailable reports whether err means a Radarr or Sonarr call failed because the
// app could not reach its metadata service: a timeout, or a 5xx whose body says so. Anything
// else (a 404 from a wrong path, a 4xx, a decode error) is a real error.
func MetadataUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	var he *testenv.HTTPError
	if errors.As(err, &he) && he.Status >= 500 {
		body := strings.ToLower(he.Body)
		for _, k := range metadataKeywords {
			if strings.Contains(body, k) {
				return true
			}
		}
	}
	return false
}
