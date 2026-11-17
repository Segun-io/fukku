package flows

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/store"
)

// venv is the import environment with second versions on: a versions root on the intake's
// filesystem (both are temporary folders), Jellyfin as the media server, and the film in
// Radarr with its tracked file (libFile) in its folder.
type venv struct {
	*ienv
	versions string
	vstep    *recordingStep // the version jobs' post-processing step
	istep    *recordingStep // the imports' post-processing step
	done     []store.Target // what OnDone was told
}

const palMs = 5_630_580 // filmMinutes × 1.0427: a PAL speed-up of the film

func newVersionEnv(t *testing.T) *venv {
	t.Helper()
	x := &venv{ienv: newImportEnv(t)}
	x.versions = filepath.Join(t.TempDir(), "versions")
	if err := os.Mkdir(x.versions, 0o755); err != nil {
		t.Fatal(err)
	}
	x.cfg.Versions.Enabled, x.cfg.Versions.Root = true, x.versions
	x.cfg.MediaServer.Provider = "jellyfin"
	x.cfg.Paths.LibraryRoots = []string{libRoot}
	x.cfg.Paths.Mappings.Radarr = append(x.cfg.Paths.Mappings.Radarr, config.PathMapping{From: libRoot, To: appLibRoot})
	x.movies.Library = []arr.Movie{{ID: filmID, Title: "Some Film", Year: 2001, OriginalLanguage: "English",
		Path: appLibRoot + "/Some Film (2001)", HasFile: true, File: libFile()}}
	x.movies.Roots = []arr.RootFolder{{Path: appLibRoot}}
	x.rebuild(x.movies, x.series)
	x.vstep, x.istep = &recordingStep{name: "mediaServerVersions"}, &recordingStep{name: "subtitles"}
	x.eng.AddVersionStep(x.vstep)
	x.eng.AddPostStep(x.istep)
	x.eng.OnDone(func(t store.Target) { x.done = append(x.done, t) })
	return x
}

