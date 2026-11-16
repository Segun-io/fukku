package mediaserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/pathmap"
	"github.com/segun-io/fukku/internal/store"
)

// StepName is the name of the post-processing step.
const StepName = "mediaServerRefresh"

// ErrNotVisible is returned when the title did not show up within mediaServer.itemWait.
var ErrNotVisible = errors.New("the title is not visible in the media server yet")

var (
	_ flows.PostStep = (*Step)(nil)
	_ flows.Linker   = (*Step)(nil)
)

// Step is the post-processing step mediaServerRefresh (#40's PostStep contract). After the
// import it maps each imported file to the media server's view of the path, tells the server,
// waits until the server lists the file, and reports the item id in its detail ("item <id>").
// As a flows.Linker it turns that detail into the job's title link.
type Step struct {
	Server MediaServer
	Movies arr.MovieManager  // nil when Radarr is not configured
	Series arr.SeriesManager // nil when Sonarr is not configured
	Config config.Config
}

func (s *Step) Name() string { return StepName }

// Link is the title link of a finished step's detail: mediaServer.titleLinkTemplate with the
// item id. No template, or no id: no link.
func (s *Step) Link(detail string) (string, bool) {
	l := TitleLink(s.Config.MediaServer.TitleLinkTemplate, ItemID(detail))
	return l, l != ""
}

// Run performs the step. It is safe to repeat: a repeat refreshes again and looks again.
// With mediaServer.provider none it is skipped (flows.Skip). When the title does not appear
// within mediaServer.itemWait it returns an error wrapping ErrNotVisible, which the engine
// retries.
func (s *Step) Run(ctx context.Context, job store.Job, target store.Target, rows []store.Row) (bool, string, error) {
	ms := s.Config.MediaServer
	if ms.Provider == "none" || ms.Provider == "" || s.Server == nil {
		return true, "", flows.Skip("no media server is configured")
	}
	files, err := s.importedFiles(ctx, target, rows)
	if err != nil {
		return false, "", err
	}
	if len(files) == 0 {
		return false, "", errors.New("the app tracks no imported file for this job")
	}
	// The app's view of a path, then Fukku's, then the media server's. Only the last is sent.
	paths := make([]string, len(files))
	for i, f := range files {
		fukku := pathmap.From(s.appMappings(target.App), f)
		paths[i] = pathmap.To(s.Config.Paths.Mappings.MediaServer, fukku)
	}
	if err := s.Server.Refresh(ctx, paths); err != nil {
		return false, "", fmt.Errorf("refresh the media server: %w", err)
	}
	// What to wait for: every file. A show's link goes to the series, not to an episode, so
	// its folder comes first and its id is the one reported.
	look := paths
	if target.App == "sonarr" {
		series, err := s.Series.Series(ctx, target.TitleID)
		if err != nil {
			return false, "", err
		}
		if series.Path != "" {
			folder := pathmap.To(s.Config.Paths.Mappings.MediaServer, pathmap.From(s.appMappings(target.App), series.Path))
			look = append([]string{folder}, paths...)
		}
	}

	wait, poll := ms.ItemWait.D(), ms.ItemPollInterval.D()
	if poll <= 0 {
		poll = 2 * time.Second
	}
	deadline := time.Now().Add(wait)
	first := ""
	for {
		ids, missing, err := s.find(ctx, look)
		if err != nil {
			return false, "", fmt.Errorf("look for the title in the media server: %w", err)
		}
		if missing == 0 {
			first = ids[0]
			break
		}
		if !time.Now().Add(poll).Before(deadline) {
			return false, "", fmt.Errorf("%w after %s (%d of %d items listed)", ErrNotVisible, wait, len(look)-missing, len(look))
		}
		t := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return false, "", ctx.Err()
		case <-t.C:
		}
	}
	return true, "item " + first, nil
}

// ItemID returns the item id a finished step reported in its detail, or "".
func ItemID(detail string) string {
	id, ok := strings.CutPrefix(detail, "item ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(id)
}

func (s *Step) find(ctx context.Context, paths []string) (ids []string, missing int, err error) {
	for _, p := range paths {
		id, err := s.Server.Find(ctx, p)
		if err != nil {
			return nil, 0, err
		}
		if id == "" {
			missing++
		}
		ids = append(ids, id)
	}
	return ids, missing, nil
}

func (s *Step) appMappings(app string) []config.PathMapping {
	if app == "sonarr" {
		return s.Config.Paths.Mappings.Sonarr
	}
	return s.Config.Paths.Mappings.Radarr
}

// importedFiles returns the files the app tracks for what was imported, as the app sees them:
// the film's file, or the files of the episodes of the rows that were not skipped or failed.
func (s *Step) importedFiles(ctx context.Context, target store.Target, rows []store.Row) ([]string, error) {
	switch target.App {
	case "radarr":
		if s.Movies == nil {
			return nil, errors.New("radarr is not configured")
		}
		m, err := s.Movies.Movie(ctx, target.TitleID)
		if err != nil {
			return nil, err
		}
		if m.File == nil || m.File.Path == "" {
			return nil, nil
		}
		return []string{m.File.Path}, nil
	case "sonarr":
		if s.Series == nil {
			return nil, errors.New("sonarr is not configured")
		}
		want := map[int]bool{}
		for _, r := range rows {
			if r.Skip || r.Result == store.ResultSkipped || r.Result == store.ResultFailed {
				continue
			}
			var p arr.Row
			if err := json.Unmarshal(r.Proposal, &p); err != nil {
				return nil, fmt.Errorf("row %s: read proposal: %w", r.ID, err)
			}
			for _, id := range p.EpisodeIDs {
				want[id] = true
			}
		}
		eps, err := s.Series.Episodes(ctx, target.TitleID)
		if err != nil {
			return nil, err
		}
		fileIDs := map[int]bool{}
		for _, e := range eps {
			if want[e.ID] && e.FileID != 0 {
				fileIDs[e.FileID] = true
			}
		}
		if len(fileIDs) == 0 {
			return nil, nil
		}
		all, err := s.Series.EpisodeFiles(ctx, target.TitleID)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, f := range all {
			if fileIDs[f.ID] && f.Path != "" {
				out = append(out, f.Path)
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown app %q", target.App)
}
