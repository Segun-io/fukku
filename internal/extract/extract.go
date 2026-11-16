package extract

import (
	"archive/zip"
	"cmp"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/safename"
)

// Refusal codes (Output.Refused).
const (
	RefusedNotAZip        = "not_a_zip"
	RefusedTooManyEntries = "too_many_entries"
	RefusedTooLarge       = "too_large"
	RefusedRatio          = "ratio"
	RefusedFreeSpace      = "free_space"
)

// Skip reasons (Skipped.Reason).
const (
	ReasonTraversal     = "traversal"
	ReasonAbsolute      = "absolute"
	ReasonDriveLetter   = "drive_letter"
	ReasonBackslash     = "backslash"
	ReasonSymlink       = "symlink"
	ReasonSpecial       = "special"
	ReasonNestedArchive = "nested_archive"
	ReasonExtension     = "extension"
	ReasonNameTooLong   = "name_too_long"
	ReasonDuplicate     = "duplicate"
	ReasonEncrypted     = "encrypted"
)

const (
	partialSuffix          = ".partial"
	maxSkippedNameBytes    = 200
	copyBufferSize         = 32 << 10
	freeSpaceCheckInterval = 64 << 20 // re-check free space after this many written bytes
	// dirCost is what one created directory counts towards the written bytes (a block).
	dirCost = 4 << 10
	// A central directory entry needs 46 bytes plus its name and extra fields. Reading the
	// directory may take this much per permitted entry (plus a fixed allowance for the end
	// records) before the archive counts as having too many entries, so a hostile directory
	// of millions of tiny entries cannot exhaust the worker's memory.
	dirBytesPerEntry = 16 << 10
	dirBytesFixed    = 1 << 20
)

// ErrBadArgs means the task's arguments are unusable (not job-relative, or Dest would
// contain the archive).
var ErrBadArgs = errors.New("extract: bad arguments")

// archiveExtensions are never extracted and never opened (nested archives). The list is
// broader than upload.archiveExtensions on purpose: it names formats, not what is accepted.
var archiveExtensions = []string{
	".zip", ".zipx", ".rar", ".7z", ".tar", ".gz", ".tgz", ".bz2", ".tbz", ".tbz2", ".xz", ".txz",
	".zst", ".tzst", ".lz", ".lzma", ".lz4", ".z", ".cab", ".arj", ".lzh", ".ace", ".cpio",
	".iso", ".img", ".dmg", ".jar", ".apk",
}

// Limits bound one extraction.
type Limits struct {
	MaxEntries      int      // extract.maxEntries
	MaxWrittenBytes int64    // extract.maxWrittenBytes
	MaxRatio        float64  // extract.maxRatio
	RatioFloor      int64    // extract.ratioFloor: the ratio limit is never below this
	MaxNameBytes    int      // extract.maxNameBytes
	MaxPathSegments int      // extract.maxPathSegments
	MaxPathBytes    int      // extract.maxPathBytes
	FreeSpaceMargin int64    // upload.freeSpaceMargin
	Extensions      []string // upload.videoExtensions + upload.subtitleExtensions
}

// LimitsFrom takes the limits from the configuration.
func LimitsFrom(cfg config.Config) Limits {
	return Limits{
		MaxEntries:      cfg.Extract.MaxEntries,
		MaxWrittenBytes: int64(cfg.Extract.MaxWrittenBytes),
		MaxRatio:        cfg.Extract.MaxRatio,
		RatioFloor:      int64(cfg.Extract.RatioFloor),
		MaxNameBytes:    cfg.Extract.MaxNameBytes,
		MaxPathSegments: cfg.Extract.MaxPathSegments,
		MaxPathBytes:    cfg.Extract.MaxPathBytes,
		FreeSpaceMargin: int64(cfg.Upload.FreeSpaceMargin),
		Extensions:      slices.Concat(cfg.Upload.VideoExtensions, cfg.Upload.SubtitleExtensions),
	}
}

// Run unpacks args.Archive into args.Dest. An error means the task could not run (I/O,
// cancelled); a refused archive is a normal Output with Refused set.
//
// Run is safe to repeat: it first removes <Dest> and its scratch directory (Args.Scratch,
// by default <Dest>.partial), unpacks into the scratch directory and renames that to
// <Dest> at the end. A refused, cancelled or failed run removes its scratch directory, so
// it leaves nothing.
func Run(ctx context.Context, job *os.Root, args Args, lim Limits) (Output, error) {
	x := newExtractor(job, args, lim)
	return x.run(ctx)
}

