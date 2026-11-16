// Package merge adds the new tracks of an upload to a copy of a film's library file with
// mkvmerge and verifies the copy before it is offered (flow 2a, plan §8; worker only).
//
// The library file is never written. mkvmerge reads it through a read-only handle opened
// inside one of the worker's own paths.libraryRoots (as the library probe of #52 does)
// and writes a new file in the job directory: first a partial file under work/, which is
// renamed to its final name under merged/ only after verification. A failed, refused or
// cancelled run removes the partial file and leaves no output, and a run is safe to repeat:
// it starts by removing what an earlier run of the same task left behind.
package merge

import (
	"time"

	"github.com/segun-io/fukku/internal/media"
)

// Args are the args of a spool task of kind "merge" (spool.KindMerge).
//
// Like the library probe's, Library is not job-relative: it is the library file as Fukku
// sees it (absolute, after paths.mappings.radarr), and the worker reads it only when it
// lies inside one of its own paths.libraryRoots. Every other path is job-relative.
type Args struct {
	Library string `json:"library"` // e.g. "/data/media/movies/Film (2001)/Film (2001) WEBDL-1080p.mkv"
	Upload  string `json:"upload"`  // under files/, e.g. "files/Film.es.mkv"
	// Tracks are the upload's tracks to add, by ffprobe stream index (media.AudioTrack.Index,
	// media.SubtitleTrack.Index of the upload's probe): audio and subtitle tracks only, at
	// least one, each once. They are added in the upload's order, after the library file's.
	Tracks []int `json:"tracks"`
	// OffsetMs shifts every added track (mkvmerge --sync): positive starts it later, negative
	// earlier. It is the offset the user chose after the preview clips.
	OffsetMs int64 `json:"offsetMs,omitempty"`
	// LanguageTag and TrackName are given to every added track. Empty means the worker's
	// merge.languageTag and merge.trackName. The tag must be BCP 47 in its canonical form
	// (config.CanonicalLanguageTag), the one mkvmerge keeps. A name that is still empty after
	// the default leaves the added tracks unnamed: their names in the upload are cleared.
	LanguageTag string `json:"languageTag,omitempty"`
	TrackName   string `json:"trackName,omitempty"`
	// KeepOriginalDefault: the library file's audio keeps its default flag and no added
	// track is default. When false, the first added audio track becomes the default and the
	// library file's audio tracks lose the flag. Absent means merge.keepOriginalDefault.
	KeepOriginalDefault *bool `json:"keepOriginalDefault,omitempty"`
	// Dest is the job-relative directory the verified file is renamed into: merged/<name>,
	// the name of lower-case letters, digits and "-" (ValidDest), e.g. "merged/1" for the
	// job's first attempt. It belongs to this merge, and a run removes it before it starts.
	// The job's merged/ holds only merge output (no upload or extraction writes there, they
	// write under files/), and its directories are never inside one another, so no merge
	// can remove another's output or a user's file. One merge into a dest runs at a time; a
	// second one is refused at once (busy).
	Dest string `json:"dest"`
}

// The merge writes work/.merge-<h>.partial.mkv and holds the lock work/.merge-<h>.lock
// while it runs, h derived from Dest (PartialFor). Lock files stay until the job directory
// is removed.

// Output is its output. A refusal is a normal output (OK false, the task's status ok);
// the task fails only when it could not run (cancelled, timed out, an I/O error, a missing
// tool).
type Output struct {
	OK      bool   `json:"ok"`
	Refusal string `json:"refusal,omitempty"` // when not OK, one of the Refusal codes
	Detail  string `json:"detail,omitempty"`  // when not OK: what failed, for the job's error text
	// Path is the verified file, job-relative: <Dest>/<OutputName(Library)>. Its name follows
	// the library file's, so Radarr parses the same quality from it.
	Path  string       `json:"path,omitempty"`
	Size  int64        `json:"size,omitempty"`
	Media *media.Probe `json:"media,omitempty"` // the verified file's probe
	// Library is the library file as it was read: the same before and after the mux, or the
	// run is refused (library_changed). The web process can compare it with what Radarr
	// tracks before it imports the result.
	Library *LibraryFile `json:"library,omitempty"`
	// What the added tracks were given, after the defaults: the language tag, the name and
	// whether the library file's audio stayed the default. Set when the args were usable.
	LanguageTag         string `json:"languageTag,omitempty"`
	TrackName           string `json:"trackName"`
	KeepOriginalDefault bool   `json:"keepOriginalDefault"`
}

// LibraryFile describes the library file a merge read.
type LibraryFile struct {
	Path    string    `json:"path"` // echoes Args.Library
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// Refusal codes (Output.Refusal).
const (
	RefusalOutsideLibrary    = media.RefusalOutsideLibrary // the library path is not a clean path inside paths.libraryRoots
	RefusalLibraryUnreadable = "library_unreadable"        // the library file cannot be opened, probed or read by mkvmerge
	RefusalUploadUnreadable  = "upload_unreadable"         // the upload cannot be opened, probed or read by mkvmerge
	RefusalTrackNotFound     = "track_not_found"           // a chosen index is not an audio or subtitle track of the upload
	RefusalFreeSpace         = "free_space"                // not enough room for the output plus upload.freeSpaceMargin
	RefusalMuxFailed         = "mux_failed"                // mkvmerge did not exit with 0 (warnings included)
	RefusalLibraryChanged    = "library_changed"           // the library file changed while it was read
	RefusalVerifyFailed      = "verify_failed"             // the output failed a verification rule (Detail says which)
	RefusalBusy              = "busy"                      // another merge into the same dest is running
)
