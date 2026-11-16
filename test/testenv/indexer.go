package testenv

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Indexer is the fake Newznab indexer of the test environment (test/env/indexer).
type Indexer struct {
	*Client
	Key string
}

// Release is a release the fake indexer offers.
type Release struct {
	Title    string    `json:"title"`
	Category int       `json:"category"`
	Size     int64     `json:"size"`
	GUID     string    `json:"guid,omitempty"`
	PubDate  time.Time `json:"pubDate,omitzero"`
}

// IndexerRequest is a search or NZB request the fake indexer answered.
type IndexerRequest struct {
	At       time.Time         `json:"at"`
	Agent    string            `json:"agent"`
	Function string            `json:"t"`
	Query    map[string]string `json:"query"`
	Returned []string          `json:"returned"`
}

// Newznab categories of the fake indexer that the seeder gives Radarr and Sonarr.
const (
	CategoryMovies   = 2000
	CategoryMoviesHD = 2040
	CategoryTV       = 5000
	CategoryTVHD     = 5040
)

// Offer replaces the releases the indexer offers and returns them with their GUIDs.
func (ix *Indexer) Offer(ctx context.Context, releases ...Release) ([]Release, error) {
	if releases == nil {
		releases = []Release{}
	}
	var out []Release
	err := ix.Do(ctx, "PUT", "/control/releases", releases, &out)
	return out, err
}

// Clear offers nothing.
func (ix *Indexer) Clear(ctx context.Context) error {
	return ix.Do(ctx, "DELETE", "/control/releases", nil, nil)
}

// Requests returns the search and NZB requests answered since the last ForgetRequests.
func (ix *Indexer) Requests(ctx context.Context) ([]IndexerRequest, error) {
	var out []IndexerRequest
	err := ix.Do(ctx, "GET", "/control/requests", nil, &out)
	return out, err
}

// ForgetRequests clears the request log.
func (ix *Indexer) ForgetRequests(ctx context.Context) error {
	return ix.Do(ctx, "DELETE", "/control/requests", nil, nil)
}

// Blackhole is the folder pair of an app's blackhole download client: a grab writes
// <release>.nzb into NZB, and a finished download is a folder or file put into Watch.
type Blackhole struct{ NZB, Watch string }

// BlackholeAt returns an app's blackhole folders under the library filesystem root (the
// apps see it as /data, the tools container as Env.Data).
func BlackholeAt(data, app string) Blackhole {
	return Blackhole{NZB: filepath.Join(data, "blackhole", app, "nzb"), Watch: filepath.Join(data, "blackhole", app, "watch")}
}

// Empty removes everything in both folders.
func (b Blackhole) Empty() error {
	for _, dir := range []string{b.NZB, b.Watch} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// Grabbed lists the NZB files in the NZB folder.
func (b Blackhole) Grabbed() []string {
	names, _ := filepath.Glob(filepath.Join(b.NZB, "*.nzb"))
	out := []string{}
	for _, n := range names {
		out = append(out, filepath.Base(n))
	}
	return out
}
