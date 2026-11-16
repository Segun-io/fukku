package fixtures

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAwkwardSeason(t *testing.T) {
	needFFmpeg(t)
	ctx := context.Background()
	dir := t.TempDir()
	const runtime = 4 * time.Second
	path, want, err := AwkwardSeason(ctx, dir, "Probe Show", runtime)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	extractZip(t, path, out)

	for _, w := range want {
		p, err := Probe(ctx, filepath.Join(out, w.File))
		if err != nil {
			t.Fatalf("%s: %v", w.File, err)
		}
		if d := p.Duration - w.Duration; d < -200*time.Millisecond || d > 200*time.Millisecond {
			t.Errorf("%s: duration %v, want %v", w.File, p.Duration, w.Duration)
		}
		if p.Count("audio") != 1 {
			t.Fatalf("%s: streams %+v", w.File, p.Streams)
		}
		for _, s := range p.Streams {
			if s.Type == "audio" && s.Language != w.Language {
				t.Errorf("%s: audio %q, want %q", w.File, s.Language, w.Language)
			}
		}
	}

	// The double: named as episode 1 only, holding episodes 1 and 2, twice the runtime.
	if w := want[0]; w.File != "Season 01/Probe Show - S01E01.mkv" || len(w.Episodes) != 2 || w.Duration != 2*runtime {
		t.Errorf("double: %+v", w)
	}
	// The misnumbered file: named 5, really 4, and no file is named 4.
	if w := want[2]; w.File != "Season 01/Probe Show - S01E05.mkv" || w.Episodes[0] != 4 {
		t.Errorf("misnumbered: %+v", w)
	}
	names := zipNames(t, path)
	for _, n := range names {
		if strings.Contains(n, "E04") {
			t.Errorf("a file is named episode 4: %s", n)
		}
	}
	// Exactly one dub, Spanish only.
	dubs := 0
	for _, w := range want {
		if w.Dubbed {
			dubs++
			if w.Language != "spa" {
				t.Errorf("dub language %q", w.Language)
			}
		}
	}
	if dubs != 1 {
		t.Errorf("%d dubs, want 1", dubs)
	}
	// One English subtitle file, and nothing else besides the five videos.
	if len(names) != 6 {
		t.Errorf("archive has %v, want 5 videos and 1 subtitle", names)
	}
	srt, err := os.ReadFile(filepath.Join(out, "Season 01/Probe Show - S01E03.en.srt"))
	if err != nil || !strings.Contains(string(srt), "[eng]") {
		t.Errorf("subtitle: %v %q", err, srt)
	}
}

func TestSeasonZipOf(t *testing.T) {
	needFFmpeg(t)
	ctx := context.Background()
	dir := t.TempDir()
	path, err := SeasonZipOf(ctx, dir, "x.zip", Video{Duration: 2 * time.Second, Audio: []Audio{{Language: "eng"}}},
		[]Episode{{Name: "a.mkv"}, {Name: "b.mkv", Duration: 4 * time.Second, Audio: []Audio{{Language: "fra"}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := zipNames(t, path); len(n) != 2 || n[0] != "a.mkv" || n[1] != "b.mkv" {
		t.Fatalf("entries %v", n)
	}
	out := filepath.Join(dir, "out")
	extractZip(t, path, out)
	a, err := Probe(ctx, filepath.Join(out, "a.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Probe(ctx, filepath.Join(out, "b.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Duration > 3*time.Second || b.Duration < 3*time.Second {
		t.Errorf("durations %v and %v: the spec's default and the override", a.Duration, b.Duration)
	}
	if a.Streams[1].Language != "eng" || b.Streams[1].Language != "fra" {
		t.Errorf("audio: %+v %+v", a.Streams, b.Streams)
	}
}

func extractZip(t *testing.T, path, dst string) {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	for _, f := range r.File {
		target := filepath.Join(dst, f.Name)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			t.Fatal(err)
		}
		in, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(in)
		_ = in.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
