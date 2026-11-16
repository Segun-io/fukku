// Package preview renders the sync-check clips of flow 2a (#53, plan §8): short clips of
// the library file's video, at a low resolution, with the upload's audio shifted by an
// offset, so that the user can see and hear whether the dub is in sync before a merge.
// The worker runs it (task kind "preview"); the web process plans the clips (Plan) and
// streams the files the worker wrote, without parsing them.
package preview

import (
	"regexp"
	"strconv"
	"time"
)

// Limits on a task's args. A task that exceeds one is a bad task. The web process keeps
// within them: merge.previewClips is at most MaxClips and merge.previewClipLength at most
// MaxClipLength (config validation), and the API refuses an offset beyond MaxOffsetMs.
const (
	MaxClips      = 10
	MaxClipLength = 2 * time.Minute
	MaxOffsetMs   = 60_000 // ±1 min: a dub further out than that is not a sync problem
)

// Dir is the job-relative directory of the clips, under the job's work/ directory.
const Dir = "work/previews"

// Refusal codes of Output.Refusal: the clips could not be rendered from these files.
const (
	RefusalOutsideLibrary    = "outside_library"    // the library file is not under paths.libraryRoots
	RefusalLibraryUnreadable = "library_unreadable" // the library file cannot be opened
	RefusalUploadUnreadable  = "upload_unreadable"  // the upload cannot be opened
	RefusalRenderFailed      = "render_failed"      // ffmpeg failed (no such audio track, a broken file…)
	RefusalTimeout           = "timeout"            // one clip ran past merge.previewTimeout
	RefusalTooLarge          = "too_large"          // one clip grew past its size cap
)

// Args are the args of a spool task of kind "preview".
//
// Library is the one path that is not job-relative, as in a library probe
// (media.LibraryProbeArgs): the film's library file as Fukku sees it, which the worker
// reads only inside its own paths.libraryRoots, read-only. Upload is job-relative, under
// files/.
//
// OffsetMs shifts the upload's audio as mkvmerge's --sync does: positive delays it (it
// plays later against the video), negative advances it. A dub whose audio starts 800 ms
// late is brought into sync with -800.
type Args struct {
	Request    string `json:"request"`    // [0-9a-f]{16}, chosen by the web process; names the clip files and is echoed in the output
	Library    string `json:"library"`    // e.g. "/data/media/movies/Film (2001)/Film (2001) WEBDL-1080p.mkv"
	Upload     string `json:"upload"`     // e.g. "files/Film.es.mkv"
	AudioIndex int    `json:"audioIndex"` // ffprobe stream index of the upload's audio track
	OffsetMs   int64  `json:"offsetMs"`
	Clips      []Clip `json:"clips"` // 1 to MaxClips, in the library file's time
}

// Clip is one clip of the library file's timeline.
type Clip struct {
	StartMs  int64 `json:"startMs"`
	LengthMs int64 `json:"lengthMs"` // 1 ms to MaxClipLength
}

// Output is the task's output. Request echoes the args, so that the web process can tell
// the result of an earlier request (one with another offset) from the one it waits for.
// When OK, Clips lists every clip, in the order of the args, as written to
// Dir/ClipName(Request, N). A refusal names what failed in Refusal and, for an admin, in
// Detail (ffmpeg's last words; never a path).
type Output struct {
	Request string     `json:"request"`
	OK      bool       `json:"ok"`
	Refusal string     `json:"refusal,omitempty"`
	Detail  string     `json:"detail,omitempty"`
	Clips   []ClipFile `json:"clips,omitempty"`
}

// ClipFile is one rendered clip: an MP4 (H.264 and AAC, fragmented, so that it plays while
// it loads).
type ClipFile struct {
	N        int   `json:"n"` // 1-based
	StartMs  int64 `json:"startMs"`
	LengthMs int64 `json:"lengthMs"`
	Size     int64 `json:"size"` // bytes
}

var requestRE = regexp.MustCompile(`^[0-9a-f]{16}$`)

// ValidRequest reports whether r is a well-formed request id.
func ValidRequest(r string) bool { return requestRE.MatchString(r) }

// ClipName is the file name of clip n (1-based) of a request, inside Dir. The web process
// builds it from its own record, never from what a result says.
func ClipName(request string, n int) string {
	return "clip-" + request + "-" + strconv.Itoa(n) + ".mp4"
}

// ContentType is the media type of a clip.
const ContentType = "video/mp4"
