package preview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/media"
)

// The worker's side. Each clip is one ffmpeg run that reads two inherited descriptors
// (fd 3: the library file, opened confined to its root, read-only; fd 4: the upload,
// opened through the job's os.Root) with the probe's demuxer and protocol whitelists, so
// it can open no other file and no URL, and writes the MP4 to its standard output. The
// worker copies that into a temporary file in the job's work/previews/, up to a size cap,
// and renames it into place once ffmpeg has succeeded. Each run has a deadline
// (merge.previewTimeout) and runs at worker.nice; at the deadline, or when the task is
// cancelled, the whole process group is killed.
//
// It is safe to run again: the clips of other requests are removed first, a run writes
// its own temporary names and renames over its own clips, and a clip appears under its
// final name only complete.

// ErrBadArgs is returned, wrapped, when the args cannot be used (a malformed request id,
// an upload outside files/, clips beyond the limits). The worker makes it a bad task.
var ErrBadArgs = errors.New("bad preview args")

// Encoding of the clips. The video is H.264 without B-frames, so that the first frame is
// shown at 0 (with B-frames a fragmented MP4 starts its video a few frames late, which is
// what the user is judging); the audio is AAC stereo. The image's ffmpeg enables exactly
// these encoders and the mp4 muxer (Dockerfile).
const (
	audioKbps = 96
	gop       = 48
	killWait  = 2 * time.Second
	tailMax   = 4 << 10 // bytes of ffmpeg's standard error kept for the refusal's detail
	detailMax = 300
	// maxThreads caps ffmpeg's threads: by default it starts one per core, and on a large host
	// (hundreds of cores) the stacks and arenas alone exceed worker.toolMemoryLimit and the
	// run stalls instead of failing. A clip of a few seconds gains nothing from more.
	maxThreads = 8
)

// Options configure Run.
type Options struct {
	Timeout     time.Duration // merge.previewTimeout, per clip
	Nice        int           // worker.nice
	MemoryLimit int64         // worker.toolMemoryLimit
	Threads     int           // ffmpeg -threads (a cap on decoder and encoder threads); 0 leaves it to ffmpeg
	Height      int           // merge.previewHeight; smaller videos are not scaled up
	VideoKbps   int           // merge.previewVideoBitrate
	// LibraryFormats are the demuxers ffmpeg may use on the library file
	// (worker.libraryFormats, as for the library probe); empty means the upload's
	// (media.FormatWhitelist). The upload always gets the upload's.
	LibraryFormats []string
	FFmpeg         string // binary; empty means "ffmpeg" (tests pass a fake)
}

// OptionsFrom takes the options from the configuration.
func OptionsFrom(cfg config.Config) Options {
	return Options{
		Timeout:        cfg.Merge.PreviewTimeout.D(),
		Nice:           cfg.Worker.Nice,
		MemoryLimit:    int64(cfg.Worker.ToolMemoryLimit),
		Threads:        min(max(runtime.NumCPU(), 1), maxThreads),
		Height:         cfg.Merge.PreviewHeight,
		VideoKbps:      cfg.Merge.PreviewVideoBitrate,
		LibraryFormats: slices.Clone(cfg.Worker.LibraryFormats),
	}
}

// Check reports what makes args unusable; nil when they are fine.
func Check(a Args) error {
	switch {
	case !ValidRequest(a.Request):
		return fmt.Errorf("malformed request id %q", a.Request)
	case !uploadPath(a.Upload):
		return fmt.Errorf("upload %q is not a clean path under %s/", a.Upload, jobdir.Files)
	case a.AudioIndex < 0:
		return errors.New("negative audio index")
	case a.OffsetMs < -MaxOffsetMs || a.OffsetMs > MaxOffsetMs:
		return fmt.Errorf("offset %d ms is beyond ±%d ms", a.OffsetMs, MaxOffsetMs)
	case len(a.Clips) == 0 || len(a.Clips) > MaxClips:
		return fmt.Errorf("%d clips, want 1 to %d", len(a.Clips), MaxClips)
	}
	for i, c := range a.Clips {
		if c.StartMs < 0 || c.LengthMs <= 0 || c.LengthMs > MaxClipLength.Milliseconds() {
			return fmt.Errorf("clip %d: start %d ms, length %d ms: want a start ≥ 0 and a length up to %s", i+1, c.StartMs, c.LengthMs, MaxClipLength)
		}
	}
	return nil
}

func uploadPath(p string) bool {
	rest, ok := strings.CutPrefix(p, jobdir.Files+"/")
	return ok && rest != "" && path.Clean(p) == p && !strings.Contains(p, "\\") && fs.ValidPath(p)
}

