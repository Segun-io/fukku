package jobdir

import (
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
