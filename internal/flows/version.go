package flows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/fscheck"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/pathmap"
	"github.com/segun-io/fukku/internal/safename"
	"github.com/segun-io/fukku/internal/store"
)

// Second versions of films (flow 2b, #56, decision #20). The upload is kept as a second
// version of a film Radarr already has a file for: a PAL speed-up or a different cut, which
// cannot be merged (#55). The version lives outside Radarr's root folders, in versions.root:
//
//	<versions.root>/<film folder>/<film folder> - <label><ext>
//
// so Radarr never registers it (probes B3, B3e: every video in a film folder, subfolders
// included, becomes one of the film's records, and an upgrade can make it the film's file,
// B3f, B3g). The media server groups it with the film's file (Jellyfin's MergeVersions,
// package mediaserver); that grouping is lost when Radarr renames or upgrades the film's
// file (B3e), so a version keeper (mediaserver.Keeper) checks it on a schedule
// (versions.regroupInterval) and after each of Fukku's own imports, and groups it again.
//
// The confirmation (POST /api/jobs/{id}/confirm with {"flow2": "version", "version":
// {"label": "Español"}}):
//
//   - The job must be flow 2, its library file probed, and "version" among the offers
//     (versions.enabled, and a media server that can group versions); else
//     version_unavailable.
//   - The label: the request's, or the one versions.labelTemplate proposes from the upload's
//     new audio language. It must be one path element without the characters Windows and
//     Samba refuse, at most 64 bytes, contain none of versions.forbiddenLabelTokens as a
//     word, and not end in one of the words Jellyfin takes for an extra (B4); with
//     versions.requireNotUpgradeVerdict, Radarr's parse of "<film folder> - <label>" must
//     read no quality and no proper or repack. Else version_label.
//   - Radarr's tracked file must still be the one of the match (movie.movieFile's id and
//     size). If it changed, the job goes back to probed, as a stale library probe does
//     (rescan), and the confirmation is refused with library_changed.
//   - versions.root must be a folder on the intake root's filesystem, writable, outside
//     every Radarr root folder (as paths.mappings.radarr maps them) and every
//     paths.libraryRoots entry (version_unavailable or storage_unavailable), and the
//     version's name must be free (version_exists).
//   - Then one transaction: the choice into the target's options ("version"), the audit
//     entry version.confirmed, and the move to importing.
//
// The importing step places the file (placeVersion): Radarr's tracked file is checked
// again before anything is written (stale: back to probed), the record is stored
// (store.Version, status placing), then the job's file is hard-linked into place with
// linkat(2), which never replaces a file; then version.linked, and the job moves to
// imported. A repeat after a crash finds the link (same inode) and goes on. The imported
// step leaves Radarr's monitoring alone. Post-processing runs the version steps
// (AddVersionStep: the media server's grouping) instead of the import's (the subtitle
// manager knows only Radarr's file, so the job's subtitles are not handed over).

// Codes of the second versions.
const (
	// CodeVersionLabel refuses a confirmation whose label cannot be used; the message says why.
	CodeVersionLabel = "version_label"
	// CodeVersionUnavailable refuses a confirmation when no version can be made for the job:
	// not offered, or versions.root cannot be used (not a folder, inside a Radarr root
	// folder).
	CodeVersionUnavailable = "version_unavailable"
	// CodeVersionExists refuses a confirmation, or fails a job, when a file of the version's
	// name is already in versions.root: nothing is ever replaced.
	CodeVersionExists = "version_exists"
	// CodeLibraryChanged refuses a confirmation whose film's tracked file changed since the
	// match; the job went back to probed, to be matched again.
	CodeLibraryChanged = "library_changed"
	// CodeVersionFailed fails a job whose version could not be placed (the text says why).
	CodeVersionFailed = "version_failed"
)

// ReasonVersionsUnsupported (NotOffered.Reason): no media server that can group versions
// is configured (mediaServer.provider none).
const ReasonVersionsUnsupported = "versions_unsupported"

