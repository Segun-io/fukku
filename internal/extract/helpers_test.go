package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
)

// sandbox is a directory holding a job directory and things outside it that no extraction
// may touch.
type sandbox struct {
	dir  string // the sandbox
	job  string // <dir>/job
	root *os.Root
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	for _, d := range []string{filepath.Join(job, "archives"), filepath.Join(job, "files"), filepath.Join(dir, "outside")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(dir, "evil.txt"), filepath.Join(dir, "outside", "keep.txt"), filepath.Join(job, "keep.txt")} {
		if err := os.WriteFile(f, []byte("keep\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(job)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return &sandbox{dir: dir, job: job, root: root}
}

// put copies an archive into <job>/archives and returns its job-relative path.
func (s *sandbox) put(t *testing.T, src string) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	rel := "archives/" + filepath.Base(src)
	if err := os.WriteFile(filepath.Join(s.job, filepath.FromSlash(rel)), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return rel
}

// listing describes every path under dir (not following symlinks), except those under skip.
func listing(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		for _, s := range skip {
			if rel == s {
				return filepath.SkipDir
			}
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if fi.IsDir() {
			out[rel] = fi.Mode().String() // its mtime changes when dest is created in it
		} else {
			out[rel] = fmt.Sprintf("%v %d %d", fi.Mode(), fi.Size(), fi.ModTime().UnixNano())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// treeOf returns every regular file under dir with its content, and fails on anything that
// is neither a regular file nor a directory.
func treeOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return out
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		switch {
		case d.IsDir():
		case d.Type().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = string(data)
		default:
			t.Errorf("%s is a %v, not a file or directory", rel, d.Type())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// checkSandbox compares the sandbox with its listing from before the run: nothing outside
// the job directory and nothing in the job directory outside dest and dest.partial may
// change, and nothing anywhere may be a symlink or special file.
func (s *sandbox) checkSandbox(t *testing.T, before map[string]string, dest string) {
	t.Helper()
	after := listing(t, s.dir, "job/"+dest, "job/"+dest+partialSuffix)
	if !maps.Equal(before, after) {
		for k, v := range after {
			if before[k] != v {
				t.Errorf("changed or created outside the extraction: %s (%s → %s)", k, before[k], v)
			}
		}
		for k := range before {
			if _, ok := after[k]; !ok {
				t.Errorf("removed outside the extraction: %s", k)
			}
		}
	}
	treeOf(t, s.dir) // fails on symlinks and special files anywhere in the sandbox
	if _, err := os.Lstat(filepath.Join(s.job, filepath.FromSlash(dest+partialSuffix))); !os.IsNotExist(err) {
		t.Errorf("%s%s is left behind (%v)", dest, partialSuffix, err)
	}
}

// zent is one entry of a test archive.
type zent struct {
	name     string
	data     string
	mode     fs.FileMode // 0: a regular file, 0o644
	method   uint16      // 0: zip.Store; use deflate for compressible data
	flags    uint16
	raw      bool // write data as the already-compressed stream with method
	modified time.Time
}

func buildZip(t *testing.T, path string, entries ...zent) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: e.method, Flags: e.flags, Modified: e.modified}
		if hdr.Modified.IsZero() {
			hdr.Modified = time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr.SetMode(mode)
		var w io.Writer
		var err error
		if e.raw {
			hdr.CompressedSize64 = uint64(len(e.data))
			hdr.UncompressedSize64 = uint64(len(e.data))
			w, err = zw.CreateRaw(hdr)
		} else {
			w, err = zw.CreateHeader(hdr)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// testLimits are the defaults with the free-space margin off (a test machine may have less
// free space than the production default).
func testLimits() Limits {
	lim := LimitsFrom(config.Defaults())
	lim.FreeSpaceMargin = 0
	return lim
}

func sortedSkipped(s []Skipped) []string {
	out := make([]string, 0, len(s))
	for _, k := range s {
		out = append(out, fmt.Sprintf("%q: %s", k.Name, k.Reason))
	}
	sort.Strings(out)
	return out
}

func sortedPaths(files []Entry) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	slices.Sort(out)
	return out
}

// cancelAfter is a context whose Err reports cancellation after n calls.
type cancelAfter struct {
	context.Context
	n atomic.Int64
}

func newCancelAfter(n int64) *cancelAfter {
	c := &cancelAfter{Context: context.Background()}
	c.n.Store(n)
	return c
}

func (c *cancelAfter) Err() error {
	if c.n.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}
