package testenv

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Jellyfin is a Jellyfin API client authenticated with an API key.
type Jellyfin struct {
	*Client
	Key string
}

// Item is the part of a Jellyfin item the probes look at.
type Item struct {
	ID                  string        `json:"Id"`
	Name                string        `json:"Name"`
	Type                string        `json:"Type"`
	Path                string        `json:"Path"`
	MediaSources        []MediaSource `json:"MediaSources"`
	LocalTrailerCount   int           `json:"LocalTrailerCount"`
	SpecialFeatureCount int           `json:"SpecialFeatureCount"`
	ParentIndexNumber   *int          `json:"ParentIndexNumber"`
	IndexNumber         *int          `json:"IndexNumber"`
	RunTimeTicks        int64         `json:"RunTimeTicks"`
	ProductionYear      int           `json:"ProductionYear"`
	MediaSourceCount    int           `json:"MediaSourceCount"`
}

type MediaSource struct {
	ID           string        `json:"Id"`
	Path         string        `json:"Path"`
	Name         string        `json:"Name"`
	RunTimeTicks int64         `json:"RunTimeTicks"`
	MediaStreams []MediaStream `json:"MediaStreams"`
}

// MediaStream is one video, audio or subtitle stream of a media source.
type MediaStream struct {
	Type string `json:"Type"`
}

// HasMediaInfo reports whether the item carries a runtime and at least one stream, which is
// what Jellyfin fills in once it has probed the file.
func (it Item) HasMediaInfo() bool {
	for _, ms := range it.MediaSources {
		if ms.RunTimeTicks > 0 && len(ms.MediaStreams) > 0 {
			return true
		}
	}
	return false
}

// ItemsUnder returns the items of the given types whose path is under dir.
func (j *Jellyfin) ItemsUnder(ctx context.Context, dir string, types ...string) ([]Item, error) {
	var res struct {
		Items []Item `json:"Items"`
	}
	q := Q("Recursive", "true", "IncludeItemTypes", strings.Join(types, ","),
		"Fields", "Path,MediaSources,SpecialFeatureCount,LocalTrailerCount", "EnableImages", "false")
	if err := j.Do(ctx, "GET", "/Items"+q, nil, &res); err != nil {
		return nil, err
	}
	var out []Item
	prefix := strings.TrimRight(dir, "/") + "/"
	for _, it := range res.Items {
		if strings.HasPrefix(it.Path, prefix) || it.Path == dir {
			out = append(out, it)
		}
	}
	return out, nil
}

// Item returns one item with its media sources.
func (j *Jellyfin) Item(ctx context.Context, id string) (Item, error) {
	var res struct {
		Items []Item `json:"Items"`
	}
	err := j.Do(ctx, "GET", "/Items"+Q("Ids", id, "Fields", "Path,MediaSources,SpecialFeatureCount,LocalTrailerCount"), nil, &res)
	if err == nil && len(res.Items) == 0 {
		return Item{}, &HTTPError{Method: "GET", Path: "/Items?Ids=" + id, Status: 404, Body: "no such item"}
	}
	if err != nil {
		return Item{}, err
	}
	return res.Items[0], nil
}

// ScanAndWait starts a full library scan and waits until the scan task is idle again.
func (j *Jellyfin) ScanAndWait(ctx context.Context, timeout time.Duration) error {
	if err := j.Do(ctx, "POST", "/Library/Refresh", nil, nil); err != nil {
		return err
	}
	return j.WaitScanIdle(ctx, timeout)
}

// WaitScanIdle waits until the "Scan Media Library" task is not running.
func (j *Jellyfin) WaitScanIdle(ctx context.Context, timeout time.Duration) error {
	time.Sleep(time.Second) // let the task start
	return WaitFor(ctx, timeout, time.Second, "jellyfin library scan", func() (bool, error) {
		var tasks []struct {
			Key   string `json:"Key"`
			State string `json:"State"`
		}
		if err := j.Do(ctx, "GET", "/ScheduledTasks", nil, &tasks); err != nil {
			return false, err
		}
		for _, t := range tasks {
			if t.Key == "RefreshLibrary" && t.State != "Idle" {
				return false, nil
			}
		}
		return true, nil
	})
}

// Version returns the server version.
func (j *Jellyfin) Version(ctx context.Context) (string, error) {
	var info struct {
		Version string `json:"Version"`
	}
	err := j.Do(ctx, "GET", "/System/Info/Public", nil, &info)
	return info.Version, err
}

// Extras returns the file names of an item's local trailers and special features.
func (j *Jellyfin) Extras(ctx context.Context, id string) (trailers, special []string, err error) {
	var users []struct {
		ID string `json:"Id"`
	}
	if err := j.Do(ctx, "GET", "/Users", nil, &users); err != nil || len(users) == 0 {
		return nil, nil, err
	}
	get := func(kind string) ([]string, error) {
		var list []Item
		if err := j.Do(ctx, "GET", "/Items/"+id+"/"+kind+Q("userId", users[0].ID), nil, &list); err != nil {
			return nil, err
		}
		var names []string
		for _, it := range list {
			names = append(names, it.Path[strings.LastIndex(it.Path, "/")+1:])
		}
		return names, nil
	}
	if trailers, err = get("LocalTrailers"); err != nil {
		return nil, nil, err
	}
	special, err = get("SpecialFeatures")
	return trailers, special, err
}

