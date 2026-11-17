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
// SxxEyy; the keeper re-checks them against Sonarr (#96), which can adopt them. DeleteJob
// leaves a record (like audit entries); only DeleteVersion removes one, for a record of a
// placement that failed before its file existed.

// Version statuses.
const (
	VersionPlacing     = "placing"      // recorded before the link: the file may not exist yet
	VersionLinked      = "linked"       // the file is in place, not grouped yet
	VersionGrouped     = "grouped"      // the media server lists it as a version of the film's file
	VersionWaiting     = "waiting"      // not grouped now: Detail says why; checked again
	VersionFilmDeleted = "film_deleted" // Radarr no longer has the film; the file is left alone
	VersionMissing     = "missing"      // the version file is no longer on disk
)

// Statuses of episodes' versions only (#96): what the keeper's re-check found in Sonarr. Each
// is a notice: Fukku never deletes, moves or renames a library file for them.
const (
	// VersionAdopted: Sonarr tracks the version as an episode's file now. It renames, upgrades
	// and deletes it as its own.
	VersionAdopted = "adopted"
	// VersionAdoptable: Sonarr's verdict on the version no longer rejects it as not an upgrade
	// for the episode's file (a worse file replaced it, or the quality profile or its custom
	// formats changed): its next rescan takes the version for the episode's file.
	VersionAdoptable = "adoptable"
	// VersionEpisodeFileMissing: the episode's own file is gone (Sonarr lists none, or its file
	// is not on disk): Sonarr's next rescan takes the version for the episode's file.
	VersionEpisodeFileMissing = "episode_file_missing"
	// VersionSeriesMoved: Sonarr's folder for the series is elsewhere, and the version was not
	// found next to the episode's file there.
	VersionSeriesMoved = "series_moved"
	// VersionSeriesDeleted: Sonarr no longer has the series (or the episode); the file, if
	// still there, is left alone.
	VersionSeriesDeleted = "series_deleted"
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
	Detail  string // why it is waiting, film_deleted, missing or an episode's notice status; "" otherwise
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
	// TVDBID is the series' TVDB id (#96), to tell Sonarr's series id answering with another
	// show and to find the series again under a new id; 0: unknown (and for films).
	TVDBID int
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
