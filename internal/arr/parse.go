package arr

import (
	"context"
	"net/url"
	"strconv"
)

// Parsed is what an app reads in a file or release name (GET /parse?title=, probe C4).
type Parsed struct {
	// OK is false when the app could not parse the name at all (no parsedMovieInfo): then
	// nothing below is known.
	OK bool
	// Quality is the parsed quality; ID 0 ("Unknown") when the name says none.
	Quality Quality
	Source  string // e.g. bluray, webdl, tv, dvd; "unknown" or "" when the name says none
	// Resolution in lines, 0 when the name says none.
	Resolution int
	// Revision is the quality's revision: 1 normally, more for a proper or a repack.
	Revision int
	// Real is the revision's "real" count (REAL releases); 0 normally.
	Real   int
	Repack bool
}

// NameParser is implemented by an app client that can say what it reads in a name. The
// second-version check (#56) uses it: a version's name must not read as a quality.
type NameParser interface {
	Parse(ctx context.Context, title string) (Parsed, error)
}

// MovieFinder is implemented by a Radarr client that can find a film of its library by its
// TMDB id (GET /movie?tmdbId=), with no metadata service: the version keeper (#56) finds a
// film again that was deleted and added back under a new id.
type MovieFinder interface {
	// MovieByTMDB returns the library's film with the TMDB id; ok is false when there is none.
	MovieByTMDB(ctx context.Context, tmdbID int) (m Movie, ok bool, err error)
}

var (
	_ NameParser  = (*radarr)(nil)
	_ MovieFinder = (*radarr)(nil)
)

type parseJSON struct {
	ParsedMovieInfo *struct {
		Quality *struct {
			Quality struct {
				ID         int    `json:"id"`
				Name       string `json:"name"`
				Source     string `json:"source"`
				Resolution int    `json:"resolution"`
			} `json:"quality"`
			Revision struct {
				Version  int  `json:"version"`
				Real     int  `json:"real"`
				IsRepack bool `json:"isRepack"`
			} `json:"revision"`
		} `json:"quality"`
	} `json:"parsedMovieInfo"`
}

// Parse asks Radarr what it reads in title (GET /parse?title=). Give the name without its
// extension: Radarr takes the extension itself for a quality hint (".mkv" reads as
// WEBDL-720p, ".mp4" as SDTV), as it would for a file with no other clue. A name Radarr
// cannot parse at all gives OK false.
func (r *radarr) Parse(ctx context.Context, title string) (Parsed, error) {
	var out parseJSON
	if err := r.get(ctx, "/parse", url.Values{"title": {title}}, &out); err != nil {
		return Parsed{}, err
	}
	var p Parsed
	if out.ParsedMovieInfo == nil {
		return p, nil
	}
	p.OK = true
	if q := out.ParsedMovieInfo.Quality; q != nil {
		p.Quality = Quality{ID: q.Quality.ID, Name: q.Quality.Name}
		p.Source, p.Resolution = q.Quality.Source, q.Quality.Resolution
		p.Revision, p.Real, p.Repack = q.Revision.Version, q.Revision.Real, q.Revision.IsRepack
	}
	return p, nil
}

// MovieByTMDB lists the library's films with the TMDB id (GET /movie?tmdbId=).
func (r *radarr) MovieByTMDB(ctx context.Context, tmdbID int) (Movie, bool, error) {
	var out []movieJSON
	if err := r.get(ctx, "/movie", url.Values{"tmdbId": {strconv.Itoa(tmdbID)}}, &out); err != nil {
		return Movie{}, false, err
	}
	for _, m := range out {
		if m.TMDBID == tmdbID && m.ID != 0 {
			return m.movie(), true, nil
		}
	}
	return Movie{}, false, nil
}
