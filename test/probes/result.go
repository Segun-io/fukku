// Package probes holds the behaviour probes (plan §7, §14): tests that observe how the
// pinned versions of Radarr, Sonarr, Bazarr and Jellyfin behave, record what they saw, and
// compare it with what the plan assumes. They report rather than assume: every run writes a
// result file, and `make compat` turns the results into docs/compatibility.md.
//
// The probes themselves are in *_test.go files behind the `probes` build tag; run them with
// `make probes` (offline) or `make probes-online` (also those that add titles, which needs
// the *arr metadata services on the internet).
package probes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Verdict says how an observation compares with the plan.
type Verdict string

const (
	Confirmed Verdict = "confirmed" // behaves as the plan says
	Differs   Verdict = "differs"   // behaves differently: the plan or the design must change
	Partial   Verdict = "partial"   // only part of the claim could be checked
	Observed  Verdict = "observed"  // the plan made no claim (it said "unverified"); this is what happens
	Skipped   Verdict = "skipped"   // not run (e.g. needs the internet)
	Error     Verdict = "error"     // the probe could not run to the end
)

// Result is one probe's outcome, written as <dir>/<ID>.json.
type Result struct {
	ID       string         `json:"id"`
	Title    string         `json:"title"`
	Apps     []string       `json:"apps"`
	Claim    string         `json:"claim"`
	Observed []string       `json:"observed"`
	Verdict  Verdict        `json:"verdict"`
	Online   bool           `json:"online"`
	Evidence map[string]any `json:"evidence,omitempty"`
	Error    string         `json:"error,omitempty"`
	Duration string         `json:"duration"`
	At       time.Time      `json:"at"`
}

// Write stores r in dir.
func (r Result) Write(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, r.ID+".json"), append(data, '\n'), 0o600)
}

// ReadResults loads every result in dir, sorted by ID, and the versions.json the seeder wrote.
func ReadResults(dir string) ([]Result, map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, nil, err
	}
	var out []Result
	versions := map[string]string{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		if filepath.Base(f) == "versions.json" {
			if err := json.Unmarshal(data, &versions); err != nil {
				return nil, nil, err
			}
			continue
		}
		var r Result
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, nil, err
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	return out, versions, nil
}

// idLess orders B1 < B2 < … < B10 < C1 < J1.
func idLess(a, b string) bool {
	if a[0] != b[0] {
		return a[0] < b[0]
	}
	na, nb := num(a[1:]), num(b[1:])
	if na != nb {
		return na < nb
	}
	return a < b
}

func num(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
