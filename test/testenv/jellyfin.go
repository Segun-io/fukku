package testenv

import (
	"context"
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
}

type MediaSource struct {
	ID   string `json:"Id"`
	Path string `json:"Path"`
	Name string `json:"Name"`
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
