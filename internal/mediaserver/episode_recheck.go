package mediaserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/fscheck"
	"github.com/segun-io/fukku/internal/media"
	"github.com/segun-io/fukku/internal/pathmap"
	"github.com/segun-io/fukku/internal/store"
)

// The re-check of episodes' versions (#96). A version of an episode (#58) sits next to the
// episode's file in Sonarr's season folder, untracked by Sonarr, which judges it on each
// rescan and adopts it when it wins (probe B3-sonarr): after the episode's own file is
// deleted, after a worse file replaces it, or after a change of the quality profile or its
// custom formats. The keeper's passes (versions.regroupInterval) check each one against
// Sonarr, in this order, and record what they find as the version's status, with an audit
// entry and a log event when it changes (the notice):
//
//   - series_deleted: Sonarr answers 404 for the series, or the series no longer has the
//     episode. The file, if still there, is left alone.
//   - series_moved: Sonarr's folder for the series is elsewhere and the version is not in it.
//     Sonarr moves the whole folder when it moves a series' files, the version with it: then
//     the record follows the version to its new path (audit version.followed; no file is
//     touched) and the check goes on there. A version left at the old path stays there.
//   - missing: the version file is gone.
//   - adopted: Sonarr tracks the version's path as an episode's file. From now on Sonarr
//     renames, upgrades and deletes it as its own.
//   - episode_file_missing: Sonarr lists no file for the episode, or its file is not on disk:
//     Sonarr's next rescan takes the version for the episode's file.
//   - adoptable: Sonarr's verdict on the version (flows.EpisodeVerdictIn, the check made
//     before the version was placed, asked of the file where it is) no longer rejects it as
//     not an upgrade: its next rescan takes the version for the episode's file. Asked only
//     with versions.requireNotUpgradeVerdict, when the episode's file changed, every
//     finalRecheck, and on every pass while the version is adoptable.
//   - otherwise the version is grouped once the media server lists both files as one
//     episode, or waiting (the server is refreshed once per pass, and a version it still does
//     not list backs off, as films' do: a version placed again at a path Jellyfin knew can
//     stay unlisted for minutes).
//
// Fukku never deletes, moves, renames or places a library file for any of them, and never
// asks Sonarr to rescan. adopted, series_deleted and missing are checked
// again every finalRecheck; the others on every pass (series_moved too: a move of Sonarr's
// may be under way). Whatever the check finds, it decides
// nothing from a passing fault, as for films: while a paths.sentinels file, the library root
// that holds the version (paths.libraryRoots) or Sonarr's folder for the series is missing,
// or Sonarr does not answer, the record keeps its status and its detail says why; a status
// that comes from Sonarr's answer (series_deleted, series_moved, adopted) needs Sonarr's own
// status call to answer, asked again for each decision. A record whose job is still running
// is the job's step's.

// verdictMark is when Sonarr's verdict on an episode's version was last asked, and the
// episode's file it was asked against. In memory only: a restart asks again.
type verdictMark struct {
	at     time.Time
	fileID int
}

// sonarrCache is what a pass read of Sonarr, so that the versions of one series (one season
// folder) cost one set of calls (one proposal).
type sonarrCache struct {
	series    map[int]*seriesView
	proposals map[string]proposalAnswer
}

type seriesView struct {
	series arr.Series
	eps    map[int]arr.Episode
	files  map[int]arr.EpisodeFile
	gone   string // Sonarr answers 404 for the series: why it may be deleted
	err    error  // a call failed
}

type proposalAnswer struct {
	items []arr.Row
	err   error
}

