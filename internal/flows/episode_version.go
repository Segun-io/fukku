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
	"sync"
	"time"

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

// Second versions of episodes: the season-dub policy version (#58, decision #20). A dub of a
// season already in Sonarr is kept next to each episode's own file instead of replacing it,
// in Sonarr's season folder:
//
//	<season folder>/<series folder> - S01E02 - <label><ext>
//
// Jellyfin 12.4 groups the two as versions of one episode by the strictly parsed SxxEyy,
// whatever follows it (probe B2), so no merge call is needed and the grouping survives
// Sonarr's renames and upgrades of the episode's own file. Sonarr does not track the file:
// on each rescan it judges it against the episode's file, and it adopts it only when it is
// an upgrade (probe B3-sonarr: a label file is rejected as "not an upgrade", a file named
// like a better release is adopted). So the decision's rule holds for episodes: before a
// version is linked, Sonarr's verdict on the file under its library name must be "not an
// upgrade" (versions.requireNotUpgradeVerdict). The verdict is asked of the file itself: the
// upload is hard-linked under the version's name into the job's folder
// (jobs/<job>/versions/<row>/<name>), which Sonarr can see, and Sonarr's Manual Import
// proposal for it, re-checked for the episode, must carry a "not an upgrade" rejection
// (the same check its rescan runs). A file whose name would win on Sonarr's rescan is
// refused.
//
// The policy is offered when versions.enabled is on, the media server is Jellyfin, and its
// version is 12.4 or later (older ones do not group episode versions, B2). The confirmation
// (POST /api/jobs/{id}/confirm with {"seasonDub": "version", "version": {"label": "Español"}},
// the label optional) checks, for every row whose episode has a file:
//
//   - the row is one episode, whose file holds no other episode (double_episode,
//     shared_file); the episode's folder, as paths.mappings.sonarr maps it, is inside
//     paths.libraryRoots (outside_library); the version's name is free on disk and unused by
//     any record (version_exists): else version_unavailable or version_exists, with the rows;
//   - the label, as for films (checkLabel), and no season or episode number in it
//     (label_episode): else version_label;
//   - each season folder takes a hard link from the intake root (a trial, fscheck.CanLink),
//     and the sentinels are there: else storage_unavailable;
//   - Sonarr's verdict (above): else version_upgrade, with the rows. Use replace, or skip
//     those rows.
//
// The rows without a file import as usual. The importing step places the versions first,
// each under its lock (VersionLock, as films'): an earlier job's record of the name fails
// the job (version_exists); the episode must still have its file in the same folder, and
// Sonarr's verdict is asked again right before the link; then the record (store.Version,
// App sonarr, with the episode), the hard link (linkat(2), which never replaces a file), and
// the record linked with version.linked. A repeat after a crash finds its own link (the
// same inode). Then the import of the other rows, if any. Sonarr's monitoring of the
// episodes kept as versions is left alone (the versions are not Sonarr's files). The
// post-processing adds the step mediaServerEpisodeVersions (package mediaserver), which
// refreshes the media server and records each version as grouped once Jellyfin lists both
// files as one episode. The job's subtitle files for those episodes are not handed over:
// the subtitle manager knows only Sonarr's file.

// Codes of episodes' versions.
const (
	// CodeVersionUpgrade refuses a confirmation, or a row at the placement, when Sonarr would
	// take an episode's version for the episode's own file on its next rescan: it does not
	// reject it as "not an upgrade". Rows lists the rows.
	CodeVersionUpgrade = "version_upgrade"
	// CodeVersionUnchecked refuses a confirmation, or a row at the placement, when Sonarr gives
	// no verdict on the version: it does not offer the file, or reads it as another episode.
	// Rows lists the rows; the message says why for each.
	CodeVersionUnchecked = "version_unchecked"
)

// Why the season-dub policy version is not offered (SeasonDub.NotOffered), besides
// versions_disabled and versions_unsupported.
const (
	// ReasonMediaServerTooOld: the media server is a Jellyfin older than 12.4, which does not
	// group episode versions (probe B2).
	ReasonMediaServerTooOld = "media_server_too_old"
	// ReasonMediaServerUnknown: the media server's version could not be read.
	ReasonMediaServerUnknown = "media_server_unknown"
	// ReasonSeriesFolderUnusable: Sonarr's folder name for the series cannot be part of a file
	// name ("What If...?" ends in a dot and holds a "?"), so no version can be named.
	ReasonSeriesFolderUnusable = "series_folder_unusable"
	// ReasonSeasonFolderUnavailable: a season folder of the job's rows does not take a hard
	// link from the intake root (missing, not writable by Fukku, another mount), or a
	// paths.sentinels file is missing.
	ReasonSeasonFolderUnavailable = "season_folder_unavailable"
)

// LabelEpisode is a label problem of episodes' versions: the label holds a season and
// episode number (S02E05, 2x05), which the media server or Sonarr could read instead of the
// version's own.
const LabelEpisode = "label_episode"

// Why a row cannot be kept as an episode's version (EpisodeVersionRow.Problem).
const (
	EpisodeDouble         = "double_episode"  // the row is several episodes
	EpisodeSharedFile     = "shared_file"     // the episode's file also holds other episodes
	EpisodeNoFile         = "no_file"         // Sonarr lists no file for the episode
	EpisodeOutsideLibrary = "outside_library" // the episode's folder is not under paths.libraryRoots
	EpisodeNameTooLong    = "name_too_long"   // the version's name would be over 255 bytes
	EpisodeVersionExists  = "version_exists"  // a file or a record of the version's name exists
	// EpisodeNameUnreadable: the version's name does not read as exactly its episode (the
	// series' folder name holds an SxxEyy, say).
	EpisodeNameUnreadable = "name_unreadable"
	// EpisodeFolderUnreadable: the episode's folder cannot be read (not "no such file").
	EpisodeFolderUnreadable = "folder_unreadable"
)

// Notices of episodes' versions (EpisodeVersionsView.Notices, EpisodeVersionsResult.Notices).
const (
	// NoticeUntrackedBySonarr: the version is next to the episode's file, but Sonarr does not
	// track it. It never renames it, and an upgrade of the episode replaces only Sonarr's own
	// file; deleting the series with its files deletes the version too.
	NoticeUntrackedBySonarr = "untracked_by_sonarr"
	// NoticeAdoptedWithoutFile: if the episode's own file is deleted, Sonarr takes the version
	// for the episode's file on its next rescan.
	NoticeAdoptedWithoutFile = "adopted_without_episode_file"
)

// minEpisodeGrouping is the first Jellyfin that groups episode versions by SxxEyy (B2).
var minEpisodeGrouping = [2]int{12, 4}

