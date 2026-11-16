package merge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/media"
)

// ErrBadArgs means the task's arguments are unusable: a path that is not a clean
// job-relative one in its place, no tracks, a language tag that is not canonical BCP 47.
// The worker reports it as bad_task.
var ErrBadArgs = errors.New("merge: bad arguments")

const (
	maxTracks       = 64
	maxOffset       = time.Hour
	outputExtension = ".mkv"
	// endWindow is how far before the container's end the library file's video packets are
	// read when its video stream states no duration (media.VideoEndMs).
	endWindow = 30 * time.Second
)

// Options configure Run.
type Options struct {
	Probe               media.Options // ffprobe of the library file, the upload and the output
	IdentifyTimeout     time.Duration // worker.probeTimeout, per mkvmerge identification
	Nice                int           // worker.nice
	FreeSpaceMargin     int64         // upload.freeSpaceMargin
	LanguageTag         string        // merge.languageTag
	TrackName           string        // merge.trackName
	KeepOriginalDefault bool          // merge.keepOriginalDefault
	// CoverageTolerance is how far an added audio track may fall short of the film and of
	// its own claimed length (merge.coverageTolerance).
	CoverageTolerance time.Duration
	Mkvmerge          string // binary; empty means "mkvmerge" (tests pass a fake)
	// FreeSpace measures the free bytes on the filesystem of the open directory; nil means
	// statfs (tests replace it).
	FreeSpace func(dir *os.File) (int64, error)
}

// OptionsFrom takes the options from the configuration.
func OptionsFrom(cfg config.Config) Options {
	return Options{
		Probe:               media.OptionsFrom(cfg),
		IdentifyTimeout:     cfg.Worker.ProbeTimeout.D(),
		Nice:                cfg.Worker.Nice,
		FreeSpaceMargin:     int64(cfg.Upload.FreeSpaceMargin),
		LanguageTag:         cfg.Merge.LanguageTag,
		TrackName:           cfg.Merge.TrackName,
		KeepOriginalDefault: cfg.Merge.KeepOriginalDefault,
		CoverageTolerance:   cfg.Merge.CoverageTolerance.D(),
	}
}

// OutputName is the merged file's name: the library file's, with the extension .mkv
// (mkvmerge always writes Matroska), so the app parses the same title and quality from it.
func OutputName(library string) string {
	base := path.Base(filepath.ToSlash(library))
	return strings.TrimSuffix(base, path.Ext(base)) + outputExtension
}

// PartialFor returns the partial file and the lock file of a merge into dest, under work/.
func PartialFor(dest string) (partial, lock string) {
	sum := sha256.Sum256([]byte(dest))
	base := jobdir.Work + "/.merge-" + hex.EncodeToString(sum[:8])
	return base + ".partial" + outputExtension, base + ".lock"
}

// plan is a checked task.
type plan struct {
	args          Args
	partial, lock string
	final         string
	tag, name     string
	keepDefault   bool
	tracks        []int // sorted
}