func (c *sonarrCache) seriesOf(ctx context.Context, sm arr.SeriesManager, id int) *seriesView {
	if v, ok := c.cached(id); ok {
		return v
	}
	v := &seriesView{}
	s, err := sm.Series(ctx, id)
	switch {
	case arr.StatusOf(err) == http.StatusNotFound:
		v.gone = fmt.Sprintf("sonarr answers 404 for series %d", id)
	case err != nil:
		v.err = err
	default:
		v.series = s
		var eps []arr.Episode
		var files []arr.EpisodeFile
		if eps, err = sm.Episodes(ctx, id); err == nil {
			files, err = sm.EpisodeFiles(ctx, id)
		}
		if err != nil {
			v.err = err
			break
		}
		v.eps, v.files = map[int]arr.Episode{}, map[int]arr.EpisodeFile{}
		for _, e := range eps {
			v.eps[e.ID] = e
		}
		for _, f := range files {
			v.files[f.ID] = f
		}
	}
	if c != nil && ctx.Err() == nil {
		if c.series == nil {
			c.series = map[int]*seriesView{}
		}
		c.series[id] = v
	}
	return v
}

func (c *sonarrCache) cached(id int) (*seriesView, bool) {
	if c == nil {
		return nil, false
	}
	v, ok := c.series[id]
	return v, ok
}

func (c *sonarrCache) proposal(ctx context.Context, sm arr.SeriesManager, folder string) ([]arr.Row, error) {
	if c != nil {
		if a, ok := c.proposals[folder]; ok {
			return a.items, a.err
		}
	}
	items, err := sm.Proposal(ctx, folder)
	if c != nil && ctx.Err() == nil {
		if c.proposals == nil {
			c.proposals = map[string]proposalAnswer{}
		}
		c.proposals[folder] = proposalAnswer{items, err}
	}
	return items, err
}

// episodeDue reports whether a scheduled pass re-checks an episode's version (see the top of
// this file).
func (k *Keeper) episodeDue(rec store.Version) bool {
	switch rec.Status {
	case store.VersionLinked, store.VersionGrouped, store.VersionAdoptable, store.VersionEpisodeFileMissing,
		store.VersionSeriesMoved: // a move of Sonarr's can be under way
		return true
	case store.VersionWaiting:
		return !k.backingOff(rec.Path)
	case store.VersionAdopted, store.VersionSeriesDeleted, store.VersionMissing:
		return k.now().Sub(rec.CheckedAt) >= finalRecheck(k.cfg)
	}
	return false
}

// stepOwns reports whether an episode's version belongs to its job's step still: the job is
// importing or post-processing, and its step mediaServerEpisodeVersions writes the record.
func (k *Keeper) stepOwns(ctx context.Context, rec store.Version) bool {
	if rec.App != flows.AppSonarr {
		return false
	}
	job, err := k.store.Job(ctx, rec.JobID)
	if err != nil {
		return false
	}
	return job.State == store.Importing || job.State == store.Imported || job.State == store.PostProcessing
}

// noticeStatus: a status of the re-check that a version recovers from.
func noticeStatus(s string) bool {
	switch s {
	case store.VersionAdopted, store.VersionAdoptable, store.VersionEpisodeFileMissing,
		store.VersionSeriesMoved, store.VersionSeriesDeleted, store.VersionMissing:
		return true
	}
	return false
}

