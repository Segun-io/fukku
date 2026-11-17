package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/procgroup"
)

// Refusal codes of ProbedFile.Refusal.
const (
	RefusalExtension     = "extension"
	RefusalNoVideoStream = "no_video_stream"
	RefusalNoDuration    = "no_duration"
	RefusalNotText       = "not_text"
	RefusalUnreadable    = "unreadable"
	RefusalTimeout       = "timeout"
	// RefusalTooManyStreams: more video, audio and subtitle tracks than worker.maxStreamsPerFile.
	RefusalTooManyStreams = "too_many_streams"
	// RefusalResultTooLarge: the files before this one already fill worker.maxProbeResultBytes.
	RefusalResultTooLarge = "result_too_large"
)

const (
	maxSubtitleSize = 16 << 20 // 16 MiB
	maxProbeOutput  = 4 << 20  // ffprobe JSON cap; a hostile file must not fill memory
	killWait        = 2 * time.Second

	// Demuxers of the accepted containers only: a playlist or concat script renamed to .mkv
	// is then not parsed as one, and cannot make ffprobe read other files or URLs.
	formatWhitelist = "matroska,webm,mov,mp4,avi,mpegts"

	// maxDurationMs caps any duration read from a file (about 100 years), so a hostile or
	// corrupt value cannot overflow the int64 conversion. Real files are far below it.
	maxDurationMs = 100 * 365 * 24 * 3600 * 1000
	// Only the inherited descriptor may be opened: no other file, no URL.
	protocolWhitelist = "fd"
)

// Options configure Run.
type Options struct {
	Timeout     time.Duration // worker.probeTimeout, per file
	Nice        int           // worker.nice
	MemoryLimit int64         // worker.toolMemoryLimit
	VideoExt    []string      // upload.videoExtensions
	SubtitleExt []string      // upload.subtitleExtensions
	FFprobe     string        // binary; empty means "ffprobe" (tests pass a fake)
	// LibraryFormats are the demuxers library probes may use (worker.libraryFormats); empty
	// means the upload whitelist.
	LibraryFormats []string
	// Bounds of what a probe keeps of a file, so a hostile file cannot make a result too
	// large to read. Zero means the built-in default.
	MaxTitleBytes     int // worker.maxTrackTitleBytes
	MaxTagBytes       int // worker.maxTrackTagBytes
	MaxStreamsPerFile int // worker.maxStreamsPerFile
	// MaxResultBytes bounds the JSON of one probe task's files together (worker.maxProbeResultBytes).
	MaxResultBytes int64
}

// Defaults of the bounds above (the same as the settings').
const (
	defaultMaxTitleBytes     = 256
	defaultMaxTagBytes       = 64
	defaultMaxStreamsPerFile = 256
	defaultMaxResultBytes    = 8 << 20
)

// limits are the bounds applied to one probed file.
type limits struct{ title, tag, streams int }

func (o Options) limits() limits {
	l := limits{o.MaxTitleBytes, o.MaxTagBytes, o.MaxStreamsPerFile}
	if l.title <= 0 {
		l.title = defaultMaxTitleBytes
	}
	if l.tag <= 0 {
		l.tag = defaultMaxTagBytes
	}
	if l.streams <= 0 {
		l.streams = defaultMaxStreamsPerFile
	}
	return l
}

// clip cuts s to at most n bytes, on a character boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// OptionsFrom takes the probe options from the configuration.
func OptionsFrom(cfg config.Config) Options {
	return Options{
		Timeout:        time.Duration(cfg.Worker.ProbeTimeout),
		Nice:           cfg.Worker.Nice,
		MemoryLimit:    int64(cfg.Worker.ToolMemoryLimit),
		VideoExt:       cfg.Upload.VideoExtensions,
		SubtitleExt:    cfg.Upload.SubtitleExtensions,
		LibraryFormats: cfg.Worker.LibraryFormats,

		MaxTitleBytes:     cfg.Worker.MaxTrackTitleBytes,
		MaxTagBytes:       cfg.Worker.MaxTrackTagBytes,
		MaxStreamsPerFile: cfg.Worker.MaxStreamsPerFile,
		MaxResultBytes:    int64(cfg.Worker.MaxProbeResultBytes),
	}
}

