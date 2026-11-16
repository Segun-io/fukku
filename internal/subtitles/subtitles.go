// Package subtitles hands the subtitle files of an upload to the subtitle manager (Bazarr)
// after the import, because Radarr and Sonarr do not import them (plan §6, Subtitles;
// decision #23). Only the web process uses it.
package subtitles

import (
	"context"
	"errors"
	"io"
)

// Upload is one subtitle file for one film or episode.
type Upload struct {
	App       string // "radarr" or "sonarr"
	TitleID   int    // movie id, or series id
	EpisodeID int    // 0 for a film
	Language  string // the subtitle manager's language code, e.g. "es"
	Forced    bool
	HI        bool // hearing impaired
	Name      string
	Content   io.Reader
}

// SubtitleManager is what Fukku needs from a subtitle manager.
type SubtitleManager interface {
	Version(ctx context.Context) (string, error)
	// Upload sends the file. A nil error means the manager accepted it, not that it wrote it.
	Upload(ctx context.Context, u Upload) error
	// Written reports whether the manager's history shows the upload as written. It is
	// only meaningful after an Upload of the same file (same title, language, flags and
	// name): the history is compared with how it looked just before that Upload.
	Written(ctx context.Context, u Upload) (bool, error)
}

// ErrUnknownTitle means the manager does not know the film or episode (yet). Bazarr
// stores a title once it has a file, so right after an import this can pass.
var ErrUnknownTitle = errors.New("subtitles: the subtitle manager does not know the title")