// LibraryID returns the item id of the library (virtual folder) whose location contains path.
func (j *Jellyfin) LibraryID(ctx context.Context, path string) (string, error) {
	var libs []struct {
		ItemID    string   `json:"ItemId"`
		Locations []string `json:"Locations"`
	}
	if err := j.Do(ctx, "GET", "/Library/VirtualFolders", nil, &libs); err != nil {
		return "", err
	}
	for _, l := range libs {
		for _, loc := range l.Locations {
			if path == loc || strings.HasPrefix(path, strings.TrimRight(loc, "/")+"/") {
				return l.ItemID, nil
			}
		}
	}
	return "", fmt.Errorf("no library contains %s", path)
}

// RefreshItem asks Jellyfin to refresh an item (POST /Items/{id}/Refresh) with the default
// metadata mode: it looks for new and changed children without downloading anything again.
func (j *Jellyfin) RefreshItem(ctx context.Context, id string, recursive bool) error {
	return j.Do(ctx, "POST", "/Items/"+id+"/Refresh"+Q("metadataRefreshMode", "Default", "imageRefreshMode", "Default",
		"recursive", fmt.Sprint(recursive)), nil, nil)
}

// MediaUpdated reports paths to POST /Library/Media/Updated as created.
func (j *Jellyfin) MediaUpdated(ctx context.Context, paths ...string) error {
	var ups []M
	for _, p := range paths {
		ups = append(ups, M{"Path": p, "UpdateType": "Created"})
	}
	return j.Do(ctx, "POST", "/Library/Media/Updated", M{"Updates": ups}, nil)
}

// FindMovie returns the movie whose path is under dir, using a name search (cheap on a big
// library), or nil when Jellyfin does not list it yet. term must be part of the item's name.
func (j *Jellyfin) FindMovie(ctx context.Context, term, dir string) (*Item, error) {
	var res struct {
		Items []Item `json:"Items"`
	}
	q := Q("Recursive", "true", "IncludeItemTypes", "Movie", "SearchTerm", term,
		"Fields", "Path,MediaSources", "EnableImages", "false")
	if err := j.Do(ctx, "GET", "/Items"+q, nil, &res); err != nil {
		return nil, err
	}
	prefix := strings.TrimRight(dir, "/") + "/"
	for _, it := range res.Items {
		if strings.HasPrefix(it.Path, prefix) {
			return &it, nil
		}
	}
	return nil, nil
}

// LibraryMonitorDelay returns the server's LibraryMonitorDelay in seconds.
func (j *Jellyfin) LibraryMonitorDelay(ctx context.Context) (int, error) {
	var cfg struct {
		Delay int `json:"LibraryMonitorDelay"`
	}
	err := j.Do(ctx, "GET", "/System/Configuration", nil, &cfg)
	return cfg.Delay, err
}

// SetLibraryMonitorDelay changes the server's LibraryMonitorDelay (seconds) and leaves every
// other setting as it was. The change takes effect without a restart.
func (j *Jellyfin) SetLibraryMonitorDelay(ctx context.Context, seconds int) error {
	var cfg map[string]json.RawMessage
	if err := j.Do(ctx, "GET", "/System/Configuration", nil, &cfg); err != nil {
		return err
	}
	cfg["LibraryMonitorDelay"] = json.RawMessage(fmt.Sprint(seconds))
	return j.Do(ctx, "POST", "/System/Configuration", cfg, nil)
}

// AddLibraryPath adds a folder to a library (a virtual folder) without scanning it.
func (j *Jellyfin) AddLibraryPath(ctx context.Context, library, path string) error {
	return j.Do(ctx, "POST", "/Library/VirtualFolders/Paths"+Q("refreshLibrary", "false"),
		M{"Name": library, "PathInfo": M{"Path": path}}, nil)
}

// RemoveLibraryPath removes a folder from a library without scanning.
func (j *Jellyfin) RemoveLibraryPath(ctx context.Context, library, path string) error {
	return j.Do(ctx, "DELETE", "/Library/VirtualFolders/Paths"+Q("name", library, "path", path, "refreshLibrary", "false"), nil, nil)
}

// Unmerge removes the alternate versions merged into an item (DELETE /Videos/{id}/AlternateSources).
func (j *Jellyfin) Unmerge(ctx context.Context, id string) error {
	return j.Do(ctx, "DELETE", "/Videos/"+id+"/AlternateSources", nil, nil)
}

// UserMovies lists the movies the first user sees whose name matches term: what a client
// shows, with alternate versions grouped under one entry.
func (j *Jellyfin) UserMovies(ctx context.Context, term string) ([]Item, error) {
	var users []struct {
		ID string `json:"Id"`
	}
	if err := j.Do(ctx, "GET", "/Users", nil, &users); err != nil || len(users) == 0 {
		return nil, err
	}
	var res struct {
		Items []Item `json:"Items"`
	}
	q := Q("Recursive", "true", "IncludeItemTypes", "Movie", "SearchTerm", term, "Fields", "Path,MediaSources", "EnableImages", "false")
	err := j.Do(ctx, "GET", "/Users/"+users[0].ID+"/Items"+q, nil, &res)
	return res.Items, err
}
