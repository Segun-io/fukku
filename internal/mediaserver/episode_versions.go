package mediaserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/pathmap"
	"github.com/segun-io/fukku/internal/store"
)

// EpisodeVersionStepName is the name of the post-processing step of a job that kept episodes
// as second versions (#58).
const EpisodeVersionStepName = "mediaServerEpisodeVersions"

var (
	_ flows.PostStep = (*EpisodeVersionStep)(nil)
	_ flows.Linker   = (*EpisodeVersionStep)(nil)
)

// EpisodeVersionStep is the post-processing step mediaServerEpisodeVersions: it tells the
// media server about the episodes' versions a job placed next to the episodes' files
// (package flows, episode_version.go) and records each one as grouped once the server lists
// the episode's file and the version as sources of one item. Jellyfin 12.4 groups them by
// their SxxEyy (probe B2), so nothing is merged: a version the server does not group stays
// waiting, with the reason. It waits up to mediaServer.itemWait (with the fallback scan of
// mediaServer.refreshFallback); a version still not grouped is an error the engine retries.
// A version gone from disk is recorded missing (audit version.missing). The detail of a
// finished step names the series' item ("item <id>"), which gives the job its title link
// when nothing else was imported.
type EpisodeVersionStep struct {
	Server MediaServer
	Series arr.SeriesManager // nil when Sonarr is not configured
	Store  store.Store
	Config config.Config
	Log    *slog.Logger // nil: the default logger
	Now    func() time.Time
}

func (s *EpisodeVersionStep) Name() string { return EpisodeVersionStepName }

func (s *EpisodeVersionStep) Link(detail string) (string, bool) {
	l := TitleLink(s.Config.MediaServer.TitleLinkTemplate, ItemID(detail))
	return l, l != ""
}

func (s *EpisodeVersionStep) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *EpisodeVersionStep) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// episodeCheck is one version the step checks: its record, the episode's file as Fukku and
// the server see it, and the version as the server sees it.
type episodeCheck struct {
	rec     store.Version
	fileID  int
	primary string // Fukku's view
	look    [2]string
	itemID  string
	grouped bool
}