// Notices of a version (VersionOffer.Notices, VersionResult.Notices): what the UI tells the
// user about how Radarr and the media server treat it.
const (
	// NoticeOutsideRadarr: Radarr does not know the version. It never renames, upgrades or
	// deletes it, and replaces only the film's own file.
	NoticeOutsideRadarr = "outside_radarr"
	// NoticeRegroup: the media server loses the grouping when Radarr renames or upgrades the
	// film's file; Fukku groups them again (versions.regroupInterval), and until then the
	// two show as separate entries.
	NoticeRegroup = "regrouped_after_changes"
	// NoticeKeptOnDelete: deleting the film in Radarr leaves the version on disk; it must be
	// removed by hand.
	NoticeKeptOnDelete = "kept_on_delete"
	// NoticeNoSubtitles: the job has subtitle files; they are not handed over, because the
	// subtitle manager knows only Radarr's file.
	NoticeNoSubtitles = "subtitles_not_handed_over"
)

// Label problems (LabelProblem.Reason).
const (
	LabelEmpty     = "label_empty"     // no label, and none can be proposed (no tagged audio language)
	LabelInvalid   = "label_invalid"   // not one path element, or a character Windows or Samba refuses
	LabelTooLong   = "label_too_long"  // over maxLabelBytes, or the file name over 255 bytes
	LabelForbidden = "label_forbidden" // a word of versions.forbiddenLabelTokens (Token)
	LabelExtra     = "label_extra"     // ends in a word Jellyfin takes for an extra (B4)
	LabelQuality   = "label_quality"   // Radarr reads a quality or a revision in the name (Token)
)

// maxLabelBytes is the longest label.
const maxLabelBytes = 64

// jellyfinExtras are the words that make Jellyfin take a file for an extra when the name
// ends in one of them (probe B4: "- Español-trailer", "- Español-other").
var jellyfinExtras = []string{
	"trailer", "trailers", "sample", "other", "extra", "extras", "featurette", "featurettes",
	"scene", "scenes", "clip", "clips", "interview", "interviews", "behindthescenes",
	"deleted", "deletedscene", "short", "shorts",
}

// VersionRequest is ConfirmRequest.Version: the label of the version ("" or absent: the
// proposed one).
type VersionRequest struct {
	Label string `json:"label"`
}

// LabelProblem is why a label cannot be used.
type LabelProblem struct {
	Reason  string `json:"reason"`
	Token   string `json:"token,omitempty"` // the forbidden word, or what Radarr read
	Message string `json:"message"`
}

// VersionOffer is Flow2View.Version: what a second version of the job would be, while
// "version" is offered. Label is the proposed label ("" when none can be proposed), Name
// the file name with it, Problem why the proposed label cannot be used as it is (null: it
// can, as far as Fukku can tell without asking Radarr). Language is the ISO 639-1 code of
// the upload's new audio language ("" when unknown).
type VersionOffer struct {
	Label    string        `json:"label"`
	Language string        `json:"language"`
	Name     string        `json:"name"`
	Problem  *LabelProblem `json:"problem"`
	Notices  []string      `json:"notices"`
}

// VersionResult is ResultView.Version: the placed version and its grouping. Status is the
// record's (store.VersionPlacing … store.VersionMissing); Detail (why it is waiting, for
// example) is for admins only, since it may name paths and services.
type VersionResult struct {
	Name             string     `json:"name"`
	Label            string     `json:"label"`
	Status           string     `json:"status"`
	Detail           string     `json:"detail"`
	GroupedAt        *time.Time `json:"groupedAt"`
	PrimaryChangedAt *time.Time `json:"primaryChangedAt"`
	Notices          []string   `json:"notices"`
}