// Run checks and probes each path. An error means the task could not run; a refused file
// is a normal entry with OK false.
func Run(ctx context.Context, job *os.Root, args ProbeArgs, opt Options) (ProbeOutput, error) {
	out := ProbeOutput{Files: make([]ProbedFile, 0, len(args.Paths))}
	budget, used := opt.MaxResultBytes, int64(0)
	if budget <= 0 {
		budget = defaultMaxResultBytes
	}
	for _, p := range args.Paths {
		if err := ctx.Err(); err != nil {
			return ProbeOutput{}, err
		}
		pf := ProbedFile{Path: p}
		ext := strings.ToLower(path.Ext(p))
		switch {
		case hasExt(opt.VideoExt, ext):
			pf.Kind = "video"
		case hasExt(opt.SubtitleExt, ext):
			pf.Kind = "subtitle"
		default:
			pf.Refusal = RefusalExtension
			out.Files = append(out.Files, pf)
			continue
		}
		var err error
		if pf.Kind == "video" {
			err = probeVideo(ctx, job, &pf, opt, formatWhitelist)
		} else {
			checkSubtitle(job, &pf)
		}
		if err != nil {
			return ProbeOutput{}, err
		}
		if pf.Media != nil {
			// What the file adds to the result, as encoded (escapes included): the files that
			// do not fit are refused, so the result stays within what the spool reads.
			data, merr := json.Marshal(pf)
			if merr != nil {
				return ProbeOutput{}, merr
			}
			if used += int64(len(data)); used > budget {
				pf.OK, pf.Media, pf.Refusal = false, nil, RefusalResultTooLarge
			}
		}
		out.Files = append(out.Files, pf)
	}
	return out, nil
}

func hasExt(list []string, ext string) bool {
	for _, e := range list {
		if strings.EqualFold(e, ext) {
			return true
		}
	}
	return false
}

// openRegular opens p through the root and requires a regular file. On failure it returns
// nil and the caller refuses the file as unreadable.
func openRegular(job *os.Root, p string) (*os.File, int64) {
	// O_NONBLOCK: opening a FIFO must not wait for a writer. It is cleared again below.
	f, err := job.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, 0
	}
	if err := syscall.SetNonblock(int(f.Fd()), false); err != nil {
		_ = f.Close()
		return nil, 0
	}
	return f, st.Size()
}

func checkSubtitle(job *os.Root, pf *ProbedFile) {
	f, size := openRegular(job, pf.Path)
	if f == nil {
		pf.Refusal = RefusalUnreadable
		return
	}
	defer func() { _ = f.Close() }()
	if size > maxSubtitleSize {
		pf.Refusal = RefusalNotText
		return
	}
	buf := make([]byte, 64<<10)
	var total int64
	for {
		n, err := f.Read(buf)
		total += int64(n)
		if bytes.IndexByte(buf[:n], 0) >= 0 || total > maxSubtitleSize {
			pf.Refusal = RefusalNotText
			return
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			pf.Refusal = RefusalUnreadable
			return
		}
	}
	pf.OK = true
}

type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

var errOutputTooLarge = errors.New("ffprobe output too large")

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		return 0, errOutputTooLarge
	}
	return l.buf.Write(p)
}

func probeVideo(ctx context.Context, job *os.Root, pf *ProbedFile, opt Options, formats string) error {
	f, size := openRegular(job, pf.Path)
	if f == nil {
		pf.Refusal = RefusalUnreadable
		return nil
	}
	defer func() { _ = f.Close() }()
	return probeOpen(ctx, f, size, pf, opt, formats)
}

