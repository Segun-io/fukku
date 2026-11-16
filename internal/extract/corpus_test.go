package extract

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/segun-io/fukku/test/fixtures"
)

// corpusWant is what one hostile archive must give when .txt files are allowed. With the
// default extensions, every .txt file expected on disk is skipped as "extension" instead.
type corpusWant struct {
	refused string
	files   map[string]string // path under dest → content; "" means check by Expect (NoOverwrite)
	skipped map[string]string // raw entry name → reason
}

func corpusWants(l fixtures.Limits) map[string]corpusWant {
	small := "not a video\n"
	return map[string]corpusWant{
		"traversal.zip": {files: map[string]string{"ok.txt": small}, skipped: map[string]string{"../../evil.txt": ReasonTraversal}},
		"absolute.zip":  {skipped: map[string]string{"/etc/evil.txt": ReasonAbsolute}},
		// The backslash rule comes first, so C:\evil.txt is a backslash.
		"drive-letter.zip": {skipped: map[string]string{`C:\evil.txt`: ReasonBackslash, "C:/evil2.txt": ReasonDriveLetter}},
		"backslash.zip":    {skipped: map[string]string{`season\..\..\evil.txt`: ReasonBackslash}},
		// No link is created, so dirlink/evil.txt lands in a real directory under dest.
		"symlink.zip": {
			files:   map[string]string{"dirlink/evil.txt": small},
			skipped: map[string]string{"link": ReasonSymlink, "dirlink": ReasonSymlink},
		},
		"device.zip":            {skipped: map[string]string{"tty": ReasonSpecial, "fifo": ReasonSpecial, "sock": ReasonSpecial}},
		"bomb.zip":              {refused: RefusedRatio},
		"lying-size.zip":        {refused: RefusedNotAZip},
		"too-many-entries.zip":  {refused: RefusedTooManyEntries},
		"nested.zip":            {skipped: map[string]string{"Season 01.zip": ReasonNestedArchive}},
		"renamed-non-video.zip": {files: map[string]string{"Show - S01E01.mkv": strings.Repeat("hello\n", 1000)}},
		"duplicate-names.zip": {
			files:   map[string]string{"Show - S01E01.mkv": "first"},
			skipped: map[string]string{"Show - S01E01.mkv": ReasonDuplicate},
		},
		// Which spelling comes first depends on the generator; the Expect check reads the order.
		"unicode-collision.zip": {files: map[string]string{"Pok\u00e9mon.srt": ""}},
		"control-characters.zip": {files: map[string]string{
			"badname.srt": small, "tabname.srt": small, "esc[31m.srt": small, "nul.srt": small,
		}},
		"long-name.zip": {skipped: map[string]string{strings.Repeat("n", l.MaxNameBytes+51) + ".mkv": ReasonNameTooLong}},
		"deep-nesting.zip": {skipped: map[string]string{
			strings.Repeat("a/", fixtures.DeepNesting) + "x.mkv": ReasonNameTooLong,
		}},
		// Directories count towards the entries allowed.
		"many-directories.zip": {refused: RefusedTooManyEntries},
	}
}

// TestHostileCorpus runs every archive of the hostile corpus, built with the default
// limits (the bomb is a full 1 GiB of zeros), through Run inside a sandbox.
func TestHostileCorpus(t *testing.T) {
	fl := fixtures.DefaultLimits
	corpus := fixtures.HostileCorpus(fl)
	wants := corpusWants(fl)
	built := t.TempDir()

	names := make([]string, 0, len(corpus))
	for _, h := range corpus {
		names = append(names, h.Name)
	}
	for name := range wants {
		if !slices.Contains(names, name) {
			t.Errorf("expectation for %s, which is not in the corpus", name)
		}
	}

	for _, h := range corpus {
		want, ok := wants[h.Name]
		if !ok {
			t.Errorf("%s: no expectation in this test; add one", h.Name)
			continue
		}
		src, err := h.Build(built)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"txt-allowed", "defaults"} {
			t.Run(h.Name+"/"+mode, func(t *testing.T) {
				lim := testLimits()
				w := want
				if mode == "txt-allowed" {
					lim.Extensions = append(slices.Clone(lim.Extensions), ".txt")
				} else {
					w = withoutTxt(want)
				}
				runCorpusCase(t, h, src, lim, w)
			})
		}
	}
}

// withoutTxt moves the .txt files of w to the skipped list, as the default extensions do.
func withoutTxt(w corpusWant) corpusWant {
	out := corpusWant{refused: w.refused, files: map[string]string{}, skipped: map[string]string{}}
	for k, v := range w.skipped {
		out.skipped[k] = v
	}
	for k, v := range w.files {
		if strings.HasSuffix(k, ".txt") {
			out.skipped[k] = ReasonExtension // these entries' names need no cleaning
		} else {
			out.files[k] = v
		}
	}
	return out
}

