package merge_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/merge"
	"github.com/segun-io/fukku/test/fixtures"
)

const (
	filmName   = "Film (2001) WEBDL-1080p.mkv"
	uploadPath = "files/Film.es.mkv"
	dest       = "merged/1"
	finalPath  = dest + "/" + filmName
)

func needTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe", "mkvmerge"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed (run the tests with make test)", tool)
		}
	}
}

func options() merge.Options {
	return merge.Options{
		Probe: media.Options{
			Timeout:     30 * time.Second,
			Nice:        10,
			VideoExt:    []string{".mkv", ".mp4"},
			SubtitleExt: []string{".srt"},
		},
		IdentifyTimeout:     30 * time.Second,
		Nice:                10,
		LanguageTag:         "es-419",
		TrackName:           "Español (Latino)",
		KeepOriginalDefault: true,
		CoverageTolerance:   500 * time.Millisecond, // match.runtimeTolerance
	}
}

// env is a library root with one film, read-only, and a job directory with a dub of it.
type env struct {
	libRoot, library string
	jobDir           string
	job              *os.Root
	before           fileState
}

type fileState struct {
	hash string
	mod  time.Time
	size int64
}

// The film: 6 s, English audio (default) and English subtitles. The dub: the same cut,
// Spanish audio at another pitch and Spanish subtitles; ffprobe indexes video 0, audio 1,
// subtitles 2.
var (
	filmSpec = fixtures.Video{Duration: 6 * time.Second,
		Audio:     []fixtures.Audio{{Language: "eng", Default: true}},
		Subtitles: []fixtures.Subtitle{{Language: "eng"}}}
	dubSpec = fixtures.Video{Duration: 6 * time.Second,
		Audio:     []fixtures.Audio{{Language: "spa", ToneHz: 440}},
		Subtitles: []fixtures.Subtitle{{Language: "spa"}}}
)

func newEnv(t *testing.T, upload func(path string) error) *env {
	t.Helper()
	return newEnvLib(t, filmName, nil, upload)
}

// newEnvLib is newEnv with a library file of another name (and so container), written by
// library (nil: filmSpec).
func newEnvLib(t *testing.T, name string, library, upload func(path string) error) *env {
	t.Helper()
	needTools(t)
	ctx := context.Background()
	e := &env{libRoot: t.TempDir(), jobDir: t.TempDir()}
	folder := filepath.Join(e.libRoot, "Film (2001)")
	if err := os.Mkdir(folder, 0o750); err != nil {
		t.Fatal(err)
	}
	e.library = filepath.Join(folder, name)
	if library == nil {
		library = func(p string) error { return fixtures.Generate(ctx, p, filmSpec) }
	}
	if err := library(e.library); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.jobDir, "files"), 0o750); err != nil {
		t.Fatal(err)
	}
	if upload == nil {
		upload = func(p string) error { return fixtures.Generate(ctx, p, dubSpec) }
	}
	if err := upload(filepath.Join(e.jobDir, uploadPath)); err != nil {
		t.Fatal(err)
	}
	// The library is read-only, as the worker's mount is.
	if err := os.Chmod(e.library, 0o444); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{folder, e.libRoot} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = os.Chmod(e.libRoot, 0o755)
		_ = os.Chmod(folder, 0o755)
	})
	e.before = state(t, e.library)
	var err error
	if e.job, err = os.OpenRoot(e.jobDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.job.Close() })
	return e
}

func state(t *testing.T, p string) fileState {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return fileState{hash: hex.EncodeToString(h.Sum(nil)), mod: st.ModTime(), size: st.Size()}
}

func (e *env) args(tracks ...int) merge.Args {
	return merge.Args{Library: e.library, Upload: uploadPath, Tracks: tracks, Dest: dest}
}

