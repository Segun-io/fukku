// Command compat writes the latest behaviour-probe results into docs/compatibility.md,
// between the "probes:start" and "probes:end" markers (make compat).
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/segun-io/fukku/test/probes"
)

const (
	startMarker = "<!-- probes:start -->"
	endMarker   = "<!-- probes:end -->"
)

func main() {
	results := flag.String("results", "test/env/.run/results", "directory with probe results")
	doc := flag.String("doc", "docs/compatibility.md", "document to update")
	accepted := flag.String("accepted", "test/probes/accepted.json", "recorded differences (accepted findings)")
	gotest := flag.String("gotest", "test/env/.run/go-test.log", "go test output recorded by run-probes.sh, with its exit status (-gate only)")
	gate := flag.Bool("gate", false, "instead of writing the document, print CI annotations and exit 1 on a differs or error verdict")
	flag.Parse()
	if *gate {
		os.Exit(runGate(*results, *accepted, *gotest))
	}
	if err := run(*results, *doc, *accepted); err != nil {
		fmt.Fprintln(os.Stderr, "compat:", err)
		os.Exit(1)
	}
}

// runGate applies the CI rule (probes.Gate) to the results in dir and returns the exit code.
func runGate(dir, acceptedPath, gotestPath string) int {
	rs, _, err := probes.ReadResults(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "compat:", err)
		return 1
	}
	acc, err := probes.ReadAccepted(acceptedPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "compat:", err)
		return 1
	}
	f, err := os.Open(gotestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "compat: no go test record (run make probes first):", err)
		return 1
	}
	run, err := probes.ParseRun(f)
	_ = f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "compat:", err)
		return 1
	}
	code, annotations := probes.GateRun(rs, acc, run)
	var errs, accepted, warns int
	for _, a := range annotations {
		fmt.Println(a)
		switch {
		case strings.HasPrefix(a, "::error"):
			errs++
		case strings.Contains(a, "(accepted)"):
			accepted++
		default:
			warns++
		}
	}
	// The last line of a local run: say plainly whether anything needs attention.
	if code != 0 {
		fmt.Printf("probe gate: FAILED, %d problem(s): a new differs, an error, or a probe that crashed\n", errs)
	} else {
		fmt.Printf("probe gate: passed; %d accepted finding(s) from test/probes/accepted.json, %d other warning(s), nothing new\n", accepted, warns)
	}
	return code
}

func run(dir, doc, acceptedPath string) error {
	rs, versions, err := probes.ReadResults(dir)
	if err != nil {
		return err
	}
	acc, err := probes.ReadAccepted(acceptedPath)
	if err != nil {
		return err
	}
	if len(rs) == 0 {
		return fmt.Errorf("no results in %s: run make probes-online first", dir)
	}
	data, err := os.ReadFile(doc)
	if err != nil {
		return err
	}
	text := string(data)
	i, j := strings.Index(text, startMarker), strings.Index(text, endMarker)
	if i < 0 || j < i {
		return fmt.Errorf("%s has no %s … %s block", doc, startMarker, endMarker)
	}
	out := text[:i+len(startMarker)] + "\n" + render(rs, versions, acc) + text[j:]
	return os.WriteFile(doc, []byte(out), 0o644)
}

func render(rs []probes.Result, versions map[string]string, accepted []probes.Accepted) string {
	acc := map[string]probes.Accepted{}
	for _, a := range accepted {
		acc[a.ID] = a
	}
	var b strings.Builder
	var apps []string
	for k := range versions {
		apps = append(apps, k)
	}
	sort.Strings(apps)
	var vs []string
	for _, a := range apps {
		vs = append(vs, fmt.Sprintf("%s %s", a, versions[a]))
	}
	last := rs[0].At
	for _, r := range rs {
		if r.At.After(last) {
			last = r.At
		}
	}
	fmt.Fprintf(&b, "\nRun of %s UTC against %s.\n\n", last.Format("2006-01-02 15:04"), strings.Join(vs, ", "))
	b.WriteString("| Probe | Behaviour | The plan says | Observed | Verdict |\n| --- | --- | --- | --- | --- |\n")
	for _, r := range rs {
		obs := strings.Join(r.Observed, "; ")
		if r.Error != "" {
			obs += "; error: " + r.Error
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", r.ID, cell(r.Title), cell(r.Claim), cell(obs), verdict(r, acc[r.ID]))
	}
	b.WriteString("\n")
	return b.String()
}

// verdict is the verdict cell. A skipped probe says why, and which verdict of an earlier
// run it replaced, so a skip never hides a recorded result.
func verdict(r probes.Result, a probes.Accepted) string {
	s := "**" + string(r.Verdict) + "**"
	if a.ID != "" && a.Verdict == r.Verdict {
		return cell(s + " (accepted: " + a.Ref + ")")
	}
	if r.Verdict != probes.Skipped {
		return s
	}
	if r.Reason != "" {
		s += ": " + r.Reason
	}
	if r.Earlier != nil {
		s += fmt.Sprintf(" (earlier run, %s: %s)", r.Earlier.At.Format("2006-01-02"), r.Earlier.Verdict)
	}
	return cell(s)
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}
