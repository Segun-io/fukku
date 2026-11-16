package fixtures

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Expect is what a safe extractor must do with a hostile archive.
type Expect string

const (
	// RefuseArchive: stop and extract nothing more (limits, bombs, lies about sizes).
	RefuseArchive Expect = "refuse the archive"
	// RejectEntry: skip the entry, list it, and keep nothing outside the job directory.
	RejectEntry Expect = "skip and list the entry"
	// RejectContent: extraction may succeed, but the content check must refuse the file.
	RejectContent Expect = "refuse the file at the content check"
	// NoOverwrite: the second entry must not replace the first.
	NoOverwrite Expect = "never overwrite an extracted file"
	// Normalise: the name is accepted after normalisation (NFC, no control characters).
	Normalise Expect = "accept after normalising the name"
)

// Hostile is one archive of the hostile corpus (plan §10, §14).
type Hostile struct {
	Name   string // file name of the archive
	About  string
	Expect Expect
	build  func(w *zip.Writer) error
}

// Build writes the archive into dir and returns its path.
func (h Hostile) Build(dir string) (string, error) {
	path := filepath.Join(dir, h.Name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestCompression)
	})
	berr := h.build(zw)
	cerr := zw.Close()
	ferr := f.Close()
	if err := firstErr(berr, cerr, ferr); err != nil {
		return "", fmt.Errorf("%s: %w", h.Name, err)
	}
	return path, nil
}

// Limits mirror the extract.* defaults the corpus is built against.
type Limits struct {
	MaxEntries   int     // extract.maxEntries
	MaxRatio     float64 // extract.maxRatio
	MaxNameBytes int     // extract.maxNameBytes
	BombBytes    int64   // uncompressed size of the bomb entry
}

// DefaultLimits match the plan's defaults; the bomb is 1 GiB of zeros (about 1000:1).
var DefaultLimits = Limits{MaxEntries: 500, MaxRatio: 20, MaxNameBytes: 200, BombBytes: 1 << 30}

// HostileCorpus returns the hostile archives for the given limits.
func HostileCorpus(l Limits) []Hostile {
	small := []byte("not a video\n")
	return []Hostile{
		{"traversal.zip", "entry ../../evil.txt climbs out of the job directory", RejectEntry,
			files(map[string][]byte{"ok.txt": small, "../../evil.txt": small})},
		{"absolute.zip", "entry /etc/evil.txt is an absolute path", RejectEntry,
			files(map[string][]byte{"/etc/evil.txt": small})},
		{"drive-letter.zip", "entries C:\\evil.txt and C:/evil.txt name a Windows drive", RejectEntry,
			files(map[string][]byte{`C:\evil.txt`: small, "C:/evil2.txt": small})},
		{"backslash.zip", `entry season\..\..\evil.txt uses backslashes to climb out`, RejectEntry,
			files(map[string][]byte{`season\..\..\evil.txt`: small})},
		{"symlink.zip", "a symlink entry pointing at /etc/passwd, then a file written through it", RejectEntry,
			func(w *zip.Writer) error {
				if err := entry(w, "link", fs.ModeSymlink|0o777, []byte("/etc/passwd")); err != nil {
					return err
				}
				if err := entry(w, "dirlink", fs.ModeSymlink|0o777, []byte("/tmp")); err != nil {
					return err
				}
				return entry(w, "dirlink/evil.txt", 0o644, small)
			}},
		{"device.zip", "character-device, named-pipe and socket entries", RejectEntry,
			func(w *zip.Writer) error {
				for name, mode := range map[string]fs.FileMode{
					"tty":  fs.ModeDevice | fs.ModeCharDevice | 0o666,
					"fifo": fs.ModeNamedPipe | 0o644,
					"sock": fs.ModeSocket | 0o644,
				} {
					if err := entry(w, name, mode, nil); err != nil {
						return err
					}
				}
				return nil
			}},
		{"bomb.zip", fmt.Sprintf("one entry of %d bytes of zeros, about 1000:1 compressed", l.BombBytes), RefuseArchive,
			func(w *zip.Writer) error { return zeros(w, "Season 01/episode.mkv", l.BombBytes) }},
		{"lying-size.zip", "the header declares 1 KiB, the data inflates to 64 MiB", RefuseArchive,
			func(w *zip.Writer) error { return lyingSize(w, "episode.mkv", 1<<10, 64<<20) }},
		{"too-many-entries.zip", fmt.Sprintf("%d entries, one more than allowed", l.MaxEntries+1), RefuseArchive,
			func(w *zip.Writer) error {
				for i := range l.MaxEntries + 1 {
					if err := entry(w, fmt.Sprintf("f%04d.txt", i), 0o644, small); err != nil {
						return err
					}
				}
				return nil
			}},
		{"nested.zip", "a zip inside the zip; nested archives are never opened", RejectEntry,
			func(w *zip.Writer) error {
				var inner bytes.Buffer
				iz := zip.NewWriter(&inner)
				if err := entry(iz, "inner.mkv", 0o644, small); err != nil {
					return err
				}
				if err := iz.Close(); err != nil {
					return err
				}
				return entry(w, "Season 01.zip", 0o644, inner.Bytes())
			}},
		{"renamed-non-video.zip", "a text file named Episode 01.mkv", RejectContent,
			files(map[string][]byte{"Show - S01E01.mkv": []byte(strings.Repeat("hello\n", 1000))})},
		{"duplicate-names.zip", "the same path twice; the second must not replace the first", NoOverwrite,
			func(w *zip.Writer) error {
				if err := entry(w, "Show - S01E01.mkv", 0o644, []byte("first")); err != nil {
					return err
				}
				return entry(w, "Show - S01E01.mkv", 0o644, []byte("second"))
			}},
		{"unicode-collision.zip", "é as NFC and as NFD: the same name once normalised", NoOverwrite,
			files(map[string][]byte{"Pok\u00e9mon.srt": []byte("nfc"), "Poke\u0301mon.srt": []byte("nfd")})},
		{"control-characters.zip", "names with a newline, a tab, an escape and a NUL", Normalise,
			files(map[string][]byte{"bad\nname.srt": small, "tab\tname.srt": small, "esc\x1b[31m.srt": small, "nul\x00.srt": small})},
		{"long-name.zip", fmt.Sprintf("a %d-byte file name", l.MaxNameBytes+55), RejectEntry,
			files(map[string][]byte{strings.Repeat("n", l.MaxNameBytes+51) + ".mkv": small})},
		{"deep-nesting.zip", fmt.Sprintf("one entry %d directories deep; creating each level walks the whole path", DeepNesting), RejectEntry,
			files(map[string][]byte{strings.Repeat("a/", DeepNesting) + "x.mkv": small})},
		{"many-directories.zip", fmt.Sprintf("%d entries in %d directories: within the entries allowed, but not once the directories count too", l.MaxEntries*4/5, l.MaxEntries*2/5), RefuseArchive,
			func(w *zip.Writer) error {
				for i := range l.MaxEntries * 4 / 5 {
					if err := entry(w, fmt.Sprintf("d%04d/x%d.mkv", i/2, i%2), 0o644, small); err != nil {
						return err
					}
				}
				return nil
			}},
	}
}

