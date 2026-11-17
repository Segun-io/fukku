package extract

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/test/fixtures"
)

func TestLimitsFrom(t *testing.T) {
	cfg := config.Defaults()
	got := LimitsFrom(cfg)
	want := Limits{
		MaxEntries: 500, MaxWrittenBytes: 200 << 30, MaxRatio: 20, RatioFloor: 1 << 20, MaxNameBytes: 200,
		MaxPathSegments: 32, MaxPathBytes: 1024, FreeSpaceMargin: 100 << 30,
		Extensions: []string{".mkv", ".mp4", ".m4v", ".avi", ".mov", ".ts", ".webm", ".srt", ".ass", ".ssa"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LimitsFrom(defaults) = %+v, want %+v", got, want)
	}
	got.Extensions[0] = ".exe"
	if cfg.Upload.VideoExtensions[0] != ".mkv" {
		t.Error("LimitsFrom shares the configuration's extension list")
	}
}

// run extracts entries into files/out and returns the output and what is on disk.
func run(t *testing.T, lim Limits, entries ...zent) (Output, map[string]string) {
	t.Helper()
	sb := newSandbox(t)
	archive := sb.put(t, buildZip(t, filepath.Join(t.TempDir(), "a.zip"), entries...))
	before := listing(t, sb.dir, "job/files/out", "job/files/out.partial")
	out, err := Run(context.Background(), sb.root, sb.root, Args{Archive: archive, Dest: "files/out"}, lim)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	sb.checkSandbox(t, before, "files/out")
	return out, treeOf(t, filepath.Join(sb.job, "files", "out"))
}

func TestEntryRules(t *testing.T) {
	video := "video data"
	tests := []struct {
		name    string
		entries []zent
		files   map[string]string // under dest
		skipped []string          // `"name": reason`, sorted
	}{
		{"nested folders and explicit directories",
			[]zent{{name: "Season 01/"}, {name: "Season 01/Extras/", mode: fs.ModeDir | 0o700}, {name: "Season 01/Extras/a.mkv", data: video}},
			map[string]string{"Season 01/Extras/a.mkv": video}, nil},
		{"dot and empty segments",
			[]zent{{name: "./a.mkv", data: "a"}, {name: "x//b.srt", data: "b"}, {name: "./", mode: fs.ModeDir}},
			map[string]string{"a.mkv": "a", "x/b.srt": "b"}, nil},
		{"extension case",
			[]zent{{name: "FILM.MKV", data: video}, {name: "sub.Srt", data: "s"}},
			map[string]string{"FILM.MKV": video, "sub.Srt": "s"}, nil},
		{"extensions",
			[]zent{{name: "run.sh"}, {name: "noext"}, {name: "trailing.mkv "}, {name: "a.mkv.exe"}, {name: "dir/"}},
			nil, []string{`"a.mkv.exe": extension`, `"noext": extension`, `"run.sh": extension`, `"trailing.mkv ": extension`}},
		{"nested archives of every kind",
			[]zent{{name: "a.ZIP"}, {name: "b.rar"}, {name: "c.7z"}, {name: "d.tar.gz"}, {name: "e.iso"}},
			nil, []string{`"a.ZIP": nested_archive`, `"b.rar": nested_archive`, `"c.7z": nested_archive`, `"d.tar.gz": nested_archive`, `"e.iso": nested_archive`}},
		{"case-folded duplicate",
			[]zent{{name: "Film.mkv", data: "first"}, {name: "film.MKV", data: "second"}},
			map[string]string{"Film.mkv": "first"}, []string{`"film.MKV": duplicate`}},
		{"directories keep their first spelling",
			[]zent{{name: "Season 01/a.mkv", data: "a"}, {name: "SEASON 01/b.mkv", data: "b"}},
			map[string]string{"Season 01/a.mkv": "a", "Season 01/b.mkv": "b"}, nil},
		{"duplicate after cleaning",
			[]zent{{name: "a.srt", data: "first"}, {name: " a.srt", data: "second"}, {name: "a\x00.srt", data: "third"}},
			map[string]string{"a.srt": "first"}, []string{`" a.srt": duplicate`, `"a\\x00.srt": duplicate`}},
		{"a file where a directory goes, and the reverse",
			[]zent{{name: "x.mkv", data: "file"}, {name: "x.mkv/y.srt", data: "under"}, {name: "d.srt/"}, {name: "d.srt", data: "file"}},
			map[string]string{"x.mkv": "file"}, []string{`"d.srt": duplicate`, `"x.mkv/y.srt": duplicate`}},
		{"drive letter in a later segment",
			[]zent{{name: "a/C:b.mkv"}},
			nil, []string{`"a/C:b.mkv": drive_letter`}},
		{"segments that are dot segments only once cleaned",
			[]zent{{name: " ../a.mkv"}, {name: "\x01/a.mkv"}, {name: "..\x00/a.mkv"}},
			nil, []string{`" ../a.mkv": traversal`, `"..\\x00/a.mkv": traversal`, `"\\x01/a.mkv": traversal`}},
		{"long directory name",
			[]zent{{name: strings.Repeat("d", 201) + "/a.mkv"}},
			nil, []string{fmt.Sprintf("%q: name_too_long", strings.Repeat("d", 200))}},
		{"encrypted",
			[]zent{{name: "secret.mkv", data: "xx", flags: 0x1}},
			nil, []string{`"secret.mkv": encrypted`}},
		{"bidirectional override in a name",
			[]zent{{name: "a\u202evkm.srt", data: "s"}},
			map[string]string{"avkm.srt": "s"}, nil},
		{"path segments up to the limit",
			[]zent{{name: strings.Repeat("a/", 31) + "x.mkv", data: "x"}},
			map[string]string{strings.Repeat("a/", 31) + "x.mkv": "x"}, nil},
		{"one path segment too many",
			[]zent{{name: strings.Repeat("a/", 32) + "x.mkv"}, {name: "./" + strings.Repeat("b//", 32) + "y.mkv"}},
			nil, []string{
				fmt.Sprintf("%q: name_too_long", "./"+strings.Repeat("b//", 32)+"y.mkv"),
				fmt.Sprintf("%q: name_too_long", strings.Repeat("a/", 32)+"x.mkv"),
			}},
		{"path bytes up to the limit",
			[]zent{{name: strings.Repeat(strings.Repeat("d", 200)+"/", 5) + strings.Repeat("x", 15) + ".mkv", data: "x"}},
			map[string]string{strings.Repeat(strings.Repeat("d", 200)+"/", 5) + strings.Repeat("x", 15) + ".mkv": "x"}, nil},
		{"one path byte too many",
			[]zent{{name: strings.Repeat(strings.Repeat("d", 200)+"/", 5) + strings.Repeat("x", 16) + ".mkv"}},
			nil, []string{fmt.Sprintf("%q: name_too_long", strings.Repeat("d", 200))}},
		{"path bytes are counted after cleaning",
			[]zent{{name: strings.Repeat(strings.Repeat("d", 200)+"\x00/", 5) + strings.Repeat("x", 15) + ".mkv", data: "x"}},
			map[string]string{strings.Repeat(strings.Repeat("d", 200)+"/", 5) + strings.Repeat("x", 15) + ".mkv": "x"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, disk := run(t, testLimits(), tt.entries...)
			if out.Refused != "" {
				t.Fatalf("refused %s", out.Refused)
			}
			if tt.files == nil {
				tt.files = map[string]string{}
			}
			if !maps.Equal(disk, tt.files) {
				t.Errorf("on disk %q, want %q", disk, tt.files)
			}
			slices.Sort(tt.skipped)
			if got := sortedSkipped(out.Skipped); !slices.Equal(got, tt.skipped) && len(got)+len(tt.skipped) > 0 {
				t.Errorf("skipped\n got %v\nwant %v", got, tt.skipped)
			}
			for _, f := range out.Files {
				if c, ok := disk[strings.TrimPrefix(f.Path, "files/out/")]; !ok || int64(len(c)) != f.Size {
					t.Errorf("reported %+v, on disk %q", f, c)
				}
			}
		})
	}
}

func TestModesAndTimesIgnored(t *testing.T) {
	sb := newSandbox(t)
	stamp := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	archive := sb.put(t, buildZip(t, filepath.Join(t.TempDir(), "m.zip"),
		zent{name: "d/", mode: fs.ModeDir | 0o777, modified: stamp},
		zent{name: "d/x.mkv", data: "x", mode: fs.ModeSetuid | 0o777, modified: stamp},
		zent{name: "y.srt", data: "y", mode: 0o000, modified: stamp},
	))
	if _, err := Run(context.Background(), sb.root, sb.root, Args{Archive: archive, Dest: "files/out"}, testLimits()); err != nil {
		t.Fatal(err)
	}
	for rel, maxPerm := range map[string]fs.FileMode{"d": 0o755, "d/x.mkv": 0o644, "y.srt": 0o644} {
		fi, err := os.Lstat(filepath.Join(sb.job, "files", "out", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&^(fs.ModeDir|maxPerm) != 0 {
			t.Errorf("%s has mode %v, want at most %v", rel, fi.Mode(), maxPerm)
		}
		if fi.Mode().Perm()&0o600 != 0o600 {
			t.Errorf("%s has mode %v: the owner cannot read and write it", rel, fi.Mode())
		}
		if fi.ModTime().Equal(stamp) {
			t.Errorf("%s has the archive's time", rel)
		}
	}
}

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"Show - S01E01.mkv":        "Show - S01E01.mkv",
		"bad\nname\t\x00\x1b":      `bad\x0aname\x09\x00\x1b`,
		"a\xffb":                   `a\xffb`,
		"a\u202eb\u0085":           `a\u202eb\u0085`,
		`C:\evil.txt`:              `C:\evil.txt`,
		"Pok\u00e9mon フック":         "Pok\u00e9mon フック",
		strings.Repeat("n", 300):   strings.Repeat("n", 200),
		strings.Repeat("é", 150):   strings.Repeat("é", 100),
		strings.Repeat("\x00", 60): strings.Repeat(`\x00`, 50),
	} {
		if got := printable(in); got != want {
			t.Errorf("printable(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArchiveRefusals(t *testing.T) {
	big := strings.Repeat("0123456789abcdef", 4096) // 64 KiB, stored: ratio about 1
	tests := []struct {
		name    string
		lim     func(*Limits)
		entries []zent
		refused string
	}{
		{"too large, counted as written", func(l *Limits) { l.MaxWrittenBytes = 100 << 10 },
			[]zent{{name: "a.mkv", data: big}, {name: "b.mkv", data: big}}, RefusedTooLarge},
		{"exactly at the size limit is fine", func(l *Limits) { l.MaxWrittenBytes = 128 << 10 },
			[]zent{{name: "a.mkv", data: big}, {name: "b.mkv", data: big}}, ""},
		{"ratio", func(l *Limits) { l.RatioFloor = 0 },
			[]zent{{name: "a.srt", data: strings.Repeat("a", 64<<10), method: zip.Deflate}}, RefusedRatio},
		{"ratio floor lets small text through", nil,
			[]zent{{name: "a.srt", data: strings.Repeat("a", 64<<10), method: zip.Deflate}}, ""},
		{"too many entries", func(l *Limits) { l.MaxEntries = 2 },
			[]zent{{name: "a.mkv"}, {name: "b.mkv"}, {name: "c/"}}, RefusedTooManyEntries},
		{"free space", func(l *Limits) { l.FreeSpaceMargin = math.MaxInt64 },
			[]zent{{name: "a.mkv", data: "x"}}, RefusedFreeSpace},
		{"unsupported compression", nil,
			[]zent{{name: "a.mkv", data: "BZh9", method: 12, raw: true}}, RefusedNotAZip},
		{"corrupt deflate data", nil,
			[]zent{{name: "a.mkv", data: "\xff\xff\xff\xff", method: zip.Deflate, raw: true}}, RefusedNotAZip},
		// Four directories and two files are six things created.
		{"directories count towards the entries", func(l *Limits) { l.MaxEntries = 5 },
			[]zent{{name: "a/b/c/d/x.mkv"}, {name: "y.mkv"}}, RefusedTooManyEntries},
		{"directories and files up to the entries allowed", func(l *Limits) { l.MaxEntries = 6 },
			[]zent{{name: "a/b/c/d/x.mkv"}, {name: "a/b/c/d/"}, {name: "y.mkv"}}, ""},
		{"directories count towards the written bytes", func(l *Limits) { l.MaxWrittenBytes = 3 * dirCost },
			[]zent{{name: "a/b/c/d/"}}, RefusedTooLarge},
		{"directories up to the written bytes", func(l *Limits) { l.MaxWrittenBytes = 4 * dirCost },
			[]zent{{name: "a/b/c/d/"}}, ""},
		{"directories count towards the ratio", func(l *Limits) { l.RatioFloor = 0 },
			[]zent{{name: "a/"}}, RefusedRatio},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lim := testLimits()
			if tt.lim != nil {
				tt.lim(&lim)
			}
			out, disk := run(t, lim, tt.entries...)
			if out.Refused != tt.refused {
				t.Fatalf("refused %q, want %q (%+v)", out.Refused, tt.refused, out)
			}
			if tt.refused != "" && (len(disk) > 0 || len(out.Files) > 0 || len(out.Skipped) > 0) {
				t.Errorf("a refused archive left %v, reported %+v", disk, out)
			}
		})
	}
}

func TestNotAZip(t *testing.T) {
	for name, data := range map[string]string{
		"empty": "", "text": "hello, world\n", "truncated": "PK\x03\x04\x14\x00",
	} {
		t.Run(name, func(t *testing.T) {
			sb := newSandbox(t)
			if err := os.WriteFile(filepath.Join(sb.job, "archives", "x.zip"), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := Run(context.Background(), sb.root, sb.root, Args{Archive: "archives/x.zip", Dest: "files/x"}, testLimits())
			if err != nil || out.Refused != RefusedNotAZip {
				t.Fatalf("got %+v, %v; want refused not_a_zip", out, err)
			}
			if _, err := os.Lstat(filepath.Join(sb.job, "files", "x")); !os.IsNotExist(err) {
				t.Error("dest exists")
			}
		})
	}
}

// TestHugeDirectory: a central directory of many tiny entries is refused while it is read,
// before its entries are held in memory.
func TestHugeDirectory(t *testing.T) {
	entries := make([]zent, 40000)
	for i := range entries {
		entries[i] = zent{name: fmt.Sprintf("%05d", i)}
	}
	sb := newSandbox(t)
	archive := sb.put(t, buildZip(t, filepath.Join(t.TempDir(), "many.zip"), entries...))
	lim := testLimits()
	lim.MaxEntries = 10
	x := newExtractor(sb.root, Args{Archive: archive, Dest: "files/out"}, lim)
	out, err := x.run(context.Background())
	if err != nil || out.Refused != RefusedTooManyEntries {
		t.Fatalf("got %+v, %v; want too_many_entries", out, err)
	}
	if budget := int64(lim.MaxEntries)*dirBytesPerEntry + dirBytesFixed; x.dirRead > budget {
		t.Errorf("read %d bytes of directory, budget %d", x.dirRead, budget)
	}
}

func TestFreeSpaceDuringExtraction(t *testing.T) {
	sb := newSandbox(t)
	data := strings.Repeat("x", 100<<10)
	archive := sb.put(t, buildZip(t, filepath.Join(t.TempDir(), "f.zip"),
		zent{name: "a.mkv", data: data}, zent{name: "b.mkv", data: data}, zent{name: "c.mkv", data: data}))
	lim := testLimits()
	lim.FreeSpaceMargin = 1 << 30
	x := newExtractor(sb.root, Args{Archive: archive, Dest: "files/out"}, lim)
	x.checkEvery = 64 << 10
	calls := 0
	x.freeSpace = func(*os.File) (int64, error) {
		calls++
		if calls > 2 {
			return 1 << 20, nil // the disk fills up while extracting
		}
		return 2 << 30, nil
	}
	out, err := x.run(context.Background())
	if err != nil || out.Refused != RefusedFreeSpace {
		t.Fatalf("got %+v, %v; want free_space", out, err)
	}
	if calls != 3 {
		t.Errorf("free space checked %d times, want 3", calls)
	}
	if tree := treeOf(t, filepath.Join(sb.job, "files")); len(tree) > 0 {
		t.Errorf("left behind: %v", tree)
	}

	x = newExtractor(sb.root, Args{Archive: archive, Dest: "files/out"}, lim)
	x.freeSpace = func(*os.File) (int64, error) { return 0, errors.New("statfs failed") }
	if _, err := x.run(context.Background()); err == nil {
		t.Error("a failing free-space check is not an error")
	}
}

func TestBadArgs(t *testing.T) {
	sb := newSandbox(t)
	archive := sb.put(t, buildZip(t, filepath.Join(t.TempDir(), "a.zip"), zent{name: "a.mkv", data: "a"}))
	for _, a := range []Args{
		{Archive: archive, Dest: ""},
		{Archive: archive, Dest: "."},
		{Archive: archive, Dest: "../out"},
		{Archive: archive, Dest: "/tmp/out"},
		{Archive: archive, Dest: "files/../out"},
		{Archive: archive, Dest: "files/out/"},
		{Archive: archive, Dest: `files\out`},
		{Archive: archive, Dest: "archives"}, // removing dest would delete the archive
		{Archive: archive, Dest: archive},
		{Archive: "files/out.partial/a.zip", Dest: "files/out"},
		{Archive: "../a.zip", Dest: "files/out"},
		{Archive: "", Dest: "files/out"},
		{Archive: archive, Dest: "files/out", Scratch: "../work"},
		{Archive: archive, Dest: "files/out", Scratch: "work/"},
		{Archive: archive, Dest: "files/out", Scratch: "archives"},        // removing it would delete the archive
		{Archive: archive, Dest: "files/out", Scratch: "files/out"},       // the same as dest
		{Archive: archive, Dest: "files/out", Scratch: "files/out/x"},     // inside dest
		{Archive: archive, Dest: "files/out", Scratch: "files"},           // dest inside it
		{Archive: "work/s/a.zip", Dest: "files/out", Scratch: "work/s"},   // the archive inside it
		{Archive: archive, Dest: "files/out", Scratch: archive + "/more"}, // inside the archive
	} {
		if _, err := Run(context.Background(), sb.root, sb.root, a, testLimits()); !errors.Is(err, ErrBadArgs) {
			t.Errorf("Run(%+v) error %v, want ErrBadArgs", a, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sb.job, filepath.FromSlash(archive))); err != nil {
		t.Errorf("the archive is gone: %v", err)
	}
	if _, err := Run(context.Background(), sb.root, sb.root, Args{Archive: "archives/missing.zip", Dest: "files/out"}, testLimits()); err == nil || errors.Is(err, ErrBadArgs) {
		t.Errorf("a missing archive gives %v, want an I/O error", err)
	}
	if _, err := Run(context.Background(), sb.root, sb.root, Args{Archive: "archives", Dest: "files/out"}, testLimits()); !errors.Is(err, ErrBadArgs) {
		t.Errorf("a directory as the archive gives %v, want ErrBadArgs", err)
	}
}

// TestRoot: the job root confines Run even when the job directory holds a symlink that
// points out of it.
func TestRoot(t *testing.T) {
	sb := newSandbox(t)
	archive := sb.put(t, buildZip(t, filepath.Join(t.TempDir(), "a.zip"), zent{name: "a.mkv", data: "a"}))
	if err := os.Symlink(filepath.Join(sb.dir, "outside"), filepath.Join(sb.job, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), sb.root, sb.root, Args{Archive: archive, Dest: "escape/out"}, testLimits()); err == nil {
		t.Error("Run wrote through a symlink out of the job directory")
	}
	if tree := treeOf(t, filepath.Join(sb.dir, "outside")); !maps.Equal(tree, map[string]string{"keep.txt": "keep\n"}) {
		t.Errorf("outside changed: %v", tree)
	}
}

func seasonArchive(t *testing.T) string {
	t.Helper()
	return buildZip(t, filepath.Join(t.TempDir(), "s.zip"),
		zent{name: "Season 01/"},
		zent{name: "Season 01/Show - S01E01.mkv", data: strings.Repeat("1", 70<<10)},
		zent{name: "Season 01/Show - S01E02.mkv", data: strings.Repeat("2", 70<<10)},
		zent{name: "Season 01/Show - S01E01.en.srt", data: "1\n00:00:01,000 --> 00:00:02,000\nhi\n", method: zip.Deflate},
		zent{name: "Season 01/notes.txt", data: "skip me"},
	)
}

// TestRepeatable: a leftover <Dest>.partial and a finished <Dest> both give the result of a
// first run.
func TestRepeatable(t *testing.T) {
	sb := newSandbox(t)
	archive := sb.put(t, seasonArchive(t))
	args := Args{Archive: archive, Dest: "files/Season 01"}
	dest := filepath.Join(sb.job, "files", "Season 01")
	before := listing(t, sb.dir, "job/files/Season 01", "job/files/Season 01.partial")

	first, err := Run(context.Background(), sb.root, sb.root, args, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	firstTree := treeOf(t, dest)
	if len(first.Files) != 3 || len(first.Skipped) != 1 || len(firstTree) != 3 {
		t.Fatalf("first run: %+v, on disk %v", first, firstTree)
	}

	// A crash left a half-written .partial, with a file the archive does not have.
	partial := dest + partialSuffix
	if err := os.MkdirAll(filepath.Join(partial, "Season 01"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"Season 01/Show - S01E01.mkv", "junk.mkv"} {
		if err := os.WriteFile(filepath.Join(partial, filepath.FromSlash(f)), []byte("half"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(dest); err != nil {
		t.Fatal(err)
	}
	again, err := Run(context.Background(), sb.root, sb.root, args, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, first) || !maps.Equal(treeOf(t, dest), firstTree) {
		t.Errorf("over a leftover .partial: %+v, %v; want %+v, %v", again, treeOf(t, dest), first, firstTree)
	}

	// A finished dest, with something added since.
	if err := os.WriteFile(filepath.Join(dest, "extra.mkv"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err = Run(context.Background(), sb.root, sb.root, args, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again, first) || !maps.Equal(treeOf(t, dest), firstTree) {
		t.Errorf("over a finished dest: %+v, %v; want %+v, %v", again, treeOf(t, dest), first, firstTree)
	}
	sb.checkSandbox(t, before, "files/Season 01")
}

// TestScratch: with Args.Scratch the run unpacks there, not into <Dest>.partial, so a
// second run with another scratch directory leaves the first one's alone.
func TestScratch(t *testing.T) {
	sb := newSandbox(t)
	archive := sb.put(t, seasonArchive(t))
	dest := filepath.Join(sb.job, "files", "Season 01")
	// Another run's half-written scratch directories, which must survive.
	for _, d := range []string{"work/extract-1-x1", "files/Season 01.partial"} {
		if err := os.MkdirAll(filepath.Join(sb.job, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sb.job, filepath.FromSlash(d), "half.mkv"), []byte("half"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A leftover of this run's own scratch directory is replaced.
	own := filepath.Join(sb.job, "work", "extract-2-x1")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := Run(context.Background(), sb.root, sb.root, Args{Archive: archive, Dest: "files/Season 01", Scratch: "work/extract-2-x1"}, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 3 || len(treeOf(t, dest)) != 3 {
		t.Fatalf("output %+v, on disk %v", out, treeOf(t, dest))
	}
	if _, err := os.Stat(own); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("scratch directory left behind: %v", err)
	}
	for _, d := range []string{"work/extract-1-x1/half.mkv", "files/Season 01.partial/half.mkv"} {
		if _, err := os.Stat(filepath.Join(sb.job, filepath.FromSlash(d))); err != nil {
			t.Errorf("another run's %s is gone: %v", d, err)
		}
	}
}

func TestCancelled(t *testing.T) {
	for _, n := range []int64{0, 1, 2, 3, 4, 6, 8} {
		t.Run(fmt.Sprintf("after %d checks", n), func(t *testing.T) {
			sb := newSandbox(t)
			archive := sb.put(t, seasonArchive(t))
			args := Args{Archive: archive, Dest: "files/Season 01"}
			if err := os.MkdirAll(filepath.Join(sb.job, "files", "Season 01.partial"), 0o755); err != nil {
				t.Fatal(err)
			}
			before := listing(t, sb.dir, "job/files/Season 01", "job/files/Season 01.partial")
			out, err := Run(newCancelAfter(n), sb.root, sb.root, args, testLimits())
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("got %+v, %v; want context.Canceled", out, err)
			}
			sb.checkSandbox(t, before, "files/Season 01")
			if tree := treeOf(t, filepath.Join(sb.job, "files")); len(tree) > 0 {
				t.Errorf("a cancelled run left %v", tree)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sb := newSandbox(t)
	archive := sb.put(t, seasonArchive(t))
	if _, err := Run(ctx, sb.root, sb.root, Args{Archive: archive, Dest: "files/x"}, testLimits()); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context gives %v", err)
	}
}

// TestCancelledWhileCreatingDirectories: the directory loop checks the context itself. The
// entry is a directory only, so no file copy would notice a cancellation.
func TestCancelledWhileCreatingDirectories(t *testing.T) {
	name := strings.Repeat("d/", 10)
	for n := int64(2); n <= 12; n++ {
		sb := newSandbox(t)
		archive := sb.put(t, buildZip(t, filepath.Join(t.TempDir(), "d.zip"), zent{name: name}))
		before := listing(t, sb.dir, "job/files/out", "job/files/out.partial")
		_, err := Run(newCancelAfter(n), sb.root, sb.root, Args{Archive: archive, Dest: "files/out"}, testLimits())
		// unpack and the entry loop take two checks, and each of the ten levels one more.
		wantCancel := n < 12
		if errors.Is(err, context.Canceled) != wantCancel || !wantCancel && err != nil {
			t.Errorf("cancelled after %d checks: error %v, want cancelled %v", n, err, wantCancel)
		}
		sb.checkSandbox(t, before, "files/out")
		if wantCancel {
			if _, err := os.Lstat(filepath.Join(sb.job, "files", "out")); !os.IsNotExist(err) {
				t.Errorf("cancelled after %d checks, files/out exists", n)
			}
		}
	}
}

// TestFreeSpaceWhileCreatingDirectories: directories count towards the bytes after which
// free space is checked again.
func TestFreeSpaceWhileCreatingDirectories(t *testing.T) {
	sb := newSandbox(t)
	archive := sb.put(t, buildZip(t, filepath.Join(t.TempDir(), "d.zip"), zent{name: "a/b/c/"}))
	lim := testLimits()
	lim.FreeSpaceMargin = 1 << 30
	x := newExtractor(sb.root, Args{Archive: archive, Dest: "files/out"}, lim)
	x.checkEvery = 2 * dirCost
	calls := 0
	x.freeSpace = func(*os.File) (int64, error) {
		calls++
		if calls > 1 {
			return 1 << 20, nil
		}
		return 2 << 30, nil
	}
	out, err := x.run(context.Background())
	if err != nil || out.Refused != RefusedFreeSpace {
		t.Fatalf("got %+v, %v; want free_space", out, err)
	}
	if calls != 2 {
		t.Errorf("free space checked %d times, want 2 (at the start and after two directories)", calls)
	}
}

func TestOutputJSON(t *testing.T) {
	out, _ := run(t, testLimits(), zent{name: "a.txt"})
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"files":[],"skipped":[{"name":"a.txt","reason":"extension"}]}`; string(data) != want {
		t.Errorf("JSON %s, want %s", data, want)
	}
	out, _ = run(t, testLimits(), zent{name: "a.mkv"})
	data, _ = json.Marshal(out)
	if want := `{"files":[{"path":"files/out/a.mkv","size":0}],"skipped":[]}`; string(data) != want {
		t.Errorf("JSON %s, want %s", data, want)
	}
	var back Output
	if err := json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(back, out) {
		t.Errorf("round trip %+v, %v", back, err)
	}
}

func needFFmpeg(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed (run the tests with make test)", tool)
		}
	}
}

// TestSeasonZip unpacks a well-formed season archive completely, with the sizes reported.
func TestSeasonZip(t *testing.T) {
	needFFmpeg(t)
	src, err := fixtures.SeasonZip(context.Background(), t.TempDir(), "Probe Show", 1, 3,
		fixtures.Video{Duration: 2 * time.Second, Audio: []fixtures.Audio{{Language: "eng"}}})
	if err != nil {
		t.Fatal(err)
	}
	sb := newSandbox(t)
	archive := sb.put(t, src)
	before := listing(t, sb.dir, "job/files/Season 01", "job/files/Season 01.partial")
	out, err := Run(context.Background(), sb.root, sb.root, Args{Archive: archive, Dest: "files/Season 01"}, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	sb.checkSandbox(t, before, "files/Season 01")
	if out.Refused != "" || len(out.Skipped) != 0 {
		t.Fatalf("got %+v", out)
	}

	r, err := zip.OpenReader(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if len(out.Files) != len(r.File) {
		t.Fatalf("%d files written, the archive has %d", len(out.Files), len(r.File))
	}
	for i, zf := range r.File {
		got := out.Files[i]
		if want := "files/Season 01/" + zf.Name; got.Path != want || got.Size != int64(zf.UncompressedSize64) {
			t.Errorf("file %d: %+v, want %s of %d bytes", i, got, want, zf.UncompressedSize64)
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		want, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		disk, err := os.ReadFile(filepath.Join(sb.job, filepath.FromSlash(got.Path)))
		if err != nil || string(disk) != string(want) {
			t.Errorf("%s differs from the archive (%v)", got.Path, err)
		}
	}
}

// A job that has only archives has no files/ directory yet: the extraction creates it.
func TestCreatesTheParentOfDest(t *testing.T) {
	sb := newSandbox(t)
	if err := os.Remove(filepath.Join(sb.job, "files")); err != nil {
		t.Fatal(err)
	}
	archive := sb.put(t, buildZip(t, filepath.Join(t.TempDir(), "a.zip"), zent{name: "a.mkv", data: "video"}))
	out, err := Run(context.Background(), sb.root, sb.root, Args{Archive: archive, Dest: "files/out"}, testLimits())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Files) != 1 || out.Refused != "" {
		t.Fatalf("got %+v", out)
	}
	if got := treeOf(t, filepath.Join(sb.job, "files", "out")); got["a.mkv"] != "video" {
		t.Errorf("on disk %v", got)
	}
}
