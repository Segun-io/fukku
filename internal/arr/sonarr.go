package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/segun-io/fukku/internal/config"
)

// sonarr is the SeriesManager. The Importer methods come from the shared client.
type sonarr struct {
	*client
}

// NewSonarr returns a SeriesManager for the Sonarr instance in cfg.
func NewSonarr(cfg config.Arr) SeriesManager {
	return &sonarr{newClient("sonarr", cfg)}
}

type seriesJSON struct {
	ID               int           `json:"id"`
	TVDBID           int           `json:"tvdbId"`
	Title            string        `json:"title"`
	Year             int           `json:"year"`
	Overview         string        `json:"overview"`
	OriginalLanguage *languageJSON `json:"originalLanguage"`
	QualityProfileID int           `json:"qualityProfileId"`
	SeriesType       string        `json:"seriesType"`
	Path             string        `json:"path"`
	Seasons          []struct {
		SeasonNumber int `json:"seasonNumber"`
	} `json:"seasons"`
}

func (j seriesJSON) series() Series {
	s := Series{ID: j.ID, TVDBID: j.TVDBID, Title: j.Title, Year: j.Year, Overview: j.Overview, Type: j.SeriesType, Path: j.Path, QualityProfileID: j.QualityProfileID}
	if j.OriginalLanguage != nil {
		s.OriginalLanguage = j.OriginalLanguage.Name
	}
	for _, x := range j.Seasons {
		s.Seasons = append(s.Seasons, x.SeasonNumber)
	}
	return s
}

// Lookup returns what Sonarr finds for a title or "tvdb:<id>". It may miss original-language
// titles that Radarr finds; the caller offers English-title and id search (plan section 6).
func (s *sonarr) Lookup(ctx context.Context, term string) ([]Series, error) {
	var out []seriesJSON
	if err := s.get(ctx, "/series/lookup", url.Values{"term": {term}}, &out); err != nil {
		return nil, err
	}
	res := make([]Series, 0, len(out))
	for _, j := range out {
		res = append(res, j.series())
	}
	return res, nil
}

func (s *sonarr) Series(ctx context.Context, id int) (Series, error) {
	var out seriesJSON
	if err := s.get(ctx, fmt.Sprintf("/series/%d", id), nil, &out); err != nil {
		return Series{}, err
	}
	return out.series(), nil
}

// Add puts the show in the library with search off and nothing monitored, so Sonarr goes
// looking for no episode (flow 3). The lookup by id returns the complete record that
// POST /series expects.
func (s *sonarr) Add(ctx context.Context, a AddSeries) (Series, error) {
	var found []map[string]json.RawMessage
	if err := s.get(ctx, "/series/lookup", url.Values{"term": {"tvdb:" + strconv.Itoa(a.TVDBID)}}, &found); err != nil {
		return Series{}, err
	}
	var body map[string]json.RawMessage
	for _, f := range found {
		var id int
		if json.Unmarshal(f["tvdbId"], &id) == nil && id == a.TVDBID {
			body = f
			break
		}
	}
	if body == nil {
		return Series{}, fmt.Errorf("sonarr: no show with tvdb id %d in the lookup", a.TVDBID)
	}
	tags := []int{}
	if a.Tag != "" {
		id, err := s.ensureTag(ctx, a.Tag)
		if err != nil {
			return Series{}, err
		}
		tags = append(tags, id)
	}
	typ := a.Type
	if typ == "" {
		typ = "standard"
	}
	set := map[string]any{
		"qualityProfileId": a.QualityProfileID,
		"rootFolderPath":   a.RootFolder,
		"seriesType":       typ,
		"tags":             tags,
		// Not deployment choices, so not settings: the show is kept, its episodes are not
		// monitored until a job asks, and nothing is searched.
		"monitored":    true,
		"seasonFolder": true,
		"addOptions": map[string]any{
			"monitor":                      "none",
			"searchForMissingEpisodes":     false,
			"searchForCutoffUnmetEpisodes": false,
		},
	}
	for k, v := range set {
		data, err := json.Marshal(v)
		if err != nil {
			return Series{}, err
		}
		body[k] = data
	}
	var out seriesJSON
	if err := s.post(ctx, "/series", body, &out); err != nil {
		return Series{}, err
	}
	return out.series(), nil
}