// probeOpen probes the open file f: the content check of probeVideo.
func probeOpen(ctx context.Context, f *os.File, size int64, pf *ProbedFile, opt Options, formats string) error {
	out, refusal, err := runFFprobe(ctx, f, opt, formats, "-show_format", "-show_streams")
	if err != nil || refusal != "" {
		pf.Refusal = refusal
		return err
	}
	var raw rawProbe
	if err := json.Unmarshal(out, &raw); err != nil {
		pf.Refusal = RefusalNoVideoStream
		return nil
	}
	m, tooMany := raw.toProbe(size, opt.limits())
	switch {
	case tooMany:
		pf.Refusal = RefusalTooManyStreams
	case len(m.Video) == 0:
		pf.Refusal = RefusalNoVideoStream
	case m.DurationMs <= 0:
		pf.Refusal = RefusalNoDuration
	default:
		pf.OK = true
		pf.Media = &m
	}
	return nil
}

// runFFprobe runs ffprobe on the open file f (as descriptor 3, the only thing it may open)
// with the demuxers in formats, within opt.Timeout, and returns its JSON output. A file
// ffprobe cannot read gives the refusal no_video_stream, a run past the timeout timeout;
// an error means ffprobe could not be run at all (or ctx ended).
func runFFprobe(ctx context.Context, f *os.File, opt Options, formats string, show ...string) ([]byte, string, error) {
	bin, err := lookBinary(opt.FFprobe, "ffprobe")
	if err != nil {
		return nil, "", err // a broken install is an error, never a refusal of the upload
	}
	// ffprobe shares f's file offset and starts reading where it stands: at the start, not
	// where an earlier run on the same descriptor left it. It is put back at the start
	// afterwards too, so the caller's own reads begin there.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	defer func() { _, _ = f.Seek(0, io.SeekStart) }()
	probeArgs := append([]string{"-v", "error", "-print_format", "json"}, show...)
	probeArgs = append(probeArgs, "-format_whitelist", formats, "-protocol_whitelist", protocolWhitelist, "-fd", "3", "-i", "fd:")
	name, probeArgs, err := Wrap(opt.Nice, opt.MemoryLimit, bin, probeArgs)
	if err != nil {
		return nil, "", err
	}

	fctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()
	cmd := exec.CommandContext(fctx, name, probeArgs...) //nolint:gosec // the binary is configuration, the arguments are fixed
	g := procgroup.New(cmd)
	cmd.WaitDelay = killWait
	cmd.ExtraFiles = []*os.File{f}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	stdout := &limitedBuffer{max: maxProbeOutput}
	cmd.Stdout = stdout
	stderr := &limitedBuffer{max: 4 << 10}
	cmd.Stderr = stderr
	// Whatever happened, nothing of the process group may outlive the call: Run makes the
	// group's last kill before the leader is reaped.
	err = g.Run()
	if perr := ctx.Err(); perr != nil {
		return nil, "", perr
	}
	if errors.Is(fctx.Err(), context.DeadlineExceeded) {
		return nil, RefusalTimeout, nil
	}
	if err != nil {
		if lerr := LimitFailure(stderr.buf.String()); lerr != nil {
			return nil, "", lerr // prlimit could not set the limit: the deployment, not the file
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) && (ee.ExitCode() == 126 || ee.ExitCode() == 127) {
			// nice or prlimit could not run ffprobe: not a verdict on the file
			return nil, "", fmt.Errorf("ffprobe could not be run (exit %d)", ee.ExitCode())
		}
		if ee != nil || errors.Is(err, errOutputTooLarge) {
			return nil, RefusalNoVideoStream, nil
		}
		return nil, "", err // ffprobe could not be started: the task cannot run
	}
	return stdout.buf.Bytes(), "", nil
}

