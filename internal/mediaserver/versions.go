package mediaserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/pathmap"
	"github.com/segun-io/fukku/internal/store"
)

// The grouping of films' second versions (#56, decision #20). A version lives outside
// Radarr's root folders (package flows places it); the media server shows it as a version
// of the film's file only once the two items are merged (Jellyfin's MergeVersions). The
// merge survives scans and metadata refreshes (probe J2) but not a rename or an upgrade of
// the film's file by Radarr (probe B3e), which can happen at any time, through Fukku or
// not (an RSS grab, a search, a completed download, B3f). So the grouping is checked:
//
//   - by the post-processing step of the job that placed the version (VersionStep, the
//     first grouping), which waits for the media server to list both files;
//   - after each of Fukku's own imports, for the film's versions (Keeper.Nudge);
//   - on a schedule, every versions.regroupInterval, for every version (Keeper.Run).
//
// A check asks Radarr for the film's current file, finds both files in the media server
// (refreshing it when one is missing), and merges them again when the version is not among
// the film item's sources. The record (store.Version) says what came of it; a film deleted
// in Radarr (404) or a version file gone from disk ends the checks (film_deleted, missing),
// and the version is left as it is.

// VersionStepName is the name of the post-processing step of a second-version job.
const VersionStepName = "mediaServerVersions"

// keeperWait is the longest a scheduled check waits for the media server to list a file
// after a refresh; the next check looks again.
const keeperWait = 30 * time.Second

// Keeper checks and re-applies the grouping of second versions. Build it with NewKeeper.
type Keeper struct {
	store  store.Store
	movies arr.MovieManager // nil: Radarr is not configured
	server MediaServer      // nil: no media server
	cfg    config.Config
	log    *slog.Logger
	now    func() time.Time
	locks  *flows.JobLocks // one check of a version at a time (key: its path)
	nudges chan int        // Radarr movie ids to check soon
}

// NewKeeper builds a keeper. server should be a Grouper; without one every check waits.
func NewKeeper(cfg config.Config, log *slog.Logger, st store.Store, movies arr.MovieManager, server MediaServer) *Keeper {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Keeper{store: st, movies: movies, server: server, cfg: cfg, log: log, now: time.Now,
		locks: flows.NewJobLocks(), nudges: make(chan int, 64)}
}

// Run checks every version at once, then every versions.regroupInterval (0: never on a
// schedule) and whenever a film is nudged, until ctx ends.
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

// pass checks the versions that are linked, grouped or waiting: every one, or the film's
// (titleID ≠ 0). A version being placed is its job's, and film_deleted and missing are
// final. A linked one is normally grouped by its job's step; one whose step gave up is
// taken over here (a check reads the record under the version's lock, so the two never
// group it twice).
func (k *Keeper) pass(ctx context.Context, titleID int) {
	recs, err := k.store.AllVersions(ctx)
	if err != nil {
		if ctx.Err() == nil {
			k.log.Error("version_check_failed", "error", err)
		}
		return
	}
	for _, rec := range recs {
		if ctx.Err() != nil {
			return
		}
		if titleID != 0 && (rec.App != flows.AppRadarr || rec.TitleID != titleID) {
			continue
		}
		switch rec.Status {
		case store.VersionLinked, store.VersionGrouped, store.VersionWaiting:
		default:
			continue
		}
		if _, err := k.Check(ctx, rec.Path, min(k.cfg.MediaServer.ItemWait.D(), keeperWait), false); err != nil && ctx.Err() == nil {
			k.log.Warn("version_check_failed", "job", rec.JobID, "title", rec.TitleID, "path", rec.Path, "error", err)
		}
	}
}