func (s *EpisodeVersionStep) Run(ctx context.Context, job store.Job, target store.Target, _ []store.Row) (bool, string, error) {
	ms := s.Config.MediaServer
	if ms.Provider == "none" || ms.Provider == "" || s.Server == nil {
		return true, "", flows.Skip("no media server is configured: the versions are not checked")
	}
	g, ok := s.Server.(Grouper)
	if !ok {
		return true, "", flows.Skip("the media server cannot list an item's versions")
	}
	if s.Series == nil {
		return false, "", errors.New("sonarr is not configured")
	}
	want := flows.EpisodeVersionPaths(target)
	all, err := s.Store.VersionsOf(ctx, job.ID)
	if err != nil {
		return false, "", err
	}
	var recs []store.Version
	for _, r := range all {
		if r.App == flows.AppSonarr && slices.Contains(want, r.Path) && r.Status != store.VersionPlacing {
			recs = append(recs, r)
		}
	}
	if len(recs) == 0 { // every version was refused at the placement
		return true, "", flows.Skip("the job placed no version")
	}
	eps, err := s.Series.Episodes(ctx, target.TitleID)
	if err != nil {
		return false, "", fmt.Errorf("sonarr: %w", err)
	}
	files, err := s.Series.EpisodeFiles(ctx, target.TitleID)
	if err != nil {
		return false, "", fmt.Errorf("sonarr: %w", err)
	}
	fileOf := map[int]int{}
	for _, e := range eps {
		fileOf[e.ID] = e.FileID
	}
	pathOf := map[int]string{}
	for _, f := range files {
		pathOf[f.ID] = f.Path
	}
	msMap := s.Config.Paths.Mappings.MediaServer
	var checks []*episodeCheck
	var refresh []string
	for _, rec := range recs {
		if _, err := os.Lstat(rec.Path); errors.Is(err, fs.ErrNotExist) {
			if err := s.final(ctx, rec, store.VersionMissing, "version.missing", "the version file is no longer on disk"); err != nil {
				return false, "", err
			}
			continue
		} else if err != nil {
			return false, "", err
		}
		fid := fileOf[rec.EpisodeID]
		if fid == 0 || pathOf[fid] == "" {
			if err := s.waiting(ctx, rec, "Sonarr has no file for the episode now: on its next rescan it takes the version for the episode's file"); err != nil {
				return false, "", err
			}
			continue
		}
		primary := pathmap.From(s.Config.Paths.Mappings.Sonarr, pathOf[fid])
		c := &episodeCheck{rec: rec, fileID: fid, primary: primary, look: [2]string{pathmap.To(msMap, primary), pathmap.To(msMap, rec.Path)}}
		checks = append(checks, c)
		refresh = append(refresh, c.look[1])
	}
	if len(checks) > 0 {
		if err := s.Server.Refresh(ctx, refresh); err != nil {
			return false, "", fmt.Errorf("refresh the media server: %w", err)
		}
		wait, poll := ms.ItemWait.D(), ms.ItemPollInterval.D()
		if poll <= 0 {
			poll = 2 * time.Second
		}
		pending, err := s.waitGrouped(ctx, g, checks, wait, poll)
		if err != nil {
			return false, "", err
		}
		if pending > 0 && ms.RefreshFallback && ms.RefreshMethod != MethodLibraryScan && ms.RefreshMethod != MethodNone {
			started, err := s.Server.Scan(ctx)
			if err != nil {
				return false, "", fmt.Errorf("the fallback library scan failed: %w", err)
			}
			s.log().Info("media_server_fallback_scan", "job", job.ID, "outcome", map[bool]string{true: "started", false: "already_running"}[started], "for", "episode versions")
			if _, err = s.waitGrouped(ctx, g, checks, wait, poll); err != nil {
				return false, "", err
			}
		}
	}
	waitingFor := 0
	for _, c := range checks {
		if !c.grouped {
			waitingFor++
			why := fmt.Sprintf("the media server does not list %s as a version of the episode's file %s yet (is the season folder in a Shows library of Jellyfin 12.4 or later, as paths.mappings.mediaServer maps it?)", c.look[1], c.look[0])
			if err := s.waiting(ctx, c.rec, why); err != nil {
				return false, "", err
			}
			continue
		}
		if err := s.grouped(ctx, c); err != nil {
			return false, "", err
		}
	}
	if waitingFor > 0 {
		return false, "", fmt.Errorf("%w: %d of %d version(s) not grouped with their episode's file", ErrNotVisible, waitingFor, len(checks))
	}
	return true, s.seriesItem(ctx, target), nil
}

// waitGrouped polls until every check is grouped or wait has passed, and returns how many
// are not.
func (s *EpisodeVersionStep) waitGrouped(ctx context.Context, g Grouper, checks []*episodeCheck, wait, poll time.Duration) (int, error) {
	deadline := time.Now().Add(wait)
	for {
		pending := 0
		for _, c := range checks {
			if c.grouped {
				continue
			}
			id, ok, err := s.groupedNow(ctx, g, c.look)
			if err != nil {
				return 0, fmt.Errorf("look for the episode in the media server: %w", err)
			}
			if ok {
				c.itemID, c.grouped = id, true
				continue
			}
			pending++
		}
		if pending == 0 || !time.Now().Add(poll).Before(deadline) {
			return pending, nil
		}
		t := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return 0, ctx.Err()
		case <-t.C:
		}
	}
}

