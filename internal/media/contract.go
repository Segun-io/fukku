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
	Refusal string `json:"refusal,omitempty"` // when not OK: extension, no_video_stream, no_duration, not_text, unreadable, timeout
	Media   *Probe `json:"media,omitempty"`   // for an accepted video
}
