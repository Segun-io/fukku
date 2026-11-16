package media_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/test/fixtures"
)

func needTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed (run the tests with make test)", tool)
		}
	}
}

func opts() media.Options {
	return media.Options{
		Timeout:     30 * time.Second,
		VideoExt:    []string{".mkv", ".mp4", ".avi"},
		SubtitleExt: []string{".srt", ".ass", ".ssa"},
	}
}

func openRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func probeOne(t *testing.T, dir, name string, o media.Options) media.ProbedFile {
	t.Helper()
	out, err := media.Run(context.Background(), openRoot(t, dir), media.ProbeArgs{Paths: []string{name}}, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 1 || out.Files[0].Path != name {
		t.Fatalf("output: %+v", out)
	}
	return out.Files[0]
}

func TestProbeFixtures(t *testing.T) {
	needTools(t)
	ctx := context.Background()
	dir := t.TempDir()
	film := filepath.Join(dir, "film.mkv")
	spec := fixtures.Video{
		Duration: 20 * time.Second, Width: 640, Height: 360,
		Audio: []fixtures.Audio{
			{Language: "eng", Title: "English", Default: true},
			{Language: "spa", Title: "Español (Latino)", ToneHz: 440},
		},
		Subtitles: []fixtures.Subtitle{{Language: "eng"}, {Language: "spa", Forced: true}},
	}
	if err := fixtures.Generate(ctx, film, spec); err != nil {
		t.Fatal(err)
	}
	if err := fixtures.PALSpeedup(ctx, film, filepath.Join(dir, "pal.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := fixtures.ShiftAudio(ctx, film, filepath.Join(dir, "shifted.mkv"), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := fixtures.Trim(ctx, film, filepath.Join(dir, "trim.mkv"), 2*time.Second, 12*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := fixtures.Broken(film, filepath.Join(dir, "broken.mkv")); err != nil {
		t.Fatal(err)
	}

	// A normal film with two audio languages and titles.
	f := probeOne(t, dir, "film.mkv", opts())
	if !f.OK || f.Kind != "video" || f.Media == nil {
		t.Fatalf("film: %+v", f)
	}
	m := f.Media
	st, _ := os.Stat(film)
	if m.Size != st.Size() || !strings.Contains(m.Container, "matroska") {
		t.Errorf("size/container: %+v", m)
	}
	if math.Abs(float64(m.DurationMs)-20000) > 200 {
		t.Errorf("duration %d", m.DurationMs)
	}
	if len(m.Video) != 1 || m.Video[0].Codec != "h264" || m.Video[0].Width != 640 || m.Video[0].Height != 360 ||
		m.Video[0].FrameRate != "24000/1001" {
		t.Errorf("video: %+v", m.Video)
	}
	if len(m.Audio) != 2 || m.Audio[0].Language != "eng" || m.Audio[0].Title != "English" || !m.Audio[0].Default ||
		m.Audio[1].Language != "spa" || m.Audio[1].Title != "Español (Latino)" || m.Audio[1].Default ||
		m.Audio[0].Channels != 1 || m.Audio[0].Codec != "aac" {
		t.Errorf("audio: %+v", m.Audio)
	}
	if len(m.Subtitles) != 2 || m.Subtitles[0].Language != "eng" || m.Subtitles[0].Forced ||
		m.Subtitles[1].Language != "spa" || !m.Subtitles[1].Forced || m.Subtitles[0].Codec != "subrip" {
		t.Errorf("subtitles: %+v", m.Subtitles)
	}

	// PAL copy: about 4.27% shorter than the 23.976 original (ratio 1.0427).
	pal := probeOne(t, dir, "pal.mkv", opts())
	if !pal.OK {
		t.Fatalf("pal: %+v", pal)
	}
	if r := float64(m.DurationMs) / float64(pal.Media.DurationMs); math.Abs(r-1.0427) > 0.01 {
		t.Errorf("PAL duration ratio %.4f", r)
	}
	if pal.Media.Video[0].FrameRate != "25/1" {
		t.Errorf("PAL frame rate %q", pal.Media.Video[0].FrameRate)
	}

	shifted := probeOne(t, dir, "shifted.mkv", opts())
	if !shifted.OK || len(shifted.Media.Audio) != 2 {
		t.Errorf("shifted: %+v", shifted)
	}

	trim := probeOne(t, dir, "trim.mkv", opts())
	if !trim.OK || trim.Media.DurationMs > 15000 || trim.Media.DurationMs < 8000 { // a different cut: clearly shorter than 20 s
		t.Errorf("trim: %+v %+v", trim, trim.Media)
	}

	broken := probeOne(t, dir, "broken.mkv", opts())
	if broken.OK || broken.Media != nil || broken.Refusal == "" {
		t.Errorf("broken accepted: %+v", broken)
	}
}

func TestProbeNiceAndOrder(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	if err := fixtures.Generate(context.Background(), filepath.Join(dir, "a.mp4"), fixtures.Video{Duration: 3 * time.Second}); err != nil {
		t.Fatal(err)
	}
	o := opts()
	o.Nice = 10
	out, err := media.Run(context.Background(), openRoot(t, dir),
		media.ProbeArgs{Paths: []string{"a.mp4", "missing.mkv", "notes.txt"}}, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 3 || !out.Files[0].OK || out.Files[1].Refusal != "unreadable" || out.Files[2].Refusal != "extension" {
		t.Errorf("%+v", out.Files)
	}
}

func TestSubtitles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ok.srt", []byte(fixtures.SRT(12*time.Second, "eng")))
	write("latin1.ass", []byte("Dialogue: caf\xe9\n")) // not UTF-8 is fine
	write("bin.srt", []byte("1\n00:00:00,000 --> 00:00:01,000\nhi\x00\x01\n"))
	write("big.srt", bytes.Repeat([]byte("a"), 16<<20+1))
	write("limit.ssa", bytes.Repeat([]byte("a"), 16<<20))
	write("fake.mkv", []byte("this is just text, not a video\n"))

	for name, want := range map[string]string{
		"ok.srt": "", "latin1.ass": "", "limit.ssa": "", "bin.srt": "not_text", "big.srt": "not_text",
	} {
		f := probeOne(t, dir, name, opts())
		if f.Kind != "subtitle" || f.OK != (want == "") || f.Refusal != want {
			t.Errorf("%s: %+v", name, f)
		}
	}
	if needToolsQuiet() {
		f := probeOne(t, dir, "fake.mkv", opts())
		if f.OK || f.Refusal == "" {
			t.Errorf("text named .mkv accepted: %+v", f)
		}
	}
}

func needToolsQuiet() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}

func TestPlaylistIsRefused(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	if err := fixtures.Generate(context.Background(), filepath.Join(dir, "other.mp4"), fixtures.Video{Duration: 3 * time.Second}); err != nil {
		t.Fatal(err)
	}
	script := "ffconcat version 1.0\nfile '" + filepath.Join(dir, "other.mp4") + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "list.mkv"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	hls := "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3,\nfile:" + filepath.Join(dir, "other.mp4") + "\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(filepath.Join(dir, "hls.mkv"), []byte(hls), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list.mkv", "hls.mkv"} {
		f := probeOne(t, dir, name, opts())
		if f.OK || f.Media != nil || f.Refusal == "" {
			t.Errorf("%s accepted: %+v", name, f)
		}
	}
}

