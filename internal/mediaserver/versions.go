package mediaserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/fscheck"
	"github.com/segun-io/fukku/internal/pathmap"
	"github.com/segun-io/fukku/internal/store"
)

// The grouping of films' second versions (#56, decision #20). A version lives outside
// Radarr's root folders (package flows places it); the media server shows it as a version
// of the film's file only once the two items are merged (Jellyfin's MergeVersions). The
// merge survives scans and metadata refreshes (probe J2) but not a rename or an upgrade of
// the film's file by Radarr (probes B3e, M3-versions), which can happen at any time,
// through Fukku or not (an RSS grab, a search, a completed download, B3f). So the grouping
// is checked:
//
//   - by the post-processing step of the job that placed the version (VersionStep, the
//     first grouping), which refreshes the server and waits for it to list both files;
//   - after each of Fukku's own imports, for the film's versions (Keeper.Nudge);
//   - on a schedule, every versions.regroupInterval, for every version (Keeper.Run).
//
// A check asks Radarr for the film's current file, finds both files in the media server,
// and merges them again when the version is not among the film item's sources. A scheduled
// pass refreshes the server at most once, for every file missing there, waits once (up to
// mediaServer.itemWait) for all of them together, and then looks again.
//
// Two statuses stop the regular checks: film_deleted (Radarr answers, and has no film of
// that id or of the version's TMDB id) and missing (the version's file is gone). Neither
// may come from a passing fault, so:
//
//   - while a sentinel (paths.sentinels) or the folder the version was placed in is
//     missing, nothing is decided: the record keeps its status and its detail says why;
//   - a 404 for the film counts only once Radarr's own status call answers in the same pass
//     (a wrong URL base or proxy answers 404 to that too);
//   - a film added back to Radarr under a new id is found again by its TMDB id
//     (GET /movie?tmdbId=), and the version follows it (audit version.reattached);
//   - film_deleted and missing are checked again every finalRecheck, so a version comes back
//     on its own once its film or its file does.

// VersionStepName is the name of the post-processing step of a second-version job.
const VersionStepName = "mediaServerVersions"

// finalRecheck is how often a film_deleted or missing version is checked again: six
// scheduled passes, at least an hour.
func finalRecheck(cfg config.Config) time.Duration {
	return max(6*cfg.Versions.RegroupInterval.D(), time.Hour)
}

// Keeper checks and re-applies the grouping of second versions. Build it with NewKeeper.
type Keeper struct {
	store  store.Store
	movies arr.MovieManager // nil: Radarr is not configured
	server MediaServer      // nil: no media server
	cfg    config.Config
	log    *slog.Logger
	now    func() time.Time
	locks  *flows.JobLocks // shared with the placement (flows.VersionLock)
	nudges chan int        // Radarr movie ids to check soon
}

// NewKeeper builds a keeper. server should be a Grouper; without one every check waits.
// locks must be the JobLocks the import engine uses (nil: its own, for tests).
func NewKeeper(cfg config.Config, log *slog.Logger, st store.Store, movies arr.MovieManager, server MediaServer, locks *flows.JobLocks) *Keeper {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if locks == nil {
		locks = flows.NewJobLocks()
	}
	return &Keeper{store: st, movies: movies, server: server, cfg: cfg, log: log, now: time.Now,
		locks: locks, nudges: make(chan int, 64)}
}

// Run checks every version at once, then every versions.regroupInterval (0: never on a
// schedule) and whenever a film is nudged, until ctx ends. It runs whatever
// versions.enabled says: versions placed before it was turned off still need their grouping.
func (k *Keeper) Run(ctx context.Context) error {
	var tick <-chan time.Time
	if d := k.cfg.Versions.RegroupInterval.D(); d > 0 {
		t := time.NewTicker(d)
		defer t.Stop()
		tick = t.C
	}
	k.pass(ctx, 0)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			k.pass(ctx, 0)
		case id := <-k.nudges:
			k.pass(ctx, id)
		}
	}
}

// Nudge asks for a check of the versions of the target's film soon (after an import of
// Fukku's, which may have replaced the film's file). It never blocks: when many are
// waiting, the next scheduled check covers it.
func (k *Keeper) Nudge(t store.Target) {
	if t.App != flows.AppRadarr || t.TitleID == 0 {
		return
	}
	select {
	case k.nudges <- t.TitleID:
	default:
	}
}