// checkEpisode re-checks an episode's version (see the top of this file), under its lock. It
// returns the record as it is after and, while the version waits for the media server, the
// paths (as the server sees them) the pass refreshes.
func (k *Keeper) checkEpisode(ctx context.Context, rec store.Version, o checkOpts) (store.Version, []string, error) {
	if err := k.episodeStorage(rec.Path); err != nil {
		return k.failed(ctx, rec, fmt.Errorf("not checked: %w", err))
	}
	if k.series == nil {
		return k.failed(ctx, rec, errors.New("sonarr is not configured"))
	}
	v := o.sonarr.seriesOf(ctx, k.series, rec.TitleID)
	if v.err != nil {
		return k.failed(ctx, rec, fmt.Errorf("sonarr: %w", v.err))
	}
	gone := v.gone
	ep, known := v.eps[rec.EpisodeID]
	if gone == "" && !known {
		gone = fmt.Sprintf("sonarr's series %d has no episode %d", rec.TitleID, rec.EpisodeID)
	}
	if gone != "" {
		// Asked again for each decision: a wrong URL base or proxy answers 404 to it too.
		if _, why := k.series.Status(ctx); why != nil {
			return k.failed(ctx, rec, fmt.Errorf("%s, and its status call fails too (%w): not taken for a deleted series", gone, why))
		}
		return k.episodeNotice(ctx, rec, store.VersionSeriesDeleted, "version.series_deleted",
			"Sonarr no longer has the series or the episode ("+gone+"); "+leftAlone(rec.Path))
	}
	sonarrMap := k.cfg.Paths.Mappings.Sonarr
	seriesDir := fromApp(sonarrMap, v.series.Path)
	if info, err := os.Stat(seriesDir); err != nil || !info.IsDir() {
		return k.failed(ctx, rec, fmt.Errorf("not checked: Sonarr's folder for the series, %s as paths.mappings.sonarr maps it, cannot be read", seriesDir))
	}
	if !within(seriesDir, rec.Path) {
		return k.seriesMoved(ctx, rec, v, ep, seriesDir, o)
	}
	if _, err := os.Lstat(rec.Path); errors.Is(err, fs.ErrNotExist) {
		return k.episodeNotice(ctx, rec, store.VersionMissing, "version.missing", "the version file is no longer on disk")
	} else if err != nil {
		return k.failed(ctx, rec, err)
	}
	if f, which, ok := v.trackedAt(sonarrMap, rec.Path); ok {
		if _, why := k.series.Status(ctx); why != nil {
			return k.failed(ctx, rec, fmt.Errorf("sonarr lists the version as a file, but its status call fails (%w): not taken for an adoption", why))
		}
		why := fmt.Sprintf("Sonarr tracks the version as the file of %s (episode file %d): it renames, upgrades and deletes it as its own now", which, f.ID)
		if rec.PrimaryPath != "" && rec.PrimaryPath != rec.Path {
			if _, err := os.Lstat(rec.PrimaryPath); err == nil {
				why += "; the episode's earlier file " + rec.PrimaryPath + " is still on disk, untracked"
			} else if errors.Is(err, fs.ErrNotExist) {
				why += "; the episode's earlier file " + rec.PrimaryPath + " is gone"
			}
		}
		return k.episodeNotice(ctx, rec, store.VersionAdopted, "version.adopted", why)
	}
	episode := fmt.Sprintf("S%02dE%02d", rec.SeasonNumber, rec.EpisodeNumber)
	f, has := v.files[ep.FileID]
	if ep.FileID == 0 || !has || strings.TrimSpace(f.Path) == "" {
		return k.episodeNotice(ctx, rec, store.VersionEpisodeFileMissing, "version.episode_file_missing", fmt.Sprintf(
			"Sonarr lists no file for %s: its next rescan takes the version for the episode's file (import the episode's file through Sonarr first to keep the version a second version)", episode))
	}
	primary := fromApp(sonarrMap, f.Path)
	if _, err := os.Lstat(primary); errors.Is(err, fs.ErrNotExist) {
		if info, derr := os.Stat(filepath.Dir(primary)); derr != nil || !info.IsDir() {
			return k.failed(ctx, rec, fmt.Errorf("not checked: the folder of the episode's file %s cannot be read", primary))
		}
		return k.episodeNotice(ctx, rec, store.VersionEpisodeFileMissing, "version.episode_file_missing", fmt.Sprintf(
			"Sonarr's file for %s, %s, is not on disk: its next rescan drops it and takes the version for the episode's file", episode, primary))
	} else if err != nil {
		return k.failed(ctx, rec, err)
	}
	if k.cfg.Versions.RequireNotUpgradeVerdict && k.verdictDue(rec, ep.FileID) {
		vd, err := k.episodeVerdict(ctx, rec, o.sonarr)
		if err != nil {
			return k.failed(ctx, rec, fmt.Errorf("sonarr's verdict: %w", err))
		}
		k.verdictAsked(rec.Path, ep.FileID)
		switch vd.Code {
		case flows.CodeVersionUpgrade:
			return k.episodeNotice(ctx, rec, store.VersionAdoptable, "version.adoptable", vd.Reason)
		case flows.CodeVersionUnchecked: // no verdict: nothing is decided from it
			k.log.Warn("version_unchecked", "job", rec.JobID, "path", rec.Path, "reason", vd.Reason)
		}
	}

	g, ok := k.server.(Grouper)
	if k.server == nil || !ok {
		return k.episodeWaiting(ctx, rec, "no media server that can list an episode's versions is configured", nil)
	}
	ms := k.cfg.Paths.Mappings.MediaServer
	look := [2]string{pathmap.To(ms, primary), pathmap.To(ms, rec.Path)}
	item, grouped, err := episodeGrouped(ctx, k.server, g, look)
	if err != nil {
		return k.failed(ctx, rec, fmt.Errorf("look for the episode in the media server: %w", err))
	}
	if !grouped {
		return k.episodeWaiting(ctx, rec, fmt.Sprintf("the media server does not list %s as a version of the episode's file %s yet", look[1], look[0]), look[:])
	}
	return k.episodeGrouped(ctx, rec, ep.FileID, primary, item)
}

