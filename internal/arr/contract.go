// Package arr talks to Radarr and Sonarr (API v3). Only the web process uses it.
package arr

import (
	"context"
	"encoding/json"
)

type (
	Status  struct{ AppName, Version string }
	Quality struct {
		ID   int
		Name string
	}
	Language struct {
		ID   int
		Name string
	}
	Profile struct {
		ID             int
		Name           string
		MinFormatScore int
	}
	RootFolder struct {
		Path      string
		FreeSpace int64
	}
	Rejection struct {
		Reason    string
		Permanent bool
	}
)

// Row is one file of a Manual Import proposal, for either app.
type Row struct {
	Path              string // as the app sees it
	Size              int64
	MovieID           int // Radarr; 0 until a film is set
	SeriesID          int // Sonarr
	SeasonNumber      int
	EpisodeIDs        []int
	Quality           Quality
	Languages         []Language
	ReleaseGroup      string
	CustomFormats     []string
	CustomFormatScore int
	Rejections        []Rejection
	// Raw is the app's own item. Recheck and Import send it back with the fields above
	// written over it, so what Fukku does not model survives the round trip.
	Raw json.RawMessage
}

// Command is an app command (GET /command/<id>).
type Command struct {
	ID      int
	Name    string
	Status  string // queued, started, completed, failed, aborted, cancelled
	Result  string // successful, unsuccessful, unknown
	Message string
}

// Importer is what the two apps share.
type Importer interface {
	Status(ctx context.Context) (Status, error)
	Profiles(ctx context.Context) ([]Profile, error)
	RootFolders(ctx context.Context) ([]RootFolder, error)
	// Qualities lists every quality the app knows (GET /qualitydefinition), in the app's
	// order: what a quality picker offers.
	Qualities(ctx context.Context) ([]Quality, error)
	// Languages lists every language the app knows (GET /language).
	Languages(ctx context.Context) ([]Language, error)
	// Proposal lists the files under folder (a path as the app sees it):
	// GET /manualimport?folder=&filterExistingFiles=false. It never sends a title id: with
	// one, both apps ignore the folder and scan the title's own (probes C6, C6-sonarr).
	Proposal(ctx context.Context, folder string) ([]Row, error)
	// Recheck sends edited rows back (POST /manualimport) and returns the fresh verdicts.
	Recheck(ctx context.Context, rows []Row) ([]Row, error)
	// Import starts a ManualImport command (mode "move" or "copy") and returns its id.
	Import(ctx context.Context, rows []Row, mode string) (commandID int, err error)
	Command(ctx context.Context, id int) (Command, error)
	// Commands lists recent commands, to find an import whose id was lost in a crash.
	Commands(ctx context.Context) ([]Command, error)
}

type MovieFile struct {
	ID        int
	Path      string // as Radarr sees it
	Size      int64
	Quality   Quality
	Languages []Language
	RuntimeMs int64 // from mediaInfo.runTime; 0 if unknown
}

type Movie struct {
	ID               int // 0: a lookup result that is not in the library
	TMDBID           int
	Title            string
	Year             int
	OriginalLanguage string
	Monitored        bool
	Overview         string
	HasFile          bool       // the app says the film has a file (set on lookup results too)
	Path             string     // the film's folder, as Radarr sees it
	File             *MovieFile // the tracked file (movie.movieFile); nil when there is none
}

type AddMovie struct {
	TMDBID           int
	QualityProfileID int
	RootFolder       string
	Monitored        bool
	Tag              string // created if missing
}

type MovieManager interface {
	Importer
	Lookup(ctx context.Context, term string) ([]Movie, error) // a title, or "tmdb:<id>"
	Movie(ctx context.Context, id int) (Movie, error)
	Add(ctx context.Context, m AddMovie) (Movie, error)  // addOptions.searchForMovie=false
	Busy(ctx context.Context, movieID int) (bool, error) // an active download in the queue
	SetMonitored(ctx context.Context, movieID int, monitored bool) error
}

type Episode struct {
	ID           int
	SeasonNumber int
	Number       int
	Title        string
	RuntimeMs    int64 // the expected runtime; 0 if unknown
	FileID       int   // 0: no file
	Monitored    bool
}

type Series struct {
	ID               int // 0: a lookup result that is not in the library
	TVDBID           int
	Title            string
	Year             int
	OriginalLanguage string
	Overview         string
	Type             string // standard, anime, daily
	Path             string
	Seasons          []int
}

type AddSeries struct {
	TVDBID           int
	QualityProfileID int
	RootFolder       string
	Type             string
	Tag              string
}

// EpisodeFile is a file Sonarr tracks for a show's episodes.
type EpisodeFile struct {
	ID   int
	Path string // as Sonarr sees it
}

type SeriesManager interface {
	Importer
	Lookup(ctx context.Context, term string) ([]Series, error) // a title, or "tvdb:<id>"
	Series(ctx context.Context, id int) (Series, error)
	Add(ctx context.Context, s AddSeries) (Series, error) // search off, nothing monitored
	Episodes(ctx context.Context, seriesID int) ([]Episode, error)
	// EpisodeFiles lists the files Sonarr tracks for the show (GET /episodefile?seriesId=).
	EpisodeFiles(ctx context.Context, seriesID int) ([]EpisodeFile, error)
	Busy(ctx context.Context, seriesID int) (bool, error)
	SetEpisodesMonitored(ctx context.Context, episodeIDs []int, monitored bool) error
}
