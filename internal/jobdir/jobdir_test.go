package jobdir

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const id = "0123456789abcdef0123456789abcdef"

func TestValidID(t *testing.T) {
	for _, good := range []string{id, strings.Repeat("0", 32), strings.Repeat("f", 32)} {
		if !ValidID(good) {
			t.Errorf("%q refused", good)
		}
	}
	for _, bad := range []string{
		"", "..", "../" + id[3:], id[:31], id + "0", strings.ToUpper(id), id[:31] + "g",
		id[:16] + "/" + id[17:], id + "\n", " " + id[1:],
	} {
		if ValidID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDir(t *testing.T) {
	if got, want := Dir("/data/intake", id), filepath.Join("/data/intake", "jobs", id); got != want {
		t.Fatalf("Dir %q, want %q", got, want)
	}
	for _, bad := range []string{"", "..", "../other", "/etc"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Dir(%q) did not panic", bad)
				}
			}()
			Dir("/data/intake", bad)
		}()
	}
}

func TestRemove(t *testing.T) {
	root := t.TempDir()
	other := strings.Repeat("a", 32)
	for _, j := range []string{id, other} {
		if err := os.MkdirAll(filepath.Join(Dir(root, j), Files, "Season 01"), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(Dir(root, id), Files, "Season 01", "e1.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 { // a second Remove finds nothing and is no error
		if err := Remove(root, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(Dir(root, id)); !os.IsNotExist(err) {
		t.Fatalf("job directory still there: %v", err)
	}
	if _, err := os.Stat(Dir(root, other)); err != nil {
		t.Fatalf("the other job: %v", err)
	}
}

// Remove and RemoveUpload work through an os.Root of the intake root: with jobs/ or
// .incoming/ replaced by a symbolic link out of it (the worker can write the intake root,
// #137), nothing outside is deleted, and a symlinked job directory is removed itself.
func TestRemoveStaysInsideTheIntakeRoot(t *testing.T) {
	root, outside, outsideUploads := t.TempDir(), t.TempDir(), t.TempDir()
	keep := []string{
		filepath.Join(outside, id, Files, "e1.mkv"),
		filepath.Join(outsideUploads, id),
		filepath.Join(outsideUploads, id+".info"),
	}
	if err := os.MkdirAll(filepath.Dir(keep[0]), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, p := range keep {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, Jobs)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideUploads, filepath.Join(root, Incoming)); err != nil {
		t.Fatal(err)
	}
	_ = Remove(root, id)
	_ = RemoveUpload(root, id)
	for _, p := range keep {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("%s outside the intake root was deleted: %v", p, err)
		}
	}

	// A real jobs/ whose job directory is a link: the link goes, its target stays.
	if err := os.Remove(filepath.Join(root, Jobs)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, Jobs), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, id), Dir(root, id)); err != nil {
		t.Fatal(err)
	}
	if err := Remove(root, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(Dir(root, id)); !os.IsNotExist(err) {
		t.Fatalf("the link is still there: %v", err)
	}
	if _, err := os.Stat(keep[0]); err != nil {
		t.Fatalf("the link's target was deleted: %v", err)
	}
}

func TestRemoveUpload(t *testing.T) {
	root := t.TempDir()
	inc := filepath.Join(root, Incoming)
	if err := os.MkdirAll(inc, 0o750); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(inc, strings.Repeat("b", 32))
	for _, p := range []string{filepath.Join(inc, id), filepath.Join(inc, id+".info"), keep} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := RemoveUpload(root, id); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(inc)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(keep) {
		t.Fatalf(".incoming holds %v", entries)
	}
	defer func() {
		if recover() == nil {
			t.Error("RemoveUpload with a malformed id did not panic")
		}
	}()
	_ = RemoveUpload(root, "../x")
}

// Open refuses a job directory, or a jobs/ directory, that a compromised worker replaced with
// a symbolic link out of the intake root (#63), and still opens a real one.
func TestOpenRefusesALinkOutOfTheIntakeRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := Open(root, id); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing job directory: err = %v, want fs.ErrNotExist", err)
	}
	if err := os.MkdirAll(Dir(root, id), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Open(root, id)
	if err != nil {
		t.Fatalf("real job directory: %v", err)
	}
	_ = r.Close()

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, Files), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(Dir(root, id)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, Dir(root, id)); err != nil {
		t.Fatal(err)
	}
	if r, err := Open(root, id); err == nil {
		_ = r.Close()
		t.Fatal("a symlinked job directory leading out of the intake root was opened")
	}

	// The same with jobs/ itself replaced.
	if err := os.RemoveAll(filepath.Join(root, Jobs)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, Jobs)); err != nil {
		t.Fatal(err)
	}
	if r, err := Open(root, id); err == nil {
		_ = r.Close()
		t.Fatal("a symlinked jobs/ directory leading out of the intake root was opened")
	}
}