// seriesMoved is the check of a version that is not inside Sonarr's folder for the series,
// which is seriesDir now (see the top of this file).
func (k *Keeper) seriesMoved(ctx context.Context, rec store.Version, v *seriesView, ep arr.Episode, seriesDir string, o checkOpts) (store.Version, []string, error) {
	if _, why := k.series.Status(ctx); why != nil {
		return k.failed(ctx, rec, fmt.Errorf("sonarr's folder for the series is %s now, but its status call fails (%w): not taken for a move", seriesDir, why))
	}
	_, err := os.Lstat(rec.Path)
	switch {
	case err == nil:
		return k.episodeNotice(ctx, rec, store.VersionSeriesMoved, "version.series_moved", fmt.Sprintf(
			"Sonarr's folder for the series is %s now: the version stays at %s, outside it and away from the episode's file (move it next to the episode's file by hand, or remove it)", seriesDir, rec.Path))
	case !errors.Is(err, fs.ErrNotExist):
		return k.failed(ctx, rec, err)
	}
	to, found := k.movedVersion(rec, v, ep, seriesDir)
	if !found {
		return k.episodeNotice(ctx, rec, store.VersionSeriesMoved, "version.series_moved", fmt.Sprintf(
			"Sonarr's folder for the series is %s now, and the version is neither at %s nor next to the episode's file there", seriesDir, rec.Path))
	}
	defer k.locks.Lock(flows.VersionLock(to))()
	if other, err := k.store.VersionByPath(ctx, to); err == nil {
		return k.episodeNotice(ctx, rec, store.VersionSeriesMoved, "version.series_moved", fmt.Sprintf(
			"Sonarr's folder for the series is %s now; a file of the version's name is at %s, but the record of job %s holds that path", seriesDir, to, other.JobID))
	} else if !errors.Is(err, store.ErrNotFound) {
		return k.failed(ctx, rec, err)
	}
	from := rec.Path
	rec.Path = to
	out, err := k.store.PutVersion(ctx, rec, k.entry(rec, "version.followed", map[string]string{
		"title": rec.Title, "titleId": strconv.Itoa(rec.TitleID), "episode": fmt.Sprintf("S%02dE%02d", rec.SeasonNumber, rec.EpisodeNumber),
		"previous": from, "path": to, "series": seriesDir,
	}))
	if err != nil {
		return out, nil, err
	}
	k.log.Info("version_followed", "job", rec.JobID, "from", from, "to", to)
	k.found(from)
	k.mu.Lock()
	delete(k.verdicts, from)
	k.mu.Unlock()
	return k.checkEpisode(ctx, out, o)
}

