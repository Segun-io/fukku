package preview_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/preview"
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

func opts() preview.Options {
	return preview.Options{Timeout: time.Minute, Height: 240, VideoKbps: 800}
}

func TestPlan(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration int64
		n        int
		length   time.Duration
		want     []preview.Clip
	}{
		{"start, middle, end", 5_400_000, 3, 20 * time.Second, []preview.Clip{{0, 20_000}, {2_690_000, 20_000}, {5_380_000, 20_000}}},
		{"one clip is the middle", 60_000, 1, 20 * time.Second, []preview.Clip{{20_000, 20_000}}},
		{"two clips: start and end", 60_000, 2, 20 * time.Second, []preview.Clip{{0, 20_000}, {40_000, 20_000}}},
		{"a film shorter than a clip", 12_000, 3, 20 * time.Second, []preview.Clip{{0, 12_000}}},
		{"a film as long as a clip", 20_000, 3, 20 * time.Second, []preview.Clip{{0, 20_000}}},
		{"barely longer: no clip twice", 20_001, 3, 20 * time.Second, []preview.Clip{{0, 20_000}, {1, 20_000}}},
		{"no duration", 0, 3, 20 * time.Second, nil},
		{"no clips", 60_000, 0, 20 * time.Second, nil},
	} {
		if got := preview.Plan(tc.duration, tc.n, tc.length); !slices.Equal(got, tc.want) {
			t.Errorf("%s: Plan = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func goodArgs() preview.Args {
	return preview.Args{
		Request: "0123456789abcdef", Library: "/data/media/movies/Film (2001)/Film.mkv", Upload: "files/Film.es.mkv",
		AudioIndex: 1, Clips: []preview.Clip{{StartMs: 0, LengthMs: 20_000}},
	}
}

func TestCheck(t *testing.T) {
	if err := preview.Check(goodArgs()); err != nil {
		t.Fatalf("good args: %v", err)
	}
	for name, change := range map[string]func(*preview.Args){
		"request":             func(a *preview.Args) { a.Request = "../../etc" },
		"upload outside":      func(a *preview.Args) { a.Upload = "files/../../other/x.mkv" },
		"upload not in files": func(a *preview.Args) { a.Upload = "archives/x.mkv" },
		"upload absolute":     func(a *preview.Args) { a.Upload = "/data/intake/jobs/x/files/x.mkv" },
		"upload files/ only":  func(a *preview.Args) { a.Upload = "files/" },
		"upload backslash":    func(a *preview.Args) { a.Upload = `files/..\x.mkv` },
		"negative track":      func(a *preview.Args) { a.AudioIndex = -1 },
		"offset too far":      func(a *preview.Args) { a.OffsetMs = preview.MaxOffsetMs + 1 },
		"offset too early":    func(a *preview.Args) { a.OffsetMs = -preview.MaxOffsetMs - 1 },
		"no clips":            func(a *preview.Args) { a.Clips = nil },
		"too many clips":      func(a *preview.Args) { a.Clips = make([]preview.Clip, preview.MaxClips+1) },
		"clip too long":       func(a *preview.Args) { a.Clips[0].LengthMs = preview.MaxClipLength.Milliseconds() + 1 },
		"clip empty":          func(a *preview.Args) { a.Clips[0].LengthMs = 0 },
		"clip before start":   func(a *preview.Args) { a.Clips[0].StartMs = -1 },
	} {
		a := goodArgs()
		change(&a)
		if preview.Check(a) == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := preview.Run(context.Background(), nil, nil, a, opts()); !errors.Is(err, preview.ErrBadArgs) {
			t.Errorf("%s: Run error %v, want ErrBadArgs", name, err)
		}
	}
}

// The audio is read from start − offset; what lies before the upload's start is silence.
func TestCommandOffsets(t *testing.T) {
	for _, tc := range []struct {
		start, offset          int64
		videoSS, audioSS, pads string
	}{
		{0, 0, "0.000", "0.000", "delays=0:"},
		{0, -800, "0.000", "0.800", "delays=0:"},
		{0, 800, "0.000", "0.000", "delays=800:"},
		{60_000, 800, "60.000", "59.200", "delays=0:"},
		{60_000, -1500, "60.000", "61.500", "delays=0:"},
		{500, 1200, "0.500", "0.000", "delays=700:"},
	} {
		a := goodArgs()
		a.OffsetMs = tc.offset
		args := preview.Command(a, preview.Clip{StartMs: tc.start, LengthMs: 20_000}, opts(), media.FormatWhitelist)
		var ss []string
		for i, v := range args {
			if v == "-ss" {
				ss = append(ss, args[i+1])
			}
		}
		joined := strings.Join(args, " ")
		if !slices.Equal(ss, []string{tc.videoSS, tc.audioSS}) || !strings.Contains(joined, tc.pads) {
			t.Errorf("start %d offset %d: -ss %v, filter %q; want %s %s and %s", tc.start, tc.offset, ss, joined, tc.videoSS, tc.audioSS, tc.pads)
		}
		for _, want := range []string{"[1:1]", "-protocol_whitelist fd", "-format_whitelist " + media.FormatWhitelist, "-bf 0", "-t 20.000", "min(240,"} {
			if !strings.Contains(joined, want) {
				t.Errorf("command lacks %q: %s", want, joined)
			}
		}
	}
}

// fixture is a library root with a film and a job directory whose upload is the film with
// its audio shifted by shift.
type fixture struct {
	lib, film string
	job       *os.Root
	jobDir    string
}

func newFixture(t *testing.T, length time.Duration, shift time.Duration) fixture {
	t.Helper()
	ctx := context.Background()
	f := fixture{lib: t.TempDir(), jobDir: t.TempDir()}
	if err := os.Mkdir(filepath.Join(f.lib, "Film (2001)"), 0o750); err != nil {
		t.Fatal(err)
	}
	f.film = filepath.Join(f.lib, "Film (2001)", "Film (2001) WEBDL-1080p.mkv")
	spec := fixtures.Video{Duration: length, Audio: []fixtures.Audio{{Language: "eng", Default: true}}}
	if err := fixtures.Generate(ctx, f.film, spec); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.jobDir, "files"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := fixtures.ShiftAudio(ctx, f.film, filepath.Join(f.jobDir, "files", "Film.es.mkv"), shift); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(f.jobDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	f.job = root
	return f
}

func (f fixture) args(request string, offset int64, clips ...preview.Clip) preview.Args {
	return preview.Args{Request: request, Library: f.film, Upload: "files/Film.es.mkv", AudioIndex: 1, OffsetMs: offset, Clips: clips}
}

func (f fixture) clipPath(request string, n int) string {
	return filepath.Join(f.jobDir, filepath.FromSlash(preview.Dir), preview.ClipName(request, n))
}

// The acceptance test of #53: with the matching audio the clips show the flash and the
// beep together; with a copy whose audio is 800 ms late they do not, and an offset of
// −800 ms brings them together. Judged by analysing the rendered clips.
func TestRunSync(t *testing.T) {
	needTools(t)
	ctx := context.Background()
	clips := preview.Plan(12_000, 3, 3*time.Second)
	for _, tc := range []struct {
		name    string
		shift   time.Duration
		offset  int64
		inSync  bool
		request string
	}{
		{"matching", 0, 0, true, "000000000000000a"},
		{"800 ms late", 800 * time.Millisecond, 0, false, "000000000000000b"},
		{"800 ms late, offset −800", 800 * time.Millisecond, -800, true, "000000000000000c"},
		{"300 ms late, offset −300", 300 * time.Millisecond, -300, true, "000000000000000d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, 12*time.Second, tc.shift)
			out, err := preview.Run(ctx, f.job, []string{f.lib}, f.args(tc.request, tc.offset, clips...), opts())
			if err != nil {
				t.Fatal(err)
			}
			if !out.OK || out.Request != tc.request || len(out.Clips) != len(clips) {
				t.Fatalf("output %+v", out)
			}
			for i, c := range out.Clips {
				p := f.clipPath(tc.request, c.N)
				st, err := os.Stat(p)
				if err != nil || st.Size() != c.Size || c.N != i+1 || c.StartMs != clips[i].StartMs {
					t.Fatalf("clip %d: %+v, file %v %v", i+1, c, st, err)
				}
				rep, err := fixtures.SyncOffset(ctx, p)
				if err != nil {
					t.Fatalf("clip %d: %v (%+v)", c.N, err, rep)
				}
				together := rep.Offset.Abs() <= 50*time.Millisecond
				if together != tc.inSync || (!tc.inSync && rep.Offset.Abs() < 150*time.Millisecond) {
					t.Errorf("clip %d (from %d ms): audio %s against the video, want in sync: %t", c.N, c.StartMs, rep.Offset, tc.inSync)
				}
				pr, err := fixtures.Probe(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				if pr.Count("video") != 1 || pr.Count("audio") != 1 || pr.Streams[0].Height != 240 || pr.Streams[0].Codec != "h264" {
					t.Errorf("clip %d: %+v", c.N, pr)
				}
				if d := pr.Duration - 3*time.Second; d.Abs() > 100*time.Millisecond {
					t.Errorf("clip %d lasts %s", c.N, pr.Duration)
				}
			}
		})
	}
}

// A new request removes the clips of the earlier one; running a request again is safe.
func TestRunReplacesEarlierClips(t *testing.T) {
	needTools(t)
	ctx := context.Background()
	f := newFixture(t, 4*time.Second, 0)
	roots := []string{f.lib}
	clip := preview.Clip{StartMs: 0, LengthMs: 1000}
	first, second := "1111111111111111", "2222222222222222"
	for _, req := range []string{first, first, second, second} {
		if out, err := preview.Run(ctx, f.job, roots, f.args(req, 0, clip), opts()); err != nil || !out.OK {
			t.Fatalf("%s: %+v %v", req, out, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(f.jobDir, filepath.FromSlash(preview.Dir)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{preview.ClipName(second, 1)}) {
		t.Errorf("preview directory holds %v", names)
	}
}

// The library file is read only inside a library root, as the library probe reads it; the
// upload only inside the job.
func TestRunConfinement(t *testing.T) {
	needTools(t)
	ctx := context.Background()
	f := newFixture(t, 2*time.Second, 0)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.mkv")
	if err := fixtures.Generate(ctx, secret, fixtures.Video{Duration: 2 * time.Second}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(f.lib, "Film (2001)", "link.mkv")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(f.jobDir, "files", "link.mkv")); err != nil {
		t.Fatal(err)
	}
	clip := preview.Clip{StartMs: 0, LengthMs: 1000}
	for _, tc := range []struct {
		name, library, upload, refusal string
		roots                          []string
	}{
		{"outside every root", secret, "files/Film.es.mkv", preview.RefusalOutsideLibrary, []string{f.lib}},
		{"no roots", f.film, "files/Film.es.mkv", preview.RefusalOutsideLibrary, nil},
		{"climbs out", filepath.Join(f.lib, "..", filepath.Base(outside), "secret.mkv"), "files/Film.es.mkv", preview.RefusalOutsideLibrary, []string{f.lib}},
		{"a link out of the root", link, "files/Film.es.mkv", preview.RefusalLibraryUnreadable, []string{f.lib}},
		{"missing", filepath.Join(f.lib, "Film (2001)", "gone.mkv"), "files/Film.es.mkv", preview.RefusalLibraryUnreadable, []string{f.lib}},
		{"an upload linked out of the job", f.film, "files/link.mkv", preview.RefusalUploadUnreadable, []string{f.lib}},
		{"an upload that is gone", f.film, "files/gone.mkv", preview.RefusalUploadUnreadable, []string{f.lib}},
	} {
		a := f.args("3333333333333333", 0, clip)
		a.Library, a.Upload = tc.library, tc.upload
		out, err := preview.Run(ctx, f.job, tc.roots, a, opts())
		if err != nil || out.OK || out.Refusal != tc.refusal {
			t.Errorf("%s: %+v %v, want refusal %s", tc.name, out, err, tc.refusal)
		}
	}
	// A stream that is not audio cannot be the audio track.
	a := f.args("4444444444444444", 0, clip)
	a.AudioIndex = 0
	if out, err := preview.Run(ctx, f.job, []string{f.lib}, a, opts()); err != nil || out.Refusal != preview.RefusalRenderFailed || out.Detail == "" {
		t.Errorf("video stream as the audio: %+v %v", out, err)
	}
	if _, err := os.Stat(f.clipPath("4444444444444444", 1)); !os.IsNotExist(err) {
		t.Errorf("a failed clip was left: %v", err)
	}
}

// fakeFFmpeg writes a script that stands in for ffmpeg.
func fakeFFmpeg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// A hung ffmpeg, and what it started, are killed at merge.previewTimeout, and the preview
// is refused as timeout. The task's own cancel stops it too.
func TestRunHungFFmpegIsKilled(t *testing.T) {
	needTools(t)
	f := newFixture(t, 2*time.Second, 0)
	pidFile := filepath.Join(t.TempDir(), "pid")
	o := opts()
	o.FFmpeg = fakeFFmpeg(t, "sleep 60 &\necho $! > "+pidFile+"\nwait")
	o.Timeout = 300 * time.Millisecond
	start := time.Now()
	out, err := preview.Run(context.Background(), f.job, []string{f.lib}, f.args("5555555555555555", 0, preview.Clip{LengthMs: 1000}), o)
	if err != nil || out.OK || out.Refusal != preview.RefusalTimeout {
		t.Fatalf("%+v %v", out, err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %s", took)
	}
	pid, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	// The child is gone, not left running in the background. (Without an init process to
	// reap it, it may stay a zombie: that counts as gone.)
	deadline := time.Now().Add(3 * time.Second)
	for running(strings.TrimSpace(string(pid))) {
		if time.Now().After(deadline) {
			t.Fatal("the hung ffmpeg's child still runs")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(f.jobDir, filepath.FromSlash(preview.Dir))); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.jobDir, filepath.FromSlash(preview.Dir))); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	o.Timeout = time.Minute
	if _, err := preview.Run(ctx, f.job, []string{f.lib}, f.args("5555555555555555", 0, preview.Clip{LengthMs: 1000}), o); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("cancelled task: %v", err)
	}
}

// running reports whether the process is alive and not a zombie.
func running(pid string) bool {
	stat, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return false
	}
	// pid (comm) state …; comm may hold spaces, the state follows the last ")".
	rest := string(stat[strings.LastIndexByte(string(stat), ')')+1:])
	return !strings.HasPrefix(strings.TrimSpace(rest), "Z")
}

// An ffmpeg that writes far more than the bit rates allow is stopped (too_large), and one
// that cannot be run is an error, not a verdict on the files.
func TestRunLimits(t *testing.T) {
	needTools(t)
	f := newFixture(t, 2*time.Second, 0)
	o := opts()
	o.VideoKbps = 100
	o.FFmpeg = fakeFFmpeg(t, "exec head -c 50000000 /dev/zero")
	out, err := preview.Run(context.Background(), f.job, []string{f.lib}, f.args("6666666666666666", 0, preview.Clip{LengthMs: 1000}), o)
	if err != nil || out.Refusal != preview.RefusalTooLarge {
		t.Errorf("too large: %+v %v", out, err)
	}
	o.FFmpeg = fakeFFmpeg(t, "exit 0")
	out, err = preview.Run(context.Background(), f.job, []string{f.lib}, f.args("6666666666666666", 0, preview.Clip{LengthMs: 1000}), o)
	if err != nil || out.Refusal != preview.RefusalRenderFailed {
		t.Errorf("nothing written: %+v %v", out, err)
	}
	o.FFmpeg = filepath.Join(t.TempDir(), "no-such-ffmpeg")
	if _, err := preview.Run(context.Background(), f.job, []string{f.lib}, f.args("6666666666666666", 0, preview.Clip{LengthMs: 1000}), o); err == nil {
		t.Error("a missing ffmpeg is not an error")
	}
}
