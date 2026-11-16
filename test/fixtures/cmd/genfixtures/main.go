// Command genfixtures writes the media fixtures and the hostile corpus into a directory, to
// look at by hand (make fixtures). Tests generate their own copies.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/segun-io/fukku/test/fixtures"
)

func main() {
	out := flag.String("out", ".cache/fixtures", "output directory")
	quick := flag.Bool("quick", false, "only what the stack tests upload (make e2e): a short film, one season zip, the hostile corpus")
	flag.Parse()
	run := run
	if *quick {
		run = runQuick
	}
	if err := run(context.Background(), *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runQuick writes what test/e2e uploads: media/film.mkv (5 s), media/Probe Show - Season 01.zip
// (two 5 s episodes) and the hostile corpus in hostile/.
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
	return buildHostile(filepath.Join(out, "hostile"))
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