// refusal stops the extraction and refuses the whole archive.
type refusal string

func (r refusal) Error() string { return "extract: archive refused: " + string(r) }

type seenPath struct {
	dir  bool
	path string // the spelling first written, relative to <Dest>
}

type extractor struct {
	job     *os.Root
	args    Args
	lim     Limits
	partial string

	freeSpace  func(dir *os.File) (int64, error) // replaced in tests
	checkEvery int64                             // free-space check interval in written bytes
	fold       cases.Caser
	buf        []byte

	dir        *os.File // <Dest>.partial, for free-space checks
	maxRatio   float64  // MaxRatio × the archive's size, at least RatioFloor
	written    int64    // bytes written so far, over all entries, plus dirCost per directory
	created    int      // files and directories created
	sinceCheck int64    // bytes written since the last free-space check
	dirRead    int64    // bytes read while parsing the central directory
	seen       map[string]seenPath
	out        Output
}

func newExtractor(job *os.Root, args Args, lim Limits) *extractor {
	return &extractor{
		job: job, args: args, lim: lim, partial: scratchOf(args),
		freeSpace: freeSpace, checkEvery: freeSpaceCheckInterval,
		fold: cases.Fold(), buf: make([]byte, copyBufferSize),
		seen: map[string]seenPath{},
	}
}

func (x *extractor) run(ctx context.Context) (Output, error) {
	if err := checkArgs(x.args); err != nil {
		return Output{}, err
	}
	for _, p := range []string{x.args.Dest, x.partial} {
		if err := x.job.RemoveAll(p); err != nil {
			return Output{}, fmt.Errorf("extract: remove %s: %w", p, err)
		}
	}

	out, err := x.unpack(ctx)
	if x.dir != nil {
		_ = x.dir.Close()
	}
	var ref refusal
	if errors.As(err, &ref) {
		out, err = Output{Refused: string(ref)}, nil
	}
	if err == nil && out.Refused == "" {
		err = x.job.Rename(x.partial, x.args.Dest)
	}
	if err != nil || out.Refused != "" {
		if rerr := x.job.RemoveAll(x.partial); rerr != nil {
			err = errors.Join(err, fmt.Errorf("extract: remove %s: %w", x.partial, rerr))
		}
	}
	if err != nil {
		return Output{}, fmt.Errorf("extract: %w", err)
	}
	if out.Files == nil {
		out.Files = []Entry{}
	}
	if out.Skipped == nil {
		out.Skipped = []Skipped{}
	}
	return out, nil
}

func (x *extractor) unpack(ctx context.Context) (Output, error) {
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	f, err := x.job.Open(x.args.Archive)
	if err != nil {
		return Output{}, fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return Output{}, fmt.Errorf("open archive: %w", err)
	}
	if !st.Mode().IsRegular() {
		return Output{}, fmt.Errorf("%w: %s is not a regular file", ErrBadArgs, x.args.Archive)
	}

	zr, err := x.openZip(f, st.Size())
	if err != nil {
		return Output{}, err
	}
	if len(zr.File) > x.lim.MaxEntries {
		return Output{}, refusal(RefusedTooManyEntries)
	}
	if err := checkLayout(zr.File, st.Size()); err != nil {
		return Output{}, err
	}

	if parent := path.Dir(x.partial); parent != "." {
		if err := x.job.MkdirAll(parent, 0o755); err != nil {
			return Output{}, err
		}
	}
	if err := x.job.Mkdir(x.partial, 0o755); err != nil {
		return Output{}, err
	}
	if x.dir, err = x.job.Open(x.partial); err != nil {
		return Output{}, err
	}
	if err := x.checkFreeSpace(); err != nil {
		return Output{}, err
	}
	x.maxRatio = max(x.lim.MaxRatio*float64(st.Size()), float64(x.lim.RatioFloor))

	for _, zf := range zr.File {
		if err := ctx.Err(); err != nil {
			return Output{}, err
		}
		reason, err := x.entry(ctx, zf)
		if err != nil {
			return Output{}, err
		}
		if reason != "" {
			x.out.Skipped = append(x.out.Skipped, Skipped{Name: printable(zf.Name), Reason: reason})
		}
	}
	return x.out, nil
}