// EpisodeVersionsView is SeasonDub.Version: what keeping the dub as second versions would
// be, while "version" is among the offers. Label is the proposed label ("" when none can be
// proposed), Problem why it cannot be used as it is. Rows are the rows whose episode has a
// file, each with the version's name under the proposed label or why it cannot be one.
type EpisodeVersionsView struct {
	Label    string              `json:"label"`
	Language string              `json:"language"`
	Problem  *LabelProblem       `json:"problem"`
	Rows     []EpisodeVersionRow `json:"rows"`
	Notices  []string            `json:"notices"`
}

// EpisodeVersionRow is one row of EpisodeVersionsView.
type EpisodeVersionRow struct {
	Row           string `json:"row"`
	EpisodeID     int    `json:"episodeId"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	Name          string `json:"name"`    // "" while the label or the row has a problem
	Problem       string `json:"problem"` // "" or one of EpisodeDouble …
}

// EpisodeVersionsResult is ResultView.EpisodeVersions: the versions the job placed. Status
// is the record's (store.VersionPlacing … ); Detail is for admins only.
type EpisodeVersionsResult struct {
	Label    string                 `json:"label"`
	Versions []EpisodeVersionResult `json:"versions"`
	Notices  []string               `json:"notices"`
}

// EpisodeVersionResult is one version of EpisodeVersionsResult. Status refused (Refusal: the
// code, version_upgrade, version_unchecked, version_exists or version_failed; Detail the
// reason, for admins) when the placement did not place it.
type EpisodeVersionResult struct {
	Row           string     `json:"row"`
	SeasonNumber  int        `json:"seasonNumber"`
	EpisodeNumber int        `json:"episodeNumber"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	Refusal       string     `json:"refusal"`
	Detail        string     `json:"detail"`
	GroupedAt     *time.Time `json:"groupedAt"`
}

// VersionRefused is EpisodeVersionResult.Status of a version the placement refused.
const VersionRefused = "refused"

// episodeVersions is what the confirmation stores in the target's options
// ("episodeVersions") for the importing step.
type episodeVersions struct {
	Label    string           `json:"label"`
	Versions []episodeVersion `json:"versions"`
}

// episodeVersion is one version to place: the row and its job file, the episode, Sonarr's
// file of the episode at the confirmation, the season folder as Fukku sees it, the name,
// and Path, Folder/Name.
type episodeVersion struct {
	Row           string `json:"row"`
	FileID        int64  `json:"fileId"`
	EpisodeID     int    `json:"episodeId"`
	Season        int    `json:"season"`
	Episode       int    `json:"episode"`
	EpisodeFileID int    `json:"episodeFileId"`
	Folder        string `json:"folder"`
	Name          string `json:"name"`
	Path          string `json:"path"`
}

func episodeVersionsOf(t store.Target) (episodeVersions, bool) {
	var opts struct {
		EpisodeVersions *episodeVersions `json:"episodeVersions"`
	}
	if len(t.Options) > 0 {
		_ = json.Unmarshal(t.Options, &opts)
	}
	if opts.EpisodeVersions == nil || len(opts.EpisodeVersions.Versions) == 0 {
		return episodeVersions{}, false
	}
	return *opts.EpisodeVersions, true
}

// HasEpisodeVersions reports whether the job of target t keeps some episodes as second
// versions (it was confirmed with the season-dub policy version).
func HasEpisodeVersions(t store.Target) bool {
	_, ok := episodeVersionsOf(t)
	return ok
}

// EpisodeVersionPaths are the paths (as Fukku sees them) of the episodes' versions the job
// of target t places; nil for any other job.
func EpisodeVersionPaths(t store.Target) []string {
	c, _ := episodeVersionsOf(t)
	var out []string
	for _, v := range c.Versions {
		out = append(out, v.Path)
	}
	return out
}

// EpisodeVersionRows are the rows the job of target t keeps as second versions of their
// episodes; nil for any other job. Their subtitle files are not handed over.
func EpisodeVersionRows(t store.Target) []string {
	c, _ := episodeVersionsOf(t)
	var out []string
	for _, v := range c.Versions {
		out = append(out, v.Row)
	}
	return out
}

// ---- whether it is offered ----------------------------------------------------------------

// serverVersion caches the media server's version: asked once per serverVersionTTL (an
// answer) or serverVersionRetry (a failure), so the proposal does not ask on every view.
type serverVersion struct {
	mu  sync.Mutex
	f   func(context.Context) (string, error)
	at  time.Time
	v   string
	err error
}

const (
	serverVersionTTL     = 5 * time.Minute
	serverVersionRetry   = 30 * time.Second
	serverVersionTimeout = 5 * time.Second
)

// SetMediaServerVersion gives the proposal the media server's version call
// (mediaserver.MediaServer.Version), which decides whether episodes' versions are offered
// (Jellyfin 12.4 or later, B2). Without it they are not. Call it before the server starts.
func (p *Proposals) SetMediaServerVersion(f func(context.Context) (string, error)) {
	p.msVersion = &serverVersion{f: f}
}

func (sv *serverVersion) get(ctx context.Context, now time.Time) (string, error) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if !sv.at.IsZero() {
		ttl := serverVersionTTL
		if sv.err != nil {
			ttl = serverVersionRetry
		}
		if now.Sub(sv.at) < ttl {
			return sv.v, sv.err
		}
	}
	cctx, cancel := context.WithTimeout(ctx, serverVersionTimeout)
	defer cancel()
	sv.v, sv.err = sv.f(cctx)
	sv.at = now
	return sv.v, sv.err
}

// atLeast reports whether version v ("12.4.0") is want or later; ok is false when v does
// not start with two numbers.
func atLeast(v string, want [2]int) (yes, ok bool) {
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3)
	if len(parts) < 2 {
		return false, false
	}
	major, err1 := strconv.Atoi(parts[0])
	digits := parts[1]
	if i := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		digits = digits[:i] // "4-rc1"
	}
	minor, err2 := strconv.Atoi(digits)
	if err1 != nil || err2 != nil {
		return false, false
	}
	return major > want[0] || (major == want[0] && minor >= want[1]), true
}

// episodeVersionsNotOffered is why episodes' versions are not offered; nil: they are.
func (p *Proposals) episodeVersionsNotOffered(ctx context.Context) *NotOffered {
	no := func(reason string) *NotOffered { return &NotOffered{Offer: PolicyVersion, Reason: reason} }
	switch {
	case !p.cfg.Versions.Enabled:
		return no(ReasonVersionsDisabled)
	case !canGroup(p.cfg) || p.msVersion == nil:
		return no(ReasonVersionsUnsupported)
	}
	v, err := p.msVersion.get(ctx, p.now())
	if err != nil {
		p.log.Warn("media_server_version", "error", err)
		return no(ReasonMediaServerUnknown)
	}
	yes, ok := atLeast(v, minEpisodeGrouping)
	switch {
	case !ok:
		return no(ReasonMediaServerUnknown)
	case !yes:
		return no(ReasonMediaServerTooOld)
	}
	return nil
}

