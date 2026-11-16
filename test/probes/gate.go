package probes

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
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