func runCorpusCase(t *testing.T, h fixtures.Hostile, src string, lim Limits, want corpusWant) {
	sb := newSandbox(t)
	archive := sb.put(t, src)
	dest := "files/" + strings.TrimSuffix(h.Name, ".zip")
	before := listing(t, sb.dir, "job/"+dest, "job/"+dest+partialSuffix)

	x := newExtractor(sb.root, Args{Archive: archive, Dest: dest}, lim)
	out, err := x.run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	sb.checkSandbox(t, before, dest)
	disk := treeOf(t, filepath.Join(sb.job, filepath.FromSlash(dest)))

	// The case's own Expect.
	switch h.Expect {
	case fixtures.RefuseArchive:
		if out.Refused == "" {
			t.Errorf("not refused: %+v", out)
		}
		if _, err := os.Lstat(filepath.Join(sb.job, filepath.FromSlash(dest))); !os.IsNotExist(err) {
			t.Errorf("a refused archive left %s behind", dest)
		}
	case fixtures.RejectEntry:
		if out.Refused != "" || len(out.Skipped) == 0 {
			t.Errorf("want skipped entries, got %+v", out)
		}
	case fixtures.NoOverwrite:
		first, second := firstTwo(t, src)
		if len(out.Files) != 1 || disk[strings.TrimPrefix(out.Files[0].Path, dest+"/")] != first {
			t.Errorf("want only the first copy (%q) on disk, got %v", first, disk)
		}
		if len(out.Skipped) != 1 || out.Skipped[0].Reason != ReasonDuplicate || out.Skipped[0].Name != printable(second) {
			t.Errorf("want the second copy skipped as duplicate, got %+v", out.Skipped)
		}
	case fixtures.Normalise, fixtures.RejectContent:
		if out.Refused != "" || len(out.Files) == 0 {
			t.Errorf("want the archive unpacked, got %+v", out)
		}
	default:
		t.Fatalf("unknown Expect %q", h.Expect)
	}

	// The exact outcome.
	if out.Refused != want.refused {
		t.Errorf("refused %q, want %q", out.Refused, want.refused)
	}
	if h.Expect == fixtures.NoOverwrite && len(want.skipped) == 0 {
		_, second := firstTwo(t, src)
		want.skipped = map[string]string{second: ReasonDuplicate}
	}
	var wantSkipped []Skipped
	for name, reason := range want.skipped {
		wantSkipped = append(wantSkipped, Skipped{Name: printable(name), Reason: reason})
	}
	if got, w := sortedSkipped(out.Skipped), sortedSkipped(wantSkipped); !slices.Equal(got, w) {
		t.Errorf("skipped\n got %v\nwant %v", got, w)
	}
	var wantPaths []string
	for p := range want.files {
		wantPaths = append(wantPaths, path.Join(dest, p))
	}
	slices.Sort(wantPaths)
	if got := sortedPaths(out.Files); !slices.Equal(got, wantPaths) {
		t.Errorf("files\n got %q\nwant %q", got, wantPaths)
	}
	if len(disk) != len(out.Files) {
		t.Errorf("on disk %d files, reported %d", len(disk), len(out.Files))
	}
	for _, f := range out.Files {
		rel := strings.TrimPrefix(f.Path, dest+"/")
		content, ok := disk[rel]
		if !ok {
			t.Errorf("reported %s is not on disk", f.Path)
			continue
		}
		if int64(len(content)) != f.Size {
			t.Errorf("%s: size %d reported, %d on disk", f.Path, f.Size, len(content))
		}
		if w := want.files[rel]; w != "" && content != w {
			t.Errorf("%s: content %q, want %q", f.Path, content, w)
		}
	}

	if h.Name == "bomb.zip" {
		st, _ := os.Stat(src)
		limit := int64(max(lim.MaxRatio*float64(st.Size()), float64(lim.RatioFloor)))
		t.Logf("bomb: %d bytes written before the refusal, limit %d", x.written, limit)
		if x.written > limit+copyBufferSize {
			t.Errorf("bomb: %d bytes written, more than the limit %d plus one buffer", x.written, limit)
		}
	}
}

// firstTwo returns the content of the archive's first entry and the name of its second.
func firstTwo(t *testing.T, src string) (string, string) {
	t.Helper()
	r, err := zip.OpenReader(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if len(r.File) != 2 {
		t.Fatalf("%s has %d entries, want 2", src, len(r.File))
	}
	rc, err := r.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), r.File[1].Name
}
