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
//   - the series: Sonarr's series id must still be the version's show. A 404, or a series
//     with another TVDB id than the record's (a restored or rebuilt Sonarr, another
//     instance), is looked for again by the TVDB id (GET /series?tvdbId=), and the record
//     follows a show added back under a new id (audit version.reattached). The episode is
//     found by its id, else by its season and number (Sonarr gives an episode a new id when
//     its metadata is rebuilt; audit version.reattached too).
//   - series_deleted: none of that finds the series or the episode. The file, if still
//     there, is left alone.
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
//     before the version was placed, asked of the file where it is, from Sonarr's proposal
//     of the season folder with filterExistingFiles=true) no longer rejects it as not an upgrade: its
//     next rescan takes the version for the episode's file. Asked only with
//     versions.requireNotUpgradeVerdict, when the episode's file changed, every
//     finalRecheck, and on every pass while the version is adoptable. A version stays
//     adoptable until a verdict rejects it again: no verdict (the call failed, Sonarr gives
//     none) decides nothing. A failed verdict backs off by season folder, as an unlisted
//     version does, and the check goes on without it.
//   - otherwise the version is grouped once the media server lists both files as one
//     episode, or waiting (the server is refreshed once per pass, and a version it still does
//     not list backs off, as films' do: a version placed again at a path Jellyfin knew can
//     stay unlisted for minutes).
//
// Fukku never deletes, moves, renames or places a library file for any of them, and never
// asks Sonarr to rescan. adopted, series_deleted and missing are checked again every
// finalRecheck; the others on every pass (series_moved too: a move of Sonarr's may be under
// way). Whatever the check finds, it decides nothing from a passing fault, as for films:
// while a paths.sentinels file, the library root that holds the version (paths.libraryRoots)
// or Sonarr's folder for the series is missing, or Sonarr does not answer, the record keeps
// its status and its detail says why; a status that comes from Sonarr's answer
// (series_deleted, series_moved, adopted) needs Sonarr's own status call to answer, asked
// again for each decision. Sonarr's slow calls (the series, its episodes and files, the
// verdict) are made before the version's lock is taken (prepareEpisode), once per pass for
// each series and season folder. A record whose job is still running (or stalled while
// importing or post-processing) is the job's step's.

// verdictMark is when Sonarr's verdict on an episode's version was last asked, and the
// episode's file it was asked against. In memory only: a restart asks again.
type verdictMark struct {
	at     time.Time
	fileID int
}

// sonarrFault is a check that failed because Sonarr did not answer: logged once per outage.
type sonarrFault struct{ err error }

func (e sonarrFault) Error() string { return "sonarr: " + e.err.Error() }
func (e sonarrFault) Unwrap() error { return e.err }