// Run renders the clips of args into the job's Dir. A file that cannot be used is a
// refusal in the output (OK false); an error means the task could not run (bad args,
// ffmpeg missing, the job directory not writable, the task cancelled).
func Run(ctx context.Context, job *os.Root, roots []string, args Args, opt Options) (Output, error) {
	if err := Check(args); err != nil {
		return Output{}, fmt.Errorf("%w: %w", ErrBadArgs, err)
	}
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	out := Output{Request: args.Request}
	argv0, prefix, err := command(opt)
	if err != nil {
		return Output{}, err // a broken install is an error, never a verdict on the files
	}
	lib, _, refusal := media.OpenLibrary(roots, args.Library)
	switch refusal {
	case "":
	case media.RefusalOutsideLibrary:
		out.Refusal = RefusalOutsideLibrary
		return out, nil
	default:
		out.Refusal = RefusalLibraryUnreadable
		return out, nil
	}
	defer func() { _ = lib.Close() }()
	up, _, ok := media.OpenRegular(job, args.Upload)
	if !ok {
		out.Refusal = RefusalUploadUnreadable
		return out, nil
	}
	defer func() { _ = up.Close() }()

	if err := job.MkdirAll(Dir, 0o750); err != nil {
		return Output{}, fmt.Errorf("preview directory: %w", err)
	}
	if err := clean(job, args.Request); err != nil {
		return Output{}, err
	}
	for i, c := range args.Clips {
		n := i + 1
		size, refusal, detail, err := render(ctx, job, lib, up, args, c, n, opt, argv0, prefix)
		if err != nil {
			return Output{}, err
		}
		if refusal != "" {
			out.Refusal, out.Detail = refusal, fmt.Sprintf("clip %d: %s", n, detail)
			return out, nil
		}
		out.Clips = append(out.Clips, ClipFile{N: n, StartMs: c.StartMs, LengthMs: c.LengthMs, Size: size})
	}
	out.OK = true
	return out, nil
}

// command is the program to run and the arguments before ffmpeg's own: nice when
// worker.nice is set, prlimit when worker.toolMemoryLimit is.
func command(opt Options) (string, []string, error) {
	name := opt.FFmpeg
	if name == "" {
		name = "ffmpeg"
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		return "", nil, err
	}
	name, args, err := media.Wrap(opt.Nice, opt.MemoryLimit, bin, nil)
	return name, args, err
}

// clean removes from Dir every file that is not of this request: the clips of earlier
// requests, and what an interrupted run left.
func clean(job *os.Root, request string) error {
	d, err := job.Open(Dir)
	if err != nil {
		return fmt.Errorf("preview directory: %w", err)
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return fmt.Errorf("preview directory: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "clip-"+request+"-") {
			continue
		}
		if err := job.RemoveAll(Dir + "/" + name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("preview directory: %w", err)
		}
	}
	return nil
}

// render writes clip n. It returns the clip's size, or a refusal with its detail.
func render(ctx context.Context, job *os.Root, lib, up *os.File, a Args, c Clip, n int, opt Options, argv0 string, prefix []string) (int64, string, string, error) {
	final := Dir + "/" + ClipName(a.Request, n)
	tmp := Dir + "/.clip-" + a.Request + "-" + strconv.Itoa(n) + ".part"
	if err := job.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, "", "", err
	}
	f, err := job.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return 0, "", "", fmt.Errorf("clip %d: %w", n, err)
	}
	keep := false
	defer func() {
		_ = f.Close()
		if !keep {
			_ = job.Remove(tmp)
		}
	}()
	// ffmpeg reads the descriptors from where they are: every run starts at the beginning.
	for _, in := range []*os.File{lib, up} {
		if _, err := in.Seek(0, io.SeekStart); err != nil {
			return 0, "", "", err
		}
	}

	formats := media.FormatWhitelist
	if len(opt.LibraryFormats) > 0 {
		formats = strings.Join(opt.LibraryFormats, ",")
	}
	cctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, argv0, append(prefix, Command(a, c, opt, formats)...)...) //nolint:gosec // the binary is configuration, the arguments are built here
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	g := &group{cmd: cmd}
	cmd.Cancel = func() error { g.kill(); return nil }
	cmd.WaitDelay = killWait
	cmd.ExtraFiles = []*os.File{lib, up}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	sink := &capped{w: f, max: maxClipBytes(c, opt), kill: g.kill}
	cmd.Stdout = sink
	stderr := &tail{max: tailMax}
	cmd.Stderr = stderr
	err = run(cmd, g)
	if perr := ctx.Err(); perr != nil {
		return 0, "", "", perr
	}
	switch {
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		return 0, RefusalTimeout, "ran past merge.previewTimeout (" + opt.Timeout.String() + ")", nil
	case sink.over():
		return 0, RefusalTooLarge, fmt.Sprintf("larger than %d bytes", sink.max), nil
	case err != nil:
		var ee *exec.ExitError
		if errors.As(err, &ee) && (ee.ExitCode() == 126 || ee.ExitCode() == 127) {
			return 0, "", "", fmt.Errorf("ffmpeg could not be run (exit %d)", ee.ExitCode())
		}
		if ee == nil {
			return 0, "", "", err // ffmpeg could not be started, or the clip not written
		}
		if lerr := media.LimitFailure(string(stderr.buf)); lerr != nil {
			return 0, "", "", lerr // prlimit could not set the limit: the deployment, not the files
		}
		if media.MemoryExhausted(err, string(stderr.buf), opt.MemoryLimit > 0) {
			return 0, RefusalRenderFailed, media.MemoryDetail("ffmpeg") + ": " + stderr.last(), nil
		}
		return 0, RefusalRenderFailed, stderr.last(), nil
	case sink.n == 0:
		return 0, RefusalRenderFailed, "ffmpeg wrote nothing", nil
	}
	if err := f.Sync(); err != nil {
		return 0, "", "", err
	}
	if err := f.Close(); err != nil {
		return 0, "", "", err
	}
	if err := job.Rename(tmp, final); err != nil {
		return 0, "", "", err
	}
	keep = true
	return sink.n, "", "", nil
}