// openZip reads the central directory, refusing an archive whose directory is larger than
// MaxEntries entries can need.
func (x *extractor) openZip(f io.ReaderAt, size int64) (*zip.Reader, error) {
	br := &budgetReader{r: f, left: int64(x.lim.MaxEntries)*dirBytesPerEntry + dirBytesFixed}
	zr, err := zip.NewReader(br, size)
	x.dirRead = br.read
	br.left = -1 // the directory is read; entry data is limited by the write counters
	switch {
	case br.exceeded:
		return nil, refusal(RefusedTooManyEntries)
	case errors.Is(err, zip.ErrInsecurePath):
		// Only with GODEBUG=zipinsecurepath=0; every name is checked below anyway.
	case err != nil:
		return nil, refusal(RefusedNotAZip)
	}
	return zr, nil
}

var errDirBudget = errors.New("extract: central directory too large")

// budgetReader fails once more than left bytes have been read; left < 0 means no limit.
// read counts every byte asked for, with or without a limit.
type budgetReader struct {
	r        io.ReaderAt
	left     int64
	read     int64
	exceeded bool
}

func (b *budgetReader) ReadAt(p []byte, off int64) (int, error) {
	if b.left >= 0 {
		if int64(len(p)) > b.left {
			b.exceeded = true
			return 0, errDirBudget
		}
		b.left -= int64(len(p))
	}
	b.read += int64(len(p))
	return b.r.ReadAt(p, off)
}

// entry applies the rules to one entry and writes it. It returns the reason when the entry
// is skipped; an error stops the whole extraction (a refusal, I/O, or cancellation).
func (x *extractor) entry(ctx context.Context, zf *zip.File) (string, error) {
	name := zf.Name

	// 1. The raw name.
	if reason := rawNameReason(name); reason != "" {
		return reason, nil
	}

	// 2. The type. Modes, owners and times from the archive are otherwise ignored.
	mode := zf.Mode()
	isDir := mode.IsDir() || strings.HasSuffix(name, "/")
	switch {
	case mode&fs.ModeSymlink != 0:
		return ReasonSymlink, nil
	case mode.Type()&^fs.ModeDir != 0:
		return ReasonSpecial, nil
	case !isDir && zf.Flags&0x1 != 0:
		return ReasonEncrypted, nil
	}

	var segs []string
	for s := range strings.SplitSeq(name, "/") {
		if s != "" && s != "." { // "a//b" and "./a" are the same paths as "a/b" and "a"
			segs = append(segs, s)
		}
	}

	// 3. The extension, on the raw name.
	if !isDir {
		last := ""
		if len(segs) > 0 {
			last = segs[len(segs)-1]
		}
		if reason := x.extensionReason(last); reason != "" {
			return reason, nil
		}
	}

	// 4. Each segment through safename.Clean, and the whole path within bounds (every
	// level costs a walk through the os.Root when its directory is created).
	if len(segs) > x.lim.MaxPathSegments {
		return ReasonNameTooLong, nil
	}
	cleaned := make([]string, len(segs))
	for i, s := range segs {
		c, err := safename.Clean(s, x.lim.MaxNameBytes)
		switch {
		case errors.Is(err, safename.ErrTooLong):
			return ReasonNameTooLong, nil
		case err != nil:
			// Empty, "." or "..", or a drive letter, only once control characters and
			// surrounding spaces are gone: treated as an attempt to leave the directory.
			return ReasonTraversal, nil
		}
		cleaned[i] = c
	}
	if n := len(strings.Join(cleaned, "/")); n > x.lim.MaxPathBytes {
		return ReasonNameTooLong, nil
	}
	if !isDir {
		// Cleaning cannot change an allowed extension; check again rather than rely on it.
		if reason := x.extensionReason(cleaned[len(cleaned)-1]); reason != "" {
			return reason, nil
		}
	}

	// 5. Never overwrite. Paths are compared after cleaning and case folding; a directory
	// keeps the spelling it was first created with.
	dirs, file := cleaned, ""
	if !isDir {
		dirs, file = cleaned[:len(cleaned)-1], cleaned[len(cleaned)-1]
	}
	parent := ""
	for _, d := range dirs {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		p := path.Join(parent, d)
		key := x.fold.String(p)
		if s, ok := x.seen[key]; ok {
			if !s.dir {
				return ReasonDuplicate, nil // a file of that name is in the way
			}
			parent = s.path
			continue
		}
		if reason, err := x.mkdir(p); reason != "" || err != nil {
			return reason, err
		}
		x.seen[key] = seenPath{dir: true, path: p}
		parent = p
	}
	if isDir {
		return "", nil
	}
	p := path.Join(parent, file)
	key := x.fold.String(p)
	if _, ok := x.seen[key]; ok {
		return ReasonDuplicate, nil
	}
	x.seen[key] = seenPath{path: p}
	return x.write(ctx, zf, p)
}