// versionChoice is what the confirmation stores in the target's options ("version") for the
// importing step. Path is versions.root/Folder/Name as Fukku sees it; LibraryFileID and
// LibrarySize are Radarr's tracked file at the match, checked again before the write.
type versionChoice struct {
	FileID        int64  `json:"fileId"`
	Label         string `json:"label"`
	Folder        string `json:"folder"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	LibraryFileID int    `json:"libraryFileId"`
	LibrarySize   int64  `json:"librarySize"`
}

// versionOf returns the version choice of a confirmed flow 2 job; ok is false for any other.
func versionOf(t store.Target) (versionChoice, bool) {
	var opts struct {
		Version *versionChoice `json:"version"`
	}
	if len(t.Options) > 0 {
		_ = json.Unmarshal(t.Options, &opts)
	}
	if opts.Version == nil || opts.Version.Path == "" {
		return versionChoice{}, false
	}
	return *opts.Version, true
}

// IsVersion reports whether the job of target t makes a second version (it was confirmed so).
func IsVersion(t store.Target) bool {
	_, ok := versionOf(t)
	return ok
}

// canGroup reports whether the configured media server can group versions.
func canGroup(cfg config.Config) bool { return cfg.MediaServer.Provider == "jellyfin" }

// ---- the label ----------------------------------------------------------------------------

// languageCode is the ISO 639-1 code of a language tag as files carry it ("spa", "es-419",
// "es"), or "" when it names no language.
func languageCode(tag string) string {
	t, err := language.Parse(strings.TrimSpace(tag))
	if err != nil {
		return ""
	}
	b, conf := t.Base()
	if conf != language.Exact {
		return ""
	}
	if code := b.String(); len(code) == 2 {
		return code
	}
	return ""
}

// languageName is the name of a language in that language, with a capital first letter:
// "es" → "Español".
func languageName(code string) string {
	n := display.Self.Name(language.Make(code))
	if n == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(n)
	return string(unicode.ToTitle(r)) + n[size:]
}

// newLanguage is the language a version adds: the ISO 639-1 code of the upload's first
// audio track whose language the library file has no track in, else of its first tagged
// audio track; "" when no audio track is tagged.
func newLanguage(upload, library media.Probe) string {
	have := map[string]bool{}
	for _, a := range library.Audio {
		if c := languageCode(a.Language); c != "" {
			have[c] = true
		}
	}
	first := ""
	for _, a := range upload.Audio {
		c := languageCode(a.Language)
		if c == "" {
			continue
		}
		if !have[c] {
			return c
		}
		if first == "" {
			first = c
		}
	}
	return first
}

// proposeLabel fills versions.labelTemplate for the language code ("" when the template
// needs a language and there is none).
func proposeLabel(template, code string) string {
	if code == "" && strings.Contains(template, "{language") {
		return ""
	}
	return strings.TrimSpace(strings.NewReplacer("{languageName}", languageName(code), "{languageCode}", code).Replace(template))
}

// labelWords are the words of s, lower-cased: runs of letters and digits.
func labelWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// checkLabel cleans a label (NFC, control characters removed) and returns it, or why it
// cannot be used. Radarr's verdict is not part of it (radarrVerdict).
func checkLabel(label string, cfg config.Versions) (string, *LabelProblem) {
	clean, err := safename.Clean(label, maxLabelBytes)
	switch {
	case errors.Is(err, safename.ErrEmpty):
		return "", &LabelProblem{Reason: LabelEmpty, Message: "The version needs a label, for example the language of its audio."}
	case errors.Is(err, safename.ErrTooLong):
		return "", &LabelProblem{Reason: LabelTooLong, Message: fmt.Sprintf("The label is too long: at most %d bytes.", maxLabelBytes)}
	case err != nil:
		return "", &LabelProblem{Reason: LabelInvalid, Message: "The label must not contain / or \\, or be . or .."}
	}
	if strings.ContainsAny(clean, `<>:"|?*`) || strings.HasPrefix(clean, ".") || strings.HasSuffix(clean, ".") {
		return "", &LabelProblem{Reason: LabelInvalid, Message: `The label must not contain < > : " | ? *, or start or end with a dot.`}
	}
	words := labelWords(clean)
	if len(words) == 0 {
		return "", &LabelProblem{Reason: LabelInvalid, Message: "The label needs at least one letter or digit."}
	}
	for _, tok := range cfg.ForbiddenLabelTokens {
		tw := labelWords(tok)
		if len(tw) == 0 {
			continue
		}
		for i := 0; i+len(tw) <= len(words); i++ {
			if slices.Equal(words[i:i+len(tw)], tw) {
				return "", &LabelProblem{Reason: LabelForbidden, Token: tok, Message: fmt.Sprintf(
					"The label must not contain %q: the name would read as a quality or a source (versions.forbiddenLabelTokens).", tok)}
			}
		}
	}
	if last := words[len(words)-1]; slices.Contains(jellyfinExtras, last) {
		return "", &LabelProblem{Reason: LabelExtra, Token: last, Message: fmt.Sprintf(
			"The label must not end in %q: the media server would show the file as an extra, not as a version.", last)}
	}
	return clean, nil
}

// versionName is the version's file name: "<film folder> - <label><ext>".
func versionName(folder, label, ext string) string { return folder + " - " + label + ext }