// movedVersion looks for the version's file next to the episode's file in Sonarr's folder for
// the series, then next to the files of the season's other episodes.
func (k *Keeper) movedVersion(rec store.Version, v *seriesView, ep arr.Episode, seriesDir string) (string, bool) {
	m := k.cfg.Paths.Mappings.Sonarr
	var dirs []string
	add := func(fileID int) {
		if f, ok := v.files[fileID]; fileID != 0 && ok && strings.TrimSpace(f.Path) != "" {
			if d := filepath.Dir(fromApp(m, f.Path)); !slices.Contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
	}
	add(ep.FileID)
	others := make([]arr.Episode, 0, len(v.eps))
	for _, e := range v.eps {
		if e.SeasonNumber == rec.SeasonNumber && e.ID != ep.ID {
			others = append(others, e)
		}
	}
	slices.SortFunc(others, func(a, b arr.Episode) int { return a.Number - b.Number })
	for _, e := range others {
		add(e.FileID)
	}
	name := filepath.Base(rec.Path)
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if !within(seriesDir, p) {
			continue
		}
		if _, _, in := media.LibraryPath(k.cfg.Paths.LibraryRoots, p); !in {
			continue
		}
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}

// trackedAt is Sonarr's file at path (as Fukku sees it), if any, and the episodes it is the
// file of ("S01E02").
func (v *seriesView) trackedAt(m []config.PathMapping, path string) (arr.EpisodeFile, string, bool) {
	for _, f := range v.files {
		if strings.TrimSpace(f.Path) == "" || fromApp(m, f.Path) != path {
			continue
		}
		var eps []string
		for _, e := range v.eps {
			if e.FileID == f.ID {
				eps = append(eps, fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.Number))
			}
		}
		slices.Sort(eps)
		which := strings.Join(eps, ", ")
		if which == "" {
			which = "no episode"
		}
		return f, which, true
	}
	return arr.EpisodeFile{}, "", false
}

// episodeStorage is the check that the version's mounts are there: the sentinels, and the
// library root that holds the version.
func (k *Keeper) episodeStorage(path string) error {
	if err := fscheck.Sentinels(k.cfg.Paths); err != nil {
		return err
	}
	root, _, ok := media.LibraryPath(k.cfg.Paths.LibraryRoots, path)
	if !ok {
		return fmt.Errorf("the version %s is not inside paths.libraryRoots", path)
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("the library root %s cannot be read", root)
	}
	if !info.IsDir() {
		return fmt.Errorf("the library root %s is not a folder", root)
	}
	return nil
}