// due reports whether a pass checks rec: linked, grouped and waiting ones always (a linked
// one is normally grouped by its job's step; one whose step gave up is taken over here, and
// a check reads the record under the version's lock, so the two never group it twice);
// film_deleted and missing ones every finalRecheck, or when their film is nudged; never one
// being placed.
func (k *Keeper) due(rec store.Version, titleID int) bool {
	if titleID != 0 && (rec.App != flows.AppRadarr || rec.TitleID != titleID) {
		return false
	}
	switch rec.Status {
	case store.VersionLinked, store.VersionGrouped, store.VersionWaiting:
		return true
	case store.VersionFilmDeleted, store.VersionMissing:
		return titleID != 0 || k.now().Sub(rec.CheckedAt) >= finalRecheck(k.cfg)
	}
	return false
}

// pass checks the due versions: every one, or the film's (titleID ≠ 0). See the top of
// this file for the one refresh and the one wait.
func (k *Keeper) pass(ctx context.Context, titleID int) {
	recs, err := k.store.AllVersions(ctx)
	if err != nil {
		if ctx.Err() == nil {
			k.log.Error("version_check_failed", "error", err)
		}
		return
	}
	var due []store.Version
	for _, rec := range recs {
		if k.due(rec, titleID) {
			due = append(due, rec)
		}
	}
	if len(due) == 0 {
		return
	}
	p := &passState{}
	var again []string
	var missing []string
	for _, rec := range due {
		if ctx.Err() != nil {
			return
		}
		_, miss, err := k.check(ctx, rec.Path, p, checkOpts{})
		if err != nil && ctx.Err() == nil {
			k.log.Warn("version_check_failed", "job", rec.JobID, "title", rec.TitleID, "path", rec.Path, "error", err)
		}
		if len(miss) > 0 {
			again = append(again, rec.Path)
			for _, m := range miss {
				if !slices.Contains(missing, m) {
					missing = append(missing, m)
				}
			}
		}
	}
	if len(missing) == 0 || ctx.Err() != nil || k.cfg.MediaServer.RefreshMethod == MethodNone {
		return
	}
	if err := k.refreshOnce(ctx, missing); err != nil {
		if ctx.Err() == nil {
			k.log.Warn("version_check_failed", "error", err, "missing", len(missing))
		}
		return
	}
	s := &Step{Server: k.server, Config: k.cfg}
	if _, _, err := s.waitFor(ctx, missing, k.cfg.MediaServer.ItemWait.D(), k.poll()); err != nil {
		return
	}
	for _, path := range again {
		if _, _, err := k.check(ctx, path, p, checkOpts{}); err != nil && ctx.Err() == nil {
			k.log.Warn("version_check_failed", "path", path, "error", err)
		}
	}
}

// refreshOnce tells the server about the files a pass found missing: one Refresh for all
// of them, or, with mediaServer.refreshMethod libraryScan, one Scan, which starts nothing
// while a scan is running. none tells it nothing.
func (k *Keeper) refreshOnce(ctx context.Context, paths []string) error {
	switch k.cfg.MediaServer.RefreshMethod {
	case MethodNone:
		return nil
	case MethodLibraryScan:
		started, err := k.server.Scan(ctx)
		if err == nil {
			k.log.Info("media_server_fallback_scan", "outcome", map[bool]string{true: "started", false: "already_running"}[started], "for", "versions")
		}
		return err
	}
	if err := k.server.Refresh(ctx, paths); err != nil {
		return fmt.Errorf("refresh the media server: %w", err)
	}
	return nil
}

func (k *Keeper) poll() time.Duration {
	if d := k.cfg.MediaServer.ItemPollInterval.D(); d > 0 {
		return d
	}
	return 2 * time.Second
}

// passState is what one pass learns once: whether Radarr's status call answers.
type passState struct {
	asked, up bool
	why       error
}

func (k *Keeper) radarrUp(ctx context.Context, p *passState) (bool, error) {
	if !p.asked {
		p.asked = true
		_, p.why = k.movies.Status(ctx)
		p.up = p.why == nil
	}
	return p.up, p.why
}

// checkOpts: refresh makes a check tell the server about a missing file itself and wait up
// to wait for it (the job's step); with scan, mediaServer.refreshFallback then scans every
// library once. Without refresh (a scheduled pass) the check reports the missing paths.
type checkOpts struct {
	refresh, scan bool
	wait          time.Duration
}

