package config

import (
	"os"
	"strings"
	"testing"
)

// TestSettingsDocumented keeps docs/settings.md and the code in step: every setting must have
// a row naming its key, its environment variable and its default, and every row must name a
// real setting.
func TestSettingsDocumented(t *testing.T) {
	data, err := os.ReadFile("../../docs/settings.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(line, "|")
		key := strings.Trim(strings.TrimSpace(cells[1]), "`")
		rows[key] = line
	}

	cfg := Defaults()
	known := map[string]bool{}
	for _, l := range Leaves(&cfg) {
		known[l.Key] = true
		row, ok := rows[l.Key]
		if !ok {
			t.Errorf("docs/settings.md has no row for %s", l.Key)
			continue
		}
		cells := strings.Split(row, "|")
		if env := strings.TrimSpace(cells[2]); env != "`"+l.Env+"`" {
			t.Errorf("%s: env column is %s, want `%s`", l.Key, env, l.Env)
		}
		want := "`" + l.Default + "`"
		if l.Default == "" {
			want = "(empty)"
		}
		if got := strings.TrimSpace(cells[3]); got != want {
			t.Errorf("%s: default column is %s, want %s", l.Key, got, want)
		}
	}
	for key := range rows {
		if !known[key] {
			t.Errorf("docs/settings.md documents %s, which is not a setting", key)
		}
	}
}