type rawStream struct {
	Index        int               `json:"index"`
	CodecType    string            `json:"codec_type"`
	CodecName    string            `json:"codec_name"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	Channels     int               `json:"channels"`
	AvgFrameRate string            `json:"avg_frame_rate"`
	RFrameRate   string            `json:"r_frame_rate"`
	Duration     string            `json:"duration"`
	Tags         map[string]string `json:"tags"`
	Disposition  map[string]int    `json:"disposition"`
}

type rawProbe struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		BitRate    string `json:"bit_rate"`
	} `json:"format"`
	Streams []rawStream `json:"streams"`
}

// toProbe keeps the video, audio and subtitle streams (other kinds, and cover art, are not
// tracks). A file with more than lim.streams of them is not truncated, which would make
// every later decision (a merge's track count, the upload's track choice) work from a
// partial list: tooMany is set instead and the caller refuses the file.
func (r rawProbe) toProbe(size int64, lim limits) (m Probe, tooMany bool) {
	m = Probe{
		Container: clip(r.Format.FormatName, lim.tag),
		Size:      size,
		Video:     []VideoTrack{},
		Audio:     []AudioTrack{},
		Subtitles: []SubtitleTrack{},
	}
	if d, err := strconv.ParseFloat(r.Format.Duration, 64); err == nil && d > 0 && !math.IsInf(d, 0) {
		m.DurationMs = capMs(d * 1000)
	}
	m.BitRate, _ = strconv.ParseInt(r.Format.BitRate, 10, 64)
	kept := 0
	for _, s := range r.Streams {
		lang, title := clip(s.Tags["language"], lim.tag), clip(s.Tags["title"], lim.title)
		s.CodecName = clip(s.CodecName, lim.tag)
		def := s.Disposition["default"] == 1
		switch s.CodecType {
		case "video":
			if s.Disposition["attached_pic"] == 1 {
				continue // cover art, not a video stream
			}
			if kept++; kept > lim.streams {
				return m, true
			}
			m.Video = append(m.Video, VideoTrack{Index: s.Index, Codec: s.CodecName, Width: s.Width,
				Height: s.Height, FrameRate: frameRate(s.AvgFrameRate, s.RFrameRate), Default: def,
				DurationMs: streamDuration(s.Duration, s.Tags)})
		case "audio":
			if kept++; kept > lim.streams {
				return m, true
			}
			m.Audio = append(m.Audio, AudioTrack{Index: s.Index, Codec: s.CodecName, Language: lang,
				Title: title, Channels: s.Channels, Default: def})
		case "subtitle":
			if kept++; kept > lim.streams {
				return m, true
			}
			m.Subtitles = append(m.Subtitles, SubtitleTrack{Index: s.Index, Codec: s.CodecName, Language: lang,
				Title: title, Default: def, Forced: s.Disposition["forced"] == 1,
				HearingImpaired: s.Disposition["hearing_impaired"] == 1})
		}
	}
	return m, false
}

// streamDuration is a stream's duration in ms: ffprobe's own (MP4 and most containers),
// else Matroska's DURATION tag ("00:20:00.020000000"; the key may carry a language
// suffix). 0 when neither says.
func streamDuration(d string, tags map[string]string) int64 {
	if ms := secondsMs(d); ms > 0 {
		return ms
	}
	for k, v := range tags {
		if strings.EqualFold(k, "DURATION") || strings.HasPrefix(strings.ToUpper(k), "DURATION-") {
			if ms := clockMs(v); ms > 0 {
				return ms
			}
		}
	}
	return 0
}

// capMs converts milliseconds to int64, within -maxDurationMs and maxDurationMs: a float
// beyond the int64 range has no defined conversion. NaN gives the cap.
func capMs(ms float64) int64 {
	if !(ms < maxDurationMs) { // also true for NaN
		return maxDurationMs
	}
	if ms < -maxDurationMs {
		return -maxDurationMs
	}
	return int64(math.Round(ms))
}

// secondsMs reads a number of seconds ("20.020000"); 0 for anything else.
func secondsMs(s string) int64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0
	}
	return capMs(f * 1000)
}

// clockMs reads "HH:MM:SS.fraction"; 0 for anything else.
func clockMs(s string) int64 {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil || h < 0 || m < 0 || m > 59 || math.IsNaN(sec) || sec < 0 || sec >= 60 {
		return 0
	}
	return capMs((float64(h)*3600 + float64(m)*60 + sec) * 1000)
}

func frameRate(vals ...string) string {
	for _, v := range vals {
		if v != "" && !strings.HasPrefix(v, "0/") {
			return v
		}
	}
	return ""
}

// lookBinary resolves name (or def when empty) to a path, so a missing tool is an error up
// front rather than an exit status that looks like a verdict on the file.
func lookBinary(name, def string) (string, error) {
	if name == "" {
		name = def
	}
	return exec.LookPath(name)
}
