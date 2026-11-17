package store

import (
	"context"
	"time"
)

// The second versions of films (#56, decision #20). A version is a file Fukku placed outside
// Radarr's root folders and grouped with the film's file in the media server. Jellyfin loses
// that grouping when Radarr renames or upgrades the film's file (probe B3e), so the record
// outlives the job that placed it: the version keeper checks it on a schedule and groups it
// again. Second versions of episodes (#58) are records too (App "sonarr", with the episode):
// a file next to the episode's own in Sonarr's season folder, which Jellyfin groups by its
// SxxEyy, so the keeper leaves them alone. DeleteJob leaves it (like audit entries); only DeleteVersion removes one, for a
// record of a placement that failed before its file existed.

// Version statuses.
const (
	VersionPlacing     = "placing"      // recorded before the link: the file may not exist yet
	VersionLinked      = "linked"       // the file is in place, not grouped yet
	VersionGrouped     = "grouped"      // the media server lists it as a version of the film's file
	VersionWaiting     = "waiting"      // not grouped now: Detail says why; checked again
	VersionFilmDeleted = "film_deleted" // Radarr no longer has the film; the file is left alone
	VersionMissing     = "missing"      // the version file is no longer on disk
)

// Version is one placed second version.
type Version struct {
	ID      int64
	JobID   string // the job that placed it; the job itself may be gone
	App     string // "radarr", or "sonarr" for an episode's version (#58)
	TitleID int    // Radarr's movie id, or Sonarr's series id
	TMDBID  int    // the film's TMDB id, to find it again under a new movie id; 0: unknown
	Title   string
	Year    int
	Path    string // the version file as Fukku sees it; unique
	Label   string
	Status  string
	Detail  string // why it is waiting, film_deleted or missing; "" otherwise
	// The film's tracked file the version was last grouped with (Radarr's movie-file id and
	// the path as Fukku sees it), and the media server's items of both. Empty until grouped.
	PrimaryFileID int
	PrimaryPath   string
	PrimaryItemID string
	ItemID        string
	CreatedAt     time.Time // zero on the first PutVersion: now
	CheckedAt     time.Time // last check; zero: never
	GroupedAt     time.Time // last time the grouping was (re)applied or seen; zero: never
	// PrimaryChangedAt is when the version was last grouped with another film file than
	// before (Radarr renamed or upgraded it); zero: never.
	PrimaryChangedAt time.Time
	// An episode's version (#58): Sonarr's episode id, and the season and episode its name
	// carries (SxxEyy). 0 for a film's.
	EpisodeID     int
	SeasonNumber  int
	EpisodeNumber int
}

// Versions is the part of Store that #56 adds.
type Versions interface {
	// PutVersion inserts v (ID 0) or replaces the record with v.ID (ErrNotFound when there
	// is none). Path must be set and unique: another record with the same path is ErrExists.
	// A zero CreatedAt becomes now on insert and is kept on replace. The audit entries are
	// stored in the same transaction (an empty JobID becomes v.JobID) and logged, so a
	// status change and its entry are never apart. Returns the record as stored.
	PutVersion(ctx context.Context, v Version, audit ...AuditEntry) (Version, error)
	// DeleteVersion removes the record with the id; ErrNotFound when there is none.
	DeleteVersion(ctx context.Context, id int64) error
	// VersionByPath returns the record for a path; ErrNotFound when there is none.
	VersionByPath(ctx context.Context, path string) (Version, error)
	// VersionsOf lists the records of a job, by ID.
	VersionsOf(ctx context.Context, jobID string) ([]Version, error)
	// AllVersions lists every record, by ID.
	AllVersions(ctx context.Context) ([]Version, error)
}