// Check checks the grouping of the version at path and re-applies it (see the top of this
// file), telling the server about a missing file and waiting up to wait for it; with scan
// (and mediaServer.refreshFallback), a file still missing then makes it scan every library
// once and wait again. It returns the record as it is after. An error is a check that could
// not be made (an app did not answer, a mount is missing): the record keeps its status,
// and its detail says why.
func (k *Keeper) Check(ctx context.Context, path string, wait time.Duration, scan bool) (store.Version, error) {
	rec, _, err := k.check(ctx, path, &passState{}, checkOpts{refresh: true, scan: scan, wait: wait})
	return rec, err
}

// check is Check; without opts.refresh it reports the server's paths it found missing.
func (k *Keeper) check(ctx context.Context, path string, p *passState, o checkOpts) (store.Version, []string, error) {
	defer k.locks.Lock(flows.VersionLock(path))()
	rec, err := k.store.VersionByPath(ctx, path)
	if err != nil {
		return rec, nil, err
	}
	rec.CheckedAt = k.now()
	// No decision while a mount may be missing: a late NAS must not mark every version gone.
	if err := k.storage(rec); err != nil {
		return k.failed(ctx, rec, fmt.Errorf("not checked: %w", err))
	}
	g, ok := k.server.(Grouper)
	if k.server == nil || !ok {
		rec, err := k.waiting(ctx, rec, "no media server that can group versions is configured")
		return rec, nil, err
	}
	if k.movies == nil {
		return k.failed(ctx, rec, errors.New("radarr is not configured"))
	}
	m, err := k.movies.Movie(ctx, rec.TitleID)
	if arr.StatusOf(err) == http.StatusNotFound {
		if up, why := k.radarrUp(ctx, p); !up {
			return k.failed(ctx, rec, fmt.Errorf("radarr answers 404 for movie %d, and its status call fails too (%w): not taken for a deleted film", rec.TitleID, why))
		}
		var found bool
		if m, found, err = k.findAgain(ctx, rec); err != nil {
			return k.failed(ctx, rec, fmt.Errorf("radarr: %w", err))
		}
		if !found {
			rec, err := k.final(ctx, rec, store.VersionFilmDeleted, "version.orphaned",
				"Radarr no longer has the film: the version is left on disk, remove it by hand")
			return rec, nil, err
		}
		from := rec.TitleID
		rec.TitleID = m.ID
		if rec, err = k.store.PutVersion(ctx, rec, k.entry(rec, "version.reattached", map[string]string{
			"previousTitleId": strconv.Itoa(from), "tmdbId": strconv.Itoa(rec.TMDBID),
		})); err != nil {
			return rec, nil, err
		}
		k.log.Info("version_reattached", "job", rec.JobID, "path", rec.Path, "from", from, "to", m.ID)
	} else if err != nil {
		return k.failed(ctx, rec, fmt.Errorf("radarr: %w", err))
	}
	if _, err := os.Lstat(rec.Path); errors.Is(err, fs.ErrNotExist) {
		rec, err := k.final(ctx, rec, store.VersionMissing, "version.missing", "the version file is no longer on disk")
		return rec, nil, err
	} else if err != nil {
		return k.failed(ctx, rec, err)
	}
	if m.File == nil || m.File.Path == "" {
		rec, err := k.waiting(ctx, rec, "Radarr has no file for the film now")
		return rec, nil, err
	}
	primary := pathmap.From(k.cfg.Paths.Mappings.Radarr, m.File.Path)
	ms := k.cfg.Paths.Mappings.MediaServer
	look := []string{pathmap.To(ms, primary), pathmap.To(ms, rec.Path)}
	var ids []string
	if o.refresh {
		ids, err = k.items(ctx, look, o.wait, o.scan)
	} else {
		ids, _, err = (&Step{Server: k.server, Config: k.cfg}).find(ctx, look)
	}
	if err != nil {
		return k.failed(ctx, rec, err)
	}
	if slices.Contains(ids, "") {
		var missing []string
		for i, id := range ids {
			if id == "" {
				missing = append(missing, look[i])
			}
		}
		rec, err := k.waiting(ctx, rec, fmt.Sprintf("the media server does not list %s yet (is it in one of its libraries, as paths.mappings.mediaServer maps it?)", missing[0]))
		return rec, missing, err
	}
	sources, err := g.Sources(ctx, ids[0])
	if err != nil {
		return k.failed(ctx, rec, err)
	}
	merged := false
	if !slices.Contains(sources, look[1]) {
		if err := g.MergeVersions(ctx, ids); err != nil {
			return k.failed(ctx, rec, fmt.Errorf("merge the versions: %w", err))
		}
		merged = true
		if sources, err = g.Sources(ctx, ids[0]); err != nil {
			return k.failed(ctx, rec, err)
		}
		if !slices.Contains(sources, look[1]) {
			rec, err := k.waiting(ctx, rec, "the media server did not group the version with the film's file after merging them")
			return rec, nil, err
		}
	}

	now := rec.CheckedAt
	first := rec.GroupedAt.IsZero()
	changed := rec.PrimaryFileID != 0 && (rec.PrimaryFileID != m.File.ID || rec.PrimaryPath != primary)
	old, was := rec.PrimaryPath, rec.Status
	rec.Status, rec.Detail = store.VersionGrouped, ""
	rec.PrimaryFileID, rec.PrimaryPath, rec.PrimaryItemID, rec.ItemID = m.File.ID, primary, ids[0], ids[1]
	if merged || first {
		rec.GroupedAt = now
	}
	if changed {
		rec.PrimaryChangedAt = now
	}
	detail := map[string]string{
		"title": rec.Title, "titleId": strconv.Itoa(rec.TitleID), "path": rec.Path,
		"primary": primary, "primaryFileId": strconv.Itoa(m.File.ID), "item": ids[0],
	}
	var entries []store.AuditEntry
	switch {
	case first:
		entries = append(entries, k.entry(rec, "version.grouped", detail))
	case merged:
		// The grouping was lost: Radarr renamed or replaced the film's file, or the media
		// server dropped it.
		detail["previous"], detail["fileChanged"] = old, strconv.FormatBool(changed)
		entries = append(entries, k.entry(rec, "version.regrouped", detail))
	}
	if rec, err = k.store.PutVersion(ctx, rec, entries...); err != nil {
		return rec, nil, err
	}
	switch {
	case first:
		k.log.Info("version_grouped", "job", rec.JobID, "path", rec.Path, "item", ids[0])
	case merged:
		k.log.Info("version_regrouped", "job", rec.JobID, "path", rec.Path, "item", ids[0], "previous", old, "fileChanged", changed)
	}
	if was == store.VersionFilmDeleted || was == store.VersionMissing {
		k.log.Info("version_recovered", "job", rec.JobID, "path", rec.Path, "was", was)
	}
	return rec, nil, nil
}

