package media_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/test/fixtures"
)

func TestLibraryPath(t *testing.T) {
	roots := []string{"/data/media/movies", "/data/media/movies/kids", "/srv/films/", "relative", ""}
	for _, tc := range []struct {
		path, root, rel string
		ok              bool
	}{
		{"/data/media/movies/Film (2001)/Film.mkv", "/data/media/movies", "Film (2001)/Film.mkv", true},
		{"/data/media/movies/kids/Film/Film.mkv", "/data/media/movies/kids", "Film/Film.mkv", true}, // the longest root
		{"/srv/films/Film.mkv", "/srv/films", "Film.mkv", true},                                     // a root with a trailing slash
		{"/data/media/movies", "", "", false},                                                       // a root itself
		{"/data/media/movies-old/Film.mkv", "", "", false},                                          // a sibling, not a child
		{"/data/media/movies/../tv/Show/S01E01.mkv", "", "", false},                                 // climbs out
		{"/data/media/movies/Film/../../../../etc/passwd", "", "", false},
		{"/data/media/movies/./Film.mkv", "", "", false}, // not clean: refused, not cleaned
		{"/data/media/movies//Film.mkv", "", "", false},
		{"/data/media/movies/Film/", "", "", false},
		{"/etc/passwd", "", "", false},
		{"relative/Film.mkv", "", "", false},
		{"", "", "", false},
	} {
		root, rel, ok := media.LibraryPath(roots, tc.path)
		if ok != tc.ok || root != tc.root || rel != tc.rel {
			t.Errorf("LibraryPath(%q) = %q %q %t, want %q %q %t", tc.path, root, rel, ok, tc.root, tc.rel, tc.ok)
		}
	}
	if _, _, ok := media.LibraryPath(nil, "/data/media/movies/Film.mkv"); ok {
		t.Error("no roots: accepted")
	}
	if root, rel, ok := media.LibraryPath([]string{"/"}, "/data/Film.mkv"); !ok || root != "/" || rel != "data/Film.mkv" {
		t.Errorf("root /: %q %q %t", root, rel, ok)
	}
}

// The library probe reads a film inside a library root, and nothing outside one: not
// through "..", not through a link to a file outside, not through a linked folder.
func TestRunLibraryConfinement(t *testing.T) {
	needTools(t)
	ctx := context.Background()
	lib, outside := t.TempDir(), t.TempDir()
	filmDir := filepath.Join(lib, "Film (2001)")
	if err := os.Mkdir(filmDir, 0o750); err != nil {
		t.Fatal(err)
	}
	film := filepath.Join(filmDir, "Film (2001) WEBDL-1080p.mkv")
	spec := fixtures.Video{Duration: 4 * time.Second, Audio: []fixtures.Audio{{Language: "eng"}}}
	if err := fixtures.Generate(ctx, film, spec); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.mkv")
	if err := fixtures.Generate(ctx, secret, spec); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(filmDir, "link.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(lib, "Linked (2002)")); err != nil {
		t.Fatal(err)
	}
	// A relative link that stays inside the root is followed: it cannot lead anywhere else.
	if err := os.Symlink("Film (2001)/Film (2001) WEBDL-1080p.mkv", filepath.Join(lib, "inside.mkv")); err != nil {
		t.Fatal(err)
	}
	roots := []string{lib}
	run := func(p string) media.LibraryProbeOutput {
		t.Helper()
		out, err := media.RunLibrary(ctx, roots, media.LibraryProbeArgs{Path: p}, opts())
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if out.Path != p {
			t.Errorf("%s: output names %q", p, out.Path)
		}
		return out
	}

	out := run(film)
	if !out.OK || out.Media == nil || math.Abs(float64(out.Media.DurationMs)-4000) > 200 {
		t.Fatalf("film: %+v", out)
	}
	if v := out.Media.Video; len(v) != 1 || math.Abs(float64(v[0].DurationMs)-4000) > 200 {
		t.Errorf("the video stream's duration: %+v", v)
	}
	if in := run(filepath.Join(lib, "inside.mkv")); !in.OK {
		t.Errorf("a link inside the root: %+v", in)
	}

	for name, tc := range map[string]struct{ path, refusal string }{
		"climbs out with ..":      {lib + "/../" + filepath.Base(outside) + "/secret.mkv", media.RefusalOutsideLibrary},
		"climbs out of a folder":  {filmDir + "/../../" + filepath.Base(outside) + "/secret.mkv", media.RefusalOutsideLibrary},
		"climbs out, cleaned":     {secret, media.RefusalOutsideLibrary},
		"a link to a file out":    {filepath.Join(filmDir, "link.mkv"), media.RefusalUnreadable},
		"through a linked folder": {filepath.Join(lib, "Linked (2002)", "secret.mkv"), media.RefusalUnreadable},
		"the root itself":         {lib, media.RefusalOutsideLibrary},
		"a folder":                {filmDir, media.RefusalUnreadable},
		"missing":                 {filepath.Join(filmDir, "gone.mkv"), media.RefusalUnreadable},
		"relative":                {"Film (2001)/Film (2001) WEBDL-1080p.mkv", media.RefusalOutsideLibrary},
	} {
		if got := run(tc.path); got.OK || got.Media != nil || got.Refusal != tc.refusal {
			t.Errorf("%s (%s): %+v, want %s", name, tc.path, got, tc.refusal)
		}
	}

	// With no roots configured nothing is read; a root that is not there is unreadable.
	roots = nil
	if got := run(film); got.Refusal != media.RefusalOutsideLibrary {
		t.Errorf("no roots: %+v", got)
	}
	roots = []string{filepath.Join(lib, "unmounted")}
	if got := run(filepath.Join(lib, "unmounted", "Film.mkv")); got.Refusal != media.RefusalUnreadable {
		t.Errorf("missing root: %+v", got)
	}
}