// radarrVerdict is the problem Radarr's parse of a version's name (without its extension)
// shows: a quality, a source, a resolution, or a proper or repack. nil: it reads nothing.
func radarrVerdict(p arr.Parsed, stem string) *LabelProblem {
	src := strings.ToLower(p.Source)
	if p.Quality.ID == 0 && (src == "" || src == "unknown") && p.Resolution == 0 && p.Revision <= 1 && !p.Repack {
		return nil
	}
	what := p.Quality.Name
	switch {
	case p.Revision > 1 || p.Repack:
		what = "a proper or repack"
	case what == "" || p.Quality.ID == 0:
		what = strings.TrimSpace(p.Source + " " + strconv.Itoa(p.Resolution) + "p")
	}
	return &LabelProblem{Reason: LabelQuality, Token: what, Message: fmt.Sprintf(
		"Radarr reads %s in %q: choose a label without a quality or source word (versions.requireNotUpgradeVerdict).", what, stem)}
}

// filmFolder is the name of Radarr's folder for the film (movie.path as Radarr sees it), as
// a version's folder and name prefix.
func filmFolder(appPath string) (string, error) {
	p := strings.TrimRight(strings.ReplaceAll(appPath, `\`, "/"), "/")
	if p == "" {
		return "", errors.New("radarr gives no folder for the film")
	}
	base := path.Base(p)
	clean, err := safename.Clean(base, 200)
	if err != nil || clean != base || strings.ContainsAny(base, `<>:"|?*`) {
		return "", fmt.Errorf("radarr's folder name %q cannot be used as a file name", base)
	}
	return clean, nil
}

// trackedChanged is an error when the film's tracked file is no longer the one the match
// recorded (Radarr's movie-file id and size; a size of 0 at the match is not compared).
func trackedChanged(lib LibraryFile, m arr.Movie) error {
	switch {
	case m.File == nil:
		return errors.New("the film has no file in Radarr any more since the match: match it again")
	case m.File.ID != lib.FileID:
		return fmt.Errorf("radarr's file for the film changed since the match (movie file %d, now %d): match it again", lib.FileID, m.File.ID)
	case lib.Size > 0 && m.File.Size != lib.Size:
		return fmt.Errorf("radarr's file for the film changed since the match (size %d, now %d): match it again", lib.Size, m.File.Size)
	}
	return nil
}

// ---- the view -----------------------------------------------------------------------------

// viewVersion is Flow2View.Version for a job whose comparison offers a version.
func viewVersion(cfg config.Config, lib LibraryFile, upload store.File, up media.Probe, files []store.File) *VersionOffer {
	v := &VersionOffer{Notices: versionNotices(files)}
	if lib.Probe != nil {
		v.Language = newLanguage(up, *lib.Probe)
	}
	proposed := proposeLabel(cfg.Versions.LabelTemplate, v.Language)
	label, problem := checkLabel(proposed, cfg.Versions)
	v.Label, v.Problem = proposed, problem
	if problem == nil {
		v.Label = label
		if folder, err := filmFolder(path.Dir(lib.AppPath)); err == nil {
			v.Name = versionName(folder, label, strings.ToLower(path.Ext(upload.Path)))
		}
	}
	return v
}

func versionNotices(files []store.File) []string {
	out := []string{NoticeOutsideRadarr, NoticeRegroup, NoticeKeptOnDelete}
	if slices.ContainsFunc(files, func(f store.File) bool { return f.Kind == store.KindSubtitle && f.Status == store.FileOK }) {
		out = append(out, NoticeNoSubtitles)
	}
	return out
}

// versionResult is ResultView.Version for a version job; nil for another.
func (e *Engine) versionResult(ctx context.Context, job store.Job, t store.Target, admin bool) (*VersionResult, error) {
	c, ok := versionOf(t)
	if !ok {
		return nil, nil
	}
	files, err := e.st.Files(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	r := &VersionResult{Name: c.Name, Label: c.Label, Status: store.VersionPlacing, Notices: versionNotices(files)}
	recs, err := e.st.VersionsOf(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	for _, v := range recs {
		if v.Path != c.Path {
			continue
		}
		r.Status = v.Status
		if admin {
			r.Detail = v.Detail
		}
		if !v.GroupedAt.IsZero() {
			r.GroupedAt = ptr(v.GroupedAt.UTC())
		}
		if !v.PrimaryChangedAt.IsZero() {
			r.PrimaryChangedAt = ptr(v.PrimaryChangedAt.UTC())
		}
	}
	return r, nil
}

// ---- the confirmation ---------------------------------------------------------------------

// confirmVersion is the confirmation of a flow 2 job as a second version (see the top of
// this file). s is the job's session, rec its flow 2 record.
func (e *Engine) confirmVersion(ctx context.Context, user, email string, s *session, rec Flow2, req ConfirmRequest) (Confirmation, error) {
	cfg := e.p.cfg
	if len(req.Overrides) > 0 || len(req.Subtitles) > 0 || req.SeasonDub != "" {
		return Confirmation{}, badRequest("A second version takes only flow2 and version: no overrides, subtitles or seasonDub.")
	}
	if rec.Library.Probe == nil {
		return Confirmation{}, refuse(CodeWrongState, "The film's library file is not compared yet.")
	}
	files, err := e.st.Files(ctx, s.job.ID)
	if err != nil {
		return Confirmation{}, err
	}
	f, up, ok := Flow2Upload(files)
	if !ok {
		return Confirmation{}, refuse(CodeVersionUnavailable, "The job has no video to keep as a second version.")
	}
	cmp := CompareProbes(*rec.Library.Probe, up, cfg.Match)
	offers, notOffered := Offers(cmp.Finding, cfg)
	if !slices.Contains(offers, OfferVersion) {
		why := ""
		for _, n := range notOffered {
			if n.Offer == OfferVersion {
				why = n.Reason
			}
		}
		return Confirmation{}, refuse(CodeVersionUnavailable, "Second versions are not offered (%s).", why)
	}
	label := ""
	if req.Version != nil {
		label = req.Version.Label
	}
	if strings.TrimSpace(label) == "" {
		label = proposeLabel(cfg.Versions.LabelTemplate, newLanguage(up, *rec.Library.Probe))
	}
	label, problem := checkLabel(label, cfg.Versions)
	if problem != nil {
		return Confirmation{}, refuse(CodeVersionLabel, "%s", problem.Message)
	}
	if e.p.radarr == nil {
		return Confirmation{}, appFailure(AppRadarr, errors.New("not configured"))
	}

	// Radarr's tracked file must still be the match's: else the comparison was with another
	// file, and the job goes back to probed, like a stale library probe.
	m, err := e.p.radarr.Movie(ctx, s.t.TitleID)
	if err != nil {
		return Confirmation{}, appFailure(AppRadarr, err)
	}
	if why := trackedChanged(rec.Library, m); why != nil {
		if _, err := e.p.backToProbed(ctx, s.job, s.t, why); err != nil {
			return Confirmation{}, err
		}
		return Confirmation{}, refuse(CodeLibraryChanged, "The film's file in Radarr changed since the match, so the comparison no longer holds: the job goes back to the match. Match the film again.")
	}
	folder, err := filmFolder(m.Path)
	if err != nil {
		return Confirmation{}, refuse(CodeVersionUnavailable, "Radarr's folder for the film cannot be used for the version (%v).", err)
	}
	name := versionName(folder, label, strings.ToLower(path.Ext(f.Path)))
	if len(name) > 255 {
		return Confirmation{}, refuse(CodeVersionLabel, "The version's file name would be longer than 255 bytes: choose a shorter label.")
	}
	stem := strings.TrimSuffix(name, path.Ext(name))
	if cfg.Versions.RequireNotUpgradeVerdict {
		parser, ok := e.p.radarr.(arr.NameParser)
		if !ok {
			return Confirmation{}, refuse(CodeVersionUnavailable, "Radarr's verdict on the name cannot be asked (versions.requireNotUpgradeVerdict).")
		}
		parsed, err := parser.Parse(ctx, stem)
		if err != nil {
			return Confirmation{}, appFailure(AppRadarr, err)
		}
		if p := radarrVerdict(parsed, stem); p != nil {
			return Confirmation{}, refuse(CodeVersionLabel, "%s", p.Message)
		}
	}
	dest := filepath.Join(cfg.Versions.Root, folder, name)
	if err := e.checkVersionRoot(ctx); err != nil {
		return Confirmation{}, err
	}
	if _, err := os.Lstat(dest); err == nil {
		return Confirmation{}, refuse(CodeVersionExists, "A file named %q is already in the versions folder: choose another label. Nothing is ever replaced.", name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		e.log.Warn("version_unavailable", "job", s.job.ID, "error", err)
		return Confirmation{}, refuse(CodeStorageUnavailable, "The versions folder cannot be read.")
	}

	choice := versionChoice{
		FileID: f.ID, Label: label, Folder: folder, Name: name, Path: dest,
		LibraryFileID: rec.Library.FileID, LibrarySize: rec.Library.Size,
	}
	opts, err := withOption(s.t.Options, "version", choice)
	if err != nil {
		return Confirmation{}, err
	}
	audit := []store.AuditEntry{{Actor: user, Email: email, Action: "version.confirmed", Detail: map[string]string{
		"app": s.t.App, "titleId": strconv.Itoa(s.t.TitleID), "title": s.t.Title,
		"label": label, "name": name, "file": path.Base(f.Path), "finding": cmp.Finding,
		"libraryFileId": strconv.Itoa(rec.Library.FileID),
	}}}
	note := fmt.Sprintf("second version confirmed for %s: %s (%s)", s.t.App, name, cmp.Finding)

	defer e.locks.Lock(s.job.ID)()
	next, err := e.st.ConfirmImport(ctx, s.job.ID, s.t, nil, opts, audit, store.Change{Actor: user, Note: note})
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrInvalidTransition) || errors.Is(err, store.ErrNotFound) {
		return Confirmation{}, wrongState()
	}
	if err != nil {
		return Confirmation{}, err
	}
	e.log.Info("job_state", "job", s.job.ID, "from", store.AwaitingConfirmation, "to", next.State, "attempt", next.Attempt, "version", name)
	t := s.t
	t.Options = opts
	return Confirmation{Job: next, Target: t}, nil
}

// checkVersionRoot checks versions.root before a version is confirmed: the sentinels, a
// folder on the intake root's filesystem (the version is a hard link), writable, and
// outside every Radarr root folder (as paths.mappings.radarr maps them).
func (e *Engine) checkVersionRoot(ctx context.Context) error {
	cfg := e.p.cfg
	root := cfg.Versions.Root
	if root == "" {
		return refuse(CodeVersionUnavailable, "versions.root is not set.")
	}
	if err := e.checkVersionStorage(); err != nil {
		e.log.Warn("version_storage_unavailable", "error", err)
		return refuse(CodeStorageUnavailable, "The versions folder is not available: a mount is missing, it is not writable, or it is on another filesystem than the intake folder.")
	}
	roots, err := e.p.radarr.RootFolders(ctx)
	if err != nil {
		return appFailure(AppRadarr, err)
	}
	for _, r := range roots {
		mine := filepath.Clean(pathmap.From(cfg.Paths.Mappings.Radarr, r.Path))
		if filepath.IsAbs(mine) && config.Overlaps(root, mine) {
			return refuse(CodeVersionUnavailable, "versions.root overlaps Radarr's root folder %s: Radarr would take the version for one of the film's files (decision #20).", r.Path)
		}
	}
	return nil
}

// versionStorage is the filesystem check of versions.root (checkVersionStorage).
func versionStorage(cfg config.Config) error {
	if err := fscheck.Sentinels(cfg.Paths); err != nil {
		return err
	}
	if err := fscheck.Writable(cfg.Versions.Root); err != nil {
		return err
	}
	return fscheck.SameDevice(cfg.Paths.IntakeRoot, cfg.Versions.Root)
}

// ---- the placement ------------------------------------------------------------------------

// placeVersion is the importing step of a version job (see the top of this file).
func (e *Engine) placeVersion(ctx context.Context, job store.Job, t store.Target, c versionChoice) error {
	if !e.due(job.ID) {
		return nil
	}
	cfg := e.p.cfg
	files, err := e.st.Files(ctx, job.ID)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(files, func(f store.File) bool { return f.ID == c.FileID })
	if i < 0 || files[i].Kind != store.KindVideo || files[i].Status != store.FileOK || !strings.HasPrefix(files[i].Path, jobdir.Files+"/") {
		return e.failVersion(ctx, job, t, CodeVersionFailed, "the job's video is no longer there")
	}
	src := filepath.Join(jobdir.Dir(cfg.Paths.IntakeRoot, job.ID), filepath.FromSlash(files[i].Path))
	if c.Path != filepath.Join(cfg.Versions.Root, c.Folder, c.Name) {
		return e.failVersion(ctx, job, t, CodeVersionFailed, fmt.Sprintf("versions.root changed since the confirmation (the version was to be %s)", c.Path))
	}
	rec, err := e.st.VersionByPath(ctx, c.Path)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	placed, err := samePlaced(src, c.Path)
	if err != nil {
		return e.failVersion(ctx, job, t, CodeVersionFailed, err.Error())
	}
	linked := false
	if !placed {
		if _, err := os.Lstat(c.Path); err == nil {
			return e.failVersion(ctx, job, t, CodeVersionExists, fmt.Sprintf("%s exists: nothing is replaced", c.Path))
		}
		// Before anything is written: Radarr's tracked file must still be the match's.
		if e.p.radarr == nil {
			return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: "radarr is not configured"})
		}
		m, err := e.p.radarr.Movie(ctx, t.TitleID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if Unavailable(err) {
				return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: "radarr (movie): " + err.Error()})
			}
			return e.failVersion(ctx, job, t, CodeVersionFailed, "radarr (movie): "+err.Error())
		}
		if why := trackedChanged(LibraryFile{FileID: c.LibraryFileID, Size: c.LibrarySize}, m); why != nil {
			e.forget(job.ID)
			_, err := e.p.backToProbed(ctx, job, t, why)
			return err
		}
		// The record first, so that the version keeper knows of the file once it exists.
		rec = versionRecord(rec, job, t, c, store.VersionPlacing)
		if rec, err = e.st.PutVersion(ctx, rec); err != nil {
			return err
		}
		if err := linkVersion(cfg.Versions.Root, src, c.Folder, c.Name); err != nil {
			code := CodeVersionFailed
			switch {
			case errors.Is(err, fs.ErrExist):
				code = CodeVersionExists
			case crossDevice(err):
				err = fmt.Errorf("%w: versions.root must be on the intake root's filesystem", err)
			}
			return e.failVersion(ctx, job, t, code, fmt.Sprintf("place %s: %v", c.Path, err))
		}
		linked = true
	}
	if rec.ID == 0 || rec.JobID != job.ID || rec.Status == store.VersionPlacing || rec.Status == "" {
		was := rec.Status
		rec = versionRecord(rec, job, t, c, store.VersionLinked)
		if rec, err = e.st.PutVersion(ctx, rec); err != nil {
			return err
		}
		linked = linked || was == store.VersionPlacing
	}
	if linked {
		if err := e.st.Audit(ctx, store.AuditEntry{Actor: actor, Action: "version.linked", JobID: job.ID, Detail: map[string]string{
			"app": t.App, "titleId": strconv.Itoa(t.TitleID), "title": t.Title,
			"path": c.Path, "label": c.Label, "file": path.Base(files[i].Path),
		}}); err != nil {
			return err
		}
		e.log.Info("version_linked", "job", job.ID, "path", c.Path)
	}
	e.forget(job.ID)
	return e.p.move(ctx, job, &t, store.Imported, store.Change{Note: "second version placed: " + c.Name})
}