// storage is the check that the version's mounts are there: the sentinels, and the folder
// the version was placed in (versions.root then; versions.root may have changed since).
func (k *Keeper) storage(rec store.Version) error {
	if err := fscheck.Sentinels(k.cfg.Paths); err != nil {
		return err
	}
	root := filepath.Dir(filepath.Dir(rec.Path))
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("the versions folder %s cannot be read", root)
	}
	if !info.IsDir() {
		return fmt.Errorf("the versions folder %s is not a folder", root)
	}
	return nil
}

// findAgain finds the version's film under another id, by its TMDB id (the film was deleted
// and added back to Radarr). found is false without a TMDB id or a client that can look.
func (k *Keeper) findAgain(ctx context.Context, rec store.Version) (arr.Movie, bool, error) {
	f, ok := k.movies.(arr.MovieFinder)
	if !ok || rec.TMDBID == 0 {
		return arr.Movie{}, false, nil
	}
	return f.MovieByTMDB(ctx, rec.TMDBID)
}

// items finds the paths' items, refreshing the server when one is missing and waiting up to
// wait for them; with scan, it scans every library once more (mediaServer.refreshFallback).
// A missing item has id "".
func (k *Keeper) items(ctx context.Context, look []string, wait time.Duration, scan bool) ([]string, error) {
	s := &Step{Server: k.server, Config: k.cfg}
	ids, missing, err := s.find(ctx, look)
	if err != nil || missing == 0 {
		return ids, err
	}
	if err := k.server.Refresh(ctx, look); err != nil {
		return nil, fmt.Errorf("refresh the media server: %w", err)
	}
	if ids, missing, err = s.waitFor(ctx, look, wait, k.poll()); err != nil || missing == 0 {
		return ids, err
	}
	ms := k.cfg.MediaServer
	if scan && ms.RefreshFallback && ms.RefreshMethod != MethodLibraryScan && ms.RefreshMethod != MethodNone {
		started, err := k.server.Scan(ctx)
		if err != nil {
			return nil, fmt.Errorf("the fallback library scan failed: %w", err)
		}
		k.log.Info("media_server_fallback_scan", "outcome", map[bool]string{true: "started", false: "already_running"}[started], "for", "version")
		ids, _, err = s.waitFor(ctx, look, wait, k.poll())
		return ids, err
	}
	return ids, nil
}