type episodeJSON struct {
	ID            int    `json:"id"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	Title         string `json:"title"`
	Runtime       int    `json:"runtime"` // minutes
	EpisodeFileID int    `json:"episodeFileId"`
	Monitored     bool   `json:"monitored"`
}

func (s *sonarr) Episodes(ctx context.Context, seriesID int) ([]Episode, error) {
	var out []episodeJSON
	if err := s.get(ctx, "/episode", url.Values{"seriesId": {strconv.Itoa(seriesID)}}, &out); err != nil {
		return nil, err
	}
	eps := make([]Episode, 0, len(out))
	for _, e := range out {
		eps = append(eps, Episode{
			ID: e.ID, SeasonNumber: e.SeasonNumber, Number: e.EpisodeNumber, Title: e.Title,
			RuntimeMs: int64(e.Runtime) * 60_000, FileID: e.EpisodeFileID, Monitored: e.Monitored,
		})
	}
	return eps, nil
}

// EpisodeFiles lists the files Sonarr tracks for the show.
func (s *sonarr) EpisodeFiles(ctx context.Context, seriesID int) ([]EpisodeFile, error) {
	var out []struct {
		ID   int    `json:"id"`
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if err := s.get(ctx, "/episodefile", url.Values{"seriesId": {strconv.Itoa(seriesID)}}, &out); err != nil {
		return nil, err
	}
	files := make([]EpisodeFile, 0, len(out))
	for _, f := range out {
		files = append(files, EpisodeFile{ID: f.ID, Path: f.Path, Size: f.Size})
	}
	return files, nil
}

// Busy reports an active download for the show (GET /queue/details?seriesId=).
func (s *sonarr) Busy(ctx context.Context, seriesID int) (bool, error) {
	var out []json.RawMessage
	if err := s.get(ctx, "/queue/details", url.Values{"seriesId": {strconv.Itoa(seriesID)}}, &out); err != nil {
		return false, err
	}
	return len(out) > 0, nil
}

func (s *sonarr) SetEpisodesMonitored(ctx context.Context, episodeIDs []int, monitored bool) error {
	if len(episodeIDs) == 0 {
		return nil
	}
	body := map[string]any{"episodeIds": episodeIDs, "monitored": monitored}
	return s.put(ctx, "/episode/monitor", body, nil)
}

// SetSeasonMonitored reads the series record, changes the flag of the one season and puts
// the record back, so that what Fukku does not model survives the round trip.
func (s *sonarr) SetSeasonMonitored(ctx context.Context, seriesID, season int, monitored bool) error {
	var body map[string]json.RawMessage
	if err := s.get(ctx, fmt.Sprintf("/series/%d", seriesID), nil, &body); err != nil {
		return err
	}
	var seasons []map[string]json.RawMessage
	if err := json.Unmarshal(body["seasons"], &seasons); err != nil {
		return fmt.Errorf("sonarr: series %d: seasons unreadable: %w", seriesID, err)
	}
	found := false
	for _, sn := range seasons {
		var n int
		if json.Unmarshal(sn["seasonNumber"], &n) != nil || n != season {
			continue
		}
		flag, err := json.Marshal(monitored)
		if err != nil {
			return err
		}
		sn["monitored"] = flag
		found = true
	}
	if !found {
		return fmt.Errorf("sonarr: series %d has no season %d", seriesID, season)
	}
	data, err := json.Marshal(seasons)
	if err != nil {
		return err
	}
	body["seasons"] = data
	return s.put(ctx, fmt.Sprintf("/series/%d", seriesID), body, nil)
}

// SeriesFinder is implemented by a Sonarr client that can find a show of its library by its
// TVDB id: the re-check of episodes' versions (#96) follows a series deleted and added back
// under a new id.
type SeriesFinder interface {
	// SeriesByTVDB returns the library's show with the TVDB id; ok is false when there is none.
	SeriesByTVDB(ctx context.Context, tvdbID int) (s Series, ok bool, err error)
}

var _ SeriesFinder = (*sonarr)(nil)

// SeriesByTVDB lists the library's shows with the TVDB id (GET /series?tvdbId=).
func (s *sonarr) SeriesByTVDB(ctx context.Context, tvdbID int) (Series, bool, error) {
	var out []seriesJSON
	if err := s.get(ctx, "/series", url.Values{"tvdbId": {strconv.Itoa(tvdbID)}}, &out); err != nil {
		return Series{}, false, err
	}
	for _, j := range out {
		if j.TVDBID == tvdbID && j.ID != 0 {
			return j.series(), true, nil
		}
	}
	return Series{}, false, nil
}
