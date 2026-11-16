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
	flag.Parse()
	if err := run(*results, *doc); err != nil {
		fmt.Fprintln(os.Stderr, "compat:", err)
		os.Exit(1)
	}
}

func run(dir, doc string) error {
	rs, versions, err := probes.ReadResults(dir)
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
	out := text[:i+len(startMarker)] + "\n" + render(rs, versions) + text[j:]
	return os.WriteFile(doc, []byte(out), 0o644)
}

func render(rs []probes.Result, versions map[string]string) string {
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
		fmt.Fprintf(&b, "| %s | %s | %s | %s | **%s** |\n", r.ID, cell(r.Title), cell(r.Claim), cell(obs), r.Verdict)
	}
	b.WriteString("\n")
	return b.String()
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}