func (e *env) run(t *testing.T, a merge.Args, opt merge.Options) merge.Output {
	t.Helper()
	out, err := merge.Run(context.Background(), e.job, []string{e.libRoot}, a, opt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out
}

// check runs after every case: the library file is byte for byte and to the mtime what
// it was, no partial file is left, and an output exists only for an OK result.
func (e *env) check(t *testing.T, out merge.Output) {
	t.Helper()
	if now := state(t, e.library); now != e.before {
		t.Errorf("the library file changed: %+v, was %+v", now, e.before)
	}
	work, _ := os.ReadDir(filepath.Join(e.jobDir, "work"))
	for _, w := range work {
		if !strings.HasSuffix(w.Name(), ".lock") { // lock files stay, empty
			t.Errorf("left in work/: %s", w.Name())
		}
	}
	_, err := os.Stat(filepath.Join(e.jobDir, dest))
	if out.OK != (err == nil) {
		t.Errorf("ok %t, but dest: %v", out.OK, err)
	}
	if _, err := os.Stat(filepath.Join(e.jobDir, uploadPath)); err != nil {
		t.Errorf("the upload: %v", err)
	}
}

// mkvTrack is what the tests read back from `mkvmerge -J` of an output.
type mkvTrack struct {
	Type       string `json:"type"`
	Codec      string `json:"codec"`
	Properties struct {
		Language         string `json:"language"`
		LanguageIETF     string `json:"language_ietf"`
		TrackName        string `json:"track_name"`
		DefaultTrack     bool   `json:"default_track"`
		MinimumTimestamp int64  `json:"minimum_timestamp"` // ns
	} `json:"properties"`
}

func identify(t *testing.T, p string) []mkvTrack {
	t.Helper()
	raw, err := exec.CommandContext(t.Context(), "mkvmerge", "--output-charset", "UTF-8", "-J", p).Output()
	if err != nil {
		t.Fatalf("mkvmerge -J %s: %v", p, err)
	}
	var id struct {
		Tracks []mkvTrack `json:"tracks"`
	}
	if err := json.Unmarshal(raw, &id); err != nil {
		t.Fatal(err)
	}
	return id.Tracks
}

func describe(tracks []mkvTrack) string {
	var b strings.Builder
	for _, tr := range tracks {
		b.WriteString(tr.Type + ":" + tr.Properties.LanguageIETF + ":" + tr.Properties.TrackName)
		if tr.Properties.DefaultTrack {
			b.WriteString(":default")
		}
		b.WriteString(" ")
	}
	return strings.TrimSpace(b.String())
}

// A matching dub merges into one file: the film's tracks first and unchanged, the dub's
// audio and subtitles after them with the language tag and name, the original audio still
// the default. The file has the library file's name, under dest.
func TestMergeMatchingDub(t *testing.T) {
	e := newEnv(t, nil)
	out := e.run(t, e.args(1, 2), options())
	e.check(t, out)
	if !out.OK || out.Path != finalPath || out.Media == nil || out.Library == nil {
		t.Fatalf("output %+v", out)
	}
	if out.LanguageTag != "es-419" || out.TrackName != "Español (Latino)" || !out.KeepOriginalDefault {
		t.Errorf("echo %q %q %t", out.LanguageTag, out.TrackName, out.KeepOriginalDefault)
	}
	if out.Library.Path != e.library || out.Library.Size != e.before.size || !out.Library.ModTime.Equal(e.before.mod) {
		t.Errorf("library %+v, want %+v", out.Library, e.before)
	}
	st, err := os.Stat(filepath.Join(e.jobDir, finalPath))
	if err != nil || st.Size() != out.Size {
		t.Fatalf("final file: %v (size %d)", err, out.Size)
	}
	got := describe(identify(t, filepath.Join(e.jobDir, finalPath)))
	want := "video:und: audio:en::default subtitles:en: audio:es-419:Español (Latino) subtitles:es-419:Español (Latino)"
	if got != want {
		t.Errorf("tracks\n got %s\nwant %s", got, want)
	}
	if len(out.Media.Audio) != 2 || len(out.Media.Subtitles) != 2 || len(out.Media.Video) != 1 {
		t.Errorf("probe %+v", out.Media)
	}
	entries, _ := os.ReadDir(filepath.Join(e.jobDir, dest))
	if len(entries) != 1 {
		t.Errorf("dest holds %d entries", len(entries))
	}
}

// The offset from the preview is applied to the added tracks only; with
// keepOriginalDefault off the dub becomes the default audio.
func TestMergeOffsetAndDefault(t *testing.T) {
	e := newEnv(t, nil)
	a := e.args(1)
	a.OffsetMs = 800
	keep := false
	a.KeepOriginalDefault = &keep
	a.LanguageTag, a.TrackName = "es-ES", "Castellano"
	out := e.run(t, a, options())
	e.check(t, out)
	if !out.OK {
		t.Fatalf("output %+v", out)
	}
	tracks := identify(t, filepath.Join(e.jobDir, finalPath))
	if got, want := describe(tracks), "video:und: audio:en: subtitles:en: audio:es-ES:Castellano:default"; got != want {
		t.Errorf("tracks\n got %s\nwant %s", got, want)
	}
	if ms := tracks[3].Properties.MinimumTimestamp / 1e6; ms < 790 || ms > 830 {
		t.Errorf("the added audio starts at %d ms, want about 800", ms)
	}
	if ms := tracks[1].Properties.MinimumTimestamp / 1e6; ms > 30 {
		t.Errorf("the original audio starts at %d ms, want 0", ms)
	}
	// The output is longer by the offset: the dub ends later. That is no verification
	// failure, but the video is still the library file's.
	if out.Media.DurationMs < 6700 || out.Media.Video[0].DurationMs > 6100 {
		t.Errorf("durations %d, video %d", out.Media.DurationMs, out.Media.Video[0].DurationMs)
	}
}

// An MP4 upload (mov_text subtitles) merges like a Matroska one.
func TestMergeMP4Upload(t *testing.T) {
	e := newEnv(t, nil)
	mp4 := "files/Film.es.mp4"
	if err := fixtures.Generate(context.Background(), filepath.Join(e.jobDir, mp4), dubSpec); err != nil {
		t.Fatal(err)
	}
	a := e.args(1, 2)
	a.Upload = mp4
	out := e.run(t, a, options())
	e.check(t, out)
	if !out.OK {
		t.Fatalf("output %+v", out)
	}
	if got := describe(identify(t, filepath.Join(e.jobDir, finalPath))); !strings.HasSuffix(got, "audio:es-419:Español (Latino) subtitles:es-419:Español (Latino)") {
		t.Errorf("tracks %s", got)
	}
}

// A broken upload is refused before anything is written; so is an upload that is not
// a video at all.
func TestMergeBrokenUpload(t *testing.T) {
	e := newEnv(t, func(p string) error {
		tmp := p + ".src.mkv"
		if err := fixtures.Generate(context.Background(), tmp, dubSpec); err != nil {
			return err
		}
		defer func() { _ = os.Remove(tmp) }()
		return fixtures.Broken(tmp, p)
	})
	out := e.run(t, e.args(1), options())
	e.check(t, out)
	if out.OK || out.Refusal != merge.RefusalUploadUnreadable {
		t.Errorf("output %+v", out)
	}
}

// fakeMkvmerge is a mkvmerge that identifies with the real one but muxes with script, so
// the run reaches verification (or a failed mux) with an output of the test's choosing.
// Descriptor 3 is the library file, 4 the upload, 5 the output.
func fakeMkvmerge(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mkvmerge")
	body := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = --identify ] && exec mkvmerge \"$@\"; done\n" + script + "\n"
	if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// An output that does not have the added tracks (here: a copy of the library file, with
// exit 0) fails verification, and nothing is offered.
func TestMergeVerificationFails(t *testing.T) {
	e := newEnv(t, nil)
	opt := options()
	opt.Mkvmerge = fakeMkvmerge(t, "cat /proc/self/fd/3 >&5; exit 0")
	out := e.run(t, e.args(1), opt)
	e.check(t, out)
	if out.OK || out.Refusal != merge.RefusalVerifyFailed || !strings.Contains(out.Detail, "tracks") {
		t.Errorf("output %+v", out)
	}
	// An output of another film (the upload's video under the library's name) fails too.
	opt.Mkvmerge = fakeMkvmerge(t, "cat /proc/self/fd/4 >&5; exit 0")
	out = e.run(t, e.args(1), opt)
	e.check(t, out)
	if out.OK || out.Refusal != merge.RefusalVerifyFailed {
		t.Errorf("output %+v", out)
	}
}

// mkvmerge's exit code must be 0: warnings (1) and errors (2) are a failed mux, whose
// partial file is removed.
func TestMergeMuxFails(t *testing.T) {
	e := newEnv(t, nil)
	for _, code := range []string{"1", "2"} {
		opt := options()
		opt.Mkvmerge = fakeMkvmerge(t, "cat /proc/self/fd/3 >&5; echo 'Warning: something odd'; exit "+code)
		out := e.run(t, e.args(1), opt)
		e.check(t, out)
		if out.OK || out.Refusal != merge.RefusalMuxFailed || !strings.Contains(out.Detail, "something odd") {
			t.Errorf("exit %s: output %+v", code, out)
		}
	}
	// A mkvmerge killed halfway (by the kernel, say) is a failed mux too.
	opt := options()
	opt.Mkvmerge = fakeMkvmerge(t, "cat /proc/self/fd/3 >&5; echo 'Warning: about to die'; kill -9 $$")
	out := e.run(t, e.args(1), opt)
	e.check(t, out)
	if out.OK || out.Refusal != merge.RefusalMuxFailed || !strings.Contains(out.Detail, "-1") {
		t.Errorf("killed: output %+v", out)
	}
}

// A run that is cancelled while mkvmerge writes kills it and removes the partial file. A
// partial file left by a killed worker is removed by the next run, which then succeeds.
func TestMergeCancelledAndKilled(t *testing.T) {
	e := newEnv(t, nil)
	opt := options()
	opt.Mkvmerge = fakeMkvmerge(t, "cat /proc/self/fd/3 >&5; exec sleep 60")
	ctx, cancel := context.WithCancel(context.Background())
	rel, _ := merge.PartialFor(dest)
	partial := filepath.Join(e.jobDir, rel)
	go func() {
		for {
			if st, err := os.Stat(partial); err == nil && st.Size() > 0 {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	start := time.Now()
	_, err := merge.Run(ctx, e.job, []string{e.libRoot}, e.args(1), opt)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v, want context.Canceled", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Errorf("the cancel took %s", time.Since(start))
	}
	e.check(t, merge.Output{})

	// What a killed mkvmerge leaves: a partial file, and maybe an earlier output in dest.
	if err := os.MkdirAll(filepath.Join(e.jobDir, "work"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, []byte("half a film"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.jobDir, dest), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.jobDir, dest, "stale.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := e.run(t, e.args(1), options())
	e.check(t, out)
	if !out.OK {
		t.Fatalf("the next run: %+v", out)
	}
	entries, _ := os.ReadDir(filepath.Join(e.jobDir, dest))
	if len(entries) != 1 || entries[0].Name() != filmName {
		t.Errorf("dest: %v", entries)
	}
	// And running the same task again gives the same result.
	if again := e.run(t, e.args(1), options()); !again.OK || again.Path != out.Path {
		t.Errorf("again: %+v", again)
	}
	e.check(t, out)
}

// Free space is checked before mkvmerge runs: the library file, the added tracks and
// the margin must fit.
func TestMergeFreeSpace(t *testing.T) {
	e := newEnv(t, nil)
	opt := options()
	opt.FreeSpaceMargin = 1 << 30
	var measured int64
	opt.FreeSpace = func(dir *os.File) (int64, error) {
		measured++
		return e.before.size + 1<<30, nil // room for the library file and the margin only
	}
	out := e.run(t, e.args(1), opt)
	e.check(t, out)
	if out.OK || out.Refusal != merge.RefusalFreeSpace || measured != 1 {
		t.Errorf("output %+v, measured %d", out, measured)
	}
	opt.FreeSpace = func(*os.File) (int64, error) { return e.before.size + 2<<30, nil }
	if out := e.run(t, e.args(1), opt); !out.OK {
		t.Errorf("with room: %+v", out)
	}
}

// The library file is read only inside the library roots: not outside them, not through a
// link that leads out of a root. Chosen tracks must be audio or subtitle tracks of the
// upload.
func TestMergeRefusals(t *testing.T) {
	e := newEnv(t, nil)
	outside := t.TempDir()
	if err := fixtures.Generate(context.Background(), filepath.Join(outside, filmName), filmSpec); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(e.libRoot, 0o755)
	link := filepath.Join(e.libRoot, "Linked.mkv")
	if err := os.Symlink(filepath.Join(outside, filmName), link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*merge.Args)
		refusal string
	}{
		{"outside", func(a *merge.Args) { a.Library = filepath.Join(outside, filmName) }, merge.RefusalOutsideLibrary},
		{"climbs out", func(a *merge.Args) { a.Library = e.libRoot + "/../" + filepath.Base(outside) + "/" + filmName }, merge.RefusalOutsideLibrary},
		{"link out", func(a *merge.Args) { a.Library = link }, merge.RefusalLibraryUnreadable},
		{"missing", func(a *merge.Args) { a.Library = filepath.Join(e.libRoot, "Gone.mkv") }, merge.RefusalLibraryUnreadable},
		{"video track", func(a *merge.Args) { a.Tracks = []int{0} }, merge.RefusalTrackNotFound},
		{"no such track", func(a *merge.Args) { a.Tracks = []int{1, 9} }, merge.RefusalTrackNotFound},
	} {
		a := e.args(1)
		tc.mutate(&a)
		out := e.run(t, a, options())
		e.check(t, out)
		if out.OK || out.Refusal != tc.refusal {
			t.Errorf("%s: %+v, want %s", tc.name, out, tc.refusal)
		}
	}
}

// Arguments that cannot be used are a bad task, and nothing is touched.
func TestMergeBadArgs(t *testing.T) {
	job, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = job.Close() }()
	good := merge.Args{Library: "/lib/Film.mkv", Upload: "files/a.mkv", Tracks: []int{1}, Dest: "merged/1"}
	long := strings.Repeat("x", 300)
	for name, mutate := range map[string]func(*merge.Args){
		"relative library": func(a *merge.Args) { a.Library = "lib/Film.mkv" },
		"upload outside":   func(a *merge.Args) { a.Upload = "../a.mkv" },
		"upload archives":  func(a *merge.Args) { a.Upload = "archives/a.mkv" },
		"upload unclean":   func(a *merge.Args) { a.Upload = "files/./a.mkv" },
		"dest files":       func(a *merge.Args) { a.Dest = "files" },
		"dest under files": func(a *merge.Args) { a.Dest = "files/merged" },
		"dest merged":      func(a *merge.Args) { a.Dest = "merged" },
		"dest outside":     func(a *merge.Args) { a.Dest = "work/merged" },
		"dest nested":      func(a *merge.Args) { a.Dest = "merged/1/2" },
		"dest climbs":      func(a *merge.Args) { a.Dest = "merged/.." },
		"dest upper-case":  func(a *merge.Args) { a.Dest = "merged/One" },
		"dest dot":         func(a *merge.Args) { a.Dest = "merged/." },
		"sign language":    func(a *merge.Args) { a.LanguageTag = "sgn-BR" },
		"underscore tag":   func(a *merge.Args) { a.LanguageTag = "en_US" },
		"deprecated tag":   func(a *merge.Args) { a.LanguageTag = "iw" },
		"extlang tag":      func(a *merge.Args) { a.LanguageTag = "zh-yue" },
		"upper-case tag":   func(a *merge.Args) { a.LanguageTag = "ES-419" },
		"no tracks":        func(a *merge.Args) { a.Tracks = nil },
		"same track twice": func(a *merge.Args) { a.Tracks = []int{1, 1} },
		"negative track":   func(a *merge.Args) { a.Tracks = []int{-1} },
		"huge offset":      func(a *merge.Args) { a.OffsetMs = 2 * 3600 * 1000 },
		"bad tag":          func(a *merge.Args) { a.LanguageTag = "not a tag" },
		"control in name":  func(a *merge.Args) { a.TrackName = "a\nb" },
		"long name":        func(a *merge.Args) { a.TrackName = long },
	} {
		a := good
		mutate(&a)
		if _, err := merge.Run(context.Background(), job, []string{"/lib"}, a, options()); !errors.Is(err, merge.ErrBadArgs) {
			t.Errorf("%s: %v, want ErrBadArgs", name, err)
		}
	}
	entries, _ := fs.ReadDir(job.FS(), ".")
	if len(entries) != 0 {
		t.Errorf("bad args wrote %v", entries)
	}
}

func TestOutputName(t *testing.T) {
	for in, want := range map[string]string{
		"/m/Film (2001)/Film (2001) WEBDL-1080p.mkv": "Film (2001) WEBDL-1080p.mkv",
		"/m/Film (2001)/Film (2001) Bluray-720p.mp4": "Film (2001) Bluray-720p.mkv",
		"/m/Film/Film": "Film.mkv",
	} {
		if got := merge.OutputName(in); got != want {
			t.Errorf("OutputName(%q) = %q, want %q", in, got, want)
		}
	}
}