// waiting records that the version is not grouped now, and why.
func (k *Keeper) waiting(ctx context.Context, rec store.Version, why string) (store.Version, error) {
	was := rec.Status
	rec.Status, rec.Detail = store.VersionWaiting, why
	out, err := k.store.PutVersion(ctx, rec)
	if err == nil && was != store.VersionWaiting {
		k.log.Warn("version_waiting", "job", rec.JobID, "path", rec.Path, "reason", why)
	}
	return out, err
}

// failed records why a check could not be made, keeping the status, and returns cause.
func (k *Keeper) failed(ctx context.Context, rec store.Version, cause error) (store.Version, []string, error) {
	if ctx.Err() != nil {
		return rec, nil, ctx.Err()
	}
	rec.Detail = cause.Error()
	out, err := k.store.PutVersion(ctx, rec)
	return out, nil, errors.Join(cause, err)
}

// final records a status that ends the regular checks (film_deleted, missing), with an
// audit entry, in the same write, the first time.
func (k *Keeper) final(ctx context.Context, rec store.Version, status, action, why string) (store.Version, error) {
	was := rec.Status
	rec.Status, rec.Detail = status, why
	var entries []store.AuditEntry
	if was != status {
		entries = append(entries, k.entry(rec, action, map[string]string{
			"title": rec.Title, "titleId": strconv.Itoa(rec.TitleID), "path": rec.Path, "reason": why}))
	}
	out, err := k.store.PutVersion(ctx, rec, entries...)
	if err == nil && was != status {
		k.log.Warn("version_"+status, "job", rec.JobID, "path", rec.Path, "reason", why)
	}
	return out, err
}

func (k *Keeper) entry(rec store.Version, action string, d map[string]string) store.AuditEntry {
	return store.AuditEntry{Actor: "system", Action: action, JobID: rec.JobID, Detail: d}
}

var (
	_ flows.PostStep = (*VersionStep)(nil)
	_ flows.Linker   = (*VersionStep)(nil)
)

// VersionStep is the post-processing step of a second-version job (mediaServerVersions):
// the first grouping of the version with the film's file. It waits up to
// mediaServer.itemWait for the media server to list both (with the fallback scan of
// mediaServer.refreshFallback), and reports the film's item ("item <id>"), which gives the
// job its title link. A version not grouped yet is an error the engine retries; a film
// deleted in Radarr or a version gone from disk gives up. Later checks are the keeper's.
type VersionStep struct {
	Keeper *Keeper
	Store  store.Store
	Config config.Config
}

func (s *VersionStep) Name() string { return VersionStepName }

func (s *VersionStep) Link(detail string) (string, bool) {
	l := TitleLink(s.Config.MediaServer.TitleLinkTemplate, ItemID(detail))
	return l, l != ""
}

func (s *VersionStep) Run(ctx context.Context, job store.Job, _ store.Target, _ []store.Row) (bool, string, error) {
	ms := s.Config.MediaServer
	if ms.Provider == "none" || ms.Provider == "" || s.Keeper == nil || s.Keeper.server == nil {
		return true, "", flows.Skip("no media server is configured: the version is not grouped with the film")
	}
	recs, err := s.Store.VersionsOf(ctx, job.ID)
	if err != nil {
		return false, "", err
	}
	if len(recs) == 0 {
		return false, "", flows.GiveUp(errors.New("the job placed no version"))
	}
	rec, err := s.Keeper.Check(ctx, recs[len(recs)-1].Path, ms.ItemWait.D(), true)
	if err != nil {
		return false, "", err
	}
	switch rec.Status {
	case store.VersionGrouped:
		return true, "item " + rec.PrimaryItemID, nil
	case store.VersionFilmDeleted, store.VersionMissing:
		return false, "", flows.GiveUp(errors.New(rec.Detail))
	}
	return false, "", fmt.Errorf("%w: %s", ErrNotVisible, rec.Detail)
}