// versionRecord is rec (zero: a new one) for the job's choice, with status.
func versionRecord(rec store.Version, job store.Job, t store.Target, c versionChoice, status string) store.Version {
	rec.JobID, rec.App, rec.TitleID, rec.Title, rec.Year = job.ID, t.App, t.TitleID, t.Title, t.Year
	rec.Path, rec.Label, rec.Status, rec.Detail = c.Path, c.Label, status, ""
	return rec
}

func (e *Engine) failVersion(ctx context.Context, job store.Job, t store.Target, code, text string) error {
	e.forget(job.ID)
	return e.p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: code, ErrorText: "second version: " + text})
}

// samePlaced reports whether dest is already the job's file (the same inode): placed by an
// earlier pass.
func samePlaced(src, dest string) (bool, error) {
	d, err := os.Lstat(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(s, d), nil
}

// linkVersion hard-links src as root/folder/name. The folder is created inside root
// (os.Root: no "..", no link out of it) when it is missing and must be a real folder; the
// link never replaces a file (fs.ErrExist).
func linkVersion(root, src, folder, name string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if err := r.Mkdir(folder, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, err := r.Lstat(folder)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s in versions.root is not a folder", folder)
	}
	dir, err := r.Open(folder)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	sd, err := os.Open(filepath.Dir(src))
	if err != nil {
		return err
	}
	defer func() { _ = sd.Close() }()
	return linkAt(sd, filepath.Base(src), dir, name)
}