func TestSymlinkOutsideJobIsUnreadable(t *testing.T) {
	needTools(t)
	outside, job := t.TempDir(), t.TempDir()
	if err := fixtures.Generate(context.Background(), filepath.Join(outside, "secret.mkv"), fixtures.Video{Duration: 3 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.srt"), []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"secret.mkv", "secret.srt"} {
		if err := os.Symlink(filepath.Join(outside, n), filepath.Join(job, "link-"+n)); err != nil {
			t.Fatal(err)
		}
		f := probeOne(t, job, "link-"+n, opts())
		if f.OK || f.Refusal != "unreadable" {
			t.Errorf("%s: %+v", n, f)
		}
	}
	if err := os.Mkdir(filepath.Join(job, "dir.mkv"), 0o700); err != nil {
		t.Fatal(err)
	}
	if f := probeOne(t, job, "dir.mkv", opts()); f.Refusal != "unreadable" {
		t.Errorf("directory: %+v", f)
	}
	if f := probeOne(t, job, "../secret.mkv", opts()); f.Refusal != "unreadable" {
		t.Errorf("dotdot: %+v", f)
	}
}

// A fake ffprobe hangs on files that start with HANG, and otherwise runs the real one.
func TestHangingFFprobeIsKilled(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	fake := filepath.Join(t.TempDir(), "ffprobe")
	script := `#!/bin/sh
if [ "$(head -c 4 /proc/self/fd/3)" = HANG ]; then
  sleep 300 &
  echo $! > "` + pidFile + `"
  wait
fi
exec ffprobe "$@"
`
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hang.mkv"), []byte("HANG"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fixtures.Generate(context.Background(), filepath.Join(dir, "ok.mkv"), fixtures.Video{Duration: 3 * time.Second}); err != nil {
		t.Fatal(err)
	}
	o := opts()
	o.FFprobe = fake
	o.Timeout = 1 * time.Second
	o.Nice = 5

	start := time.Now()
	out, err := media.Run(context.Background(), openRoot(t, dir), media.ProbeArgs{Paths: []string{"hang.mkv", "ok.mkv"}}, o)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
	if out.Files[0].Refusal != "timeout" || out.Files[0].OK {
		t.Errorf("hang: %+v", out.Files[0])
	}
	if !out.Files[1].OK {
		t.Errorf("next file not probed: %+v", out.Files[1])
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil && !zombie(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil && !zombie(pid) {
		t.Errorf("child process %d is still running", pid)
	}
}

func zombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return err != nil || strings.Contains(string(b), ") Z ")
}

func TestOutputCap(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nhead -c 20000000 /dev/zero | tr '\\0' 'a'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := opts()
	o.FFprobe = fake
	f := probeOne(t, dir, "x.mkv", o)
	if f.OK || f.Refusal != "no_video_stream" {
		t.Errorf("%+v", f)
	}
}

func TestMissingFFprobeIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, nice := range []int{0, 10} { // 10 is the default worker.nice
		o := opts()
		o.FFprobe = "/nonexistent/ffprobe"
		o.Nice = nice
		out, err := media.Run(context.Background(), openRoot(t, dir), media.ProbeArgs{Paths: []string{"x.mkv"}}, o)
		if err == nil {
			t.Fatalf("nice %d: want an error when ffprobe cannot run, got %+v", nice, out)
		}
	}
	// A binary that exists but exits 127 (as nice does for a missing command) is an error too.
	fake := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 127\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	o := opts()
	o.FFprobe = fake
	if _, err := media.Run(context.Background(), openRoot(t, dir), media.ProbeArgs{Paths: []string{"x.mkv"}}, o); err == nil {
		t.Fatal("exit 127: want an error")
	}
}