// DeepNesting is the depth of the deep-nesting entry, far beyond extract.maxPathSegments.
const DeepNesting = 1000

func files(m map[string][]byte) func(*zip.Writer) error {
	return func(w *zip.Writer) error {
		for name, data := range m {
			if err := entry(w, name, 0o644, data); err != nil {
				return err
			}
		}
		return nil
	}
}

func entry(w *zip.Writer, name string, mode fs.FileMode, data []byte) error {
	hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	hdr.SetMode(mode)
	fw, err := w.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = fw.Write(data)
	return err
}

// zeros writes an entry of size zero bytes. Compressing a gigabyte takes about a minute
// under the race detector, so one MiB is compressed once and its stream repeated: each copy
// ends in a sync flush (byte-aligned, not final) and only refers back into its own output,
// so the copies decode as one stream. A final empty stored block ends it.
func zeros(w *zip.Writer, name string, size int64) error {
	const chunk = 1 << 20
	block := func(n int64) ([]byte, error) {
		var b bytes.Buffer
		fw, err := flate.NewWriter(&b, flate.BestCompression)
		if err != nil {
			return nil, err
		}
		if _, err := fw.Write(make([]byte, n)); err != nil {
			return nil, err
		}
		if err := fw.Flush(); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	full, err := block(chunk)
	if err != nil {
		return err
	}
	var tail []byte
	if rem := size % chunk; rem > 0 {
		if tail, err = block(rem); err != nil {
			return err
		}
	}
	final := []byte{0x01, 0x00, 0x00, 0xff, 0xff}

	crc := uint32(0)
	zero := make([]byte, chunk)
	for left := size; left > 0; left -= chunk {
		crc = crc32.Update(crc, crc32.IEEETable, zero[:min(left, chunk)])
	}
	n := size / chunk
	raw, err := w.CreateRaw(&zip.FileHeader{
		Name: name, Method: zip.Deflate, CRC32: crc,
		CompressedSize64:   uint64(n)*uint64(len(full)) + uint64(len(tail)) + uint64(len(final)),
		UncompressedSize64: uint64(size),
	})
	if err != nil {
		return err
	}
	for range n {
		if _, err := raw.Write(full); err != nil {
			return err
		}
	}
	if _, err := raw.Write(tail); err != nil {
		return err
	}
	_, err = raw.Write(final)
	return err
}

// lyingSize writes an entry whose header claims declared bytes while its data inflates to actual.
func lyingSize(w *zip.Writer, name string, declared, actual uint64) error {
	var comp bytes.Buffer
	fw, err := flate.NewWriter(&comp, flate.BestCompression)
	if err != nil {
		return err
	}
	data := make([]byte, actual)
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := fw.Close(); err != nil {
		return err
	}
	hdr := &zip.FileHeader{
		Name: name, Method: zip.Deflate,
		CRC32:              crc32.ChecksumIEEE(data[:declared]),
		CompressedSize64:   uint64(comp.Len()),
		UncompressedSize64: declared,
	}
	raw, err := w.CreateRaw(hdr)
	if err != nil {
		return err
	}
	_, err = raw.Write(comp.Bytes())
	return err
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