// Check checks the grouping of the version at path and re-applies it (see the top of this
// file), waiting up to wait for the media server to list a file after a refresh; with scan
// (and mediaServer.refreshFallback), a file still missing then makes it scan every library
// once and wait again. It returns the record as it is after. An error is a call that
// failed (an app did not answer, say): the record's status is kept, its detail says why.
func (k *Keeper) Check(ctx context.Context, path string, wait time.Duration, scan bool) (store.Version, error) {
	defer k.locks.Lock("version:" + path)()
	rec, err := k.store.VersionByPath(ctx, path)
	if err != nil {
		return rec, err
	}
	rec.CheckedAt = k.now()
	g, ok := k.server.(Grouper)
	if k.server == nil || !ok {
		return k.waiting(ctx, rec, "no media server that can group versions is configured")
	}
	if k.movies == nil {
		return k.failed(ctx, rec, errors.New("radarr is not configured"))
	}
	m, err := k.movies.Movie(ctx, rec.TitleID)
	if arr.StatusOf(err) == http.StatusNotFound {
		return k.final(ctx, rec, store.VersionFilmDeleted, "version.orphaned",
			"Radarr no longer has the film: the version is left on disk, remove it by hand")
	}
	if err != nil {
		return k.failed(ctx, rec, fmt.Errorf("radarr: %w", err))
	}
	if _, err := os.Lstat(rec.Path); errors.Is(err, fs.ErrNotExist) {
		return k.final(ctx, rec, store.VersionMissing, "version.missing", "the version file is no longer on disk")
	} else if err != nil {
		return k.failed(ctx, rec, err)
	}
	if m.File == nil || m.File.Path == "" {
		return k.waiting(ctx, rec, "Radarr has no file for the film now")
	}
	primary := pathmap.From(k.cfg.Paths.Mappings.Radarr, m.File.Path)
	ms := k.cfg.Paths.Mappings.MediaServer
	look := []string{pathmap.To(ms, primary), pathmap.To(ms, rec.Path)}
	ids, err := k.items(ctx, look, wait, scan)
	if err != nil {
		return k.failed(ctx, rec, err)
	}
	if slices.Contains(ids, "") {
		missing := look[0]
		if ids[0] != "" {
			missing = look[1]
		}
		return k.waiting(ctx, rec, fmt.Sprintf("the media server does not list %s yet (is it in one of its libraries, as paths.mappings.mediaServer maps it?)", missing))
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
			return k.waiting(ctx, rec, "the media server did not group the version with the film's file after merging them")
		}
	}

	now := rec.CheckedAt
	first := rec.GroupedAt.IsZero()
	changed := rec.PrimaryFileID != 0 && (rec.PrimaryFileID != m.File.ID || rec.PrimaryPath != primary)
	old := rec.PrimaryPath
	rec.Status, rec.Detail = store.VersionGrouped, ""
	rec.PrimaryFileID, rec.PrimaryPath, rec.PrimaryItemID, rec.ItemID = m.File.ID, primary, ids[0], ids[1]
	if merged || first {
		rec.GroupedAt = now
	}
	if changed {
		rec.PrimaryChangedAt = now
	}
	if rec, err = k.store.PutVersion(ctx, rec); err != nil {
		return rec, err
	}
	detail := map[string]string{
		"title": rec.Title, "titleId": strconv.Itoa(rec.TitleID), "path": rec.Path,
		"primary": primary, "primaryFileId": strconv.Itoa(m.File.ID), "item": ids[0],
	}
	switch {
	case first:
		k.audit(ctx, rec, "version.grouped", detail)
		k.log.Info("version_grouped", "job", rec.JobID, "path", rec.Path, "item", ids[0])
	case merged:
		// The grouping was lost: Radarr renamed or replaced the film's file, or the media
		// server dropped it.
		detail["previous"], detail["fileChanged"] = old, strconv.FormatBool(changed)
		k.audit(ctx, rec, "version.regrouped", detail)
		k.log.Info("version_regrouped", "job", rec.JobID, "path", rec.Path, "item", ids[0], "previous", old, "fileChanged", changed)
	}
	return rec, nil
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
	poll := k.cfg.MediaServer.ItemPollInterval.D()
	if poll <= 0 {
		poll = 2 * time.Second
	}
	if ids, missing, err = s.waitFor(ctx, look, wait, poll); err != nil || missing == 0 {
		return ids, err
	}
	ms := k.cfg.MediaServer
	if scan && ms.RefreshFallback && ms.RefreshMethod != MethodLibraryScan && ms.RefreshMethod != MethodNone {
		started, err := k.server.Scan(ctx)
		if err != nil {
			return nil, fmt.Errorf("the fallback library scan failed: %w", err)
		}
		k.log.Info("media_server_fallback_scan", "outcome", map[bool]string{true: "started", false: "already_running"}[started], "for", "version")
		ids, _, err = s.waitFor(ctx, look, wait, poll)
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
func (k *Keeper) failed(ctx context.Context, rec store.Version, cause error) (store.Version, error) {
	if ctx.Err() != nil {
		return rec, ctx.Err()
	}
	rec.Detail = cause.Error()
	out, err := k.store.PutVersion(ctx, rec)
	return out, errors.Join(cause, err)
}

// final records a status that ends the checks (film_deleted, missing), with an audit entry
// the first time.
func (k *Keeper) final(ctx context.Context, rec store.Version, status, action, why string) (store.Version, error) {
	was := rec.Status
	rec.Status, rec.Detail = status, why
	out, err := k.store.PutVersion(ctx, rec)
	if err != nil {
		return out, err
	}
	if was != status {
		k.audit(ctx, rec, action, map[string]string{"title": rec.Title, "titleId": strconv.Itoa(rec.TitleID), "path": rec.Path, "reason": why})
		k.log.Warn("version_"+status, "job", rec.JobID, "path", rec.Path, "reason", why)
	}
	return out, nil
}

func (k *Keeper) audit(ctx context.Context, rec store.Version, action string, d map[string]string) {
	if err := k.store.Audit(ctx, store.AuditEntry{Actor: "system", Action: action, JobID: rec.JobID, Detail: d}); err != nil {
		k.log.Error("version_audit_failed", "job", rec.JobID, "action", action, "error", err)
	}
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
