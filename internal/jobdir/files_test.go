//go:build linux

package jobdir

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The attacks of #113 on the checks of a job's file: a symbolic link as the file or on the
// way to it (also one that stays inside the job), something that is not a regular file, a
// hard link, a wrong size, a name swapped while the file is moved. Each is refused, and a
// refused move leaves nothing in the job.

// tree makes a job directory and a worker directory in a fresh intake root and returns
// their roots.
func tree(t *testing.T) (intake string, job, wrk *os.Root) {
	t.Helper()
	intake = t.TempDir()
	for _, d := range []string{filepath.Join(Jobs, id), filepath.Join(Worker, id)} {
		if err := os.MkdirAll(filepath.Join(intake, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	job, err := Open(intake, id)
	if err != nil {
		t.Fatal(err)
	}
	wrk, err = OpenWorker(intake, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = job.Close(); _ = wrk.Close() })
	return intake, job, wrk
}

func write(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

func TestOpenFile(t *testing.T) {
	intake, job, _ := tree(t)
	dir := Dir(intake, id)
	outside := filepath.Join(intake, "outside.mkv") // stands for a library file
	write(t, outside, "library")
	write(t, filepath.Join(dir, "files/Season 01/e1.mkv"), "upload")
	write(t, filepath.Join(dir, "files/real/e2.mkv"), "other")
	symlink(t, outside, filepath.Join(dir, "files/out.mkv"))
	symlink(t, "Season 01/e1.mkv", filepath.Join(dir, "files/in.mkv")) // stays inside the job
	symlink(t, "real", filepath.Join(dir, "files/linked"))             // a directory link inside
	symlink(t, intake, filepath.Join(dir, "files/up"))                 // a directory link out
	if err := syscall.Mkfifo(filepath.Join(dir, "files/fifo.mkv"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, info, err := OpenFile(job, "files/Season 01/e1.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len("upload")) {
		t.Errorf("size %d", info.Size())
	}
	buf := make([]byte, 16)
	if n, _ := f.Read(buf); string(buf[:n]) != "upload" {
		t.Errorf("read %q", buf[:n])
	}
	_ = f.Close()

	for rel, want := range map[string]error{
		"files/out.mkv":           ErrLink,
		"files/in.mkv":            ErrLink,
		"files/linked/e2.mkv":     ErrLink,
		"files/up/outside.mkv":    ErrLink,
		"files/fifo.mkv":          ErrNotRegular, // and the open does not hang
		"files/Season 01":         ErrNotRegular,
		"files/Season 01/e1.mkv/": ErrBadPath,
		"files/missing.mkv":       fs.ErrNotExist,
		"files/e1.mkv/x":          fs.ErrNotExist,
		"../outside.mkv":          ErrBadPath,
		"/etc/passwd":             ErrBadPath,
		"files/./x":               ErrBadPath,
		"files//x":                ErrBadPath,
		`files\x`:                 ErrBadPath,
		"":                        ErrBadPath,
	} {
		f, _, err := OpenFile(job, rel)
		if f != nil {
			_ = f.Close()
		}
		if !errors.Is(err, want) {
			t.Errorf("OpenFile(%q) = %v, want %v", rel, err, want)
		}
	}

	if _, err := CheckFile(job, "files/Season 01/e1.mkv", 6); err != nil {
		t.Errorf("CheckFile: %v", err)
	}
	if _, err := CheckFile(job, "files/Season 01/e1.mkv", 7); !errors.Is(err, ErrChanged) {
		t.Errorf("CheckFile with another size: %v", err)
	}
	if _, err := CheckFile(job, "files/out.mkv", -1); !errors.Is(err, ErrLink) {
		t.Errorf("CheckFile of a link: %v", err)
	}
}

func TestMakeDir(t *testing.T) {
	intake, job, _ := tree(t)
	d, err := MakeDir(job, "versions/r1")
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	if info, err := os.Lstat(filepath.Join(Dir(intake, id), "versions/r1")); err != nil || !info.IsDir() {
		t.Fatalf("versions/r1: %v", err)
	}
	away := t.TempDir()
	symlink(t, away, filepath.Join(Dir(intake, id), "versions/r2"))
	if _, err := MakeDir(job, "versions/r2/x"); !errors.Is(err, ErrLink) {
		t.Errorf("MakeDir through a link: %v", err)
	}
	if entries, _ := os.ReadDir(away); len(entries) != 0 {
		t.Errorf("created outside the job: %v", entries)
	}
}

func TestSeal(t *testing.T) {
	intake, job, wrk := tree(t)
	src := filepath.Join(intake, Worker, id, "files/a/Season 01/e1.mkv")
	write(t, src, "extracted")
	before, _ := os.Stat(src)

	for range 2 { // the repeat finds it moved, and checks it again
		if err := Seal(wrk, job, "files/a/Season 01/e1.mkv", 9); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(Dir(intake, id), "files/a/Season 01/e1.mkv")
	after, err := os.Lstat(dst)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("not moved with its inode: %v", err)
	}
	if _, err := os.Lstat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("still in the worker's directory: %v", err)
	}
}

func TestSealRefuses(t *testing.T) {
	library := filepath.Join(t.TempDir(), "film.mkv") // a file outside the job of the same size
	write(t, library, "12345")

	cases := map[string]struct {
		setup func(t *testing.T, wdir, jdir string)
		rel   string
		want  error
	}{
		"a link as the file": {
			setup: func(t *testing.T, wdir, _ string) { symlink(t, library, filepath.Join(wdir, "files/a/x.mkv")) },
			rel:   "files/a/x.mkv", want: ErrLink,
		},
		"a link to a file inside": {
			setup: func(t *testing.T, wdir, _ string) {
				write(t, filepath.Join(wdir, "files/a/real.mkv"), "12345")
				symlink(t, "real.mkv", filepath.Join(wdir, "files/a/x.mkv"))
			},
			rel: "files/a/x.mkv", want: ErrLink,
		},
		"a linked directory": {
			setup: func(t *testing.T, wdir, _ string) {
				symlink(t, filepath.Dir(library), filepath.Join(wdir, "files/a"))
			},
			rel: "files/a/film.mkv", want: ErrLink,
		},
		"a hard link": {
			setup: func(t *testing.T, wdir, _ string) {
				write(t, filepath.Join(wdir, "keep.mkv"), "12345")
				if err := os.MkdirAll(filepath.Join(wdir, "files/a"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(filepath.Join(wdir, "keep.mkv"), filepath.Join(wdir, "files/a/x.mkv")); err != nil {
					t.Fatal(err)
				}
			},
			rel: "files/a/x.mkv", want: ErrChanged,
		},
		"another size": {
			setup: func(t *testing.T, wdir, _ string) { write(t, filepath.Join(wdir, "files/a/x.mkv"), "123456") },
			rel:   "files/a/x.mkv", want: ErrChanged,
		},
		"a directory": {
			setup: func(t *testing.T, wdir, _ string) {
				if err := os.MkdirAll(filepath.Join(wdir, "files/a/x.mkv"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			rel: "files/a/x.mkv", want: ErrNotRegular,
		},
		"a link in the job's directory": {
			setup: func(t *testing.T, wdir, jdir string) {
				write(t, filepath.Join(wdir, "files/a/x.mkv"), "12345")
				symlink(t, filepath.Dir(library), filepath.Join(jdir, "files/a"))
			},
			rel: "files/a/x.mkv", want: ErrLink,
		},
		"a name the job has": {
			setup: func(t *testing.T, wdir, jdir string) {
				write(t, filepath.Join(wdir, "files/a/x.mkv"), "12345")
				write(t, filepath.Join(jdir, "files/a/x.mkv"), "kept!")
			},
			rel: "files/a/x.mkv", want: fs.ErrExist,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			intake, job, wrk := tree(t)
			wdir, jdir := filepath.Join(intake, Worker, id), Dir(intake, id)
			c.setup(t, wdir, jdir)
			err := Seal(wrk, job, c.rel, 5)
			if !errors.Is(err, c.want) {
				t.Fatalf("Seal = %v, want %v", err, c.want)
			}
			// Nothing of the worker's is in the job (only what the test put there).
			got, _ := os.ReadFile(filepath.Join(jdir, c.rel))
			if info, err := os.Lstat(filepath.Join(jdir, c.rel)); err == nil && string(got) != "kept!" {
				t.Errorf("the job has %s (%v)", c.rel, info.Mode())
			}
			if data, _ := os.ReadFile(library); string(data) != "12345" {
				t.Errorf("the outside file changed: %q", data)
			}
		})
	}
}

// TestSealSwap swaps the worker's name for a link to an outside file between the check and
// the rename: what was moved is not the file checked, so it is taken back out of the job.
func TestSealSwap(t *testing.T) {
	library := filepath.Join(t.TempDir(), "film.mkv")
	write(t, library, "12345")
	for name, swap := range map[string]func(t *testing.T, p string){
		"a link":         func(t *testing.T, p string) { symlink(t, library, p) },
		"another file":   func(t *testing.T, p string) { write(t, p, "54321") },
		"a directory":    func(t *testing.T, p string) { symlink(t, library, filepath.Join(p, "inner")) },
		"a hard link of": nil,
	} {
		t.Run(name, func(t *testing.T) {
			intake, job, wrk := tree(t)
			wdir := filepath.Join(intake, Worker, id)
			p := filepath.Join(wdir, "files/x.mkv")
			write(t, p, "12345")
			rename = func(from *os.File, n string, to *os.File) error {
				if swap == nil { // a second name made after the check
					if err := os.Link(p, filepath.Join(wdir, "keep.mkv")); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Rename(p, filepath.Join(wdir, "moved.mkv")); err != nil {
						t.Fatal(err)
					}
					swap(t, p)
				}
				return renameNoReplace(from, n, to)
			}
			t.Cleanup(func() { rename = renameNoReplace })
			if err := Seal(wrk, job, "files/x.mkv", 5); !errors.Is(err, ErrChanged) {
				t.Fatalf("Seal = %v", err)
			}
			if _, err := os.Lstat(filepath.Join(Dir(intake, id), "files/x.mkv")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("left in the job: %v", err)
			}
		})
	}
}

func TestPrepareAndRemoveTheWorkersDirectory(t *testing.T) {
	intake := t.TempDir()
	if err := Prepare(intake); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{Incoming, Jobs, Worker} {
		if info, err := os.Stat(filepath.Join(intake, d)); err != nil || !info.IsDir() {
			t.Errorf("%s: %v", d, err)
		}
	}
	w, err := MakeWorker(intake, id)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	if _, err := OpenWorker(intake, id); err != nil {
		t.Fatal(err)
	}
	if err := Remove(intake, id); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWorker(intake, id); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the worker's directory is left: %v", err)
	}
}
