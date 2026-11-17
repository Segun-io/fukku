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
//   - The job must be a Radarr flow 2 job, its library file probed, and "version" among the
//     offers (versions.enabled, and a media server that can group versions); else
//     version_unavailable.
//   - The label: the request's, or the one versions.labelTemplate proposes from the upload's
//     new audio language. It must be one path element without the characters Windows and
//     Samba refuse and without invisible format characters (Unicode Cf), at most 64 bytes,
//     contain none of versions.forbiddenLabelTokens as a word, and not end in one of the
//     words Jellyfin takes for an extra (B4); the file name at most 255 bytes. With
//     versions.requireNotUpgradeVerdict, Radarr's parse of "<film folder> - <label>" must
//     read nothing that ranks a release beyond what it reads in "<film folder>" alone, and
//     a name it cannot parse at all is refused (no verdict). Else version_label.
//   - Radarr's tracked file must still be the one of the match (movie.movieFile's id and
//     size). If it changed, the job goes back to probed, as a stale library probe does
//     (rescan), and the confirmation is refused with library_changed.
//   - versions.root must be a folder on the same mount as the intake root (a trial link),
//     writable, outside every Radarr root folder (as paths.mappings.radarr maps them,
//     symbolic links resolved) and every paths.libraryRoots entry (version_unavailable or
//     storage_unavailable), and the version's name must be free on disk and unused by any
//     earlier version record (version_exists).
//   - Then one transaction: the choice into the target's options ("version"), the audit
//     entry version.confirmed, and the move to importing.
//
// The importing step places the file (placeVersion), under the version's lock
// (VersionLock, shared with the keeper): a record of the name from another job fails it
// (version_exists); Radarr's tracked file is checked again before anything is written
// (stale: back to probed); the record is stored (store.Version, status placing), then the
// job's file is hard-linked into place with linkat(2), which never replaces a file (a
// failed link removes the record again); then the record becomes linked together with
// version.linked, in one write, and the job moves to imported. A repeat after a crash finds
// the link (same inode) and the record still placing, and goes on. The imported
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
	LabelUnchecked = "label_unchecked" // Radarr cannot parse the name at all, so it cannot be checked
)

// maxLabelBytes is the longest label; maxNameBytes the longest file name (most filesystems').
const (
	maxLabelBytes = 64
	maxNameBytes  = 255
)

func nameTooLong() *LabelProblem {
	return &LabelProblem{Reason: LabelTooLong, Message: fmt.Sprintf("The version's file name would be longer than %d bytes: choose a shorter label.", maxNameBytes)}
}

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

// refuseLabel is the refusal version_label for a label problem: its message, reason and
// token, so that the UI can say why in the user's language.
func refuseLabel(p *LabelProblem) *Error {
	err := refuse(CodeVersionLabel, "%s", p.Message)
	err.Reason, err.Token = p.Reason, p.Token
	return err
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
	TMDBID        int    `json:"tmdbId,omitempty"`
}

