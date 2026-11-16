package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/config"
)

// radarr is the MovieManager. The Importer methods come from the shared client.
type radarr struct {
	*client
	minimumAvailability string
}

// NewRadarr returns a MovieManager for the Radarr instance in cfg.
func NewRadarr(cfg config.Arr) MovieManager {
	ma := cfg.MinimumAvailability
	if ma == "" {
		ma = "released"
	}
	return &radarr{client: newClient("radarr", cfg), minimumAvailability: ma}
}

type movieFileJSON struct {
	ID           int            `json:"id"`
	Path         string         `json:"path"`
	RelativePath string         `json:"relativePath"`
	Size         int64          `json:"size"`
	Quality      qualityJSON    `json:"quality"`
	Languages    []languageJSON `json:"languages"`
	MediaInfo    *struct {
		RunTime json.RawMessage `json:"runTime"`
	} `json:"mediaInfo"`
}

type movieJSON struct {
	ID               int            `json:"id"`
	TMDBID           int            `json:"tmdbId"`
	Title            string         `json:"title"`
	Year             int            `json:"year"`
	OriginalLanguage *languageJSON  `json:"originalLanguage"`
	Monitored        bool           `json:"monitored"`
	Overview         string         `json:"overview"`
	HasFile          bool           `json:"hasFile"`
	Path             string         `json:"path"`
	MovieFile        *movieFileJSON `json:"movieFile"`
}

func (j movieJSON) movie() Movie {
	m := Movie{ID: j.ID, TMDBID: j.TMDBID, Title: j.Title, Year: j.Year, Monitored: j.Monitored, Overview: j.Overview, HasFile: j.HasFile, Path: j.Path}
	if j.OriginalLanguage != nil {
		m.OriginalLanguage = j.OriginalLanguage.Name
	}
	if f := j.MovieFile; f != nil {
		mf := &MovieFile{
			ID: f.ID, Path: f.Path, Size: f.Size,
			Quality: Quality{ID: f.Quality.Quality.ID, Name: f.Quality.Quality.Name},
		}
		if mf.Path == "" && f.RelativePath != "" {
			mf.Path = path.Join(j.Path, f.RelativePath)
		}
		for _, l := range f.Languages {
			mf.Languages = append(mf.Languages, Language(l))
		}
		if f.MediaInfo != nil {
			mf.RuntimeMs = parseRunTime(f.MediaInfo.RunTime)
		}
		m.File = mf
	}
	return m
}

// parseRunTime reads mediaInfo.runTime: a time span ("9:00", "1:23:45", "00:09:00.5",
// "1.02:03:04") or, defensively, a number of seconds. Anything else is 0 (unknown).
func parseRunTime(raw json.RawMessage) int64 {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		var secs float64
		if json.Unmarshal(raw, &secs) == nil && secs > 0 {
			return int64(secs * 1000)
		}
		return 0
	}
	days := 0
	if i := strings.IndexByte(s, '.'); i >= 0 && i < strings.IndexByte(s, ':') {
		d, err := strconv.Atoi(s[:i])
		if err != nil {
			return 0
		}
		days, s = d, s[i+1:]
	}
	parts := strings.Split(s, ":")
	if len(parts) == 2 { // Radarr 6.4.4 writes "9:00" below an hour, "1:23:45" above
		parts = append([]string{"0"}, parts...)
	}
	if len(parts) != 3 {
		return 0
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0
	}
	d := time.Duration(days)*24*time.Hour + time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec*float64(time.Second))
	return d.Milliseconds()
}

func (r *radarr) Lookup(ctx context.Context, term string) ([]Movie, error) {
	var out []movieJSON
	if err := r.get(ctx, "/movie/lookup", url.Values{"term": {term}}, &out); err != nil {
		return nil, err
	}
	ms := make([]Movie, 0, len(out))
	for _, j := range out {
		ms = append(ms, j.movie())
	}
	return ms, nil
}

// Movie returns the film with its tracked file taken from movie.movieFile: the first entry
// of /moviefile?movieId= is not necessarily the tracked file (probe B3).
func (r *radarr) Movie(ctx context.Context, id int) (Movie, error) {
	var out movieJSON
	if err := r.get(ctx, fmt.Sprintf("/movie/%d", id), nil, &out); err != nil {
		return Movie{}, err
	}
	return out.movie(), nil
}

func (r *radarr) Add(ctx context.Context, m AddMovie) (Movie, error) {
	// The lookup by id returns the complete record that POST /movie expects.
	var body map[string]json.RawMessage
	if err := r.get(ctx, "/movie/lookup/tmdb", url.Values{"tmdbId": {strconv.Itoa(m.TMDBID)}}, &body); err != nil {
		return Movie{}, err
	}
	tags := []int{}
	if m.Tag != "" {
		id, err := r.ensureTag(ctx, m.Tag)
		if err != nil {
			return Movie{}, err
		}
		tags = append(tags, id)
	}
	set := map[string]any{
		"qualityProfileId":    m.QualityProfileID,
		"rootFolderPath":      m.RootFolder,
		"monitored":           m.Monitored,
		"minimumAvailability": r.minimumAvailability,
		"tags":                tags,
		// monitor "movieOnly" says: this film, not the collection it belongs to. It is not
		// a deployment choice, so it is not a setting.
		"addOptions": map[string]any{"searchForMovie": false, "monitor": "movieOnly"},
	}
	for k, v := range set {
		data, err := json.Marshal(v)
		if err != nil {
			return Movie{}, err
		}
		body[k] = data
	}
	var out movieJSON
	if err := r.post(ctx, "/movie", body, &out); err != nil {
		return Movie{}, err
	}
	return out.movie(), nil
}

// Busy reports an active download for the film (GET /queue/details?movieId=).
func (r *radarr) Busy(ctx context.Context, movieID int) (bool, error) {
	var out []json.RawMessage
	if err := r.get(ctx, "/queue/details", url.Values{"movieId": {strconv.Itoa(movieID)}}, &out); err != nil {
		return false, err
	}
	return len(out) > 0, nil
}

func (r *radarr) SetMonitored(ctx context.Context, movieID int, monitored bool) error {
	body := map[string]any{"movieIds": []int{movieID}, "monitored": monitored}
	return r.put(ctx, "/movie/editor", body, nil)
}
