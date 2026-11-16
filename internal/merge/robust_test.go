package merge_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/merge"
	"github.com/segun-io/fukku/test/fixtures"
)

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, out)
	}
}

// Library files in MP4, AVI and MPEG-TS merge too: their untagged tracks are "und" in the
// output, and their default flags come from ffprobe, the original audio still the default.
func TestMergeOtherLibraryContainers(t *testing.T) {
	noSubs := fixtures.Video{Duration: 6 * time.Second, Audio: []fixtures.Audio{{Language: "eng", Default: true}}}
	for _, tc := range []struct {
		name string
		spec fixtures.Video
		want string
	}{
		// AVI has no language tags: its audio is "und" before and after.
		{"Film (2001) WEBDL-1080p.mp4", filmSpec, "video:und::default audio:en::default subtitles:en: audio:es-419:Español (Latino)"},
		{"Film (2001) DVD.avi", noSubs, "video:und::default audio:und::default audio:es-419:Español (Latino)"},
		{"Film (2001) HDTV-1080p.ts", noSubs, "video:und::default audio:en::default audio:es-419:Español (Latino)"},
	} {
		t.Run(filepath.Ext(tc.name), func(t *testing.T) {
			e := newEnvLib(t, tc.name, func(p string) error { return fixtures.Generate(context.Background(), p, tc.spec) }, nil)
			out := e.run(t, e.args(1), options())
			e.check(t, out)
			if !out.OK {
				t.Fatalf("output %+v", out)
			}
			want := dest + "/" + strings.TrimSuffix(tc.name, filepath.Ext(tc.name)) + ".mkv"
			if out.Path != want {
				t.Errorf("path %q, want %q", out.Path, want)
			}
			// The flags come from ffprobe's dispositions, and where a type has none (AVI and
			// TS have none at all), its first track is the default; subtitles never are.
			if got := describe(identify(t, filepath.Join(e.jobDir, out.Path))); got != tc.want {
				t.Errorf("tracks\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

// An empty track name clears the upload's own track names instead of keeping them.
func TestMergeEmptyTrackName(t *testing.T) {
	titled := dubSpec
	titled.Audio = []fixtures.Audio{{Language: "spa", ToneHz: 440, Title: "Upload title"}}
	e := newEnv(t, func(p string) error { return fixtures.Generate(context.Background(), p, titled) })
	opt := options()
	opt.TrackName = ""
	out := e.run(t, e.args(1), opt)
	e.check(t, out)
	if !out.OK || out.TrackName != "" {
		t.Fatalf("output %+v", out)
	}
	tracks := identify(t, filepath.Join(e.jobDir, finalPath))
	if name := tracks[len(tracks)-1].Properties.TrackName; name != "" {
		t.Errorf("the added track is named %q", name)
	}
}

// A dub cut short (its file truncated: the header still says the full length, so the probe
// accepts it) is caught by verification and nothing is offered.
func TestMergeTruncatedUpload(t *testing.T) {
	long := 30 * time.Second
	lib, dub := filmSpec, dubSpec
	lib.Duration, dub.Duration = long, long
	e := newEnvLib(t, filmName, func(p string) error { return fixtures.Generate(context.Background(), p, lib) },
		func(p string) error {
			if err := fixtures.Generate(context.Background(), p, dub); err != nil {
				return err
			}
			st, err := os.Stat(p)
			if err != nil {
				return err
			}
			return os.Truncate(p, st.Size()*7/10)
		})
	out := e.run(t, e.args(1), options())
	e.check(t, out)
	if out.OK || (out.Refusal != merge.RefusalVerifyFailed && out.Refusal != merge.RefusalMuxFailed) {
		t.Fatalf("output %+v", out)
	}
	t.Logf("refused: %s: %s", out.Refusal, out.Detail)
}

// A Matroska library file without statistics tags, whose audio runs on after the picture:
// its video is measured from its last packets, and the merge passes.
func TestMergeLibraryWithoutStatistics(t *testing.T) {
	e := newEnvLib(t, filmName, func(p string) error {
		src := p + ".src.mkv"
		ffmpeg(t, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24000/1001:duration=6",
			"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=6.5",
			"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac",
			"-metadata:s:a:0", "language=eng", src)
		defer func() { _ = os.Remove(src) }()
		cmd := exec.CommandContext(t.Context(), "mkvmerge", "-q", "--disable-track-statistics-tags", "--no-track-tags", "-o", p, src)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("mkvmerge: %v: %s", err, out)
		}
		return nil
	}, nil)
	out := e.run(t, e.args(1), options())
	e.check(t, out)
	if !out.OK {
		t.Fatalf("output %+v", out)
	}
	if v := out.Media.Video[0].DurationMs; v < 5950 || v > 6050 {
		t.Errorf("the output's video lasts %d ms", v)
	}
}

// While a merge into dest holds its lock, another one is refused as busy and touches
// neither the partial file nor dest. Once the lock is free, what is left there is stale
// and the next run removes it.
func TestMergeBusy(t *testing.T) {
	e := newEnv(t, nil)
	partial, lockName := merge.PartialFor(dest)
	for _, d := range []string{"work", dest} {
		if err := os.MkdirAll(filepath.Join(e.jobDir, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(e.jobDir, dest, "other.mkv")
	for _, p := range []string{filepath.Join(e.jobDir, partial), other} {
		if err := os.WriteFile(p, []byte("the other merge's"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	held, err := os.OpenFile(filepath.Join(e.jobDir, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	out := e.run(t, e.args(1), options())
	if out.OK || out.Refusal != merge.RefusalBusy {
		t.Errorf("output %+v", out)
	}
	for _, p := range []string{filepath.Join(e.jobDir, partial), other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("the other merge's file: %v", err)
		}
	}
	_ = held.Close()
	out = e.run(t, e.args(1), options())
	e.check(t, out)
	if !out.OK {
		t.Fatalf("after the lock: %+v", out)
	}
	if _, err := os.Stat(other); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale dest content: %v", err)
	}
}

// A rename into dest that cannot be made durable is undone: the run fails and leaves no
// output, so the result never names a file that may not be there.
func TestMergeSyncFailureUndone(t *testing.T) {
	e := newEnv(t, nil)
	restore := merge.SetSyncDir(func(*os.Root, string) error { return errors.New("disk on fire") })
	defer restore()
	_, err := merge.Run(context.Background(), e.job, []string{e.libRoot}, e.args(1), options())
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.jobDir, finalPath)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the output is left: %v", err)
	}
	e.check(t, merge.Output{OK: true}) // dest itself exists, empty
	entries, _ := os.ReadDir(filepath.Join(e.jobDir, dest))
	if len(entries) != 0 {
		t.Errorf("dest holds %v", entries)
	}
}
