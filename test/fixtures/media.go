// Package fixtures generates test media and hostile archives at test time (plan §14). Nothing
// it produces is committed. Media generation needs ffmpeg and ffprobe (in the dev image).
package fixtures

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Audio is one generated audio track. Each track beeps once a second at its own pitch, in
// step with a white flash in the video, so sync can be judged by eye and ear.
type Audio struct {
	Language string // ISO 639-2, e.g. eng, spa
	Title    string // track name, e.g. "Español (Latino)"
	ToneHz   int    // 0 means 880
	Default  bool
}

// Subtitle is one generated subtitle track (SRT text, one cue per 5 s).
type Subtitle struct {
	Language string
	Title    string
	Forced   bool
}

// Video describes a generated test file.
type Video struct {
	Duration  time.Duration
	Width     int    // 0 means 640
	Height    int    // 0 means 360
	FrameRate string // ffmpeg rate, e.g. 24000/1001 (the default), 25
	Audio     []Audio
	Subtitles []Subtitle
	Title     string // container title tag
}

func (v Video) withDefaults() Video {
	if v.Width == 0 {
		v.Width = 640
	}
	if v.Height == 0 {
		v.Height = 360
	}
	if v.FrameRate == "" {
		v.FrameRate = "24000/1001"
	}
	if v.Duration == 0 {
		v.Duration = 10 * time.Second
	}
	return v
}

func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}

// Generate writes a video to path. The container follows the extension (.mkv, .mp4, ...).
func Generate(ctx context.Context, path string, spec Video) error {
	spec = spec.withDefaults()
	dur := seconds(spec.Duration)
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf(
			"testsrc2=size=%dx%d:rate=%s:duration=%s,drawbox=x=0:y=0:w=iw:h=ih/6:color=white:t=fill:enable='lt(mod(t\\,1)\\,0.1)'",
			spec.Width, spec.Height, spec.FrameRate, dur),
	}
	for _, a := range spec.Audio {
		hz := a.ToneHz
		if hz == 0 {
			hz = 880
		}
		args = append(args, "-f", "lavfi", "-i", fmt.Sprintf(
			"sine=frequency=%d:sample_rate=48000:duration=%s,volume='if(lt(mod(t\\,1)\\,0.1)\\,0.8\\,0)':eval=frame", hz, dur))
	}
	var srtFiles []string
	for i, s := range spec.Subtitles {
		f := filepath.Join(filepath.Dir(path), fmt.Sprintf(".sub-%d-%s.srt", i, filepath.Base(path)))
		if err := os.WriteFile(f, []byte(SRT(spec.Duration, s.Language)), 0o600); err != nil {
			return err
		}
		srtFiles = append(srtFiles, f)
		args = append(args, "-i", f)
	}
	defer func() {
		for _, f := range srtFiles {
			_ = os.Remove(f)
		}
	}()

	args = append(args, "-map", "0:v")
	for i := range spec.Audio {
		args = append(args, "-map", fmt.Sprintf("%d:a", i+1))
	}
	for i := range spec.Subtitles {
		args = append(args, "-map", fmt.Sprintf("%d:s", len(spec.Audio)+1+i))
	}
	args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-crf", "38", "-pix_fmt", "yuv420p", "-g", "48")
	if len(spec.Audio) > 0 {
		args = append(args, "-c:a", "aac", "-b:a", "48k", "-ac", "1")
	}
	if len(spec.Subtitles) > 0 {
		sc := "srt"
		if strings.EqualFold(filepath.Ext(path), ".mp4") {
			sc = "mov_text"
		}
		args = append(args, "-c:s", sc)
	}
	for i, a := range spec.Audio {
		args = append(args, fmt.Sprintf("-metadata:s:a:%d", i), "language="+a.Language)
		if a.Title != "" {
			args = append(args, fmt.Sprintf("-metadata:s:a:%d", i), "title="+a.Title)
		}
		disp := "0"
		if a.Default {
			disp = "default"
		}
		args = append(args, fmt.Sprintf("-disposition:a:%d", i), disp)
	}
	for i, s := range spec.Subtitles {
		args = append(args, fmt.Sprintf("-metadata:s:s:%d", i), "language="+s.Language)
		if s.Title != "" {
			args = append(args, fmt.Sprintf("-metadata:s:s:%d", i), "title="+s.Title)
		}
		disp := "0"
		if s.Forced {
			disp = "forced"
		}
		args = append(args, fmt.Sprintf("-disposition:s:%d", i), disp)
	}
	if spec.Title != "" {
		args = append(args, "-metadata", "title="+spec.Title)
	}
	args = append(args, "-t", dur, path)
	return run(ctx, "ffmpeg", args...)
}