// groupedNow finds the item of either file and reports whether its sources are both files.
func (s *EpisodeVersionStep) groupedNow(ctx context.Context, g Grouper, look [2]string) (string, bool, error) {
	for _, p := range look {
		id, err := s.Server.Find(ctx, p)
		if err != nil || id == "" {
			if err != nil {
				return "", false, err
			}
			continue
		}
		srcs, err := g.Sources(ctx, id)
		if err != nil {
			return "", false, err
		}
		if slices.Contains(srcs, look[0]) && slices.Contains(srcs, look[1]) {
			return id, true, nil
		}
	}
	return "", false, nil
}

// grouped records the version as grouped (audit version.grouped the first time).
func (s *EpisodeVersionStep) grouped(ctx context.Context, c *episodeCheck) error {
	rec := c.rec
	now := s.now()
	first := rec.GroupedAt.IsZero()
	rec.Status, rec.Detail, rec.CheckedAt = store.VersionGrouped, "", now
	if first || rec.PrimaryFileID != c.fileID {
		rec.GroupedAt = now
	}
	if rec.PrimaryFileID != 0 && rec.PrimaryFileID != c.fileID {
		rec.PrimaryChangedAt = now
	}
	rec.PrimaryFileID, rec.PrimaryPath, rec.PrimaryItemID, rec.ItemID = c.fileID, c.primary, c.itemID, c.itemID
	var entries []store.AuditEntry
	if first {
		entries = append(entries, store.AuditEntry{Actor: "system", Action: "version.grouped", JobID: rec.JobID, Detail: map[string]string{
			"title": rec.Title, "titleId": strconv.Itoa(rec.TitleID), "path": rec.Path, "primary": c.primary,
			"primaryFileId": strconv.Itoa(c.fileID), "item": c.itemID, "episode": fmt.Sprintf("S%02dE%02d", rec.SeasonNumber, rec.EpisodeNumber),
		}})
	}
	if _, err := s.Store.PutVersion(ctx, rec, entries...); err != nil {
		return err
	}
	if first {
		s.log().Info("version_grouped", "job", rec.JobID, "path", rec.Path, "item", c.itemID)
	}
	return nil
}

func (s *EpisodeVersionStep) waiting(ctx context.Context, rec store.Version, why string) error {
	was := rec.Status
	rec.Status, rec.Detail, rec.CheckedAt = store.VersionWaiting, why, s.now()
	if _, err := s.Store.PutVersion(ctx, rec); err != nil {
		return err
	}
	if was != store.VersionWaiting {
		s.log().Warn("version_waiting", "job", rec.JobID, "path", rec.Path, "reason", why)
	}
	return nil
}

func (s *EpisodeVersionStep) final(ctx context.Context, rec store.Version, status, action, why string) error {
	was := rec.Status
	rec.Status, rec.Detail, rec.CheckedAt = status, why, s.now()
	var entries []store.AuditEntry
	if was != status {
		entries = append(entries, store.AuditEntry{Actor: "system", Action: action, JobID: rec.JobID, Detail: map[string]string{
			"title": rec.Title, "titleId": strconv.Itoa(rec.TitleID), "path": rec.Path, "reason": why}})
	}
	if _, err := s.Store.PutVersion(ctx, rec, entries...); err != nil {
		return err
	}
	if was != status {
		s.log().Warn("version_"+status, "job", rec.JobID, "path", rec.Path, "reason", why)
	}
	return nil
}

// seriesItem is the detail naming the series' item ("item <id>"), or "" when the server
// does not list the series' folder.
func (s *EpisodeVersionStep) seriesItem(ctx context.Context, target store.Target) string {
	sr, err := s.Series.Series(ctx, target.TitleID)
	if err != nil || strings.TrimSpace(sr.Path) == "" {
		return ""
	}
	folder := pathmap.To(s.Config.Paths.Mappings.MediaServer, pathmap.From(s.Config.Paths.Mappings.Sonarr, sr.Path))
	id, err := s.Server.Find(ctx, folder)
	if err != nil || id == "" {
		return ""
	}
	return "item " + id
}
