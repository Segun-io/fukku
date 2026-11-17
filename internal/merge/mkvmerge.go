package merge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/procgroup"
)

const (
	maxIdentifyOutput = 4 << 20  // mkvmerge -J output cap; a hostile file must not fill memory
	maxMuxLog         = 64 << 10 // the last bytes of mkvmerge's messages kept for Detail
	killWait          = 2 * time.Second
)

// mkvmerge reads its files only as /proc/self/fd/<n>: descriptors the worker opened
// through an os.Root, passed as ExtraFiles (3, 4, ...). Reopening such a path gives the same
// file whatever happens to its name meanwhile, so no link or rename can point mkvmerge
// anywhere else, and it is never given a path it could resolve itself.
func fdPath(n int) string { return "/proc/self/fd/" + strconv.Itoa(3+n) }

// Containers mkvmerge may read, by its identification's container type. Playlists (such
// as Blu-ray MPLS files), which name other files, are not among them. Uploads are limited
// to the upload.videoExtensions containers; a library file may also be in one of the
// older containers Radarr imports.
var (
	uploadContainers  = []string{"Matroska", "MP4/QuickTime", "AVI", "MPEG transport stream"}
	libraryContainers = []string{"Matroska", "MP4/QuickTime", "AVI", "MPEG transport stream",
		"MPEG program stream", "Ogg/OGM", "Flash Video"}
)

// identification is the part of `mkvmerge -J` the merge reads.
type identification struct {
	Container struct {
		Recognized bool   `json:"recognized"`
		Supported  bool   `json:"supported"`
		Type       string `json:"type"`
	} `json:"container"`
	Errors []string `json:"errors"`
	Tracks []track  `json:"tracks"`
}

type track struct {
	ID         int    `json:"id"`
	Type       string `json:"type"` // video, audio, subtitles
	Codec      string `json:"codec"`
	Properties struct {
		Language          string `json:"language"`
		LanguageIETF      string `json:"language_ietf"`
		TrackName         string `json:"track_name"`
		DefaultTrack      bool   `json:"default_track"`
		ForcedTrack       bool   `json:"forced_track"`
		PixelDimensions   string `json:"pixel_dimensions"`
		TagNumberOfFrames string `json:"tag_number_of_frames"`
		TagDuration       string `json:"tag_duration"` // statistics: "00:01:00.020000000"
		// StatisticsApp is set when mkvmerge wrote the statistics: its DURATION is then the
		// track's length from its first timestamp. ffmpeg writes a DURATION tag of its own,
		// the time at which the track ends.
		StatisticsApp    string `json:"tag__statistics_writing_app"`
		MinimumTimestamp int64  `json:"minimum_timestamp"` // ns, the track's first timestamp
	} `json:"properties"`
}

// mkvmergeType is the mkvmerge track type of a media track kind.
const (
	typeVideo    = "video"
	typeAudio    = "audio"
	typeSubtitle = "subtitles"
)

func (id identification) ofType(typ string) []track {
	var out []track
	for _, t := range id.Tracks {
		if t.Type == typ {
			out = append(out, t)
		}
	}
	return out
}

// tool runs mkvmerge with the files as descriptors 3, 4, ... and returns its exit code
// and output. stdout is written to out. The process runs in its own group at
// worker.nice, which is killed when ctx ends and whatever the outcome, so nothing outlives
// the call.
type tool struct {
	bin string // mkvmerge, resolved
	opt Options
}

func newTool(opt Options) (tool, error) {
	bin := opt.Mkvmerge
	if bin == "" {
		bin = "mkvmerge"
	}
	bin, err := exec.LookPath(bin)
	if err != nil {
		return tool{}, err // a broken install is an error, never a refusal of the files
	}
	return tool{bin: bin, opt: opt}, nil
}

// memoryExhausted reports whether a failed run of mkvmerge (its exit code, -1 when a signal
// ended it, and its messages) may have run out of memory: it said so ("malloc() returned
// nullptr", bad_alloc), or a signal ended it while worker.toolMemoryLimit was set.
func (t tool) memoryExhausted(exit int, messages string) bool {
	return (exit == -1 && t.opt.MemoryLimit > 0) || media.MemoryMessage(messages)
}

// errNotRun means mkvmerge could not be started or run: not a verdict on the files.
var errNotRun = errors.New("mkvmerge could not be run")