// Command is ffmpeg's arguments for one clip; formats is the library file's demuxer
// whitelist. The video is the library file's first video stream (not cover art) from
// c.StartMs, scaled down to opt.Height; the audio is the upload's stream a.AudioIndex,
// shifted by a.OffsetMs: it is read from c.StartMs − offset, and when that is before the
// upload's start, the missing part is silence. Gaps in the upload's audio (a track that
// starts late) become silence too (aresample's async with first_pts 0), so the audio
// keeps the upload's own timing, and it is padded to the clip's length.
func Command(a Args, c Clip, opt Options, formats string) []string {
	audioStart := max(0, c.StartMs-a.OffsetMs)
	silence := max(0, a.OffsetMs-c.StartMs)
	length := seconds(c.LengthMs)
	kbps := opt.VideoKbps
	filter := fmt.Sprintf("[0:V:0]scale=-2:'min(%d,trunc(ih/2)*2)',format=yuv420p[v];"+
		"[1:%d]aresample=async=1:first_pts=0,adelay=delays=%d:all=1,apad=whole_dur=%s[a]",
		opt.Height, a.AudioIndex, silence, length)
	var th []string // -threads before each input and the output
	if opt.Threads > 0 {
		th = []string{"-threads", strconv.Itoa(opt.Threads)}
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	args = append(args, th...)
	args = append(args,
		"-format_whitelist", formats, "-protocol_whitelist", media.ProtocolWhitelist,
		"-ss", seconds(c.StartMs), "-fd", "3", "-i", "fd:")
	args = append(args, th...)
	args = append(args,
		"-format_whitelist", media.FormatWhitelist, "-protocol_whitelist", media.ProtocolWhitelist,
		"-ss", seconds(audioStart), "-fd", "4", "-i", "fd:")
	args = append(args, th...)
	return append(args,
		"-filter_complex", filter, "-map", "[v]", "-map", "[a]", "-t", length,
		"-map_metadata", "-1", "-map_chapters", "-1",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "28",
		"-maxrate", strconv.Itoa(kbps)+"k", "-bufsize", strconv.Itoa(2*kbps)+"k",
		"-g", strconv.Itoa(gop), "-bf", "0",
		"-c:a", "aac", "-b:a", strconv.Itoa(audioKbps)+"k", "-ac", "2",
		"-f", "mp4", "-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"-protocol_whitelist", "pipe", "pipe:1")
}

// maxClipBytes caps one clip: twice what its bit rates allow, plus 1 MiB for the
// container, so that a run that ignores the rate cap is stopped.
func maxClipBytes(c Clip, opt Options) int64 {
	kbps := int64(opt.VideoKbps + audioKbps)
	return 2*kbps*1000/8*max(c.LengthMs, 1000)/1000 + 1<<20
}

// seconds formats milliseconds as ffmpeg's seconds: "12.345".
func seconds(ms int64) string {
	return strconv.FormatInt(ms/1000, 10) + "." + fmt.Sprintf("%03d", ms%1000)
}

// group kills ffmpeg's process group (the size cap, the deadline and a cancel all do).
// Once the leader is reaped its id may be reused, so run makes the last kill after the
// leader has exited and before it is reaped, and every kill after that does nothing.
type group struct {
	cmd *exec.Cmd

	mu   sync.Mutex
	done bool
}

// kill sends SIGKILL to the group, unless the last kill was made.
func (g *group) kill() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.signal()
}

// last makes the last kill: whatever ffmpeg left in the group goes, and later kills do
// nothing.
func (g *group) last() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.signal()
	g.done = true
}

func (g *group) signal() {
	if !g.done && g.cmd.Process != nil {
		_ = syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// capped writes to w up to max bytes; past that it kills ffmpeg and refuses more.
type capped struct {
	w    io.Writer
	max  int64
	n    int64
	kill func()

	mu      sync.Mutex
	tooMuch bool
}

var errTooLarge = errors.New("clip too large")

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n+int64(len(p)) > c.max {
		c.tooMuch = true
		c.kill()
		return 0, errTooLarge
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func (c *capped) over() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tooMuch
}

// tail keeps the last max bytes written.
type tail struct {
	max int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

// last is the last non-empty line, printable only and short: it goes into an error text.
func (t *tail) last() string {
	lines := bytes.Split(bytes.TrimSpace(t.buf), []byte("\n"))
	line := strings.TrimSpace(string(lines[len(lines)-1]))
	line = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, line)
	if line == "" {
		return "ffmpeg failed"
	}
	if len(line) > detailMax {
		line = strings.ToValidUTF8(line[:detailMax], "")
	}
	return line
}