// VersionLock is the key of a version's lock in the shared JobLocks: the placement and the
// version keeper (package mediaserver) take it around their reads and writes of the record.
func VersionLock(path string) string { return "version:" + path }

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
	// Invisible format characters (zero-width space, soft hyphen, joiners: Unicode Cf) would
	// make two names that look the same differ, or hide a word from the token check.
	if strings.ContainsFunc(clean, func(r rune) bool { return unicode.Is(unicode.Cf, r) }) {
		return "", &LabelProblem{Reason: LabelInvalid, Message: "The label must not contain invisible characters (zero-width spaces, soft hyphens and the like)."}
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

// readsQuality reports whether a parse reads anything that ranks a release: a quality, a
// source, a resolution, or a proper, real or repack revision.
func readsQuality(p arr.Parsed) bool {
	src := strings.ToLower(p.Source)
	return p.Quality.ID != 0 || (src != "" && src != "unknown") || p.Resolution != 0 || p.Revision > 1 || p.Real > 0 || p.Repack
}

// sameReading reports whether two parses read the same quality and revision.
func sameReading(a, b arr.Parsed) bool {
	return a.Quality.ID == b.Quality.ID && strings.EqualFold(a.Source, b.Source) && a.Resolution == b.Resolution &&
		a.Revision == b.Revision && a.Real == b.Real && a.Repack == b.Repack
}

// radarrVerdict is the problem Radarr's parse of a version's name (without its extension)
// shows, against its parse of the film folder's name alone (base). The label must add
// nothing that ranks a release: a parse that reads a quality, a source, a resolution, or a
// proper, real or repack revision is refused, unless the folder name alone reads the same
// (a film whose own title reads as a quality, such as "Cam (2018)"). A name Radarr cannot
// parse at all cannot be checked, and is refused too: versions.requireNotUpgradeVerdict asks
// for Radarr's verdict, and there is none. nil: the label is fine.
func radarrVerdict(p, base arr.Parsed, stem string) *LabelProblem {
	if !p.OK {
		return &LabelProblem{Reason: LabelUnchecked, Message: fmt.Sprintf(
			"Radarr cannot read the name %q, so the label cannot be checked: try another label, or turn versions.requireNotUpgradeVerdict off.", stem)}
	}
	if !readsQuality(p) || (base.OK && sameReading(p, base)) {
		return nil
	}
	what := p.Quality.Name
	switch {
	case p.Revision > 1 || p.Real > 0 || p.Repack:
		what = "a proper, real or repack"
	case what == "" || p.Quality.ID == 0:
		what = strings.TrimSpace(p.Source + " " + strconv.Itoa(p.Resolution) + "p")
	}
	return &LabelProblem{Reason: LabelQuality, Token: what, Message: fmt.Sprintf(
		"Radarr reads %s in %q: choose a label without a quality or source word (versions.requireNotUpgradeVerdict).", what, stem)}
}

// filmFolder is the name of Radarr's folder for the film (movie.path as Radarr sees it), as
// a version's folder and name prefix.
func filmFolder(appPath string) (string, error) { return titleFolder(AppRadarr, appPath) }

// trackedChanged is an error when the film's tracked file is no longer the one the match
// recorded: Radarr's movie-file id, size (a size of 0 at the match is not compared) and
// path (one not recorded at the match is not compared).
func trackedChanged(lib LibraryFile, m arr.Movie) error {
	switch {
	case m.File == nil:
		return errors.New("the film has no file in Radarr any more since the match: match it again")
	case m.File.ID != lib.FileID:
		return fmt.Errorf("radarr's file for the film changed since the match (movie file %d, now %d): match it again", lib.FileID, m.File.ID)
	case lib.Size > 0 && m.File.Size != lib.Size:
		return fmt.Errorf("radarr's file for the film changed since the match (size %d, now %d): match it again", lib.Size, m.File.Size)
	case lib.AppPath != "" && m.File.Path != lib.AppPath:
		// The same record under another name (Radarr renamed the film): the probe, the clips
		// and the merge would read a path that is no longer the file.
		return fmt.Errorf("radarr's file for the film moved since the match (%q, now %q): match it again", lib.AppPath, m.File.Path)
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
			if len(v.Name) > maxNameBytes {
				v.Problem, v.Name = nameTooLong(), ""
			}
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
	if s.t.App != AppRadarr {
		return Confirmation{}, refuse(CodeVersionUnavailable, "Second versions of films are made for Radarr's films only (episodes: the season-dub policy version).")
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
		return Confirmation{}, refuseLabel(problem)
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
	if len(name) > maxNameBytes {
		return Confirmation{}, refuseLabel(nameTooLong())
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
		base, err := parser.Parse(ctx, folder) // what the film's own name reads as
		if err != nil {
			return Confirmation{}, appFailure(AppRadarr, err)
		}
		if p := radarrVerdict(parsed, base, stem); p != nil {
			return Confirmation{}, refuseLabel(p)
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
	// A record of that name belongs to an earlier job (its file may be gone): it is never
	// handed to another one.
	if old, err := e.st.VersionByPath(ctx, dest); err == nil {
		return Confirmation{}, refuse(CodeVersionExists, "A version named %q was placed before (status %s): choose another label.", name, old.Status)
	} else if !errors.Is(err, store.ErrNotFound) {
		return Confirmation{}, err
	}

	choice := versionChoice{
		FileID: f.ID, Label: label, Folder: folder, Name: name, Path: dest,
		LibraryFileID: rec.Library.FileID, LibrarySize: rec.Library.Size, TMDBID: m.TMDBID,
	}
	opts, err := withOption(s.t.Options, "version", choice)
	if err != nil {
		return Confirmation{}, err
	}
	// A merge refusal (#95) is not this job's any more.
	if opts, err = withOption(opts, "mergeRefusal", nil); err != nil {
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
		return refuse(CodeStorageUnavailable, "The versions folder is not available: a mount is missing, it is not writable, or it is not on the same mount as the intake folder.")
	}
	roots, err := e.p.radarr.RootFolders(ctx)
	if err != nil {
		return appFailure(AppRadarr, err)
	}
	for _, r := range roots {
		mine := filepath.Clean(pathmap.From(cfg.Paths.Mappings.Radarr, r.Path))
		if filepath.IsAbs(mine) && config.OverlapsOnDisk(root, mine) {
			return refuse(CodeVersionUnavailable, "versions.root overlaps Radarr's root folder %s: Radarr would take the version for one of the film's files (decision #20).", r.Path)
		}
	}
	return nil
}

// versionStorage is the filesystem check of versions.root (checkVersionStorage): the
// sentinels, then a trial hard link from the intake root into versions.root, which also
// proves it writable. A trial, not a comparison of device ids: two bind mounts of one
// filesystem have the same st_dev, yet a link between them fails with EXDEV.
func versionStorage(cfg config.Config) error {
	if err := fscheck.Sentinels(cfg.Paths); err != nil {
		return err
	}
	return fscheck.CanLink(cfg.Paths.IntakeRoot, cfg.Versions.Root)
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
	// The version's lock, shared with the version keeper: neither reads the record while the
	// other is writing it. (It is never held with the job's lock taken first.)
	defer e.locks.Lock(VersionLock(c.Path))()
	rec, err := e.st.VersionByPath(ctx, c.Path)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if rec.ID != 0 && rec.JobID != job.ID {
		// An earlier job's version of that name (its file may be gone): never taken over.
		return e.failVersion(ctx, job, t, CodeVersionExists, fmt.Sprintf("%s is recorded for job %s (status %s): nothing is taken over", c.Path, rec.JobID, rec.Status))
	}
	placed, err := samePlaced(src, c.Path)
	if err != nil {
		return e.failVersion(ctx, job, t, CodeVersionFailed, err.Error())
	}
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
			if rec.ID != 0 && rec.Status == store.VersionPlacing { // an earlier pass's, before its link
				if err := e.st.DeleteVersion(ctx, rec.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
			}
			_, err := e.p.backToProbed(ctx, job, t, why)
			return err
		}
		// The record first, so that the version keeper knows of the file once it exists.
		if rec, err = e.st.PutVersion(ctx, versionRecord(rec, job, t, c, store.VersionPlacing)); err != nil {
			return err
		}
		if err := linkVersion(cfg.Versions.Root, src, c.Folder, c.Name); err != nil {
			// Nothing was placed: the record of the attempt goes, so that it never stands
			// for a file that does not exist.
			if derr := e.st.DeleteVersion(ctx, rec.ID); derr != nil && !errors.Is(derr, store.ErrNotFound) {
				return derr
			}
			code := CodeVersionFailed
			switch {
			case errors.Is(err, fs.ErrExist):
				code = CodeVersionExists
			case crossDevice(err):
				err = fmt.Errorf("%w: versions.root must be on the same mount as the intake root", err)
			}
			return e.failVersion(ctx, job, t, code, fmt.Sprintf("place %s: %v", c.Path, err))
		}
	}
	// The record says linked, with its audit entry in the same write; a repeat after a crash
	// before it finds the record still placing and writes both then.
	if rec.ID == 0 || rec.Status == store.VersionPlacing || rec.Status == "" {
		entry := store.AuditEntry{Actor: actor, Action: "version.linked", JobID: job.ID, Detail: map[string]string{
			"app": t.App, "titleId": strconv.Itoa(t.TitleID), "title": t.Title,
			"path": c.Path, "label": c.Label, "file": path.Base(files[i].Path),
		}}
		if _, err = e.st.PutVersion(ctx, versionRecord(rec, job, t, c, store.VersionLinked), entry); err != nil {
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
	if c.TMDBID != 0 {
		rec.TMDBID = c.TMDBID
	}
	rec.Path, rec.Label, rec.Status, rec.Detail = c.Path, c.Label, status, ""
	return rec
}

func (e *Engine) failVersion(ctx context.Context, job store.Job, t store.Target, code, text string) error {
	e.forget(job.ID)
	return e.p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: code, ErrorText: "second version: " + text})
}

// SettlePlacing settles the record of a placement whose job ended (done or cancelled) or
// is gone while the record still says placing: a crash between the record and its link,
// then the job was cancelled. Call it under the version's lock (VersionLock) with the record
// as read under it. When the version's file is the job's upload (the same inode), the
// record becomes linked, with version.linked, and linked is true; otherwise (no file, a
// file that is not the upload, or an upload that is gone, so nothing proves the file is
// the job's) the record is removed, so that the name is not refused for ever. A record
// whose job is still going is left to it (settled false).
func SettlePlacing(ctx context.Context, st store.Store, intakeRoot string, rec store.Version) (settled, linked bool, err error) {
	if rec.Status != store.VersionPlacing {
		return false, false, nil
	}
	job, err := st.Job(ctx, rec.JobID)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return false, false, err
	case job.State != store.Done && job.State != store.Cancelled:
		return false, false, nil
	}
	if src, ok := uploadOf(ctx, st, intakeRoot, rec); ok {
		if same, err := samePlaced(src, rec.Path); err == nil && same {
			rec.Status, rec.Detail = store.VersionLinked, ""
			_, err := st.PutVersion(ctx, rec, store.AuditEntry{Actor: actor, Action: "version.linked", JobID: rec.JobID,
				Detail: map[string]string{"path": rec.Path, "label": rec.Label, "titleId": strconv.Itoa(rec.TitleID), "note": "settled after the job ended"}})
			return err == nil, err == nil, err
		}
	}
	if err := st.DeleteVersion(ctx, rec.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, false, err
	}
	return true, false, nil
}

// uploadOf is the job's upload that the version's record was placed from, as Fukku sees it.
func uploadOf(ctx context.Context, st store.Store, intakeRoot string, rec store.Version) (string, bool) {
	t, err := st.Target(ctx, rec.JobID)
	if err != nil {
		return "", false
	}
	var fileID int64
	if c, ok := versionOf(t); ok && c.Path == rec.Path {
		fileID = c.FileID
	}
	if c, ok := episodeVersionsOf(t); ok { // an episode's version (#58)
		for _, v := range c.Versions {
			if v.Path == rec.Path {
				fileID = v.FileID
			}
		}
	}
	if fileID == 0 {
		return "", false
	}
	files, err := st.Files(ctx, rec.JobID)
	if err != nil {
		return "", false
	}
	for _, f := range files {
		if f.ID == fileID && strings.HasPrefix(f.Path, jobdir.Files+"/") {
			return filepath.Join(jobdir.Dir(intakeRoot, rec.JobID), filepath.FromSlash(f.Path)), true
		}
	}
	return "", false
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
