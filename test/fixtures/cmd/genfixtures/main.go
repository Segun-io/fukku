// Command genfixtures writes the media fixtures and the hostile corpus into a directory, to
// look at by hand (make fixtures). Tests generate their own copies.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/segun-io/fukku/test/fixtures"
)

func main() {
	out := flag.String("out", ".cache/fixtures", "output directory")
	quick := flag.Bool("quick", false, "only what the stack tests upload (make e2e): a short film, one season zip, the hostile corpus, the M2 titles in m2/ and the M3 film and its dubs in m3/")
	install := flag.Bool("install", false, "only the film and the season archive of the clean-room install test (test/install), with manifest.json, into -out")
	flag.Parse()
	run := run
	switch {
	case *install:
		run = writeInstall
	case *quick:
		run = runQuick
	}
	if err := run(context.Background(), *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runQuick writes what test/e2e uploads: media/film.mkv (5 s), media/Probe Show - Season 01.zip
// (two 5 s episodes), the hostile corpus in hostile/, the M2 exit tests' files in m2/ (see
// writeM2) and the M3 exit tests' in m3/ (see writeM3).
func runQuick(ctx context.Context, out string) error {
	media := filepath.Join(out, "media")
	for _, d := range []string{media, filepath.Join(out, "hostile")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}
	spec := fixtures.Video{Duration: 5 * time.Second, Audio: []fixtures.Audio{{Language: "eng", Title: "English", Default: true}}}
	if err := fixtures.Generate(ctx, filepath.Join(media, "film.mkv"), spec); err != nil {
		return err
	}
	if _, err := fixtures.SeasonZip(ctx, media, "Probe Show", 1, 2, spec); err != nil {
		return err
	}
	if err := buildHostile(filepath.Join(out, "hostile")); err != nil {
		return err
	}
	if err := writeM2(ctx, filepath.Join(out, "m2")); err != nil {
		return err
	}
	return writeM3(ctx, filepath.Join(out, "m3"))
}

// The M3 exit tests (test/e2e/exit-m3.spec.ts, issue #61) put a film in the library through
// flow 1 and then upload another language of it (flow 2). The film is one of the probes' free
// titles at its real runtime, as in m2/; the uploads are made from the same picture with a
// Spanish track that beeps at another pitch, in step with the flash, so the sync-check clips
// can be measured (fixtures.SyncOffset).
type m3Manifest struct {
	Version string `json:"version"`
	TMDBID  int    `json:"tmdbId"`
	Title   string `json:"title"`
	Year    int    `json:"year"`
	// Film is the English original, imported first.
	Film string `json:"film"`
	// Dub is the same cut with a Spanish audio track only; Shifted the same with its audio
	// 800 ms late (fixtures.ShiftAudio); PAL a PAL speed-up of it (fixtures.PALSpeedup).
	Dub     string `json:"dub"`
	Shifted string `json:"shifted"`
	PAL     string `json:"pal"`
}

// m3Version changes whenever what writeM3 writes changes (the files are kept while it matches).
const m3Version = "1"

// writeM3 writes films/<Title>.<year>.mkv, its Spanish dub, the dub 800 ms late and a PAL
// speed-up of the dub, and manifest.json.
func writeM3(ctx context.Context, dir string) error {
	manifestPath := filepath.Join(dir, "manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var m m3Manifest
		if json.Unmarshal(data, &m) == nil && m.Version == m3Version {
			return nil
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "films"), 0o750); err != nil {
		return err
	}
	f := m2Films[0] // Big Buck Bunny, 9 minutes
	base := "films/" + strings.ReplaceAll(f.title, " ", ".") + fmt.Sprintf(".%d", f.year)
	m := m3Manifest{
		Version: m3Version, TMDBID: f.tmdb, Title: f.title, Year: f.year,
		Film: base + ".mkv", Dub: base + ".SPANISH.mkv", Shifted: base + ".SPANISH.late.mkv", PAL: base + ".SPANISH.PAL.mkv",
	}
	spec := fixtures.Video{
		Duration: time.Duration(f.minutes) * time.Minute, Width: 320, Height: 180, FrameRate: "5",
		Audio: []fixtures.Audio{{Language: "eng", Title: "English", Default: true}},
	}
	dub := spec
	dub.Audio = []fixtures.Audio{{Language: "spa", Title: "Español", ToneHz: 440, Default: true}}
	at := func(rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }
	steps := []struct {
		name string
		fn   func() error
	}{
		{m.Film, func() error { return fixtures.Generate(ctx, at(m.Film), spec) }},
		{m.Dub, func() error { return fixtures.Generate(ctx, at(m.Dub), dub) }},
		{m.Shifted, func() error { return fixtures.ShiftAudio(ctx, at(m.Dub), at(m.Shifted), 800*time.Millisecond) }},
		{m.PAL, func() error { return fixtures.PALSpeedup(ctx, at(m.Dub), at(m.PAL)) }},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// Last: a manifest means every file is complete.
	return os.WriteFile(manifestPath, data, 0o600)
}

// The M2 exit tests (test/e2e/exit-m2.spec.ts, issue #47) add real titles through Radarr and
// Sonarr: the probes' free titles (Blender open films on TMDB, a Creative Commons web series
// on TVDB), at their real runtimes, so that the apps do not take them for samples and Fukku's
// runtime checks see what a real upload gives. A small picture at 5 frames per second keeps
// them cheap: about 1 MB per minute.
type m2Film struct {
	TMDBID   int    `json:"tmdbId"`
	Title    string `json:"title"`
	Year     int    `json:"year"`
	File     string `json:"file"`
	Subtitle string `json:"subtitle,omitempty"` // a Spanish SRT with the video's base name
}

type m2Season struct {
	TVDBID int                    `json:"tvdbId"`
	Title  string                 `json:"title"`
	File   string                 `json:"file"`
	Want   []fixtures.WantEpisode `json:"want"`
}

type m2Manifest struct {
	Version string   `json:"version"`
	Films   []m2Film `json:"films"`
	Season  m2Season `json:"season"`
}

// m2Version changes whenever what writeM2 writes changes: the files take about a minute to
// generate, so they are kept while the manifest's version matches.
const m2Version = "1"

var m2Films = []struct {
	tmdb, year, minutes int
	title               string
	subtitle            bool
}{
	{10378, 2008, 9, "Big Buck Bunny", true},
	{45745, 2010, 14, "Sintel", false},
	{133701, 2012, 12, "Tears of Steel", false},
	{9761, 2006, 11, "Elephants Dream", false},
}

// writeM2 writes films/<Title>.<year>.mkv (and a Spanish .es.srt next to the first film), the
// awkward season of Pioneer One (fixtures.AwkwardSeasonOf with 30-minute episodes) and
// manifest.json, which gives the tests the titles and the correct mapping of the season.
func writeM2(ctx context.Context, dir string) error {
	manifestPath := filepath.Join(dir, "manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var m m2Manifest
		if json.Unmarshal(data, &m) == nil && m.Version == m2Version {
			return nil
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "films"), 0o750); err != nil {
		return err
	}
	m := m2Manifest{Version: m2Version}
	for _, f := range m2Films {
		base := strings.ReplaceAll(f.title, " ", ".") + fmt.Sprintf(".%d", f.year)
		spec := fixtures.Video{
			Duration: time.Duration(f.minutes) * time.Minute, Width: 320, Height: 180, FrameRate: "5",
			Audio: []fixtures.Audio{{Language: "eng", Title: "English", Default: true}},
		}
		entry := m2Film{TMDBID: f.tmdb, Title: f.title, Year: f.year, File: "films/" + base + ".mkv"}
		if err := fixtures.Generate(ctx, filepath.Join(dir, entry.File), spec); err != nil {
			return fmt.Errorf("%s: %w", entry.File, err)
		}
		if f.subtitle {
			entry.Subtitle = "films/" + base + ".es.srt"
			if err := os.WriteFile(filepath.Join(dir, entry.Subtitle), []byte(fixtures.SRT(spec.Duration, "spa")), 0o600); err != nil {
				return err
			}
		}
		m.Films = append(m.Films, entry)
	}
	const show = "Pioneer One"
	spec := fixtures.Video{Duration: 30 * time.Minute, Width: 320, Height: 180, FrameRate: "5"}
	zip, want, err := fixtures.AwkwardSeasonOf(ctx, dir, show, spec)
	if err != nil {
		return fmt.Errorf("the awkward season: %w", err)
	}
	m.Season = m2Season{TVDBID: 170551, Title: show, File: filepath.Base(zip), Want: want}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// Last: a manifest means every file is complete.
	return os.WriteFile(manifestPath, data, 0o600)
}

func run(ctx context.Context, out string) error {
	media := filepath.Join(out, "media")
	hostile := filepath.Join(out, "hostile")
	for _, d := range []string{media, hostile} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}
	film := filepath.Join(media, "film.mkv")
	spec := fixtures.Video{Duration: 60 * time.Second, Width: 1920, Height: 1080,
		Audio: []fixtures.Audio{{Language: "eng", Title: "English", Default: true}}}
	if err := fixtures.Generate(ctx, film, spec); err != nil {
		return err
	}
	spec.Audio = []fixtures.Audio{{Language: "spa", Title: "Español (Latino)", ToneHz: 440, Default: true}}
	dub := filepath.Join(media, "dub.mkv")
	steps := []struct {
		name string
		fn   func() error
	}{
		{"dub.mkv", func() error { return fixtures.Generate(ctx, dub, spec) }},
		{"pal.mkv", func() error { return fixtures.PALSpeedup(ctx, dub, filepath.Join(media, "pal.mkv")) }},
		{"shifted.mkv", func() error {
			return fixtures.ShiftAudio(ctx, dub, filepath.Join(media, "shifted.mkv"), 800*time.Millisecond)
		}},
		{"cut.mkv", func() error { return fixtures.Trim(ctx, dub, filepath.Join(media, "cut.mkv"), 0, 50*time.Second) }},
		{"broken.mkv", func() error { return fixtures.Broken(film, filepath.Join(media, "broken.mkv")) }},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	if _, err := fixtures.SeasonZip(ctx, media, "Probe Show", 1, 3, fixtures.Video{Duration: 10 * time.Second, Audio: []fixtures.Audio{{Language: "eng"}}}); err != nil {
		return err
	}
	if _, _, err := fixtures.AwkwardSeason(ctx, media, "Awkward Show", 10*time.Second); err != nil {
		return err
	}
	if err := buildHostile(hostile); err != nil {
		return err
	}
	fmt.Println("written to", out)
	return nil
}

func buildHostile(dir string) error {
	for _, h := range fixtures.HostileCorpus(fixtures.DefaultLimits) {
		if _, err := h.Build(dir); err != nil {
			return err
		}
		fmt.Printf("%-26s %-40s %s\n", h.Name, h.Expect, h.About)
	}
	return nil
}
