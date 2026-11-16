package media_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/segun-io/fukku/internal/media"
)

// A Matroska file without statistics tags states no video duration, and its container
// lasts as long as its audio, which runs on after the picture. VideoEndMs measures the
// picture from its last packets, through the open file.
func TestProbeOpenAndVideoEnd(t *testing.T) {
	needTools(t)
	if _, err := exec.LookPath("mkvmerge"); err != nil {
		t.Skip("mkvmerge not installed (run the tests with make test)")
	}
	dir := t.TempDir()
	src, film := filepath.Join(dir, "src.mkv"), filepath.Join(dir, "film.mkv")
	for _, args := range [][]string{
		{"ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24000/1001:duration=6",
			"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=6.5", "-map", "0:v", "-map", "1:a",
			"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", src},
		{"mkvmerge", "-q", "--disable-track-statistics-tags", "--no-track-tags", "-o", film, src},
	} {
		if out, err := exec.CommandContext(t.Context(), args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", args[0], err, out)
		}
	}
	f, err := os.Open(film)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	pf, err := media.ProbeOpen(t.Context(), f, "film.mkv", nil, opts())
	if err != nil || !pf.OK || pf.Path != "film.mkv" {
		t.Fatalf("ProbeOpen: %+v %v", pf, err)
	}
	if v := pf.Media.Video[0].DurationMs; v != 0 || pf.Media.DurationMs < 6400 {
		t.Fatalf("video %d ms, container %d ms: the fixture should state no video duration", v, pf.Media.DurationMs)
	}
	ms, ok, err := media.VideoEndMs(t.Context(), f, pf.Media.DurationMs-30000, nil, opts())
	if err != nil || !ok || ms < 5990 || ms > 6020 {
		t.Errorf("VideoEndMs = %d %t %v, want about 6006", ms, ok, err)
	}
	// From the last two seconds only, the same.
	if ms2, ok, _ := media.VideoEndMs(t.Context(), f, pf.Media.DurationMs-2000, nil, opts()); !ok || ms2 != ms {
		t.Errorf("VideoEndMs near the end = %d %t, want %d", ms2, ok, ms)
	}
	// Run again on the same descriptor (whose offset ffprobe moved): the same.
	if ms3, ok, _ := media.VideoEndMs(t.Context(), f, 0, nil, opts()); !ok || ms3 != ms {
		t.Errorf("VideoEndMs again = %d %t, want %d", ms3, ok, ms)
	}
	// A file that is not a video is refused, not an error.
	txt := filepath.Join(dir, "notes.mkv")
	if err := os.WriteFile(txt, []byte("not a film"), 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := os.Open(txt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()
	if pf, err := media.ProbeOpen(t.Context(), g, "notes.mkv", nil, opts()); err != nil || pf.OK {
		t.Errorf("not a film: %+v %v", pf, err)
	}
}