func TestOptionsFrom(t *testing.T) {
	cfg := config.Defaults()
	o := media.OptionsFrom(cfg)
	if o.Timeout != time.Duration(cfg.Worker.ProbeTimeout) || o.Nice != cfg.Worker.Nice ||
		len(o.VideoExt) == 0 || len(o.SubtitleExt) == 0 {
		t.Errorf("%+v", o)
	}
}

func TestExtensionRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.exe"), []byte("MZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := probeOne(t, dir, "a.exe", opts())
	if f.OK || f.Refusal != "extension" || f.Kind != "" {
		t.Errorf("%+v", f)
	}
}

func TestProbeJSONGolden(t *testing.T) {
	const golden = `{"container":"matroska,webm","durationMs":5400000,"size":4294967296,
 "video":[{"index":0,"codec":"h264","width":1920,"height":1080,"frameRate":"24000/1001","default":true}],
 "audio":[{"index":1,"codec":"aac","language":"eng","channels":2,"default":true},
          {"index":2,"codec":"aac","language":"spa","title":"Español (Latino)","channels":2,"default":false}],
 "subtitles":[{"index":3,"codec":"subrip","language":"eng","default":false,"forced":false,"hearingImpaired":false}]}`
	p := media.Probe{
		Container: "matroska,webm", DurationMs: 5400000, Size: 4294967296,
		Video: []media.VideoTrack{{Index: 0, Codec: "h264", Width: 1920, Height: 1080, FrameRate: "24000/1001", Default: true}},
		Audio: []media.AudioTrack{
			{Index: 1, Codec: "aac", Language: "eng", Channels: 2, Default: true},
			{Index: 2, Codec: "aac", Language: "spa", Title: "Español (Latino)", Channels: 2},
		},
		Subtitles: []media.SubtitleTrack{{Index: 3, Codec: "subrip", Language: "eng"}},
	}
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := json.Compact(&want, []byte(golden)); err != nil {
		t.Fatal(err)
	}
	if string(got) != want.String() {
		t.Errorf("got  %s\nwant %s", got, want.String())
	}

	// Task shapes.
	b, _ := json.Marshal(media.ProbeOutput{Files: []media.ProbedFile{
		{Path: "files/a.mkv", Kind: "video", OK: true, Media: &p},
		{Path: "files/b.txt", Kind: "", Refusal: "extension"},
	}})
	if !strings.Contains(string(b), `"files":[{"path":"files/a.mkv","kind":"video","ok":true,"media":{`) ||
		!strings.Contains(string(b), `{"path":"files/b.txt","kind":"","ok":false,"refusal":"extension"}`) {
		t.Errorf("%s", b)
	}
	b, _ = json.Marshal(media.ProbeArgs{Paths: []string{"a"}})
	if string(b) != `{"paths":["a"]}` {
		t.Errorf("%s", b)
	}
}

func TestFIFOIsUnreadableWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"pipe.mkv", "pipe.srt"} {
		if err := syscall.Mkfifo(filepath.Join(dir, n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, n := range []string{"pipe.mkv", "pipe.srt"} {
			if f := probeOne(t, dir, n, opts()); f.OK || f.Refusal != "unreadable" {
				t.Errorf("%s: %+v", n, f)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run blocked on a FIFO")
	}
}

// Only the format whitelist stops the concat demuxer from parsing a script renamed to .mkv;
// ffprobe's own stderr says so. The script refers to a real video next to it.
func TestFormatWhitelistStopsConcat(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	if err := fixtures.Generate(context.Background(), filepath.Join(dir, "other.mp4"), fixtures.Video{Duration: 3 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "list.mkv"), []byte("ffconcat version 1.0\nfile other.mp4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(t.TempDir(), "stderr.log")
	fake := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec ffprobe \"$@\" 2>>\""+logFile+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	o := opts()
	o.FFprobe = fake
	f := probeOne(t, dir, "list.mkv", o)
	if f.OK {
		t.Fatalf("accepted: %+v", f)
	}
	b, _ := os.ReadFile(logFile)
	if !strings.Contains(string(b), "not on whitelist") || strings.Contains(string(b), "other.mp4") {
		t.Errorf("ffprobe stderr: %s", b)
	}
}
