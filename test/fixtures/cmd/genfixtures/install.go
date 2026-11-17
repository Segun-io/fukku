package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/segun-io/fukku/test/fixtures"
)

// The clean-room install test (test/install, issue #67) uploads one small film and one
// season archive through the HTTP API. They are free titles of the probes (a Blender film on
// TMDB, a Creative Commons web series on TVDB) at their real runtimes, so Radarr and Sonarr do
// not take them for samples and Fukku's runtime checks see what a real upload gives, at a small
// picture size and frame rate. The season is a plain one: two well-formed episodes.
type installManifest struct {
	Film struct {
		TMDBID int    `json:"tmdbId"`
		Title  string `json:"title"`
		Year   int    `json:"year"`
		File   string `json:"file"`
	} `json:"film"`
	Season struct {
		TVDBID   int    `json:"tvdbId"`
		Title    string `json:"title"`
		File     string `json:"file"`
		Episodes []int  `json:"episodes"`
	} `json:"season"`
}

// writeInstall writes the film, the season zip and manifest.json into dir.
func writeInstall(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	var m installManifest
	m.Film.TMDBID, m.Film.Title, m.Film.Year = 10378, "Big Buck Bunny", 2008
	m.Film.File = "Big.Buck.Bunny.2008.mkv"
	film := fixtures.Video{
		Duration: 9 * time.Minute, Width: 320, Height: 180, FrameRate: "5",
		Audio: []fixtures.Audio{{Language: "eng", Title: "English", Default: true}},
	}
	if err := fixtures.Generate(ctx, filepath.Join(dir, m.Film.File), film); err != nil {
		return err
	}

	m.Season.TVDBID, m.Season.Title, m.Season.Episodes = 170551, "Pioneer One", []int{1, 2}
	episode := fixtures.Video{
		Duration: 30 * time.Minute, Width: 320, Height: 180, FrameRate: "5",
		Audio: []fixtures.Audio{{Language: "eng", Title: "English", Default: true}},
	}
	zip, err := fixtures.SeasonZip(ctx, dir, m.Season.Title, 1, len(m.Season.Episodes), episode)
	if err != nil {
		return err
	}
	m.Season.File = filepath.Base(zip)

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// Last: a manifest means every file is complete.
	return os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600)
}