// versionJob is a flow 2 job awaiting confirmation: the library file is probed (90 min,
// English), the upload (on disk) lasts uploadMs and has an audio track in lang.
func (x *venv) versionJob(uploadMs int64, lang string) store.Job {
	x.t.Helper()
	j := x.job(AppRadarr, filmID, vid("Some Film (2001).es.mkv", uploadMs))
	files, err := x.st.Files(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	for _, f := range files {
		if f.Kind != store.KindVideo {
			continue
		}
		f.Report, _ = json.Marshal(media.Probe{Container: "matroska,webm", DurationMs: uploadMs, Size: 1000,
			Video: []media.VideoTrack{{Codec: "h264", Width: 1920, Height: 1080, DurationMs: uploadMs}},
			Audio: []media.AudioTrack{{Index: 1, Codec: "ac3", Language: lang, Default: true}}})
		if err := x.st.UpdateFile(x.ctx, f); err != nil {
			x.t.Fatal(err)
		}
	}
	x.onDisk(j)
	rec := newFlow2(libFile(), x.cfg)
	rec.Library.Probe = libProbe(filmMinutes)
	tg, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	if tg.Options, err = withOption(tg.Options, "flow2", rec); err != nil {
		x.t.Fatal(err)
	}
	if err := x.st.SetTarget(x.ctx, j.ID, tg); err != nil {
		x.t.Fatal(err)
	}
	if j, err = x.st.Transition(x.ctx, j.ID, store.Matched, store.AwaitingConfirmation, store.Change{Actor: "system"}); err != nil {
		x.t.Fatal(err)
	}
	return j
}

func (x *venv) view(j store.Job) *Flow2View {
	x.t.Helper()
	tg, err := x.st.Target(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	files, err := x.st.Files(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	return ViewFlow2(x.cfg, tg, files)
}

func (x *venv) confirmVersion(j store.Job, label string) (Confirmation, error) {
	req := ConfirmRequest{Flow2: OfferVersion}
	if label != "" {
		req.Version = &VersionRequest{Label: label}
	}
	return x.eng.Confirm(x.ctx, "user1", "user1@example.com", j, req)
}

func (x *venv) dest(name string) string { return filepath.Join(x.versions, "Some Film (2001)", name) }

func (x *venv) actions(j store.Job) []string {
	x.t.Helper()
	log, err := x.st.AuditLog(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	var out []string
	for _, e := range log {
		out = append(out, e.Action)
	}
	return out
}

// refused checks that err is the refusal code and that nothing was written.
func (x *venv) refused(j store.Job, err error, code string) {
	x.t.Helper()
	if Code(err) != code {
		x.t.Fatalf("got %v, want %s", err, code)
	}
	entries, rerr := os.ReadDir(x.versions)
	if rerr != nil {
		x.t.Fatal(rerr)
	}
	for _, e := range entries {
		sub, _ := os.ReadDir(filepath.Join(x.versions, e.Name()))
		for _, s := range sub {
			if s.Name() != "Some Film (2001) - Mine.mkv" { // a test's own file
				x.t.Errorf("written into versions.root: %s/%s", e.Name(), s.Name())
			}
		}
	}
}

// The M3 exit criterion: a PAL copy becomes a second version, outside Radarr's folder,
// placed by a link of the job's file, and grouped by the version steps alone.
func TestVersionPALCopyBecomesSecondVersion(t *testing.T) {
	x := newVersionEnv(t)
	j := x.versionJob(palMs, "spa")

	v := x.view(j)
	if v.State != Flow2Ready || *v.Finding != FindingPAL || !slices.Equal(v.Offers, []string{OfferVersion}) {
		t.Fatalf("view %+v", v)
	}
	if v.Version == nil || v.Version.Label != "Español" || v.Version.Language != "es" || v.Version.Problem != nil ||
		v.Version.Name != "Some Film (2001) - Español.mkv" ||
		!slices.Equal(v.Version.Notices, []string{NoticeOutsideRadarr, NoticeRegroup, NoticeKeptOnDelete, NoticeNoSubtitles}) {
		t.Fatalf("version offer %+v", v.Version)
	}

	c, err := x.confirmVersion(j, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Job.State != store.Importing {
		t.Fatalf("confirmed job is %s", c.Job.State)
	}
	if want := []string{"Some Film (2001) - Español", "Some Film (2001)"}; !slices.Equal(x.movies.ParseCalls, want) {
		t.Errorf("Radarr was asked to parse %q, want %q (the name without its extension, then the folder alone)", x.movies.ParseCalls, want)
	}
	if got := x.actions(j); !slices.Equal(got, []string{"version.confirmed"}) {
		t.Errorf("audit after the confirmation %v", got)
	}

	j = x.drive(x.eng, c.Job)
	if j.State != store.Done {
		t.Fatalf("job %s (%s: %s)", j.State, j.ErrorCode, j.ErrorText)
	}
	dest := x.dest("Some Film (2001) - Español.mkv")
	src := x.src(j, "files/Some Film (2001).es.mkv")
	si, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("the version is not in place: %v", err)
	}
	if !os.SameFile(si, di) {
		t.Error("the version is not a link of the job's file")
	}
	recs, err := x.st.VersionsOf(x.ctx, j.ID)
	if err != nil || len(recs) != 1 {
		t.Fatalf("records %+v (%v)", recs, err)
	}
	if r := recs[0]; r.Path != dest || r.Status != store.VersionLinked || r.TitleID != filmID || r.App != AppRadarr || r.Label != "Español" {
		t.Errorf("record %+v", r)
	}
	if got := x.actions(j); !slices.Equal(got, []string{"version.confirmed", "version.linked"}) {
		t.Errorf("audit %v", got)
	}
	// Radarr was neither asked to import nor to change the film's monitoring.
	if len(x.movies.Imports) != 0 || !x.movies.Library[0].HasFile || x.movies.Library[0].File.ID != libFile().ID {
		t.Errorf("Radarr was touched: imports %v, film %+v", x.movies.Imports, x.movies.Library[0])
	}
	// The version steps ran, not the import's; the keeper was told the job is done.
	if x.vstep.count() != 1 || x.istep.count() != 0 {
		t.Errorf("version step ran %d time(s), import step %d", x.vstep.count(), x.istep.count())
	}
	if len(x.done) != 1 || x.done[0].TitleID != filmID {
		t.Errorf("OnDone got %+v", x.done)
	}
	_, res, err := x.eng.Detail(x.ctx, j, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version == nil || res.Version.Name != "Some Film (2001) - Español.mkv" || res.Version.Status != store.VersionLinked ||
		len(res.PostProcessing) != 1 || res.PostProcessing[0].Step != "mediaServerVersions" {
		t.Errorf("result %+v (version %+v)", res, res.Version)
	}
}

// A different cut is a second version too, with the label the user typed.
func TestVersionDifferentCutWithOwnLabel(t *testing.T) {
	x := newVersionEnv(t)
	j := x.versionJob(80*minute, "spa")
	c, err := x.confirmVersion(j, "  Versión del director ")
	if err != nil {
		t.Fatal(err)
	}
	if j = x.drive(x.eng, c.Job); j.State != store.Done {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if _, err := os.Stat(x.dest("Some Film (2001) - Versión del director.mkv")); err != nil {
		t.Fatal(err)
	}
}

// The M3 exit criterion: a winning label is refused, by the forbidden tokens and by Radarr's
// own reading of the name; so are labels the media server would take for an extra, and
// names that cannot be files. Nothing is written, and the job keeps waiting.
func TestVersionWinningLabelRefused(t *testing.T) {
	x := newVersionEnv(t)
	x.movies.Parsed = map[string]arr.Parsed{
		"Some Film (2001) - Dual":   {OK: true, Quality: arr.Quality{ID: 7, Name: "Bluray-1080p"}, Source: "bluray", Resolution: 1080, Revision: 1},
		"Some Film (2001) - Final":  {OK: true, Quality: arr.Quality{Name: "Unknown"}, Source: "unknown", Revision: 2},
		"Some Film (2001) - Copia":  {OK: true, Quality: arr.Quality{Name: "Unknown"}, Source: "tv", Resolution: 480, Revision: 1},
		"Some Film (2001) - Normal": {OK: true, Quality: arr.Quality{Name: "Unknown"}, Source: "unknown", Revision: 1},
	}
	j := x.versionJob(palMs, "spa")
	for label, reason := range map[string]string{
		"Bluray":                LabelForbidden,
		"Español WEB-DL":        LabelForbidden,
		"Español web dl":        LabelForbidden, // web-dl as two words
		"1080p":                 LabelForbidden,
		"REMUX Español":         LabelForbidden,
		"Español-trailer":       LabelExtra,
		"Dual":                  LabelQuality, // Radarr reads Bluray-1080p in it
		"Final":                 LabelQuality, // a proper
		"Copia":                 LabelQuality, // a source
		"a/b":                   LabelInvalid,
		"what?":                 LabelInvalid,
		".hidden":               LabelInvalid,
		strings.Repeat("x", 65): LabelTooLong,
	} {
		_, err := x.confirmVersion(j, label)
		x.refused(j, err, CodeVersionLabel)
		var e *Error
		if errors.As(err, &e) && reason == LabelQuality && !strings.Contains(e.Message, "Radarr reads") {
			t.Errorf("%q: %v", label, err)
		}
		if _, p := checkLabel(label, x.cfg.Versions); reason != LabelQuality && (p == nil || p.Reason != reason) {
			t.Errorf("%q: problem %+v, want %s", label, p, reason)
		}
	}
	if got := x.jobNow(j).State; got != store.AwaitingConfirmation {
		t.Fatalf("job %s after the refusals", got)
	}
	if got := x.actions(j); len(got) != 0 {
		t.Errorf("audit %v", got)
	}
	// A label Radarr reads nothing in is fine.
	if _, err := x.confirmVersion(j, "Normal"); err != nil {
		t.Fatal(err)
	}
}

// Without versions.requireNotUpgradeVerdict Radarr is not asked; the tokens still apply.
func TestVersionWithoutRadarrVerdict(t *testing.T) {
	x := newVersionEnv(t)
	x.cfg.Versions.RequireNotUpgradeVerdict = false
	x.rebuild(x.movies, x.series)
	j := x.versionJob(palMs, "spa")
	_, err := x.confirmVersion(j, "HDTV")
	x.refused(j, err, CodeVersionLabel)
	if _, err := x.confirmVersion(j, ""); err != nil {
		t.Fatal(err)
	}
	if len(x.movies.ParseCalls) != 0 {
		t.Errorf("Radarr was asked: %v", x.movies.ParseCalls)
	}
}

// The M3 exit criterion: nothing in the library is ever overwritten. A name that is taken
// is refused at the confirmation, and a file that appears between the confirmation and the
// link fails the job and is left as it was.
func TestVersionNeverOverwrites(t *testing.T) {
	x := newVersionEnv(t)
	j := x.versionJob(palMs, "spa")
	mine := x.dest("Some Film (2001) - Mine.mkv")
	if err := os.MkdirAll(filepath.Dir(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("someone else's"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := x.confirmVersion(j, "Mine")
	x.refused(j, err, CodeVersionExists)

	c, err := x.confirmVersion(j, "")
	if err != nil {
		t.Fatal(err)
	}
	dest := x.dest("Some Film (2001) - Español.mkv")
	if err := os.WriteFile(dest, []byte("appeared meanwhile"), 0o644); err != nil {
		t.Fatal(err)
	}
	j, err = x.step(x.eng, c.Job)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != store.Failed || j.ErrorCode != CodeVersionExists {
		t.Fatalf("job %s %s (%s)", j.State, j.ErrorCode, j.ErrorText)
	}
	for p, want := range map[string]string{mine: "someone else's", dest: "appeared meanwhile"} {
		if b, err := os.ReadFile(p); err != nil || string(b) != want {
			t.Errorf("%s now holds %q (%v)", p, b, err)
		}
	}
	// linkVersion itself never replaces.
	if err := linkVersion(x.versions, x.src(j, "files/Some Film (2001).es.mkv"), "Some Film (2001)", "Some Film (2001) - Mine.mkv"); !errors.Is(err, os.ErrExist) {
		t.Errorf("linking over a file: %v, want ErrExist", err)
	}
}

// Radarr's tracked file is checked again before anything is written: when it changed, the
// job goes back to probed (the flow 2 staleness rule of #82), at the confirmation or in the
// importing step.
func TestVersionStaleLibraryFile(t *testing.T) {
	t.Run("at the confirmation", func(t *testing.T) {
		x := newVersionEnv(t)
		j := x.versionJob(palMs, "spa")
		x.movies.Library[0].File = &arr.MovieFile{ID: 99, Path: appLibRoot + "/Some Film (2001)/Some Film (2001) Bluray-1080p.mkv", Size: 9000}
		_, err := x.confirmVersion(j, "")
		x.refused(j, err, CodeLibraryChanged)
		j = x.jobNow(j)
		if j.State != store.Probed {
			t.Fatalf("job %s, want probed", j.State)
		}
		if _, err := x.st.Target(x.ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("the stale target is still there: %v", err)
		}
	})
	t.Run("before the link", func(t *testing.T) {
		x := newVersionEnv(t)
		j := x.versionJob(palMs, "spa")
		c, err := x.confirmVersion(j, "")
		if err != nil {
			t.Fatal(err)
		}
		x.movies.Library[0].File.Size = 4001 // replaced under the same id
		j, err = x.step(x.eng, c.Job)
		if err != nil {
			t.Fatal(err)
		}
		if j.State != store.Probed {
			t.Fatalf("job %s (%s), want probed", j.State, j.ErrorText)
		}
		x.refused(j, nil, "")
		if recs, _ := x.st.VersionsOf(x.ctx, j.ID); len(recs) != 0 {
			t.Errorf("records %+v", recs)
		}
		h, _ := x.st.History(x.ctx, j.ID)
		if last := h[len(h)-1]; last.From != store.Importing || !strings.Contains(last.Note, "changed since the match") {
			t.Errorf("last event %+v", last)
		}
	})
	t.Run("the film has no file now", func(t *testing.T) {
		x := newVersionEnv(t)
		j := x.versionJob(palMs, "spa")
		x.movies.Library[0].File, x.movies.Library[0].HasFile = nil, false
		_, err := x.confirmVersion(j, "")
		x.refused(j, err, CodeLibraryChanged)
	})
}

// A crash after the link, before the job moved on: the next pass finds the job's own file
// in place and goes on, without asking Radarr again and without a second link.
func TestVersionRepeatAfterCrash(t *testing.T) {
	x := newVersionEnv(t)
	j := x.versionJob(palMs, "spa")
	c, err := x.confirmVersion(j, "")
	if err != nil {
		t.Fatal(err)
	}
	choice, _ := versionOf(c.Target)
	src := x.src(j, "files/Some Film (2001).es.mkv")
	if err := linkVersion(x.versions, src, choice.Folder, choice.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := x.st.PutVersion(x.ctx, store.Version{JobID: j.ID, App: AppRadarr, TitleID: filmID, Path: choice.Path, Status: store.VersionPlacing}); err != nil {
		t.Fatal(err)
	}
	x.movies.Err = map[string]error{"Movie": errors.New("must not be asked")}
	if j, err = x.step(x.eng, c.Job); err != nil {
		t.Fatal(err)
	}
	if j.State != store.Imported {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	recs, _ := x.st.VersionsOf(x.ctx, j.ID)
	if len(recs) != 1 || recs[0].Status != store.VersionLinked {
		t.Errorf("records %+v", recs)
	}
	if got := x.actions(j); !slices.Equal(got, []string{"version.confirmed", "version.linked"}) {
		t.Errorf("audit %v", got)
	}
}

// A second version is offered only when it can be made: versions on, a media server that
// groups versions, and versions.root outside Radarr's root folders.
func TestVersionUnavailable(t *testing.T) {
	for name, tweak := range map[string]func(x *venv){
		"versions off":    func(x *venv) { x.cfg.Versions.Enabled = false },
		"no media server": func(x *venv) { x.cfg.MediaServer.Provider = "none" },
		"root inside a Radarr root folder": func(x *venv) {
			x.movies.Roots = append(x.movies.Roots, arr.RootFolder{Path: "/radarr/extra"})
			x.cfg.Paths.Mappings.Radarr = append(x.cfg.Paths.Mappings.Radarr, config.PathMapping{From: filepath.Dir(x.versions), To: "/radarr/extra"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			x := newVersionEnv(t)
			tweak(x)
			x.rebuild(x.movies, x.series)
			j := x.versionJob(palMs, "spa")
			_, err := x.confirmVersion(j, "")
			x.refused(j, err, CodeVersionUnavailable)
		})
	}
	t.Run("versions.root on another filesystem", func(t *testing.T) {
		x := newVersionEnv(t)
		x.eng.checkVersionStorage = func() error { return errors.New("/v is on a different filesystem than /i") }
		j := x.versionJob(palMs, "spa")
		_, err := x.confirmVersion(j, "")
		x.refused(j, err, CodeStorageUnavailable)
	})
	t.Run("a flow 1 job", func(t *testing.T) {
		x := newVersionEnv(t)
		j := x.filmJob()
		_, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{Flow2: OfferVersion})
		if Code(err) != CodeBadRequest {
			t.Errorf("got %v, want bad_request", err)
		}
	})
	t.Run("an import of a flow 2 job", func(t *testing.T) {
		x := newVersionEnv(t)
		j := x.versionJob(palMs, "spa")
		_, err := x.eng.Confirm(x.ctx, "user1", "", j, ConfirmRequest{})
		x.refused(j, err, CodeFlow2)
	})
}

// The link is made with the job's file and the versions folder as open directories; an
// EXDEV from linkat is reported as a filesystem problem.
func TestVersionCrossDevice(t *testing.T) {
	if !crossDevice(syscall.EXDEV) || crossDevice(syscall.EEXIST) {
		t.Fatal("crossDevice does not tell EXDEV")
	}
}

func TestVersionLabels(t *testing.T) {
	for tag, want := range map[string]string{"spa": "es", "es-419": "es", "es": "es", "ger": "de", "deu": "de", "und": "", "": "", "zxx": "", "xyz-1": ""} {
		if got := languageCode(tag); got != want {
			t.Errorf("languageCode(%q) = %q, want %q", tag, got, want)
		}
	}
	for code, want := range map[string]string{"es": "Español", "de": "Deutsch", "fr": "Français", "ja": "日本語"} {
		if got := languageName(code); got != want {
			t.Errorf("languageName(%q) = %q, want %q", code, got, want)
		}
	}
	lib := media.Probe{Audio: []media.AudioTrack{{Language: "eng"}}}
	up := media.Probe{Audio: []media.AudioTrack{{Language: "eng"}, {Language: ""}, {Language: "spa"}}}
	if got := newLanguage(up, lib); got != "es" {
		t.Errorf("newLanguage: %q, want es (the track the library file lacks)", got)
	}
	if got := newLanguage(media.Probe{Audio: []media.AudioTrack{{Language: "eng"}}}, lib); got != "en" {
		t.Errorf("newLanguage with nothing new: %q, want en", got)
	}
	if got := newLanguage(media.Probe{Audio: []media.AudioTrack{{}}}, lib); got != "" {
		t.Errorf("newLanguage without a tag: %q", got)
	}
	for tmpl, want := range map[string]string{"{languageName}": "Español", "{languageName} ({languageCode})": "Español (es)", "Doblaje": "Doblaje"} {
		if got := proposeLabel(tmpl, "es"); got != want {
			t.Errorf("proposeLabel(%q) = %q, want %q", tmpl, got, want)
		}
	}
	if got := proposeLabel("{languageName}", ""); got != "" {
		t.Errorf("no language: %q", got)
	}
	cfg := config.Defaults().Versions
	for _, ok := range []string{"Español", "Español (Latino)", "Versión extendida", "HDR10 original", "Doblaje 1985"} {
		if _, p := checkLabel(ok, cfg); p != nil {
			t.Errorf("%q refused: %+v", ok, p)
		}
	}
	decomposed := "Espan" + string(rune(0x0303)) + "ol" + string(rune(0x200f)) // n and a combining tilde, then a right-to-left mark
	if got, p := checkLabel(decomposed, cfg); p != nil || got != "Espa"+string(rune(0x00f1))+"ol" {
		t.Errorf("cleaned %q (%+v), want NFC without the control character", got, p)
	}
}
