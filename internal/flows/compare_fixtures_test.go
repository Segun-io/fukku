package flows_test

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/test/fixtures"
)

// The comparison on generated media, probed as the worker probes it: the library file
// through the library probe (inside a library root), the uploads through the job's probe.
// The matching copy (another language of the same cut) and the copy whose dub starts
// 800 ms late are the same cut; the PAL copy is a PAL speed-up; the trimmed copy is a
// different cut. Each finding is then moved across its boundary by the settings, with the
// durations the fixtures really have.
func TestCompareFixtures(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed (run the tests with make test)", tool)
		}
	}
	ctx := context.Background()
	lib, job := t.TempDir(), t.TempDir()
	film := filepath.Join(lib, "Film (2001)", "Film (2001) WEBDL-1080p.mkv")
	if err := os.MkdirAll(filepath.Dir(film), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(job, "files"), 0o750); err != nil {
		t.Fatal(err)
	}
	spec := fixtures.Video{Duration: 20 * time.Second, Audio: []fixtures.Audio{{Language: "eng", Default: true}}}
	if err := fixtures.Generate(ctx, film, spec); err != nil {
		t.Fatal(err)
	}
	dub := spec
	dub.Audio = []fixtures.Audio{{Language: "spa", Title: "Español (Latino)", ToneHz: 440, Default: true}}
	up := func(name string) string { return filepath.Join(job, "files", name) }
	if err := fixtures.Generate(ctx, up("matching.mkv"), dub); err != nil {
		t.Fatal(err)
	}
	if err := fixtures.ShiftAudio(ctx, up("matching.mkv"), up("shifted.mkv"), 800*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := fixtures.PALSpeedup(ctx, up("matching.mkv"), up("pal.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := fixtures.Trim(ctx, up("matching.mkv"), up("trimmed.mkv"), 0, 18*time.Second); err != nil {
		t.Fatal(err)
	}

	opt := media.Options{Timeout: 30 * time.Second, VideoExt: []string{".mkv"}, SubtitleExt: []string{".srt"}}
	libOut, err := media.RunLibrary(ctx, []string{lib}, media.LibraryProbeArgs{Path: film}, opt)
	if err != nil || !libOut.OK {
		t.Fatalf("library probe: %+v %v", libOut, err)
	}
	root, err := os.OpenRoot(job)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	names := []string{"matching.mkv", "shifted.mkv", "pal.mkv", "trimmed.mkv"}
	var paths []string
	for _, n := range names {
		paths = append(paths, "files/"+n)
	}
	out, err := media.Run(ctx, root, media.ProbeArgs{Paths: paths}, opt)
	if err != nil {
		t.Fatal(err)
	}
	probes := map[string]media.Probe{}
	for i, f := range out.Files {
		if !f.OK {
			t.Fatalf("%s: %+v", names[i], f)
		}
		probes[names[i]] = *f.Media
	}

	m := config.Defaults().Match
	library := *libOut.Media
	cmp := func(name string, m config.Match) flows.Comparison {
		return flows.CompareProbes(library, probes[name], m)
	}
	for name, want := range map[string]string{
		"matching.mkv": flows.FindingMatch,
		"shifted.mkv":  flows.FindingMatch, // the same cut; the preview catches the shift (#53)
		"pal.mkv":      flows.FindingPAL,
		"trimmed.mkv":  flows.FindingDifferent,
	} {
		if c := cmp(name, m); c.Finding != want {
			t.Errorf("%s: %+v (library %d ms, upload %d ms), want %s", name, c,
				flows.CutDuration(library), flows.CutDuration(probes[name]), want)
		}
	}
	if probes["shifted.mkv"].DurationMs-library.DurationMs < 500 {
		t.Errorf("the shifted copy's container is not longer (%d vs %d ms): the test proves nothing", probes["shifted.mkv"].DurationMs, library.DurationMs)
	}

	// The tolerance at the trimmed copy's real difference makes it a match; 1 ms less does not.
	d := cmp("trimmed.mkv", m).DeltaMs
	if d >= 0 {
		t.Fatalf("trimmed copy not shorter: %d ms", d)
	}
	at := m
	at.RuntimeTolerance = config.Duration(time.Duration(-d) * time.Millisecond)
	if c := cmp("trimmed.mkv", at); c.Finding != flows.FindingMatch {
		t.Errorf("tolerance %v: %+v", at.RuntimeTolerance, c)
	}
	at.RuntimeTolerance = config.Duration(time.Duration(-d-1) * time.Millisecond)
	if c := cmp("trimmed.mkv", at); c.Finding != flows.FindingDifferent {
		t.Errorf("tolerance %v: %+v", at.RuntimeTolerance, c)
	}

	// The PAL window placed with the PAL copy's real ratio at its edge, and just past it.
	r := cmp("pal.mkv", m).Ratio
	if math.Abs(r-25.0/(24000.0/1001.0)) > 0.002 {
		t.Errorf("PAL ratio %.5f is not 25/23.976", r)
	}
	for _, side := range []float64{1, -1} {
		at := m
		at.PALRatio = r + side*m.PALTolerance
		if c := cmp("pal.mkv", at); c.Finding != flows.FindingPAL {
			t.Errorf("ratio at the edge (%.5f around %.5f): %+v", r, at.PALRatio, c)
		}
		at.PALRatio = r + side*(m.PALTolerance+0.0001)
		if c := cmp("pal.mkv", at); c.Finding != flows.FindingDifferent {
			t.Errorf("ratio past the edge (%.5f around %.5f): %+v", r, at.PALRatio, c)
		}
	}
	// The matching copy is well within the tolerance.
	if d := cmp("matching.mkv", m).DeltaMs; d < -100 || d > 100 {
		t.Errorf("matching copy differs by %d ms", d)
	}
}