// checkEpisodeVersions settles, for the session, whether episodes' versions are offered
// (offers reads it). Without it they are not. Beyond the settings and the media server's
// version, the job's rows that have a file must be placeable at all: Sonarr's folder name for
// the series must be usable in a file name (else series_folder_unusable), and each season
// folder must take a hard link from the intake root (the trial of the confirmation, cached
// for a minute; else season_folder_unavailable). A problem is never an error of the view: the
// policy is not offered, and notOffered says why. The error is a call to Sonarr that failed.
func (s *session) checkEpisodeVersions(ctx context.Context) error {
	if !s.show() {
		return nil
	}
	s.epvChecked, s.epvNot, s.epvLib = true, s.p.episodeVersionsNotOffered(ctx), nil
	if s.epvNot != nil {
		return nil
	}
	x := s.existing()
	if len(x.rows) == 0 {
		return nil
	}
	lib, why, err := s.loadEpvLibrary(ctx)
	if err != nil {
		return err
	}
	if why != "" {
		s.epvNot = &NotOffered{Offer: PolicyVersion, Reason: why}
		return nil
	}
	s.epvLib = &lib
	seen := map[string]bool{}
	for _, id := range x.rows {
		v, problem, err := s.episodeVersionOf(ctx, lib, id, "")
		if err != nil {
			return err
		}
		if problem != "" || v.Folder == "" || seen[v.Folder] {
			continue
		}
		seen[v.Folder] = true
		if err := s.p.folderLinks(v.Folder); err != nil {
			s.p.log.Warn("version_storage_unavailable", "job", s.job.ID, "folder", v.Folder, "error", err)
			s.epvNot = &NotOffered{Offer: PolicyVersion, Reason: ReasonSeasonFolderUnavailable}
			return nil
		}
	}
	return nil
}

// linkTrials caches the trial links of season folders (Proposals.folderLinks).
type linkTrials struct {
	mu    sync.Mutex
	check func(folder string) error // nil: the sentinels and fscheck.CanLink
	at    map[string]time.Time
	err   map[string]error
}

// linkTrialTTL is how long the view reuses a season folder's trial link.
const linkTrialTTL = time.Minute

// folderLinks is the trial of a season folder for the view: the sentinels, then a hard link
// from the intake root into it (fscheck.CanLink), reused for linkTrialTTL. The confirmation
// tries again itself.
func (p *Proposals) folderLinks(folder string) error {
	lt := &p.linkTrials
	lt.mu.Lock()
	defer lt.mu.Unlock()
	now := p.now()
	if at, ok := lt.at[folder]; ok && now.Sub(at) < linkTrialTTL {
		return lt.err[folder]
	}
	if lt.at == nil {
		lt.at, lt.err = map[string]time.Time{}, map[string]error{}
	}
	var err error
	if lt.check != nil {
		err = lt.check(folder)
	} else {
		err = episodeFolderCheck(p.cfg.Paths, folder)
	}
	lt.at[folder], lt.err[folder] = now, err
	return err
}

// episodeVersionsOffered: checked, and offered.
func (s *session) episodeVersionsOffered() bool { return s.epvChecked && s.epvNot == nil }

// ---- the names ----------------------------------------------------------------------------

// episodeVersionName is an episode's version's file name: "<series> - S01E02 - <label><ext>".
func episodeVersionName(series string, season, episode int, label, ext string) string {
	return fmt.Sprintf("%s - S%02dE%02d - %s%s", series, season, episode, label, ext)
}

