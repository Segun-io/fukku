package mediaserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/safehttp"
)

const defaultTimeout = 30 * time.Second

// Error is a non-2xx answer from the server. It never carries the API key.
type Error struct {
	Method, Path string
	Status       int
	Message      string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("jellyfin: %s %s: %d %s", e.Method, e.Path, e.Status, http.StatusText(e.Status))
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// Jellyfin is a MediaServer for Jellyfin (probed against 12.4).
type Jellyfin struct {
	base    string
	key     string
	method  string
	timeout time.Duration
	http    *http.Client
}

var (
	_ MediaServer = (*Jellyfin)(nil)
	_ Grouper     = (*Jellyfin)(nil)
)

// MergeVersions groups the items as versions of one title (POST /Videos/MergeVersions).
// The grouping survives library scans and metadata refreshes (probe J2), but not a rename
// or a replacement of one of the files (probe B3e).
func (j *Jellyfin) MergeVersions(ctx context.Context, itemIDs []string) error {
	if len(itemIDs) < 2 {
		return errors.New("jellyfin: merging versions needs two items or more")
	}
	return j.do(ctx, http.MethodPost, "/Videos/MergeVersions", url.Values{"ids": {strings.Join(itemIDs, ",")}}, nil, nil)
}

// Sources returns the paths of the item's media sources: its file and the files grouped
// with it (GET /Items?Ids=). After a merge both items list both files (probe B3e).
func (j *Jellyfin) Sources(ctx context.Context, itemID string) ([]string, error) {
	var res struct {
		Items []struct {
			ID           string `json:"Id"`
			MediaSources []struct {
				Path string `json:"Path"`
			} `json:"MediaSources"`
		} `json:"Items"`
	}
	q := url.Values{"Ids": {itemID}, "Fields": {"Path,MediaSources"}, "EnableImages": {"false"}}
	if err := j.do(ctx, http.MethodGet, "/Items", q, nil, &res); err != nil {
		return nil, err
	}
	var out []string
	for _, it := range res.Items {
		if it.ID != itemID {
			continue
		}
		for _, ms := range it.MediaSources {
			if ms.Path != "" {
				out = append(out, ms.Path)
			}
		}
	}
	return out, nil
}

// NewJellyfin builds the client from the mediaServer settings.
func NewJellyfin(cfg config.MediaServer) *Jellyfin {
	t := cfg.RequestTimeout.D()
	if t <= 0 {
		t = defaultTimeout
	}
	m := cfg.RefreshMethod
	if m == "" {
		m = MethodLibraryRefresh
	}
	return &Jellyfin{base: strings.TrimRight(cfg.URL, "/"), key: cfg.APIKey.Reveal(), method: m, timeout: t, http: safehttp.NoRedirect(&http.Client{})}
}

// Version returns the server version (GET /System/Info/Public).
func (j *Jellyfin) Version(ctx context.Context) (string, error) {
	var info struct {
		Version string `json:"Version"`
	}
	if err := j.do(ctx, http.MethodGet, "/System/Info/Public", nil, nil, &info); err != nil {
		return "", err
	}
	return info.Version, nil
}

// Refresh tells the server that paths changed, the way mediaServer.refreshMethod says.
func (j *Jellyfin) Refresh(ctx context.Context, paths []string) error {
	switch j.method {
	case MethodNone:
		return nil
	case MethodLibraryScan:
		return j.do(ctx, http.MethodPost, "/Library/Refresh", nil, nil, nil)
	case MethodPathUpdate:
		ups := make([]map[string]string, 0, len(paths))
		for _, p := range paths {
			ups = append(ups, map[string]string{"Path": p, "UpdateType": "Created"})
		}
		if len(ups) == 0 {
			return nil
		}
		return j.do(ctx, http.MethodPost, "/Library/Media/Updated", nil, map[string]any{"Updates": ups}, nil)
	case MethodLibraryRefresh:
		return j.refreshLibraries(ctx, paths)
	default:
		return fmt.Errorf("jellyfin: unknown refresh method %q", j.method)
	}
}

// Scan starts a scan of every library (POST /Library/Refresh) unless the "Scan Media Library"
// task is already running: a second request could restart a long scan.
func (j *Jellyfin) Scan(ctx context.Context) (bool, error) {
	var tasks []struct {
		Key   string `json:"Key"`
		State string `json:"State"`
	}
	if err := j.do(ctx, http.MethodGet, "/ScheduledTasks", nil, nil, &tasks); err != nil {
		return false, err
	}
	for _, t := range tasks {
		if t.Key == "RefreshLibrary" && t.State != "Idle" {
			return false, nil
		}
	}
	return true, j.do(ctx, http.MethodPost, "/Library/Refresh", nil, nil, nil)
}

// refreshLibraries refreshes each library item (Movies, Shows) that holds one of the paths.
// Jellyfin has no item for a film's folder, so the library item is the parent to refresh.
// The default metadata and image modes download nothing again.
func (j *Jellyfin) refreshLibraries(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	var libs []struct {
		ItemID    string   `json:"ItemId"`
		Locations []string `json:"Locations"`
	}
	if err := j.do(ctx, http.MethodGet, "/Library/VirtualFolders", nil, nil, &libs); err != nil {
		return err
	}
	var ids, unlisted []string
	seen := map[string]bool{}
	for _, p := range paths {
		id := ""
		for _, l := range libs {
			for _, loc := range l.Locations {
				if loc = strings.TrimRight(loc, "/"); p == loc || strings.HasPrefix(p, loc+"/") {
					id = l.ItemID
				}
			}
		}
		if id == "" {
			unlisted = append(unlisted, p)
			continue
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	// The libraries that hold a path are refreshed even when another path is in none: one
	// misplaced file must not hold up the others (the version keeper refreshes many at once).
	q := url.Values{"metadataRefreshMode": {"Default"}, "imageRefreshMode": {"Default"}, "recursive": {"false"}}
	for _, id := range ids {
		if err := j.do(ctx, http.MethodPost, "/Items/"+url.PathEscape(id)+"/Refresh", q, nil, nil); err != nil {
			return err
		}
	}
	if len(unlisted) > 0 {
		return &UnlistedError{Paths: unlisted}
	}
	return nil
}

// UnlistedError is what Refresh returns, after refreshing the libraries of the other paths,
// when some paths are in no library of the server.
type UnlistedError struct{ Paths []string }

func (e *UnlistedError) Error() string {
	return fmt.Sprintf("jellyfin: no library contains %s (check paths.mappings.mediaServer)", strings.Join(e.Paths, ", "))
}

// Find returns the id of the film or episode whose file is path, or of the show whose folder
// is path, or "" when the server does not list it yet.
func (j *Jellyfin) Find(ctx context.Context, path string) (string, error) {
	var res struct {
		Items []struct {
			ID   string `json:"Id"`
			Path string `json:"Path"`
		} `json:"Items"`
	}
	q := url.Values{"Recursive": {"true"}, "IncludeItemTypes": {"Movie,Episode,Series"}, "Path": {path},
		"Fields": {"Path"}, "EnableImages": {"false"}}
	if err := j.do(ctx, http.MethodGet, "/Items", q, nil, &res); err != nil {
		return "", err
	}
	for _, it := range res.Items {
		if it.Path == path {
			return it.ID, nil
		}
	}
	return "", nil
}

func (j *Jellyfin) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	if j.base == "" {
		return errors.New("jellyfin: no url configured")
	}
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("jellyfin: %s %s: encode request: %w", method, path, err)
		}
		rdr = bytes.NewReader(data)
	}
	target := j.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	rctx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, method, target, rdr)
	if err != nil {
		return fmt.Errorf("jellyfin: %s %s: %s", method, path, j.scrub(err.Error()))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", `MediaBrowser Token="`+j.key+`"`)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := j.http.Do(req)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			err = ctx.Err()
		case errors.Is(rctx.Err(), context.DeadlineExceeded):
			err = fmt.Errorf("timed out after %s: %w", j.timeout, context.DeadlineExceeded)
		default:
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			err = errors.New(j.scrub(err.Error()))
		}
		slog.Debug("media server request failed", "method", method, "path", path, "elapsed", time.Since(start))
		return fmt.Errorf("jellyfin: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("jellyfin: %s %s: read answer: %s", method, path, j.scrub(err.Error()))
	}
	slog.Debug("media server request", "method", method, "path", path, "status", resp.StatusCode, "elapsed", time.Since(start))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return &Error{Method: method, Path: path, Status: resp.StatusCode, Message: j.scrub(msg)}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("jellyfin: %s %s: decode answer: %w", method, path, err)
		}
	}
	return nil
}

func (j *Jellyfin) scrub(s string) string {
	if j.key == "" {
		return s
	}
	return strings.ReplaceAll(s, j.key, "***")
}
