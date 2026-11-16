package fixtures

import (
	"archive/zip"
	"compress/flate"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"
)

func zipNames(t *testing.T, path string) []string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names
}

// TestHostileCorpus checks that every archive really carries the hostile property it
// claims, so extractor tests (M1) cannot pass against a harmless corpus.
func TestHostileCorpus(t *testing.T) {
	limits := DefaultLimits
	limits.BombBytes = 64 << 20 // keep the unit test fast; the ratio is what matters
	dir := t.TempDir()
	corpus := HostileCorpus(limits)

	seen := map[string]bool{}
	for _, h := range corpus {
		if seen[h.Name] {
			t.Fatalf("duplicate corpus name %s", h.Name)
		}
		seen[h.Name] = true
		path, err := h.Build(dir)
		if err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(path)
		r, err := zip.OpenReader(path)
		if err != nil {
			t.Fatalf("%s: not readable as a zip: %v", h.Name, err)
		}
		files := r.File
		has := func(pred func(*zip.File) bool) bool {
			for _, f := range files {
				if pred(f) {
					return true
				}
			}
			return false
		}
		var ok bool
		switch h.Name {
		case "traversal.zip":
			ok = has(func(f *zip.File) bool { return strings.HasPrefix(f.Name, "../") })
		case "absolute.zip":
			ok = has(func(f *zip.File) bool { return strings.HasPrefix(f.Name, "/") })
		case "drive-letter.zip":
			ok = has(func(f *zip.File) bool { return len(f.Name) > 2 && f.Name[1] == ':' })
		case "backslash.zip":
			ok = has(func(f *zip.File) bool { return strings.Contains(f.Name, `\..\`) })
		case "symlink.zip":
			ok = has(func(f *zip.File) bool { return f.Mode()&fs.ModeSymlink != 0 })
		case "device.zip":
			ok = has(func(f *zip.File) bool { return f.Mode()&fs.ModeCharDevice != 0 }) &&
				has(func(f *zip.File) bool { return f.Mode()&fs.ModeNamedPipe != 0 })
		case "bomb.zip":
			ratio := float64(files[0].UncompressedSize64) / float64(st.Size())
			ok = ratio > 900
			t.Logf("bomb ratio %.0f:1", ratio)
		case "lying-size.zip":
			// Go's reader stops at the declared size (good for M1); read the raw stream to
			// show the data really is larger than the header says.
			raw, err := files[0].OpenRaw()
			if err != nil {
				t.Fatal(err)
			}
			n, _ := io.Copy(io.Discard, flate.NewReader(raw))
			ok = files[0].UncompressedSize64 == 1<<10 && n == 64<<20
		case "too-many-entries.zip":
			ok = len(files) == limits.MaxEntries+1
		case "nested.zip":
			ok = has(func(f *zip.File) bool { return strings.HasSuffix(f.Name, ".zip") })
		case "renamed-non-video.zip":
			ok = has(func(f *zip.File) bool { return strings.HasSuffix(f.Name, ".mkv") })
		case "duplicate-names.zip":
			ok = len(files) == 2 && files[0].Name == files[1].Name
		case "unicode-collision.zip":
			ok = len(files) == 2 && files[0].Name != files[1].Name
		case "control-characters.zip":
			ok = has(func(f *zip.File) bool { return strings.ContainsAny(f.Name, "\n\t\x1b\x00") })
		case "long-name.zip":
			ok = has(func(f *zip.File) bool { return len(f.Name) > limits.MaxNameBytes })
		default:
			t.Errorf("%s: no check for this corpus entry", h.Name)
			ok = true
		}
		_ = r.Close()
		if !ok {
			t.Errorf("%s does not carry its hostile property: %s", h.Name, h.About)
		}
	}
}