func rawNameReason(name string) string {
	switch {
	case strings.Contains(name, `\`):
		return ReasonBackslash
	case strings.HasPrefix(name, "/"):
		return ReasonAbsolute
	}
	segs := strings.Split(name, "/")
	for _, s := range segs {
		if safename.HasDriveLetter(s) {
			return ReasonDriveLetter
		}
	}
	for _, s := range segs {
		if s == ".." {
			return ReasonTraversal
		}
	}
	return ""
}

func (x *extractor) extensionReason(name string) string {
	ext := strings.ToLower(path.Ext(name))
	switch {
	case slices.Contains(archiveExtensions, ext):
		return ReasonNestedArchive
	case ext == "" || !slices.ContainsFunc(x.lim.Extensions, func(e string) bool { return strings.EqualFold(e, ext) }):
		return ReasonExtension
	}
	return ""
}

func (x *extractor) mkdir(rel string) (string, error) {
	if err := x.create(); err != nil {
		return "", err
	}
	if err := x.charge(dirCost); err != nil {
		return "", err
	}
	full := path.Join(x.partial, rel)
	err := x.job.Mkdir(full, 0o755)
	switch {
	case err == nil:
		return "", nil
	case errors.Is(err, fs.ErrExist):
		// Only on a filesystem that folds names differently from us.
		if fi, lerr := x.job.Lstat(full); lerr == nil && fi.IsDir() {
			return "", nil
		}
		return ReasonDuplicate, nil
	case errors.Is(err, syscall.ENAMETOOLONG):
		return ReasonNameTooLong, nil
	}
	return "", err
}

func (x *extractor) write(ctx context.Context, zf *zip.File, rel string) (string, error) {
	rc, err := zf.Open()
	if err != nil {
		if isFormatError(err) {
			return "", refusal(RefusedNotAZip)
		}
		return "", err
	}
	defer func() { _ = rc.Close() }()

	if err := x.create(); err != nil {
		return "", err
	}
	full := path.Join(x.partial, rel)
	w, err := x.job.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	switch {
	case errors.Is(err, fs.ErrExist):
		return ReasonDuplicate, nil // only on a filesystem that folds names differently from us
	case errors.Is(err, syscall.ENAMETOOLONG):
		return ReasonNameTooLong, nil
	case err != nil:
		return "", err
	}
	n, err := x.copy(ctx, w, rc, zf.UncompressedSize64)
	if err == nil {
		err = w.Sync()
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	x.out.Files = append(x.out.Files, Entry{Path: path.Join(x.args.Dest, rel), Size: n})
	return "", nil
}

// copy copies one entry, counting bytes as they are written. A chunk that would take the
// totals over a limit is not written, so at most the limit reaches the disk.
func (x *extractor) copy(ctx context.Context, w io.Writer, r io.Reader, declared uint64) (int64, error) {
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		m, rerr := r.Read(x.buf)
		if m > 0 {
			if uint64(n)+uint64(m) > declared { // archive/zip stops here too; our own count
				return n, refusal(RefusedNotAZip)
			}
			if err := x.charge(int64(m)); err != nil {
				return n, err
			}
			if _, err := w.Write(x.buf[:m]); err != nil {
				return n, err
			}
			n += int64(m)
		}
		switch {
		case rerr == io.EOF:
			return n, nil
		case rerr != nil && isFormatError(rerr):
			return n, refusal(RefusedNotAZip)
		case rerr != nil:
			return n, rerr
		}
	}
}

// create counts one created file or directory against MaxEntries.
func (x *extractor) create() error {
	x.created++
	if x.created > x.lim.MaxEntries {
		return refusal(RefusedTooManyEntries)
	}
	return nil
}

// charge counts n bytes about to be written against the limits; after a refusal nothing
// more is written.
func (x *extractor) charge(n int64) error {
	next := x.written + n
	switch {
	case next > x.lim.MaxWrittenBytes:
		return refusal(RefusedTooLarge)
	case float64(next) > x.maxRatio:
		return refusal(RefusedRatio)
	}
	x.written = next
	x.sinceCheck += n
	if x.sinceCheck >= x.checkEvery {
		x.sinceCheck = 0
		return x.checkFreeSpace()
	}
	return nil
}

// checkLayout refuses an archive whose entries' data overlap or lie outside it. Entries
// that share their data would each inflate it again while the archive stays small, so the
// work would grow with the number of entries and no limit on written bytes would stop it.
func checkLayout(files []*zip.File, size int64) error {
	type span struct{ start, end int64 }
	spans := make([]span, 0, len(files))
	for _, f := range files {
		off, err := f.DataOffset()
		if err != nil || off < 0 || f.CompressedSize64 > uint64(size) { //nolint:gosec // a file size is not negative
			return refusal(RefusedNotAZip)
		}
		end := off + int64(f.CompressedSize64) //nolint:gosec // bounded by size above
		if end > size {
			return refusal(RefusedNotAZip)
		}
		spans = append(spans, span{off, end})
	}
	slices.SortFunc(spans, func(a, b span) int { return cmp.Compare(a.start, b.start) })
	for i := 1; i < len(spans); i++ {
		if spans[i].start < spans[i-1].end {
			return refusal(RefusedNotAZip)
		}
	}
	return nil
}

func (x *extractor) checkFreeSpace() error {
	if x.lim.FreeSpaceMargin <= 0 {
		return nil
	}
	free, err := x.freeSpace(x.dir)
	if err != nil {
		return fmt.Errorf("free space: %w", err)
	}
	if free < x.lim.FreeSpaceMargin {
		return refusal(RefusedFreeSpace)
	}
	return nil
}

// isFormatError reports whether err means the archive's data is broken.
func isFormatError(err error) bool {
	var corrupt flate.CorruptInputError
	return errors.Is(err, zip.ErrFormat) || errors.Is(err, zip.ErrChecksum) ||
		errors.Is(err, zip.ErrAlgorithm) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.As(err, &corrupt)
}

// scratchOf is the directory a run unpacks into: Args.Scratch, or <Dest>.partial.
func scratchOf(a Args) string {
	if a.Scratch != "" {
		return a.Scratch
	}
	return a.Dest + partialSuffix
}

func checkArgs(a Args) error {
	scratch := scratchOf(a)
	if !localPath(a.Archive) || !localPath(a.Dest) || !localPath(scratch) {
		return fmt.Errorf("%w: archive, dest and scratch must be clean job-relative paths, got %q, %q and %q", ErrBadArgs, a.Archive, a.Dest, a.Scratch)
	}
	if within(a.Archive, a.Dest) || within(a.Archive, scratch) || within(a.Dest, a.Archive) || within(scratch, a.Archive) {
		return fmt.Errorf("%w: dest %q or scratch %q overlaps archive %q", ErrBadArgs, a.Dest, scratch, a.Archive)
	}
	if within(scratch, a.Dest) || within(a.Dest, scratch) {
		return fmt.Errorf("%w: dest %q and scratch %q overlap", ErrBadArgs, a.Dest, scratch)
	}
	return nil
}

func localPath(p string) bool {
	return p != "." && path.Clean(p) == p && filepath.IsLocal(p) && !strings.Contains(p, `\`)
}

// within reports whether p is dir or inside it.
func within(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

// printable makes an entry name safe to show and log: invalid bytes and characters that are
// not graphic (controls, bidirectional formatting) become escapes, cut to 200 bytes.
func printable(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		var piece string
		switch {
		case r == utf8.RuneError && size == 1:
			piece = fmt.Sprintf(`\x%02x`, s[i])
		case r < utf8.RuneSelf && !unicode.IsGraphic(r):
			piece = fmt.Sprintf(`\x%02x`, r)
		case !unicode.IsGraphic(r) || safename.IsControl(r):
			piece = fmt.Sprintf(`\u%04x`, r)
		default:
			piece = s[i : i+size]
		}
		if b.Len()+len(piece) > maxSkippedNameBytes {
			break
		}
		b.WriteString(piece)
		i += size
	}
	return b.String()
}