// Run merges args.Tracks of the upload into a copy of the library file, verifies the copy
// and renames it to <Dest>/<OutputName(Library)>. roots are the worker's
// paths.libraryRoots. An error means the task could not run; a refusal is an Output with
// OK false. Whatever the outcome, the partial file is gone when Run returns, and Dest
// holds the verified file only when OK is true.
//
// While it runs, Run holds an exclusive lock for Dest (PartialFor), so no other merge into
// the same dest uses the partial file or Dest meanwhile; under the lock, what is found
// there was left by an earlier run, whose process is gone, and is removed first.
func Run(ctx context.Context, job *os.Root, roots []string, args Args, opt Options) (Output, error) {
	p, err := check(args, opt)
	if err != nil {
		return Output{}, err
	}
	if err := job.MkdirAll(jobdir.Work, 0o755); err != nil {
		return Output{}, err
	}
	unlock, busy, err := lock(job, p.lock)
	if err != nil {
		return Output{}, err
	}
	if busy {
		return echoed(refuse(RefusalBusy, "another merge into %s is running", args.Dest), p), nil
	}
	defer unlock()
	for _, name := range []string{p.partial, args.Dest} {
		if err := job.RemoveAll(name); err != nil {
			return Output{}, fmt.Errorf("merge: remove %s: %w", name, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	m := &merger{job: job, roots: roots, p: p, opt: opt}
	out, err := m.run(ctx)
	if m.dst != nil {
		if !m.placed {
			if rerr := removeIfSame(job, p.partial, m.dst); rerr != nil {
				err = errors.Join(err, rerr)
			}
		}
		_ = m.dst.Close()
	}
	if err != nil {
		return Output{}, err
	}
	return echoed(out, m.p), nil
}

// echoed adds what the added tracks were given to an output.
func echoed(out Output, p plan) Output {
	out.LanguageTag, out.TrackName, out.KeepOriginalDefault = p.tag, p.name, p.keepDefault
	return out
}

// lock takes the exclusive, non-blocking flock on the lock file. busy is true while another
// merge holds it; a process that dies releases it.
func lock(job *os.Root, name string) (unlock func(), busy bool, err error) {
	f, err := job.OpenFile(name, os.O_RDWR|os.O_CREATE|syscall.O_NONBLOCK, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("merge: lock: %w", err)
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, false, fmt.Errorf("merge: lock %s is not a regular file", name)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("merge: lock %s: %w", name, err)
	}
	return func() { _ = f.Close() }, false, nil
}

// removeIfSame removes name when it is still the open file f.
func removeIfSame(root *os.Root, name string, f *os.File) error {
	at, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	now, err := f.Stat()
	if err != nil || !os.SameFile(at, now) {
		return err
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("merge: remove %s: %w", name, err)
	}
	return nil
}

// check validates the args and fills in the defaults.
func check(a Args, opt Options) (plan, error) {
	bad := func(format string, v ...any) (plan, error) {
		return plan{}, fmt.Errorf("%w: "+format, append([]any{ErrBadArgs}, v...)...)
	}
	p := plan{args: a, tag: a.LanguageTag, name: a.TrackName, keepDefault: opt.KeepOriginalDefault}
	if p.tag == "" {
		p.tag = opt.LanguageTag
	}
	if p.name == "" {
		p.name = opt.TrackName
	}
	if a.KeepOriginalDefault != nil {
		p.keepDefault = *a.KeepOriginalDefault
	}
	switch {
	case a.Library == "" || !filepath.IsAbs(a.Library):
		return bad("library must be an absolute path, got %q", a.Library)
	case !under(a.Upload, jobdir.Files):
		return bad("upload must be a clean path under %s/, got %q", jobdir.Files, a.Upload)
	case !ValidDest(a.Dest):
		return bad("dest must be %s/<name>, the name of lower-case letters, digits and -, got %q", jobdir.Merged, a.Dest)
	case len(a.Tracks) == 0 || len(a.Tracks) > maxTracks:
		return bad("between 1 and %d tracks must be chosen, got %d", maxTracks, len(a.Tracks))
	case a.OffsetMs > maxOffset.Milliseconds() || a.OffsetMs < -maxOffset.Milliseconds():
		return bad("offset %d ms is beyond ±%s", a.OffsetMs, maxOffset)
	case !config.ValidTrackName(p.name):
		return bad("track name %q is not printable UTF-8 of at most %d bytes", p.name, config.MaxTrackName)
	}
	if c, ok := config.CanonicalLanguageTag(p.tag); !ok {
		if c != "" {
			return bad("language tag %q is not in canonical BCP 47 form, which mkvmerge keeps: use %q", p.tag, c)
		}
		return bad("language tag %q is not BCP 47", p.tag)
	}
	p.tracks = slices.Sorted(slices.Values(a.Tracks))
	for i, t := range p.tracks {
		if t < 0 || (i > 0 && t == p.tracks[i-1]) {
			return bad("tracks must be distinct stream indexes, got %v", a.Tracks)
		}
	}
	p.partial, p.lock = PartialFor(a.Dest)
	p.final = a.Dest + "/" + OutputName(a.Library)
	return p, nil
}

// under reports whether p is a clean job-relative path strictly inside dir.
func under(p, dir string) bool {
	return p != "" && path.Clean(p) == p && filepath.IsLocal(p) && !strings.Contains(p, `\`) &&
		strings.HasPrefix(p, dir+"/")
}

var destNameRE = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// ValidDest reports whether dest can be a merge's destination: merged/<name>, one
// directory directly under the job's merged/, which only merges use. Two destinations are
// then never inside one another, and no upload or extraction (which write under files/)
// can be in one.
func ValidDest(dest string) bool {
	name, ok := strings.CutPrefix(dest, jobdir.Merged+"/")
	return ok && destNameRE.MatchString(name)
}

type merger struct {
	job   *os.Root
	roots []string
	p     plan
	opt   Options
	tool  tool

	dst    *os.File // the partial file, once created
	placed bool     // renamed to its final name
}

func refuse(code, format string, v ...any) Output {
	return Output{Refusal: code, Detail: fmt.Sprintf(format, v...)}
}

func (m *merger) run(ctx context.Context) (Output, error) {
	var err error
	if m.tool, err = newTool(m.opt); err != nil {
		return Output{}, err
	}
	a := m.p.args

	// 1. The library file: inside a library root, opened read-only through an os.Root of
	// it. Everything below reads this open file, never the path again.
	root, rel, ok := media.LibraryPath(m.roots, a.Library)
	if !ok {
		return refuse(RefusalOutsideLibrary, "the library file %q is not under paths.libraryRoots", a.Library), nil
	}
	libRoot, err := os.OpenRoot(root)
	if err != nil {
		return refuse(RefusalLibraryUnreadable, "the library root %q cannot be opened", root), nil
	}
	defer func() { _ = libRoot.Close() }()
	lib, libInfo := openRegular(libRoot, rel)
	if lib == nil {
		return refuse(RefusalLibraryUnreadable, "the library file %q cannot be opened", a.Library), nil
	}
	defer func() { _ = lib.Close() }()
	libFormats := m.opt.Probe.LibraryFormats
	lp, err := media.ProbeOpen(ctx, lib, a.Library, libFormats, m.opt.Probe)
	if err != nil {
		return Output{}, err
	}
	if !lp.OK || lp.Media == nil {
		return refuse(RefusalLibraryUnreadable, "the library file %q does not probe: %s", a.Library, lp.Refusal), nil
	}
	libProbe := *lp.Media
	// A video stream that states no duration (Matroska without statistics tags) is
	// measured from its last packets: the container's duration may include audio that
	// runs on after the picture.
	if len(libProbe.Video) > 0 && libProbe.Video[0].DurationMs == 0 {
		from := libProbe.DurationMs - endWindow.Milliseconds()
		ms, ok, err := media.VideoEndMs(ctx, lib, libProbe.Video[0].Index, from, libFormats, m.opt.Probe)
		if err != nil {
			return Output{}, err
		}
		if ok {
			libProbe.Video[0].DurationMs = ms
		}
	}
	libID, why, err := m.tool.identify(ctx, lib, libraryContainers)
	if err != nil {
		return Output{}, err
	}
	if why != "" {
		return refuse(RefusalLibraryUnreadable, "the library file %q: %s", a.Library, why), nil
	}
	if !sameFile(libRoot, rel, lib, libInfo) {
		return refuse(RefusalLibraryChanged, "the library file %q changed while it was read", a.Library), nil
	}

	// 2. The upload: opened, probed as at upload time, identified, the chosen tracks found.
	up, _ := openRegular(m.job, a.Upload)
	if up == nil {
		return refuse(RefusalUploadUnreadable, "the upload %q cannot be opened", a.Upload), nil
	}
	defer func() { _ = up.Close() }()
	pr, err := media.ProbeOpen(ctx, up, a.Upload, nil, m.opt.Probe)
	if err != nil {
		return Output{}, err
	}
	if !pr.OK || pr.Media == nil {
		return refuse(RefusalUploadUnreadable, "the upload %q does not probe as a video: %s", a.Upload, pr.Refusal), nil
	}
	upProbe := *pr.Media
	upID, why, err := m.tool.identify(ctx, up, uploadContainers)
	if err != nil {
		return Output{}, err
	}
	if why != "" {
		return refuse(RefusalUploadUnreadable, "the upload %q: %s", a.Upload, why), nil
	}
	added, why := chooseTracks(upProbe, upID, m.p.tracks)
	if why != "" {
		return refuse(RefusalTrackNotFound, "%s", why), nil
	}
	// Without an added audio track the original audio stays the default, whatever was
	// asked: there is nothing to take its place.
	if !slices.ContainsFunc(added, func(t track) bool { return t.Type == typeAudio }) {
		m.p.keepDefault = true
	}
	libDefaults := m.libraryDefaults(libProbe, libID)

	// 3. Room for the output: the library file plus the added tracks, and the margin.
	if out, err := m.checkFreeSpace(libInfo.Size(), upProbe.Size, added); err != nil || out.Refusal != "" {
		return out, err
	}

	// 4. The mux, into a new partial file.
	if m.dst, err = m.job.OpenFile(m.p.partial, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644); err != nil {
		return Output{}, err
	}
	log := &limitedBuffer{max: maxMuxLog, tail: true}
	exit, err := m.tool.run(ctx, []*os.File{lib, up, m.dst}, log, m.muxArgs(libID, libDefaults, added)...)
	if err != nil {
		return Output{}, err
	}
	if exit != 0 {
		return refuse(RefusalMuxFailed, "mkvmerge exited with %d: %s", exit, muxMessages(log.buf.String(), 5)), nil
	}
	if !sameFile(libRoot, rel, lib, libInfo) {
		return refuse(RefusalLibraryChanged, "the library file %q changed during the merge", a.Library), nil
	}
	if err := m.dst.Sync(); err != nil {
		return Output{}, err
	}

	// 5. Verification, before anything is offered.
	outProbe, outID, why, err := m.inspectOutput(ctx)
	if err != nil {
		return Output{}, err
	}
	if why == "" {
		why = strings.Join(verify(expectation{
			lib: libProbe, libID: libID, libDefaults: libDefaults, up: upProbe, added: added,
			offsetMs: a.OffsetMs, tag: m.p.tag, name: m.p.name, keepDefault: m.p.keepDefault,
			coverageMs: m.opt.CoverageTolerance.Milliseconds(),
		}, outProbe, outID), "; ")
	}
	if why != "" {
		return refuse(RefusalVerifyFailed, "%s", why), nil
	}

	// 6. The verified file under its final name.
	return m.place(outProbe, LibraryFile{Path: a.Library, Size: libInfo.Size(), ModTime: libInfo.ModTime().UTC()})
}

// place renames the verified partial file into Dest. A rename that cannot be made durable
// is undone, so the output exists only when the result says so.
func (m *merger) place(outProbe media.Probe, lib LibraryFile) (Output, error) {
	a := m.p.args
	if err := m.job.MkdirAll(a.Dest, 0o755); err != nil {
		return Output{}, err
	}
	at, err := m.job.Lstat(m.p.partial)
	if err != nil {
		return Output{}, err
	}
	now, err := m.dst.Stat()
	if err != nil {
		return Output{}, err
	}
	if !os.SameFile(at, now) {
		return Output{}, fmt.Errorf("merge: %s is no longer the file mkvmerge wrote", m.p.partial)
	}
	if err := m.job.Rename(m.p.partial, m.p.final); err != nil {
		return Output{}, err
	}
	if err := syncDirFunc(m.job, a.Dest); err != nil {
		if rerr := removeIfSame(m.job, m.p.final, m.dst); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return Output{}, fmt.Errorf("merge: sync %s: %w", a.Dest, err)
	}
	m.placed = true
	return Output{OK: true, Path: m.p.final, Size: outProbe.Size, Media: &outProbe, Library: &lib}, nil
}

// libraryDefaults is the default flag each library track gets in the output, by position
// in libID.Tracks. Matroska states the flags, and they are kept. Other containers have none
// that mkvmerge reads (it would make every track default), so ffprobe's default
// disposition is used, and where a type has none, the first video and the first audio
// track are the default ones; subtitles never are. With keepDefault off, no library audio
// track is default.
func (m *merger) libraryDefaults(p media.Probe, id identification) []bool {
	flags := make([]bool, len(id.Tracks))
	if strings.EqualFold(id.Container.Type, "Matroska") {
		for i, t := range id.Tracks {
			flags[i] = t.Properties.DefaultTrack
		}
	} else {
		probeDefaults := map[string][]bool{typeVideo: {}, typeAudio: {}, typeSubtitle: {}}
		for _, v := range p.Video {
			probeDefaults[typeVideo] = append(probeDefaults[typeVideo], v.Default)
		}
		for _, v := range p.Audio {
			probeDefaults[typeAudio] = append(probeDefaults[typeAudio], v.Default)
		}
		for _, v := range p.Subtitles {
			probeDefaults[typeSubtitle] = append(probeDefaults[typeSubtitle], v.Default)
		}
		for _, typ := range []string{typeVideo, typeAudio, typeSubtitle} {
			var idx []int
			for i, t := range id.Tracks {
				if t.Type == typ {
					idx = append(idx, i)
				}
			}
			pd := probeDefaults[typ]
			found := false
			if len(pd) == len(idx) {
				for n, i := range idx {
					flags[i] = pd[n] && typ != typeSubtitle
					found = found || flags[i]
				}
			}
			if !found && len(idx) > 0 && typ != typeSubtitle {
				flags[idx[0]] = true
			}
		}
	}
	if !m.p.keepDefault {
		for i, t := range id.Tracks {
			if t.Type == typeAudio {
				flags[i] = false
			}
		}
	}
	return flags
}

// chooseTracks maps the chosen ffprobe stream indexes to mkvmerge track ids. Both tools
// list a file's tracks in file order, so the n-th audio (or subtitle) track of the probe is
// the n-th of the identification; when the two do not see the same number of tracks of a
// chosen type, nothing is guessed.
func chooseTracks(p media.Probe, id identification, indexes []int) ([]track, string) {
	audio, subs := id.ofType(typeAudio), id.ofType(typeSubtitle)
	var added []track
	for _, ix := range indexes {
		var n int
		var list []track
		var count int
		if i := slices.IndexFunc(p.Audio, func(t media.AudioTrack) bool { return t.Index == ix }); i >= 0 {
			n, list, count = i, audio, len(p.Audio)
		} else if i := slices.IndexFunc(p.Subtitles, func(t media.SubtitleTrack) bool { return t.Index == ix }); i >= 0 {
			n, list, count = i, subs, len(p.Subtitles)
		} else {
			return nil, fmt.Sprintf("stream %d is not an audio or subtitle track of the upload", ix)
		}
		if len(list) != count {
			return nil, fmt.Sprintf("mkvmerge sees %d tracks of the type of stream %d in the upload, ffprobe %d", len(list), ix, count)
		}
		added = append(added, list[n])
	}
	slices.SortFunc(added, func(a, b track) int { return a.ID - b.ID })
	return added, ""
}

// checkFreeSpace refuses the merge unless the work directory's filesystem has room for the
// output (about the library file plus the added tracks) and upload.freeSpaceMargin.
// A track's size is mkvmerge's statistics tag when the upload has one for each; otherwise
// the whole upload is counted.
func (m *merger) checkFreeSpace(libSize, upSize int64, added []track) (Output, error) {
	var tracks int64
	for _, t := range added {
		n, err := strconv.ParseInt(t.Properties.TagNumberOfBytes, 10, 64)
		if err != nil || n < 0 {
			tracks = upSize
			break
		}
		tracks += n
	}
	need := libSize + min(tracks, upSize) + m.opt.FreeSpaceMargin
	dir, err := m.job.Open(jobdir.Work)
	if err != nil {
		return Output{}, err
	}
	defer func() { _ = dir.Close() }()
	measure := m.opt.FreeSpace
	if measure == nil {
		measure = measureFreeSpace
	}
	free, err := measure(dir)
	if err != nil {
		return Output{}, fmt.Errorf("merge: free space: %w", err)
	}
	if free < need {
		return refuse(RefusalFreeSpace, "%d bytes free, the merge needs about %d (the library file, the added tracks and upload.freeSpaceMargin)", free, need), nil
	}
	return Output{}, nil
}

// muxArgs is the mkvmerge command line: the whole library file (descriptor 3), then the
// chosen tracks of the upload (descriptor 4), nothing else of it, into descriptor 5. The
// tracks are copied, never re-encoded; only text subtitles of other containers become
// Matroska's text subtitles. Every track's default flag is given, and every added track's
// language and name (an empty name clears the upload's).
func (m *merger) muxArgs(libID identification, libDefaults []bool, added []track) []string {
	args := []string{"--output", fdPath(2)}
	for i, t := range libID.Tracks {
		args = append(args, "--default-track-flag", strconv.Itoa(t.ID)+":"+flag(libDefaults[i]))
	}
	args = append(args, fdPath(0), "--no-video", "--no-attachments", "--no-chapters", "--no-global-tags", "--no-buttons")
	var audio, subs []string
	firstAudio := true
	for _, t := range added {
		id := strconv.Itoa(t.ID)
		def := false
		if t.Type == typeAudio {
			audio = append(audio, id)
			def = !m.p.keepDefault && firstAudio
			firstAudio = false
		} else {
			subs = append(subs, id)
		}
		args = append(args, "--language", id+":"+m.p.tag, "--default-track-flag", id+":"+flag(def),
			"--track-name", id+":"+m.p.name)
		if m.p.args.OffsetMs != 0 {
			args = append(args, "--sync", id+":"+strconv.FormatInt(m.p.args.OffsetMs, 10))
		}
	}
	if len(audio) > 0 {
		args = append(args, "--audio-tracks", strings.Join(audio, ","))
	} else {
		args = append(args, "--no-audio")
	}
	if len(subs) > 0 {
		args = append(args, "--subtitle-tracks", strings.Join(subs, ","))
	} else {
		args = append(args, "--no-subtitles")
	}
	// mkvmerge orders tracks by type by default; the library file's stay as they are, in
	// its order, and the added ones follow.
	order := make([]string, 0, len(libID.Tracks)+len(added))
	for _, t := range libID.Tracks {
		order = append(order, "0:"+strconv.Itoa(t.ID))
	}
	for _, t := range added {
		order = append(order, "1:"+strconv.Itoa(t.ID))
	}
	return append(args, fdPath(1), "--track-order", strings.Join(order, ","))
}

func flag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// inspectOutput probes and identifies the partial file, through a read-only descriptor of
// the very file mkvmerge wrote. why is set when either fails.
func (m *merger) inspectOutput(ctx context.Context) (media.Probe, identification, string, error) {
	ro, info := openRegular(m.job, m.p.partial)
	if ro == nil {
		return media.Probe{}, identification{}, "", fmt.Errorf("merge: %s cannot be opened", m.p.partial)
	}
	defer func() { _ = ro.Close() }()
	if now, err := m.dst.Stat(); err != nil || !os.SameFile(info, now) {
		return media.Probe{}, identification{}, "", fmt.Errorf("merge: %s is no longer the file mkvmerge wrote", m.p.partial)
	}
	pr, err := media.ProbeOpen(ctx, ro, m.p.partial, []string{"matroska"}, m.opt.Probe)
	if err != nil {
		return media.Probe{}, identification{}, "", err
	}
	if !pr.OK || pr.Media == nil {
		return media.Probe{}, identification{}, "the output does not probe as a video: " + pr.Refusal, nil
	}
	id, why, err := m.tool.identify(ctx, ro, []string{"Matroska"})
	if err != nil {
		return media.Probe{}, identification{}, "", err
	}
	if why != "" {
		return media.Probe{}, identification{}, "the output: " + why, nil
	}
	return *pr.Media, id, "", nil
}

// openRegular opens p through root, read-only, and requires a regular file. On failure it
// returns nil.
func openRegular(root *os.Root, p string) (*os.File, os.FileInfo) {
	// O_NONBLOCK: opening a FIFO must not wait for a writer. It is cleared again below.
	f, err := root.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil
	}
	if err := syscall.SetNonblock(int(f.Fd()), false); err != nil {
		_ = f.Close()
		return nil, nil
	}
	return f, st
}

// sameFile reports whether the open file f is still the one at rel, with the size and
// modification time it had when it was opened: the probe and the mux read the same
// library file, and nothing wrote to it meanwhile.
func sameFile(root *os.Root, rel string, f *os.File, was os.FileInfo) bool {
	now, err := f.Stat()
	if err != nil || now.Size() != was.Size() || !now.ModTime().Equal(was.ModTime()) {
		return false
	}
	at, err := root.Stat(rel)
	return err == nil && os.SameFile(at, now)
}

// syncDirFunc is syncDir; a test replaces it to see a failed sync undone.
var syncDirFunc = syncDir

// syncDir makes a rename into dir durable.
func syncDir(root *os.Root, dir string) error {
	f, err := root.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