// titleFolder is the name of the app's folder for a title (its path as the app sees it),
// as a version's name prefix.
func titleFolder(app, appPath string) (string, error) {
	p := strings.TrimRight(strings.ReplaceAll(appPath, `\`, "/"), "/")
	if p == "" {
		return "", fmt.Errorf("%s gives no folder for the title", app)
	}
	base := path.Base(p)
	clean, err := safename.Clean(base, 200)
	if err != nil || clean != base || strings.ContainsAny(base, `<>:"|?*`) {
		return "", fmt.Errorf("%s's folder name %q cannot be used as a file name", app, base)
	}
	return clean, nil
}

// checkEpisodeLabel is checkLabel, and no season or episode number in the label: neither on
// its own (S02E05, 2x05) nor with the SxxEyy before it, which a label starting with an
// episode number (E03, -e3) would make a double episode.
func checkEpisodeLabel(label string, s *session) (string, *LabelProblem) {
	clean, problem := checkLabel(label, s.p.cfg.Versions)
	if problem != nil {
		return "", problem
	}
	if seasonEpisodeRE.MatchString(clean) || crossRE.MatchString(clean) || !readsAsOne(episodeVersionName("Series", 1, 2, clean, ".mkv"), 1, 2) {
		return "", &LabelProblem{Reason: LabelEpisode, Message: "The label must not contain a season or episode number (such as S02E05, 2x05 or E03): the media server would read the file as another episode, or as two."}
	}
	return clean, nil
}

// readsAsOne reports whether a file name reads as exactly the one episode season×number.
func readsAsOne(name string, season, number int) bool {
	refs := parseEpisodes(name)
	return len(refs) == 1 && refs[0] == (epRef{season, number})
}

// episodeLanguage is the ISO 639-1 code of the language an episode's version adds: the
// upload's first tagged audio track whose language is not the series' original one (as
// Sonarr names it, "English"), else its first tagged one; "" when none is tagged.
func episodeLanguage(up media.Probe, original string) string {
	first := ""
	for _, a := range up.Audio {
		c := languageCode(a.Language)
		if c == "" {
			continue
		}
		if original == "" || !strings.EqualFold(display.English.Languages().Name(language.Make(c)), original) {
			return c
		}
		if first == "" {
			first = c
		}
	}
	return first
}

// epvLibrary is what the rows of episodes' versions are judged on: Sonarr's folder name for
// the series, and its episode files by id.
type epvLibrary struct {
	series string
	files  map[int]arr.EpisodeFile
}

// loadEpvLibrary reads them from Sonarr (once per session). why is a reason of NotOffered
// when they cannot be used (series_folder_unusable); the error is a call to Sonarr that
// failed.
func (s *session) loadEpvLibrary(ctx context.Context) (lib epvLibrary, why string, err error) {
	if s.epvLib != nil {
		return *s.epvLib, "", nil
	}
	if s.p.sonarr == nil {
		return lib, "", appFailure(AppSonarr, errors.New("not configured"))
	}
	sr, err := s.p.sonarr.Series(ctx, s.t.TitleID)
	if err != nil {
		return lib, "", appFailure(AppSonarr, err)
	}
	if lib.series, err = titleFolder(AppSonarr, sr.Path); err != nil {
		s.p.log.Info("version_unavailable", "job", s.job.ID, "reason", err)
		return lib, ReasonSeriesFolderUnusable, nil
	}
	files, err := s.p.sonarr.EpisodeFiles(ctx, s.t.TitleID)
	if err != nil {
		return lib, "", appFailure(AppSonarr, err)
	}
	lib.files = map[int]arr.EpisodeFile{}
	for _, f := range files {
		lib.files[f.ID] = f
	}
	return lib, "", nil
}

// episodeVersionOf is what row would be as a version with label: the choice, or why it
// cannot be one (a row problem). With label "" only the episode is filled in.
func (s *session) episodeVersionOf(ctx context.Context, lib epvLibrary, rowID, label string) (episodeVersion, string, error) {
	var v episodeVersion
	i := s.find(rowID)
	if i < 0 {
		return v, EpisodeNoFile, nil
	}
	r := s.rows[i]
	ar, _, err := decodeRow(r)
	if err != nil {
		return v, "", err
	}
	v.Row, v.FileID = r.ID, r.FileID
	if len(ar.EpisodeIDs) != 1 {
		return v, EpisodeDouble, nil
	}
	ep, ok := s.idx[ar.EpisodeIDs[0]]
	if !ok || ep.FileID == 0 {
		return v, EpisodeNoFile, nil
	}
	v.EpisodeID, v.Season, v.Episode, v.EpisodeFileID = ep.ID, ep.SeasonNumber, ep.Number, ep.FileID
	for _, o := range s.eps {
		if o.ID != ep.ID && o.FileID == ep.FileID {
			return v, EpisodeSharedFile, nil
		}
	}
	ef, ok := lib.files[ep.FileID]
	if !ok || ef.Path == "" {
		return v, EpisodeNoFile, nil
	}
	folder := filepath.Clean(pathmap.From(s.p.cfg.Paths.Mappings.Sonarr, path.Dir(strings.ReplaceAll(ef.Path, `\`, "/"))))
	if _, _, in := media.LibraryPath(s.p.cfg.Paths.LibraryRoots, folder); !in {
		return v, EpisodeOutsideLibrary, nil
	}
	v.Folder = folder
	if label == "" {
		return v, "", nil
	}
	v.Name = episodeVersionName(lib.series, ep.SeasonNumber, ep.Number, label, strings.ToLower(path.Ext(s.files[r.FileID].Path)))
	if len(v.Name) > maxNameBytes {
		return v, EpisodeNameTooLong, nil
	}
	if !readsAsOne(v.Name, ep.SeasonNumber, ep.Number) { // the series' folder name reads as an episode, say
		return v, EpisodeNameUnreadable, nil
	}
	v.Path = filepath.Join(folder, v.Name)
	if _, err := os.Lstat(v.Path); err == nil {
		return v, EpisodeVersionExists, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		s.p.log.Warn("version_unavailable", "job", s.job.ID, "path", v.Path, "error", err)
		return v, EpisodeFolderUnreadable, nil
	}
	if _, err := s.p.st.VersionByPath(ctx, v.Path); err == nil {
		return v, EpisodeVersionExists, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return v, "", err
	}
	return v, "", nil
}

// proposedEpisodeLabel is the label versions.labelTemplate proposes for the rows, from the
// first one's upload, and its language.
func (s *session) proposedEpisodeLabel(ctx context.Context, rows []string) (label, lang string) {
	original := ""
	if s.p.sonarr != nil {
		if sr, err := s.p.sonarr.Series(ctx, s.t.TitleID); err == nil {
			original = sr.OriginalLanguage
		}
	}
	for _, id := range rows {
		if i := s.find(id); i >= 0 {
			lang = episodeLanguage(probeOf(s.files[s.rows[i].FileID]), original)
			break
		}
	}
	return proposeLabel(s.p.cfg.Versions.LabelTemplate, lang), lang
}

func (s *session) episodeVersionNotices() []string {
	out := []string{NoticeUntrackedBySonarr, NoticeAdoptedWithoutFile}
	if s.hasSubtitleFiles() {
		out = append(out, NoticeNoSubtitles)
	}
	return out
}

func (s *session) hasSubtitleFiles() bool {
	for _, f := range s.files {
		if f.Kind == store.KindSubtitle && f.Status == store.FileOK {
			return true
		}
	}
	return false
}

// episodeVersionsView is SeasonDub.Version (see EpisodeVersionsView).
func (s *session) episodeVersionsView(ctx context.Context) (*EpisodeVersionsView, error) {
	x := s.existing()
	v := &EpisodeVersionsView{Rows: []EpisodeVersionRow{}, Notices: s.episodeVersionNotices()}
	proposed, lang := s.proposedEpisodeLabel(ctx, x.rows)
	v.Label, v.Language = proposed, lang
	label, problem := checkEpisodeLabel(proposed, s)
	v.Problem = problem
	if problem == nil {
		v.Label = label
	}
	lib, why, err := s.loadEpvLibrary(ctx)
	if err != nil || why != "" { // checkEpisodeVersions settled the folder name before
		return nil, err
	}
	for _, id := range x.rows {
		ev, why, err := s.episodeVersionOf(ctx, lib, id, label)
		if err != nil {
			return nil, err
		}
		row := EpisodeVersionRow{Row: id, EpisodeID: ev.EpisodeID, SeasonNumber: ev.Season, EpisodeNumber: ev.Episode, Problem: why}
		if why == "" && problem == nil {
			row.Name = ev.Name
		}
		v.Rows = append(v.Rows, row)
	}
	return v, nil
}

// ---- the confirmation ---------------------------------------------------------------------

// planEpisodeVersions is the confirmation's part of the policy version (see the top of this
// file): the versions of the rows that have a file, checked, or the refusal.
func (e *Engine) planEpisodeVersions(ctx context.Context, s *session, ex existing, req *VersionRequest) (*episodeVersions, error) {
	label := ""
	if req != nil {
		label = req.Label
	}
	if strings.TrimSpace(label) == "" {
		label, _ = s.proposedEpisodeLabel(ctx, ex.rows)
	}
	label, problem := checkEpisodeLabel(label, s)
	if problem != nil {
		return nil, refuse(CodeVersionLabel, "%s", problem.Message)
	}
	lib, why, err := s.loadEpvLibrary(ctx)
	if err != nil {
		return nil, err
	}
	if why != "" {
		return nil, refuse(CodeVersionUnavailable, "Sonarr's folder name for the series cannot be part of a file name (%s).", why)
	}
	out := &episodeVersions{Label: label}
	var unavailable, exists []string
	reasons := map[string]bool{}
	seen := map[string]bool{}
	for _, id := range ex.rows {
		v, why, err := s.episodeVersionOf(ctx, lib, id, label)
		if err != nil {
			return nil, err
		}
		if why == "" && seen[v.Path] { // two rows for one episode
			why = EpisodeVersionExists
		}
		switch why {
		case "":
			seen[v.Path] = true
			out.Versions = append(out.Versions, v)
		case EpisodeVersionExists:
			exists = append(exists, id)
		default:
			unavailable = append(unavailable, id)
			reasons[why] = true
		}
	}
	switch {
	case len(unavailable) > 0:
		why := make([]string, 0, len(reasons))
		for r := range reasons {
			why = append(why, r)
		}
		slices.Sort(why)
		err := refuse(CodeVersionUnavailable, "These rows cannot be kept as second versions (%s): skip them, or choose another policy.", strings.Join(why, ", "))
		err.Rows = unavailable
		return nil, err
	case len(exists) > 0:
		err := refuse(CodeVersionExists, "A file or a version of that name is already there for these rows: choose another label. Nothing is ever replaced.")
		err.Rows = exists
		return nil, err
	}
	// The season folders must take a hard link from the intake root, and the mounts be there.
	folders := map[string]bool{}
	for _, v := range out.Versions {
		if folders[v.Folder] {
			continue
		}
		folders[v.Folder] = true
		if err := e.checkEpisodeFolder(v.Folder); err != nil {
			e.log.Warn("version_storage_unavailable", "job", s.job.ID, "folder", v.Folder, "error", err)
			return nil, refuse(CodeStorageUnavailable, "A season folder is not available for the versions: a mount is missing, Fukku cannot write it, or it is not on the same mount as the intake folder.")
		}
	}
	if e.p.cfg.Versions.RequireNotUpgradeVerdict {
		var upgrades, unchecked, ureasons, nreasons []string
		for _, v := range out.Versions {
			vd, err := e.sonarrVerdict(ctx, s.job.ID, s.t, v)
			if err != nil {
				return nil, err
			}
			if vd.Code == "" {
				continue
			}
			e.log.Info("version_refused", "job", s.job.ID, "row", v.Row, "code", vd.Code, "reason", vd.Reason)
			line := fmt.Sprintf("S%02dE%02d: %s", v.Season, v.Episode, vd.Reason)
			if vd.Code == CodeVersionUpgrade {
				upgrades, ureasons = append(upgrades, v.Row), append(ureasons, line)
			} else {
				unchecked, nreasons = append(unchecked, v.Row), append(nreasons, line)
			}
		}
		switch {
		case len(upgrades) > 0:
			err := refuse(CodeVersionUpgrade, "Sonarr would take these files for the episodes' own on its next rescan (it does not reject them as not an upgrade), so they cannot be kept next to them: replace the episodes instead, or skip these rows. %s", strings.Join(ureasons, "; "))
			err.Rows = upgrades
			return nil, err
		case len(unchecked) > 0:
			err := refuse(CodeVersionUnchecked, "Sonarr gives no verdict on these files under their version names, so Fukku cannot tell that Sonarr would leave them alone: skip these rows, or choose another label or policy. %s", strings.Join(nreasons, "; "))
			err.Rows = unchecked
			return nil, err
		}
	}
	return out, nil
}

// episodeFolderCheck is the filesystem check of a season folder before episodes' versions
// are offered and confirmed: the sentinels, then a trial hard link from the intake root into
// it.
func episodeFolderCheck(paths config.Paths, folder string) error {
	if err := fscheck.Sentinels(paths); err != nil {
		return err
	}
	return fscheck.CanLink(paths.IntakeRoot, folder)
}

// ---- Sonarr's verdict ---------------------------------------------------------------------

// stageEpisodeVersion hard-links the job's file src as jobs/<job>/versions/<row>/<name>, the
// file Sonarr is asked about, and removes anything else in that folder (an earlier label's).
func (e *Engine) stageEpisodeVersion(jobID, src string, v episodeVersion) (string, error) {
	if !filepath.IsLocal(v.Row) || strings.ContainsAny(v.Row, `/\`) {
		return "", fmt.Errorf("row id %q", v.Row)
	}
	// Inside the job's folder only (os.Root: no "..", no link out of it). The folder must be
	// readable by Sonarr, as the job's files/ is.
	job, err := os.OpenRoot(jobdir.Dir(e.p.cfg.Paths.IntakeRoot, jobID))
	if err != nil {
		return "", err
	}
	defer func() { _ = job.Close() }()
	rel := filepath.Join(jobdir.Versions, v.Row)
	if err := job.MkdirAll(rel, 0o755); err != nil {
		return "", err
	}
	d, err := job.Open(rel)
	if err != nil {
		return "", err
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return "", err
	}
	for _, en := range entries {
		if en.Name() != v.Name {
			_ = job.RemoveAll(filepath.Join(rel, en.Name()))
		}
	}
	dst := filepath.Join(job.Name(), rel, v.Name)
	if same, err := samePlaced(src, dst); err != nil {
		return "", err
	} else if same {
		return dst, nil
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return dst, os.Link(src, dst)
}

// Verdict is Sonarr's verdict on an episode's version (EpisodeVerdict). Code is "" when the
// version may be placed, CodeVersionUpgrade when Sonarr would take it for the episode's file,
// CodeVersionUnchecked when Sonarr gives no verdict on it; Reason says why.
type Verdict struct {
	Code   string
	Reason string
}

func unchecked(format string, args ...any) Verdict {
	return Verdict{Code: CodeVersionUnchecked, Reason: fmt.Sprintf(format, args...)}
}

// sonarrVerdict asks Sonarr what it makes of the version v under its library name (see the
// top of this file). An error is a failure of the call (arr_unavailable) or of the staging
// link (storage_unavailable).
func (e *Engine) sonarrVerdict(ctx context.Context, jobID string, t store.Target, v episodeVersion) (Verdict, error) {
	if e.p.sonarr == nil {
		return Verdict{}, appFailure(AppSonarr, errors.New("not configured"))
	}
	files, err := e.st.Files(ctx, jobID)
	if err != nil {
		return Verdict{}, err
	}
	src, ok := jobVideo(files, v.FileID, e.p.cfg.Paths.IntakeRoot, jobID)
	if !ok {
		return unchecked("the job's video is no longer there"), nil
	}
	if _, err := e.stageEpisodeVersion(jobID, src, v); err != nil {
		e.log.Warn("version_stage_failed", "job", jobID, "row", v.Row, "error", err)
		return Verdict{}, refuse(CodeStorageUnavailable, "The job's folder cannot be written.")
	}
	rel := path.Join(jobdir.Versions, v.Row)
	folder := e.p.appPath(AppSonarr, jobID, rel)
	file := e.p.appPath(AppSonarr, jobID, path.Join(rel, v.Name))
	vd, err := EpisodeVerdict(ctx, e.p.sonarr, folder, file, t.TitleID, v.EpisodeID, v.Season, v.Episode)
	if err != nil {
		return Verdict{}, appFailure(AppSonarr, err)
	}
	return vd, nil
}

// EpisodeVerdict is Sonarr's verdict on an episode's version (see the top of this file):
// the file (as Sonarr sees it, in folder, named as the version) as the episode episodeID
// (season, number) of series seriesID. Sonarr's Manual Import proposal for the folder must
// offer the file and read it as that episode when it finds the series, and the name must
// read as exactly that episode (else no verdict: version_unchecked). Re-checked for the
// episode, it must be rejected as not an upgrade for the episode's file, or because the
// quality profile allows no upgrade: then the version loses to the episode's file on
// Sonarr's rescans, and Code is "". Otherwise version_upgrade. An error is a call to Sonarr
// that failed.
func EpisodeVerdict(ctx context.Context, sonarr arr.Importer, folder, file string, seriesID, episodeID, season, number int) (Verdict, error) {
	items, err := sonarr.Proposal(ctx, folder)
	if err != nil {
		return Verdict{}, err
	}
	return EpisodeVerdictIn(ctx, sonarr, items, file, seriesID, episodeID, season, number)
}

// EpisodeVerdictIn is EpisodeVerdict on the items of a proposal already asked for the file's
// folder: the re-check of placed versions (#96) asks once per season folder for all of the
// versions in it.
func EpisodeVerdictIn(ctx context.Context, sonarr arr.Importer, items []arr.Row, file string, seriesID, episodeID, season, number int) (Verdict, error) {
	name := path.Base(file)
	i := slices.IndexFunc(items, func(it arr.Row) bool { return path.Clean(it.Path) == path.Clean(file) })
	if i < 0 {
		return unchecked("Sonarr does not offer %s, so it gives no verdict", name), nil
	}
	it := items[i]
	one := []int{episodeID}
	if it.SeriesID != 0 && len(it.EpisodeIDs) > 0 && (it.SeriesID != seriesID || !slices.Equal(it.EpisodeIDs, one)) {
		return unchecked("Sonarr reads %s as another episode", name), nil
	}
	if !readsAsOne(name, season, number) {
		return unchecked("%s does not read as S%02dE%02d alone", name, season, number), nil
	}
	if it.SeriesID != seriesID || !slices.Equal(it.EpisodeIDs, one) {
		it.Raw = withoutKeys(it.Raw, "episodes", "episodeIds")
	}
	it.Path, it.SeriesID, it.MovieID, it.EpisodeIDs, it.SeasonNumber = file, seriesID, 0, one, season
	fresh, err := sonarr.Recheck(ctx, []arr.Row{it})
	if err != nil {
		return Verdict{}, err
	}
	merged, err := mergeAnswers([]arr.Row{it}, fresh)
	if err != nil {
		return Verdict{}, err
	}
	r := merged[0]
	if r.SeriesID != seriesID || !slices.Equal(r.EpisodeIDs, one) {
		return unchecked("Sonarr's re-check of %s names another episode", name), nil
	}
	var reasons []string
	for _, rej := range r.Rejections {
		if replaceCovers(rej.Reason) || noUpgrades(rej.Reason) {
			return Verdict{}, nil
		}
		reasons = append(reasons, rej.Reason)
	}
	return Verdict{Code: CodeVersionUpgrade, Reason: fmt.Sprintf(
		"Sonarr does not reject %s (%s) as not an upgrade for the episode's file (rejections: %v): it would take it for the episode's file on its next rescan",
		name, r.Quality.Name, reasons)}, nil
}

// noUpgrades reports whether a rejection is the quality profile's refusal of any upgrade
// ("Existing file and the Quality profile does not allow upgrades"): Sonarr then never takes
// another file for the episode's.
func noUpgrades(reason string) bool {
	return strings.Contains(strings.ToLower(reason), "does not allow upgrades")
}

// jobVideo is the job's video fileID as Fukku sees it, when it is an accepted video under
// files/.
func jobVideo(files []store.File, fileID int64, intakeRoot, jobID string) (string, bool) {
	for _, f := range files {
		if f.ID == fileID && f.Kind == store.KindVideo && f.Status == store.FileOK && strings.HasPrefix(f.Path, jobdir.Files+"/") {
			return filepath.Join(jobdir.Dir(intakeRoot, jobID), filepath.FromSlash(f.Path)), true
		}
	}
	return "", false
}

// ---- the placement ------------------------------------------------------------------------

// placeEpisodeVersions is the first part of the importing step of a job with episodes'
// versions (see the top of this file). done is true once every version is placed or
// refused; false when the job moved (stalled or failed) and the pass ends. A version refused
// at the placement (Sonarr's verdict changed, the name is taken, the episode lost its file or
// moved) does not stop the others or the import of the other rows: its row is recorded as
// failed, with the code and the reason (episodeVersionFailures, audit version.refused). Only
// when every version is refused and the job has nothing else to import does the job fail,
// with the reasons; the failures are forgotten then, so that a retry tries them again.
func (e *Engine) placeEpisodeVersions(ctx context.Context, job store.Job, t store.Target, c episodeVersions) (bool, error) {
	files, err := e.st.Files(ctx, job.ID)
	if err != nil {
		return false, err
	}
	// Sonarr's view, read once per pass and only when a version is still to be placed. nil
	// with no error: the job moved (stalled or failed).
	var lib *sonarrView
	load := func() (*sonarrView, error) {
		if lib != nil {
			return lib, nil
		}
		if e.p.sonarr == nil {
			return nil, e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: "sonarr is not configured"})
		}
		list, err := e.p.sonarr.Episodes(ctx, t.TitleID)
		if err != nil {
			return nil, e.sonarrFailed(ctx, job, t, err)
		}
		fl, err := e.p.sonarr.EpisodeFiles(ctx, t.TitleID)
		if err != nil {
			return nil, e.sonarrFailed(ctx, job, t, err)
		}
		lib = &sonarrView{eps: episodeIndex(list), files: map[int]arr.EpisodeFile{}}
		for _, f := range fl {
			lib.files[f.ID] = f
		}
		return lib, nil
	}
	failures := episodeVersionFailures(t)
	for _, v := range c.Versions {
		if _, failed := failures[v.Row]; failed {
			continue
		}
		placed, refused, err := e.placeEpisodeVersion(ctx, job, t, c, v, files, load)
		switch {
		case err != nil:
			return false, err
		case refused != nil:
			if t, err = e.refuseEpisodeVersion(ctx, job, t, v, *refused); err != nil {
				return false, err
			}
			failures[v.Row] = *refused
		case !placed:
			return false, nil
		}
	}
	if len(failures) < len(c.Versions) {
		return true, nil
	}
	rows, err := e.st.Rows(ctx, job.ID)
	if err != nil {
		return false, err
	}
	if slices.ContainsFunc(rows, func(r store.Row) bool { return !r.Skip }) {
		return true, nil // the other rows are imported
	}
	// Every version refused, and nothing else to do: the job fails, and a retry tries again.
	var reasons []string
	code := ""
	for _, v := range c.Versions {
		f := failures[v.Row]
		if code == "" {
			code = f.Code
		}
		reasons = append(reasons, fmt.Sprintf("S%02dE%02d (%s): %s", v.Season, v.Episode, f.Code, f.Reason))
	}
	if t, err = e.setEpisodeVersionFailures(ctx, job, t, nil); err != nil {
		return false, err
	}
	return false, e.failVersion(ctx, job, t, code, "nothing was placed: "+strings.Join(reasons, "; "))
}

// EpisodeVersionFailure is why a row's version was refused at the placement.
type EpisodeVersionFailure struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// episodeVersionFailures are the rows whose version the placement refused, from the target's
// options ("episodeVersionFailures"), by row id.
func episodeVersionFailures(t store.Target) map[string]EpisodeVersionFailure {
	var opts struct {
		Failures map[string]EpisodeVersionFailure `json:"episodeVersionFailures"`
	}
	if len(t.Options) > 0 {
		_ = json.Unmarshal(t.Options, &opts)
	}
	if opts.Failures == nil {
		return map[string]EpisodeVersionFailure{}
	}
	return opts.Failures
}

// refuseEpisodeVersion records that v's row is not placed, with the audit entry
// version.refused, and returns the target as stored.
func (e *Engine) refuseEpisodeVersion(ctx context.Context, job store.Job, t store.Target, v episodeVersion, f EpisodeVersionFailure) (store.Target, error) {
	failures := episodeVersionFailures(t)
	failures[v.Row] = f
	t, err := e.setEpisodeVersionFailures(ctx, job, t, failures)
	if err != nil {
		return t, err
	}
	e.log.Warn("version_refused", "job", job.ID, "row", v.Row, "code", f.Code, "reason", f.Reason)
	return t, e.st.Audit(ctx, store.AuditEntry{Actor: actor, Action: "version.refused", JobID: job.ID, Detail: map[string]string{
		"app": t.App, "titleId": strconv.Itoa(t.TitleID), "title": t.Title, "row": v.Row, "name": v.Name,
		"episode": fmt.Sprintf("S%02dE%02d", v.Season, v.Episode), "code": f.Code, "reason": f.Reason,
	}})
}

// setEpisodeVersionFailures stores the failures (nil: none) in the target's options, under
// the job's lock, while the job still has the same title.
func (e *Engine) setEpisodeVersionFailures(ctx context.Context, job store.Job, t store.Target, failures map[string]EpisodeVersionFailure) (store.Target, error) {
	defer e.locks.Lock(job.ID)()
	cur, err := e.st.Target(ctx, job.ID)
	if err != nil {
		return t, err
	}
	if cur.App != t.App || cur.TitleID != t.TitleID || !cur.MatchedAt.Equal(t.MatchedAt) {
		return t, nil // another title meanwhile: the transition will not happen either
	}
	var v any
	if len(failures) > 0 {
		v = failures
	}
	if cur.Options, err = withOption(cur.Options, "episodeVersionFailures", v); err != nil {
		return t, err
	}
	return cur, e.st.SetTarget(ctx, job.ID, cur)
}

// sonarrView is what the placement reads of Sonarr: the series' episodes and episode files.
type sonarrView struct {
	eps   map[int]arr.Episode
	files map[int]arr.EpisodeFile
}

// sonarrFailed ends a placement pass whose call to Sonarr failed: stalled when it does not
// answer, failed otherwise.
func (e *Engine) sonarrFailed(ctx context.Context, job store.Job, t store.Target, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if Unavailable(err) {
		return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: "sonarr: " + err.Error()})
	}
	return e.failVersion(ctx, job, t, CodeVersionFailed, "sonarr: "+err.Error())
}

// placeEpisodeVersion places one version, under its lock. placed: it is in place; refused:
// it is not, and why (nothing was written); neither: the job moved.
func (e *Engine) placeEpisodeVersion(ctx context.Context, job store.Job, t store.Target, c episodeVersions, v episodeVersion,
	files []store.File, load func() (*sonarrView, error)) (placed bool, refused *EpisodeVersionFailure, err error) {
	no := func(code, format string, args ...any) (bool, *EpisodeVersionFailure, error) {
		return false, &EpisodeVersionFailure{Code: code, Reason: fmt.Sprintf(format, args...)}, nil
	}
	src, ok := jobVideo(files, v.FileID, e.p.cfg.Paths.IntakeRoot, job.ID)
	if !ok {
		return no(CodeVersionFailed, "the job's video for %s is no longer there", v.Name)
	}
	if v.Path != filepath.Join(v.Folder, v.Name) {
		return no(CodeVersionFailed, "the stored version is inconsistent: %s", v.Path)
	}
	defer e.locks.Lock(VersionLock(v.Path))()
	rec, err := e.st.VersionByPath(ctx, v.Path)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, nil, err
	}
	if rec.ID != 0 && rec.JobID != job.ID {
		return no(CodeVersionExists, "%s is recorded for job %s (status %s): nothing is taken over", v.Path, rec.JobID, rec.Status)
	}
	there, err := samePlaced(src, v.Path)
	if err != nil {
		return no(CodeVersionFailed, "%v", err)
	}
	if !there {
		if _, err := os.Lstat(v.Path); err == nil {
			return no(CodeVersionExists, "%s exists: nothing is replaced", v.Path)
		}
		lib, err := load()
		if lib == nil || err != nil {
			return false, nil, err
		}
		ep := lib.eps[v.EpisodeID]
		ef, has := lib.files[ep.FileID]
		switch {
		case ep.FileID == 0 || !has:
			return no(CodeVersionFailed,
				"S%02dE%02d has no file in Sonarr any more: next to nothing, Sonarr would take the version for the episode's file; nothing was placed", v.Season, v.Episode)
		case filepath.Clean(pathmap.From(e.p.cfg.Paths.Mappings.Sonarr, path.Dir(strings.ReplaceAll(ef.Path, `\`, "/")))) != v.Folder:
			return no(CodeVersionFailed,
				"Sonarr's file of S%02dE%02d is in another folder since the confirmation (%s): nothing was placed", v.Season, v.Episode, path.Dir(ef.Path))
		}
		if e.p.cfg.Versions.RequireNotUpgradeVerdict {
			vd, err := e.sonarrVerdict(ctx, job.ID, t, v)
			switch {
			case Code(err) == CodeArrUnavailable:
				return false, nil, e.sonarrFailed(ctx, job, t, errors.Unwrap(err))
			case err != nil && Code(err) != "":
				return no(CodeVersionFailed, "%v", err)
			case err != nil:
				return false, nil, err
			case vd.Code != "":
				return no(vd.Code, "%s; nothing was placed for it", vd.Reason)
			}
		}
		if rec, err = e.st.PutVersion(ctx, episodeVersionRecord(rec, job, t, c, v, store.VersionPlacing)); err != nil {
			return false, nil, err
		}
		if err := linkEpisodeVersion(e.p.cfg.Paths.LibraryRoots, src, v.Folder, v.Name); err != nil {
			if derr := e.st.DeleteVersion(ctx, rec.ID); derr != nil && !errors.Is(derr, store.ErrNotFound) {
				return false, nil, derr
			}
			code := CodeVersionFailed
			switch {
			case errors.Is(err, fs.ErrExist):
				code = CodeVersionExists
			case crossDevice(err):
				err = fmt.Errorf("%w: the season folder must be on the same mount as the intake root", err)
			}
			return no(code, "place %s: %v", v.Path, err)
		}
	}
	if rec.ID == 0 || rec.Status == store.VersionPlacing || rec.Status == "" {
		entry := store.AuditEntry{Actor: actor, Action: "version.linked", JobID: job.ID, Detail: map[string]string{
			"app": t.App, "titleId": strconv.Itoa(t.TitleID), "title": t.Title, "path": v.Path, "label": c.Label,
			"episodeId": strconv.Itoa(v.EpisodeID), "episode": fmt.Sprintf("S%02dE%02d", v.Season, v.Episode), "row": v.Row,
		}}
		if _, err := e.st.PutVersion(ctx, episodeVersionRecord(rec, job, t, c, v, store.VersionLinked), entry); err != nil {
			return false, nil, err
		}
		e.log.Info("version_linked", "job", job.ID, "path", v.Path)
	}
	return true, nil, nil
}

func episodeVersionRecord(rec store.Version, job store.Job, t store.Target, c episodeVersions, v episodeVersion, status string) store.Version {
	rec.JobID, rec.App, rec.TitleID, rec.Title, rec.Year = job.ID, t.App, t.TitleID, t.Title, t.Year
	rec.Path, rec.Label, rec.Status, rec.Detail = v.Path, c.Label, status, ""
	rec.EpisodeID, rec.SeasonNumber, rec.EpisodeNumber = v.EpisodeID, v.Season, v.Episode
	return rec
}

// linkEpisodeVersion hard-links src as folder/name. folder must be inside one of roots
// (paths.libraryRoots) and is opened through an os.Root of that root (no "..", no link out
// of it); it is never created. The link never replaces a file (fs.ErrExist).
func linkEpisodeVersion(roots []string, src, folder, name string) error {
	root, rel, ok := media.LibraryPath(roots, folder)
	if !ok {
		return fmt.Errorf("%s is not inside paths.libraryRoots", folder)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	info, err := r.Lstat(rel)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", folder)
	}
	dir, err := r.Open(rel)
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

// ---- the result ---------------------------------------------------------------------------

// episodeVersionsResult is ResultView.EpisodeVersions; nil for a job without them.
func (e *Engine) episodeVersionsResult(ctx context.Context, job store.Job, t store.Target, admin bool) (*EpisodeVersionsResult, error) {
	c, ok := episodeVersionsOf(t)
	if !ok {
		return nil, nil
	}
	recs, err := e.st.VersionsOf(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	byPath := map[string]store.Version{}
	for _, r := range recs {
		byPath[r.Path] = r
	}
	files, err := e.st.Files(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	out := &EpisodeVersionsResult{Label: c.Label, Versions: []EpisodeVersionResult{}, Notices: []string{NoticeUntrackedBySonarr, NoticeAdoptedWithoutFile}}
	if slices.ContainsFunc(files, func(f store.File) bool { return f.Kind == store.KindSubtitle && f.Status == store.FileOK }) {
		out.Notices = append(out.Notices, NoticeNoSubtitles)
	}
	failures := episodeVersionFailures(t)
	for _, v := range c.Versions {
		r := EpisodeVersionResult{Row: v.Row, SeasonNumber: v.Season, EpisodeNumber: v.Episode, Name: v.Name, Status: store.VersionPlacing}
		if f, failed := failures[v.Row]; failed {
			r.Status, r.Refusal = VersionRefused, f.Code
			if admin {
				r.Detail = f.Reason
			}
		} else if rec, ok := byPath[v.Path]; ok {
			r.Status = rec.Status
			if admin {
				r.Detail = rec.Detail
			}
			if !rec.GroupedAt.IsZero() {
				r.GroupedAt = ptr(rec.GroupedAt.UTC())
			}
		}
		out.Versions = append(out.Versions, r)
	}
	return out, nil
}

// ResultVersion is the import result of a row kept as an episode's version (ImportRowView):
// its version is placed.
const ResultVersion = "version"

// episodeVersionRows maps the rows of the job's episodes' versions that are placed (linked or
// later) to ResultVersion, and those the placement refused to failed.
func (e *Engine) episodeVersionRows(ctx context.Context, job store.Job, t store.Target) (map[string]string, error) {
	c, ok := episodeVersionsOf(t)
	if !ok {
		return nil, nil
	}
	recs, err := e.st.VersionsOf(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	failures := episodeVersionFailures(t)
	for _, v := range c.Versions {
		if _, failed := failures[v.Row]; failed {
			out[v.Row] = store.ResultFailed
			continue
		}
		for _, r := range recs {
			if r.Path == v.Path && r.Status != store.VersionPlacing {
				out[v.Row] = ResultVersion
			}
		}
	}
	return out, nil
}

// episodeVersionsAudit is the confirmation's version.confirmed entry for each episode's
// version.
func episodeVersionsAudit(user, email string, t store.Target, c episodeVersions) []store.AuditEntry {
	out := make([]store.AuditEntry, 0, len(c.Versions))
	for _, v := range c.Versions {
		out = append(out, store.AuditEntry{Actor: user, Email: email, Action: "version.confirmed", Detail: map[string]string{
			"app": t.App, "titleId": strconv.Itoa(t.TitleID), "title": t.Title, "label": c.Label, "name": v.Name,
			"row": v.Row, "episodeId": strconv.Itoa(v.EpisodeID), "episode": fmt.Sprintf("S%02dE%02d", v.Season, v.Episode),
			"episodeFileId": strconv.Itoa(v.EpisodeFileID),
		}})
	}
	return out
}