// sonarrCache is what a pass read of Sonarr, so that the versions of one series (one season
// folder) cost one set of calls (one proposal).
type sonarrCache struct {
	series    map[int]*seriesView
	proposals map[string]proposalAnswer
	verdicts  map[string]verdictResult // by version path: asked by prepareEpisode
	failed    map[string]bool          // season folders whose verdict failed in this pass
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

// verdictResult is Sonarr's verdict on a version, asked against the episode's file fileID.
type verdictResult struct {
	fileID int
	vd     flows.Verdict
	err    error
}

func (c *sonarrCache) seriesOf(ctx context.Context, sm arr.SeriesManager, id int) *seriesView {
	if c != nil {
		if v, ok := c.series[id]; ok {
			return v
		}
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

// proposal is Sonarr's proposal of a season folder, asked without the files it tracks
// (filterExistingFiles=true) when the client can: the episodes' own files need no verdict,
// and each file of a proposal costs Sonarr a media-info read. Probe M3-episode-version-recheck:
// Sonarr 4.0.20 gives the same verdict on the version so, but still lists a season folder's
// tracked files, so this saves nothing there yet.
func (c *sonarrCache) proposal(ctx context.Context, sm arr.SeriesManager, folder string) ([]arr.Row, error) {
	if c != nil {
		if a, ok := c.proposals[folder]; ok {
			return a.items, a.err
		}
	}
	var items []arr.Row
	var err error
	if u, ok := sm.(arr.UntrackedProposer); ok {
		items, err = u.UntrackedProposal(ctx, folder)
	} else {
		items, err = sm.Proposal(ctx, folder)
	}
	if c != nil && ctx.Err() == nil {
		if c.proposals == nil {
			c.proposals = map[string]proposalAnswer{}
		}
		c.proposals[folder] = proposalAnswer{items, err}
	}
	return items, err
}

// verdictFor is the verdict prepareEpisode asked in this pass for the version at path against
// the episode's file fileID.
func (c *sonarrCache) verdictFor(path string, fileID int) (verdictResult, bool) {
	if c == nil {
		return verdictResult{}, false
	}
	r, ok := c.verdicts[path]
	return r, ok && r.fileID == fileID
}

// otherShow says why Sonarr's series is not the version's show (another TVDB id); "" when it
// may be.
func (v *seriesView) otherShow(rec store.Version) string {
	if rec.TVDBID != 0 && v.series.TVDBID != 0 && v.series.TVDBID != rec.TVDBID {
		return fmt.Sprintf("sonarr's series %d is another show now (TVDB %d, the version's is TVDB %d)", rec.TitleID, v.series.TVDBID, rec.TVDBID)
	}
	return ""
}

// episodeFor is the version's episode in the series: by its id, else by its season and number
// (byNumber: the episode has another id now).
func (v *seriesView) episodeFor(rec store.Version) (ep arr.Episode, found, byNumber bool) {
	if e, ok := v.eps[rec.EpisodeID]; ok {
		return e, true, false
	}
	var match []arr.Episode
	for _, e := range v.eps {
		if e.SeasonNumber == rec.SeasonNumber && e.Number == rec.EpisodeNumber {
			match = append(match, e)
		}
	}
	if len(match) != 1 {
		return arr.Episode{}, false, false
	}
	return match[0], true, true
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
// importing or post-processing, or stalled there (it resumes into the step), and its step
// mediaServerEpisodeVersions writes the record.
func (k *Keeper) stepOwns(ctx context.Context, rec store.Version) bool {
	if rec.App != flows.AppSonarr {
		return false
	}
	job, err := k.store.Job(ctx, rec.JobID)
	if err != nil {
		return false
	}
	running := []store.State{store.Importing, store.Imported, store.PostProcessing}
	return slices.Contains(running, job.State) || job.State == store.Stalled && slices.Contains(running, job.Resume)
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

// prepareEpisode makes Sonarr's slow calls for the check of an episode's version, before its
// lock is taken: the series (once per pass), and the verdict when it is due and its season
// folder is not backing off (one proposal per folder and pass). checkEpisode decides from
// them.
func (k *Keeper) prepareEpisode(ctx context.Context, rec store.Version, o checkOpts) {
	if k.series == nil || o.sonarr == nil {
		return
	}
	v := o.sonarr.seriesOf(ctx, k.series, rec.TitleID)
	if !k.cfg.Versions.RequireNotUpgradeVerdict || v.err != nil || v.gone != "" || v.otherShow(rec) != "" {
		return
	}
	ep, found, _ := v.episodeFor(rec)
	if !found || ep.FileID == 0 {
		return
	}
	if _, has := v.files[ep.FileID]; !has || !k.verdictDue(rec, ep.FileID) {
		return
	}
	folder := pathmap.To(k.cfg.Paths.Mappings.Sonarr, filepath.Dir(rec.Path))
	if k.backingOffIn(k.vfails, folder) {
		return
	}
	vd, err := k.episodeVerdict(ctx, rec, ep.ID, o.sonarr)
	if ctx.Err() != nil {
		return
	}
	c := o.sonarr
	if c.verdicts == nil {
		c.verdicts, c.failed = map[string]verdictResult{}, map[string]bool{}
	}
	c.verdicts[rec.Path] = verdictResult{fileID: ep.FileID, vd: vd, err: err}
	switch {
	case err != nil && !c.failed[folder]:
		c.failed[folder] = true
		k.missedIn(k.vfails, folder)
		k.log.Warn("version_verdict_failed", "folder", folder, "error", err)
	case err == nil:
		k.foundIn(k.vfails, folder)
	}
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
		return k.failed(ctx, rec, sonarrFault{v.err})
	}
	k.sonarrAnswers()
	gone := v.gone
	if gone == "" {
		gone = v.otherShow(rec)
	}
	seriesGone := gone != "" // the series, not only the episode
	ep, byNumber := arr.Episode{}, false
	if gone == "" {
		var found bool
		if ep, found, byNumber = v.episodeFor(rec); !found {
			gone = fmt.Sprintf("sonarr's series %d has no episode %d, nor one S%02dE%02d", rec.TitleID, rec.EpisodeID, rec.SeasonNumber, rec.EpisodeNumber)
		}
	}
	if gone != "" {
		// Asked again for each decision: a wrong URL base or proxy answers 404 to it too.
		if _, why := k.series.Status(ctx); why != nil {
			return k.failed(ctx, rec, fmt.Errorf("%s, and its status call fails too (%w): not taken for a deleted series", gone, why))
		}
		if seriesGone {
			s, found, err := k.seriesAgain(ctx, rec)
			if err != nil {
				return k.failed(ctx, rec, sonarrFault{err})
			}
			if found {
				return k.reattachSeries(ctx, rec, s, o)
			}
		}
		return k.episodeNotice(ctx, rec, store.VersionSeriesDeleted, "version.series_deleted",
			"Sonarr no longer has the series or the episode ("+gone+"); "+leftAlone(rec.Path))
	}
	if byNumber {
		from := rec.EpisodeID
		rec.EpisodeID = ep.ID
		d := k.episodeDetail(rec)
		d["previousEpisodeId"] = strconv.Itoa(from)
		out, err := k.store.PutVersion(ctx, rec, k.entry(rec, "version.reattached", d))
		if err != nil {
			return out, nil, err
		}
		rec = out
		k.log.Info("version_reattached", "job", rec.JobID, "path", rec.Path, "episode", d["episode"], "from", from, "to", ep.ID)
	}
	if rec.TVDBID == 0 && v.series.TVDBID != 0 {
		rec.TVDBID = v.series.TVDBID // learnt now; written with the record below
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

	// Sonarr's verdict, as prepareEpisode asked it.
	rejected, note := true, "" // rejected: Sonarr rejects the version (or need not be asked)
	if k.cfg.Versions.RequireNotUpgradeVerdict && k.verdictDue(rec, ep.FileID) {
		rejected = false
		switch r, asked := o.sonarr.verdictFor(rec.Path, ep.FileID); {
		case !asked:
			if k.backingOffIn(k.vfails, pathmap.To(sonarrMap, filepath.Dir(rec.Path))) {
				note = "Sonarr's verdict on the version failed lately: it is asked again later"
			}
		case r.err != nil:
			note = fmt.Sprintf("Sonarr's verdict on the version could not be asked (asked again later): %v", r.err)
		default:
			k.verdictAsked(rec.Path, ep.FileID)
			switch r.vd.Code {
			case flows.CodeVersionUpgrade:
				return k.episodeNotice(ctx, rec, store.VersionAdoptable, "version.adoptable", r.vd.Reason)
			case flows.CodeVersionUnchecked: // no verdict: nothing is decided from it
				k.log.Warn("version_unchecked", "job", rec.JobID, "path", rec.Path, "reason", r.vd.Reason)
				note = "Sonarr gives no verdict on the version: " + r.vd.Reason
			default:
				rejected = true
			}
		}
	}
	if rec.Status == store.VersionAdoptable && !rejected {
		// Still adoptable until a verdict rejects the version again.
		return k.episodeNotice(ctx, rec, store.VersionAdoptable, "version.adoptable", rec.Detail)
	}

	g, ok := k.server.(Grouper)
	if k.server == nil || !ok {
		return k.episodeWaiting(ctx, rec, join("no media server that can list an episode's versions is configured", note), nil)
	}
	ms := k.cfg.Paths.Mappings.MediaServer
	look := [2]string{pathmap.To(ms, primary), pathmap.To(ms, rec.Path)}
	item, grouped, err := episodeGrouped(ctx, k.server, g, look)
	if err != nil {
		return k.failed(ctx, rec, fmt.Errorf("look for the episode in the media server: %w", err))
	}
	if !grouped {
		return k.episodeWaiting(ctx, rec, join(fmt.Sprintf("the media server does not list %s as a version of the episode's file %s yet", look[1], look[0]), note), look[:])
	}
	return k.episodeGrouped(ctx, rec, ep.FileID, primary, item, note)
}

// seriesAgain looks for the version's show under another series id, by its TVDB id (the
// series was deleted and added back, or Sonarr was rebuilt). found is false without a TVDB id,
// a client that can look, or another id.
func (k *Keeper) seriesAgain(ctx context.Context, rec store.Version) (arr.Series, bool, error) {
	f, ok := k.series.(arr.SeriesFinder)
	if !ok || rec.TVDBID == 0 {
		return arr.Series{}, false, nil
	}
	s, found, err := f.SeriesByTVDB(ctx, rec.TVDBID)
	if err != nil || !found || s.ID == rec.TitleID {
		return arr.Series{}, false, err
	}
	return s, true, nil
}

// reattachSeries makes the record follow its show to Sonarr's series s (audit
// version.reattached), and checks it there.
func (k *Keeper) reattachSeries(ctx context.Context, rec store.Version, s arr.Series, o checkOpts) (store.Version, []string, error) {
	from := rec.TitleID
	rec.TitleID = s.ID
	d := k.episodeDetail(rec)
	d["previousTitleId"], d["tvdbId"] = strconv.Itoa(from), strconv.Itoa(rec.TVDBID)
	out, err := k.store.PutVersion(ctx, rec, k.entry(rec, "version.reattached", d))
	if err != nil {
		return out, nil, err
	}
	k.log.Info("version_reattached", "job", rec.JobID, "path", rec.Path, "from", from, "to", s.ID)
	return k.checkEpisode(ctx, out, o)
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

// episodeVerdict asks Sonarr's verdict on the version where it is, as the episode episodeID:
// the proposal of its season folder (once per pass), re-checked for the episode.
func (k *Keeper) episodeVerdict(ctx context.Context, rec store.Version, episodeID int, c *sonarrCache) (flows.Verdict, error) {
	m := k.cfg.Paths.Mappings.Sonarr
	items, err := c.proposal(ctx, k.series, pathmap.To(m, filepath.Dir(rec.Path)))
	if err != nil {
		return flows.Verdict{}, err
	}
	return flows.EpisodeVerdictIn(ctx, k.series, items, pathmap.To(m, rec.Path), rec.TitleID, episodeID, rec.SeasonNumber, rec.EpisodeNumber)
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
// Fukku sees it) in the media server's item; note is the detail (a verdict not asked).
func (k *Keeper) episodeGrouped(ctx context.Context, rec store.Version, fileID int, primary, item, note string) (store.Version, []string, error) {
	was, now := rec.Status, rec.CheckedAt
	first := rec.GroupedAt.IsZero()
	changed := rec.PrimaryFileID != 0 && rec.PrimaryFileID != fileID
	rec.Status, rec.Detail = store.VersionGrouped, note
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

// join is why, and the note after it when there is one.
func join(why, note string) string {
	if note == "" {
		return why
	}
	return why + "; " + note
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
