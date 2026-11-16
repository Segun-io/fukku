package fixtures

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Episode is one video in a SeasonZipOf archive.
type Episode struct {
	Name     string        // file name inside the archive, directories included
	Duration time.Duration // 0: the spec's default
	Audio    []Audio       // nil: the spec's default
}

// SeasonZipOf writes an archive named name (in dir) with exactly these files: the episodes,
// then the subtitles (archive path to SRT text, in path order). spec gives every episode
// its defaults.
func SeasonZipOf(ctx context.Context, dir, name string, spec Video, episodes []Episode, subtitles map[string]string) (string, error) {
	work, err := os.MkdirTemp(dir, ".season-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(work) }()

	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	for i, ep := range episodes {
		v := spec
		if ep.Duration != 0 {
			v.Duration = ep.Duration
		}
		if ep.Audio != nil {
			v.Audio = ep.Audio
		}
		src := filepath.Join(work, fmt.Sprintf("%d%s", i, filepath.Ext(ep.Name)))
		if err := Generate(ctx, src, v); err != nil {
			return "", err
		}
		if err := addFile(zw, ep.Name, src, zip.Store); err != nil {
			return "", err
		}
	}
	names := make([]string, 0, len(subtitles))
	for n := range subtitles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			return "", err
		}
		if _, err := io.WriteString(w, subtitles[n]); err != nil {
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return path, f.Close()
}

// WantEpisode is what a correct mapping says about one file of AwkwardSeason.
type WantEpisode struct {
	File     string // file name inside the archive
	Season   int
	Episodes []int         // the episodes the file really holds
	Duration time.Duration // expected length
	Language string        // audio language (ISO 639-2)
	Dubbed   bool          // not in the original language: the import should warn
}

// AwkwardSeason writes a season archive that trips up naive mapping: a double episode
// named as its first episode only, a file numbered wrongly, and a dubbed episode.
//
//	S01E01        episodes 1 and 2 in one file of twice the runtime
//	S01E03        normal, with an English subtitle file
//	S01E05        really episode 4
//	S01E06        Spanish audio only (a dub)
//	S01E07        normal
//
// Real episode 5 is not in the archive. runtime is one episode's length; callers pass a few
// seconds, not the forty minutes of a real episode, so generating stays quick. The second
// result is the correct mapping, in archive order.
func AwkwardSeason(ctx context.Context, dir, show string, runtime time.Duration) (string, []WantEpisode, error) {
	en := []Audio{{Language: "eng", Title: "English", Default: true}}
	es := []Audio{{Language: "spa", Title: "Español (Latino)", ToneHz: 440, Default: true}}
	file := func(ep int) string { return fmt.Sprintf("Season 01/%s - S01E%02d.mkv", show, ep) }
	episodes := []Episode{
		{Name: file(1), Duration: 2 * runtime},
		{Name: file(3)},
		{Name: file(5)},
		{Name: file(6), Audio: es},
		{Name: file(7)},
	}
	want := []WantEpisode{
		{File: file(1), Season: 1, Episodes: []int{1, 2}, Duration: 2 * runtime, Language: "eng"},
		{File: file(3), Season: 1, Episodes: []int{3}, Duration: runtime, Language: "eng"},
		{File: file(5), Season: 1, Episodes: []int{4}, Duration: runtime, Language: "eng"},
		{File: file(6), Season: 1, Episodes: []int{6}, Duration: runtime, Language: "spa", Dubbed: true},
		{File: file(7), Season: 1, Episodes: []int{7}, Duration: runtime, Language: "eng"},
	}
	subs := map[string]string{
		fmt.Sprintf("Season 01/%s - S01E03.en.srt", show): SRT(runtime, "eng"),
	}
	path, err := SeasonZipOf(ctx, dir, show+" - Season 01.zip", Video{Duration: runtime, Audio: en}, episodes, subs)
	return path, want, err
}

// SeasonZip writes a well-formed season archive: one short video per episode named
// "<show> - S<ss>E<ee>.mkv" inside "Season <ss>/", plus an English SRT for the first
// episode. Videos are stored, not deflated, as real archives of video are.
func SeasonZip(ctx context.Context, dir, show string, season, episodes int, spec Video) (string, error) {
	work, err := os.MkdirTemp(dir, ".season-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(work) }()

	path := filepath.Join(dir, fmt.Sprintf("%s - Season %02d.zip", show, season))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	folder := fmt.Sprintf("Season %02d/", season)
	for ep := 1; ep <= episodes; ep++ {
		name := fmt.Sprintf("%s - S%02dE%02d.mkv", show, season, ep)
		src := filepath.Join(work, name)
		if err := Generate(ctx, src, spec); err != nil {
			return "", err
		}
		if err := addFile(zw, folder+name, src, zip.Store); err != nil {
			return "", err
		}
	}
	srt, err := zw.Create(folder + fmt.Sprintf("%s - S%02dE01.en.srt", show, season))
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(srt, SRT(spec.withDefaults().Duration, "eng")); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return path, f.Close()
}

func addFile(zw *zip.Writer, name, src string, method uint16) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}