func (t tool) run(ctx context.Context, files []*os.File, out *limitedBuffer, args ...string) (exit int, err error) {
	// The UI language keeps the messages in Detail stable; the charsets make a track name
	// such as "Español (Latino)" arrive and be written as UTF-8 whatever the locale.
	args = append([]string{"--ui-language", "en_US", "--command-line-charset", "UTF-8", "--output-charset", "UTF-8"}, args...)
	name, args, err := media.Wrap(t.opt.Nice, t.opt.MemoryLimit, t.bin, args)
	if err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the binary is configuration, the arguments are built here
	cmd.SysProcAttr = sysProcAttr()
	g := procgroup.New(cmd)
	cmd.WaitDelay = killWait
	cmd.ExtraFiles = files
	// No MKVTOOLNIX_OPTIONS or other settings from the worker's environment.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent"}
	cmd.Stdout = out
	cmd.Stderr = out
	err = g.Run()
	if lerr := media.LimitFailure(out.buf.String()); lerr != nil && err != nil {
		return 0, lerr // prlimit could not set the limit: the deployment, not the files
	}
	if cerr := ctx.Err(); cerr != nil {
		return 0, cerr
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee) && (ee.ExitCode() == 126 || ee.ExitCode() == 127):
		// nice or prlimit could not run mkvmerge: not a verdict on the files
		return 0, fmt.Errorf("%w (exit %d)", errNotRun, ee.ExitCode())
	case errors.As(err, &ee):
		return ee.ExitCode(), nil // -1 when a signal ended it
	case errors.Is(err, errOutputTooLarge):
		return -1, nil
	}
	return 0, fmt.Errorf("%w: %w", errNotRun, err)
}

// identify runs `mkvmerge -J` on f within worker.probeTimeout. ok is false when mkvmerge
// does not recognise or support the file, or its type is not in allowed; why says which.
func (t tool) identify(ctx context.Context, f *os.File, allowed []string) (id identification, why string, err error) {
	ictx, cancel := context.WithTimeout(ctx, t.opt.IdentifyTimeout)
	defer cancel()
	out := &limitedBuffer{max: maxIdentifyOutput}
	exit, err := t.run(ictx, []*os.File{f}, out, "--identification-format", "json", "--identify", fdPath(0))
	if err != nil {
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			return id, "mkvmerge did not identify it within worker.probeTimeout", nil
		}
		return id, "", err
	}
	if exit != 0 && exit != 1 {
		return id, "mkvmerge cannot read it (identification exit " + strconv.Itoa(exit) + ")", nil
	}
	if err := json.Unmarshal(out.buf.Bytes(), &id); err != nil {
		return id, "mkvmerge's identification is unreadable", nil
	}
	switch {
	case !id.Container.Recognized || !id.Container.Supported:
		return id, "mkvmerge does not recognise its format", nil
	case len(id.Errors) > 0:
		return id, "mkvmerge: " + strings.Join(id.Errors, "; "), nil
	case !containsFold(allowed, id.Container.Type):
		return id, fmt.Sprintf("its container %q is not one mkvmerge may read here", id.Container.Type), nil
	}
	return id, "", nil
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// limitedBuffer keeps at most max bytes. With tail set it keeps the last ones and never
// fails (mkvmerge's messages); otherwise a write past max fails (an identification).
type limitedBuffer struct {
	buf  bytes.Buffer
	max  int
	tail bool
}

var errOutputTooLarge = errors.New("mkvmerge output too large")

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) <= l.max {
		return l.buf.Write(p)
	}
	if !l.tail {
		return 0, errOutputTooLarge
	}
	all := append(l.buf.Bytes(), p...)
	l.buf.Reset()
	l.buf.Write(all[max(0, len(all)-l.max):])
	return len(p), nil
}

// muxMessages picks what Detail says of a failed mux: mkvmerge's warnings and errors (at
// most n, the first ones), or when it printed none, its last n lines. Progress lines are
// dropped; the lines are joined by "; ".
func muxMessages(s string, n int) string {
	var all, problems []string
	for line := range strings.SplitSeq(strings.ReplaceAll(s, "\r", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Progress:") {
			continue
		}
		all = append(all, line)
		if strings.HasPrefix(line, "Warning:") || strings.HasPrefix(line, "Error:") {
			problems = append(problems, line)
		}
	}
	if len(problems) > 0 {
		return strings.Join(problems[:min(n, len(problems))], "; ")
	}
	return strings.Join(all[max(0, len(all)-n):], "; ")
}

// clockMs reads mkvmerge's "HH:MM:SS.fraction" in ms; ok is false for anything else.
func clockMs(s string) (int64, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil || h < 0 || h > 1000 || m < 0 || m > 59 || !(sec >= 0 && sec < 60) {
		return 0, false
	}
	return int64(h)*3600_000 + int64(m)*60_000 + int64(sec*1000+0.5), true
}
