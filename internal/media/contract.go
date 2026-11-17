// Package media probes media files with ffprobe (worker only).
package media

// Probe is what Fukku keeps about one video file. Field names are the JSON the API returns.
type Probe struct {
	Container  string          `json:"container"`  // ffprobe format_name, e.g. "matroska,webm"
	DurationMs int64           `json:"durationMs"` // > 0 for every accepted file
	Size       int64           `json:"size"`       // bytes
	BitRate    int64           `json:"bitRate,omitempty"`
	Video      []VideoTrack    `json:"video"` // at least one for every accepted file
	Audio      []AudioTrack    `json:"audio"`
	Subtitles  []SubtitleTrack `json:"subtitles"`
}

// VideoTrack is one video stream.
type VideoTrack struct {
	Index     int    `json:"index"` // ffprobe stream index
	Codec     string `json:"codec"` // e.g. h264, hevc
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	FrameRate string `json:"frameRate,omitempty"` // as a fraction, e.g. "24000/1001"
	Default   bool   `json:"default"`
	// DurationMs is the stream's own duration (ffprobe's stream duration, or the Matroska
	// DURATION tag), 0 when the file does not say. Unlike the container's duration it does
	// not grow when an audio track starts late, so flow 2 compares cuts with it (#52).
	DurationMs int64 `json:"durationMs,omitempty"`
}

// AudioTrack is one audio stream.
type AudioTrack struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Language string `json:"language,omitempty"` // as tagged in the file (e.g. "eng", "spa", "es-419"); empty if untagged
	Title    string `json:"title,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Default  bool   `json:"default"`
}

// SubtitleTrack is one subtitle stream.
type SubtitleTrack struct {
	Index           int    `json:"index"`
	Codec           string `json:"codec"` // e.g. subrip, ass, hdmv_pgs_subtitle
	Language        string `json:"language,omitempty"`
	Title           string `json:"title,omitempty"`
	Default         bool   `json:"default"`
	Forced          bool   `json:"forced"`
	HearingImpaired bool   `json:"hearingImpaired"`
}

// RefusalOutsideLibrary is the refusal of a library probe whose path is not a clean path
// inside one of paths.libraryRoots (LibraryProbedFile.Refusal).
const RefusalOutsideLibrary = "outside_library"

// LibraryProbeArgs are the args of a spool task of kind "libraryProbe" (#52): the probe of
// a file in the library, which flow 2 compares with the upload. Unlike every other task's,
// the path is not job-relative: it is the library file as Fukku sees it (absolute, after
// paths.mappings.radarr), and the worker reads it only when it lies inside one of its own
// paths.libraryRoots, through an os.Root of that root, read-only.
type LibraryProbeArgs struct {
	Path string `json:"path"` // e.g. "/data/media/movies/Film (2001)/Film (2001) WEBDL-1080p.mkv"
	// FileID and TrackedSize are Radarr's movie-file id and size of the file at the match.
	// The worker only echoes them, so the web process can drop the result of an earlier
	// match whose path is the same but whose file is not.
	FileID      int   `json:"fileId,omitempty"`
	TrackedSize int64 `json:"trackedSize,omitempty"`
}

// LibraryProbeOutput is its output. Path echoes the args, so the web process can tell a
// result for an earlier library file (the user chose another title meanwhile) from one for
// the file it asked about.
type LibraryProbeOutput struct {
	Path        string `json:"path"`
	FileID      int    `json:"fileId,omitempty"`      // echoes the args
	TrackedSize int64  `json:"trackedSize,omitempty"` // echoes the args
	OK          bool   `json:"ok"`
	Refusal     string `json:"refusal,omitempty"` // outside_library, unreadable, no_video_stream, no_duration, timeout, too_many_streams
	Media       *Probe `json:"media,omitempty"`   // when OK
}

// ProbeArgs are the args of a spool task of kind "probe".
type ProbeArgs struct {
	Paths []string `json:"paths"` // job-relative, e.g. "files/Film (2001).mkv"
}

// ProbeOutput is its output: one entry per path, in the same order.
type ProbeOutput struct {
	Files []ProbedFile `json:"files"`
}

// ProbedFile is the result for one path.
type ProbedFile struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`              // "video" or "subtitle", from the extension
	OK      bool   `json:"ok"`                // passed the content check
	Refusal string `json:"refusal,omitempty"` // when not OK: extension, no_video_stream, no_duration, not_text, unreadable, timeout, too_many_streams, result_too_large
	Media   *Probe `json:"media,omitempty"`   // for an accepted video
}
