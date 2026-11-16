package fixtures

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed (run the tests with make test)", tool)
		}
	}
}

func TestMediaFixtures(t *testing.T) {
	needFFmpeg(t)
	ctx := context.Background()
	dir := t.TempDir()

	base := filepath.Join(dir, "film.mkv")
	spec := Video{
		Duration: 30 * time.Second, Width: 1280, Height: 720,
		Audio:     []Audio{{Language: "eng", Title: "English", Default: true}, {Language: "spa", Title: "Español (Latino)", ToneHz: 440}},
		Subtitles: []Subtitle{{Language: "eng"}, {Language: "spa", Forced: true}},
		Title:     "Fixture",
	}
	if err := Generate(ctx, base, spec); err != nil {
		t.Fatal(err)
	}
	p, err := Probe(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if d := p.Duration - spec.Duration; d < -100*time.Millisecond || d > 100*time.Millisecond {
		t.Errorf("duration %v, want %v", p.Duration, spec.Duration)
	}
	if p.Count("video") != 1 || p.Count("audio") != 2 || p.Count("subtitle") != 2 {
		t.Fatalf("streams: %+v", p.Streams)
	}
	langs := map[string]bool{}
	for _, s := range p.Streams {
		if s.Type == "video" && (s.Width != 1280 || s.Height != 720) {
			t.Errorf("video %dx%d", s.Width, s.Height)
		}
		if s.Type == "audio" {
			langs[s.Language] = true
			if s.Language == "eng" && !s.Default || s.Language == "spa" && (s.Default || s.Title != "Español (Latino)") {
				t.Errorf("audio track %+v", s)
			}
		}
		if s.Type == "subtitle" && s.Language == "spa" && !s.Forced {
			t.Errorf("spa subtitle should be forced: %+v", s)
		}
	}
	if !langs["eng"] || !langs["spa"] {
		t.Errorf("audio languages %v", langs)
	}

	t.Run("pal", func(t *testing.T) {
		dst := filepath.Join(dir, "pal.mkv")
		if err := PALSpeedup(ctx, base, dst); err != nil {
			t.Fatal(err)
		}
		q, err := Probe(ctx, dst)
		if err != nil {
			t.Fatal(err)
		}
		ratio := p.Duration.Seconds() / q.Duration.Seconds()
		if math.Abs(ratio-1.0427) > 0.002 {
			t.Errorf("PAL ratio %.4f, want 1.0427 ± 0.002 (%v → %v)", ratio, p.Duration, q.Duration)
		}
		if q.Count("audio") != 1 {
			t.Errorf("PAL copy keeps the first audio track only: %+v", q.Streams)
		}
	})

	t.Run("shift", func(t *testing.T) {
		dst := filepath.Join(dir, "shifted.mkv")
		if err := ShiftAudio(ctx, base, dst, 800*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		start, err := output(ctx, "ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=start_time", "-of", "csv=p=0", dst)
		if err != nil {
			t.Fatal(err)
		}
		if s := string(start); s[:3] != "0.8" {
			t.Errorf("audio starts at %q, want 0.8", s)
		}
	})

	t.Run("cut", func(t *testing.T) {
		dst := filepath.Join(dir, "cut.mkv")
		if err := Trim(ctx, base, dst, 5*time.Second, 25*time.Second); err != nil {
			t.Fatal(err)
		}
		q, err := Probe(ctx, dst)
		if err != nil {
			t.Fatal(err)
		}
		if d := q.Duration - 20*time.Second; d < -200*time.Millisecond || d > 200*time.Millisecond {
			t.Errorf("cut duration %v, want 20s", q.Duration)
		}
	})

	t.Run("broken", func(t *testing.T) {
		dst := filepath.Join(dir, "broken.mkv")
		if err := Broken(base, dst); err != nil {
			t.Fatal(err)
		}
		q, err := Probe(ctx, dst)
		if err == nil && q.Duration > 0 && q.Count("video") > 0 {
			t.Errorf("broken file still probes as video: %+v", q)
		}
	})
}

func TestSeasonZip(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	path, err := SeasonZip(context.Background(), dir, "Probe Show", 1, 3, Video{Duration: 3 * time.Second, Audio: []Audio{{Language: "eng"}}})
	if err != nil {
		t.Fatal(err)
	}
	names := zipNames(t, path)
	want := []string{"Season 01/Probe Show - S01E01.mkv", "Season 01/Probe Show - S01E02.mkv", "Season 01/Probe Show - S01E03.mkv", "Season 01/Probe Show - S01E01.en.srt"}
	if len(names) != len(want) {
		t.Fatalf("entries %v", names)
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("entry %d is %q, want %q", i, names[i], n)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