func TestLibraryProbeJSON(t *testing.T) {
	b := marshal(t, media.LibraryProbeArgs{Path: "/data/media/movies/a.mkv"})
	if b != `{"path":"/data/media/movies/a.mkv"}` {
		t.Errorf("args %s", b)
	}
	b = marshal(t, media.LibraryProbeOutput{Path: "/x.mkv", Refusal: media.RefusalOutsideLibrary})
	if b != `{"path":"/x.mkv","ok":false,"refusal":"outside_library"}` {
		t.Errorf("output %s", b)
	}
	b = marshal(t, media.VideoTrack{Codec: "h264", DurationMs: 20020})
	if b != `{"index":0,"codec":"h264","width":0,"height":0,"default":false,"durationMs":20020}` {
		t.Errorf("video track %s", b)
	}
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Library files may be in older containers than uploads: with worker.libraryFormats (its
// default here) a .flv and a .mpg probe, with only the upload list they are refused, and a
// playlist renamed to a film is refused either way.
func TestRunLibraryFormats(t *testing.T) {
	needTools(t)
	ctx := context.Background()
	lib := t.TempDir()
	spec := fixtures.Video{Duration: 3 * time.Second, Audio: []fixtures.Audio{{Language: "eng"}}}
	for _, name := range []string{"film.flv", "film.mpg", "other.mp4"} {
		if name == "film.mpg" { // MPEG-PS takes no AAC
			if err := fixtures.Generate(ctx, filepath.Join(lib, name), fixtures.Video{Duration: 3 * time.Second}); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			continue
		}
		if err := fixtures.Generate(ctx, filepath.Join(lib, name), spec); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	script := "ffconcat version 1.0\nfile '" + filepath.Join(lib, "other.mp4") + "'\n"
	if err := os.WriteFile(filepath.Join(lib, "list.mkv"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	wide := opts()
	wide.LibraryFormats = config.Defaults().Worker.LibraryFormats
	probe := func(o media.Options, name string) media.LibraryProbeOutput {
		t.Helper()
		out, err := media.RunLibrary(ctx, []string{lib}, media.LibraryProbeArgs{Path: filepath.Join(lib, name)}, o)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, name := range []string{"film.flv", "film.mpg"} {
		if out := probe(wide, name); !out.OK || out.Media == nil {
			t.Errorf("%s with the library formats: %+v", name, out)
		}
		if out := probe(opts(), name); out.OK {
			t.Errorf("%s with the upload list: accepted", name)
		}
	}
	if out := probe(wide, "list.mkv"); out.OK {
		t.Errorf("a playlist renamed to a film: %+v", out)
	}
}
