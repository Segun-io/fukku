package media_test

import (
	"io"
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
	ms, ok, err := media.VideoEndMs(t.Context(), f, pf.Media.Video[0].Index, pf.Media.DurationMs-30000, nil, opts())
	if err != nil || !ok || ms < 5990 || ms > 6020 {
		t.Errorf("VideoEndMs = %d %t %v, want about 6006", ms, ok, err)
	}
	// From the last two seconds only, the same.
	if ms2, ok, _ := media.VideoEndMs(t.Context(), f, pf.Media.Video[0].Index, pf.Media.DurationMs-2000, nil, opts()); !ok || ms2 != ms {
		t.Errorf("VideoEndMs near the end = %d %t, want %d", ms2, ok, ms)
	}
	// Run again on the same descriptor (whose offset ffprobe moved): the same.
	if ms3, ok, _ := media.VideoEndMs(t.Context(), f, pf.Media.Video[0].Index, 0, nil, opts()); !ok || ms3 != ms {
		t.Errorf("VideoEndMs again = %d %t, want %d", ms3, ok, ms)
	}
	// ffprobe shares the descriptor's file position; it is left at the start.
	if _, err := f.Seek(1234, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := media.ProbeOpen(t.Context(), f, "film.mkv", nil, opts()); err != nil {
		t.Fatal(err)
	}
	if pos, err := f.Seek(0, io.SeekCurrent); err != nil || pos != 0 {
		t.Errorf("file position after a probe: %d %v", pos, err)
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

// VideoEndMs measures the stream it is given by ffprobe index, not the first video
// stream: here a 3 s and a 6 s video stream.
func TestVideoEndByIndex(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	film := filepath.Join(dir, "two.mkv")
	args := []string{"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=25:duration=3",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=25:duration=6",
		"-map", "0:v", "-map", "1:v", "-c:v", "libx264", "-preset", "ultrafast", film}
	if out, err := exec.CommandContext(t.Context(), "ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, out)
	}
	f, err := os.Open(film)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for stream, want := range map[int]int64{0: 3000, 1: 6000} {
		ms, ok, err := media.VideoEndMs(t.Context(), f, stream, 0, nil, opts())
		if err != nil || !ok || ms < want-50 || ms > want+50 {
			t.Errorf("stream %d: %d %t %v, want about %d", stream, ms, ok, err, want)
		}
	}
	if _, ok, err := media.VideoEndMs(t.Context(), f, -1, 0, nil, opts()); ok || err != nil {
		t.Errorf("no stream: %t %v", ok, err)
	}
}