// verdictDue reports whether Sonarr's verdict on the version is asked in this check: while it
// is adoptable, when the episode's file is another than at the last verdict, and every
// finalRecheck.
func (k *Keeper) verdictDue(rec store.Version, fileID int) bool {
	if rec.Status == store.VersionAdoptable {
		return true
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	m, ok := k.verdicts[rec.Path]
	return !ok || m.fileID != fileID || k.now().Sub(m.at) >= finalRecheck(k.cfg)
}

func (k *Keeper) verdictAsked(path string, fileID int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.verdicts[path] = verdictMark{at: k.now(), fileID: fileID}
}

// episodeVerdict asks Sonarr's verdict on the version where it is: the proposal of its season
// folder (once per pass), re-checked for the episode.
func (k *Keeper) episodeVerdict(ctx context.Context, rec store.Version, c *sonarrCache) (flows.Verdict, error) {
	m := k.cfg.Paths.Mappings.Sonarr
	items, err := c.proposal(ctx, k.series, pathmap.To(m, filepath.Dir(rec.Path)))
	if err != nil {
		return flows.Verdict{}, err
	}
	return flows.EpisodeVerdictIn(ctx, k.series, items, pathmap.To(m, rec.Path), rec.TitleID, rec.EpisodeID, rec.SeasonNumber, rec.EpisodeNumber)
}

func (k *Keeper) episodeDetail(rec store.Version) map[string]string {
	return map[string]string{
		"title": rec.Title, "titleId": strconv.Itoa(rec.TitleID), "path": rec.Path,
		"episode": fmt.Sprintf("S%02dE%02d", rec.SeasonNumber, rec.EpisodeNumber), "episodeId": strconv.Itoa(rec.EpisodeID),
	}
}

// episodeNotice records a status of the re-check (see the top of this file) with its audit
// entry and log event, in the same write, when it changes.
func (k *Keeper) episodeNotice(ctx context.Context, rec store.Version, status, action, why string) (store.Version, []string, error) {
	was := rec.Status
	rec.Status, rec.Detail = status, why
	var entries []store.AuditEntry
	if was != status {
		d := k.episodeDetail(rec)
		d["reason"], d["previous"] = why, was
		entries = append(entries, k.entry(rec, action, d))
	}
	out, err := k.store.PutVersion(ctx, rec, entries...)
	if err == nil && was != status {
		k.log.Warn("version_"+status, "job", rec.JobID, "path", rec.Path, "episode", fmt.Sprintf("S%02dE%02d", rec.SeasonNumber, rec.EpisodeNumber), "reason", why)
	}
	return out, nil, err
}

// recovered is the audit entry of a version that comes back from a notice status.
func (k *Keeper) recovered(rec store.Version, was string) []store.AuditEntry {
	if !noticeStatus(was) {
		return nil
	}
	d := k.episodeDetail(rec)
	d["previous"], d["status"] = was, rec.Status
	k.log.Info("version_recovered", "job", rec.JobID, "path", rec.Path, "was", was)
	return []store.AuditEntry{k.entry(rec, "version.recovered", d)}
}

// episodeWaiting records that the version is fine in Sonarr but not grouped now, and why;
// missing are the paths a pass refreshes.
func (k *Keeper) episodeWaiting(ctx context.Context, rec store.Version, why string, missing []string) (store.Version, []string, error) {
	was := rec.Status
	rec.Status, rec.Detail = store.VersionWaiting, why
	out, err := k.store.PutVersion(ctx, rec, k.recovered(rec, was)...)
	if err == nil && was != store.VersionWaiting {
		k.log.Warn("version_waiting", "job", rec.JobID, "path", rec.Path, "reason", why)
	}
	return out, missing, err
}

// episodeGrouped records the version as grouped with the episode's file fileID (primary, as
// Fukku sees it) in the media server's item.
func (k *Keeper) episodeGrouped(ctx context.Context, rec store.Version, fileID int, primary, item string) (store.Version, []string, error) {
	was, now := rec.Status, rec.CheckedAt
	first := rec.GroupedAt.IsZero()
	changed := rec.PrimaryFileID != 0 && rec.PrimaryFileID != fileID
	rec.Status, rec.Detail = store.VersionGrouped, ""
	if first || changed {
		rec.GroupedAt = now
	}
	if changed {
		rec.PrimaryChangedAt = now
	}
	rec.PrimaryFileID, rec.PrimaryPath, rec.PrimaryItemID, rec.ItemID = fileID, primary, item, item
	var entries []store.AuditEntry
	if first {
		d := k.episodeDetail(rec)
		d["primary"], d["primaryFileId"], d["item"] = primary, strconv.Itoa(fileID), item
		entries = append(entries, k.entry(rec, "version.grouped", d))
	}
	entries = append(entries, k.recovered(rec, was)...)
	out, err := k.store.PutVersion(ctx, rec, entries...)
	if err == nil && first {
		k.log.Info("version_grouped", "job", rec.JobID, "path", rec.Path, "item", item)
	}
	return out, nil, err
}

// fromApp is an app's path as Fukku sees it, cleaned (a Windows app's separators read as /).
func fromApp(m []config.PathMapping, p string) string {
	return filepath.Clean(pathmap.From(m, strings.ReplaceAll(p, `\`, "/")))
}

// within reports whether p is inside the folder dir.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// leftAlone says what became of the version's file: Fukku leaves it as it is.
func leftAlone(path string) string {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return "the version file is gone too"
	}
	return "the version is left on disk at " + path + ": remove it by hand"
}