// PALSpeedup writes a copy of src played at 25/23.976 speed with the audio tempo raised to
// match, as a PAL broadcast would be. The result is about 4.1% shorter.
func PALSpeedup(ctx context.Context, src, dst string) error {
	const ratio = 25.0 / (24000.0 / 1001.0)
	return run(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", src,
		"-filter_complex", fmt.Sprintf("[0:v]setpts=PTS/%[1]f,fps=25[v];[0:a]atempo=%[1]f[a]", ratio),
		"-map", "[v]", "-map", "[a]", "-map_metadata", "0", "-map_metadata:s:a", "0:s:a",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "38", "-c:a", "aac", "-b:a", "48k", dst)
}

// ShiftAudio writes a copy of src whose audio starts offset later than the video
// (negative offsets start it earlier). Streams are copied, not re-encoded.
func ShiftAudio(ctx context.Context, src, dst string, offset time.Duration) error {
	return run(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-i", src, "-itsoffset", seconds(offset), "-i", src,
		"-map", "0:v", "-map", "1:a", "-map_metadata", "0", "-map_metadata:s:a", "1:s:a",
		"-c", "copy", dst)
}

// Trim writes a different cut of src: the part from start to end only, re-encoded.
func Trim(ctx context.Context, src, dst string, start, end time.Duration) error {
	return run(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-ss", seconds(start), "-to", seconds(end), "-i", src,
		"-map", "0", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "38", "-c:a", "aac", "-b:a", "48k", "-c:s", "copy", dst)
}

// Broken writes a file that starts like src but is cut short and then overwritten with
// noise, so no tool can read a duration or a video stream from it.
func Broken(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	keep := min(len(data), 512)
	out := append([]byte(nil), data[:keep]...)
	noise := bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef}, 2048)
	out = append(out, noise...)
	// Corrupt the EBML/MP4 header magic too.
	for i := 0; i < min(16, len(out)); i++ {
		out[i] ^= 0xff
	}
	return os.WriteFile(dst, out, 0o600)
}

// SRT returns a subtitle file with one numbered cue every 5 s.
func SRT(d time.Duration, lang string) string {
	var b strings.Builder
	n := 1
	for t := time.Duration(0); t < d; t += 5 * time.Second {
		end := min(t+2*time.Second, d)
		fmt.Fprintf(&b, "%d\n%s --> %s\n[%s] cue %d\n\n", n, srtTime(t), srtTime(end), lang, n)
		n++
	}
	return b.String()
}

func srtTime(d time.Duration) string {
	h := d / time.Hour
	m := d % time.Hour / time.Minute
	s := d % time.Minute / time.Second
	ms := d % time.Second / time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// ProbeResult is the part of ffprobe's output the tests look at.
type ProbeResult struct {
	Duration time.Duration
	Streams  []ProbeStream
}

type ProbeStream struct {
	Type     string // video, audio, subtitle
	Codec    string
	Language string
	Title    string
	Width    int
	Height   int
	Default  bool
	Forced   bool
}

// Count returns how many streams of the type there are.
func (p ProbeResult) Count(typ string) int {
	n := 0
	for _, s := range p.Streams {
		if s.Type == typ {
			n++
		}
	}
	return n
}

// Probe runs ffprobe on path.
func Probe(ctx context.Context, path string) (ProbeResult, error) {
	out, err := output(ctx, "ffprobe", "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	if err != nil {
		return ProbeResult{}, err
	}
	var raw struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType   string            `json:"codec_type"`
			CodecName   string            `json:"codec_name"`
			Width       int               `json:"width"`
			Height      int               `json:"height"`
			Tags        map[string]string `json:"tags"`
			Disposition map[string]int    `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return ProbeResult{}, err
	}
	var res ProbeResult
	if raw.Format.Duration != "" {
		f, err := strconv.ParseFloat(raw.Format.Duration, 64)
		if err != nil {
			return ProbeResult{}, fmt.Errorf("duration %q: %w", raw.Format.Duration, err)
		}
		res.Duration = time.Duration(f * float64(time.Second))
	}
	for _, s := range raw.Streams {
		res.Streams = append(res.Streams, ProbeStream{
			Type: s.CodecType, Codec: s.CodecName, Width: s.Width, Height: s.Height,
			Language: s.Tags["language"], Title: firstNonEmpty(s.Tags["title"], s.Tags["handler_name"]),
			Default: s.Disposition["default"] == 1, Forced: s.Disposition["forced"] == 1,
		})
	}
	return res, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func run(ctx context.Context, name string, args ...string) error {
	_, err := output(ctx, name, args...)
	return err
}

func output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}
	return out, nil
}
