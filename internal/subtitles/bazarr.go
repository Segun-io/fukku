package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/segun-io/fukku/internal/safehttp"
)

// Bazarr is a SubtitleManager for Bazarr (checked against 1.6.2; probes C5, C5a-C5f).
//
// The API key goes only into the X-API-KEY header. It is never put in a URL, a log line or
// an error.
type Bazarr struct {
	base   string
	key    string
	client *http.Client

	mu       sync.Mutex
	baseline map[string]int // matching history entries before an upload was sent
}

// NewBazarr returns a client for the Bazarr at baseURL. timeout limits one request.
func NewBazarr(baseURL, apiKey string, timeout time.Duration) *Bazarr {
	return &Bazarr{
		base:     strings.TrimRight(baseURL, "/"),
		key:      apiKey,
		client:   safehttp.NoCrossOriginRedirect(&http.Client{Timeout: timeout}),
		baseline: map[string]int{},
	}
}

// Version is Bazarr's own version, from GET /api/system/status.
func (b *Bazarr) Version(ctx context.Context) (string, error) {
	var res struct {
		Data struct {
			Version string `json:"bazarr_version"`
		} `json:"data"`
	}
	if err := b.do(ctx, http.MethodGet, "/api/system/status", nil, "", &res); err != nil {
		return "", err
	}
	return res.Data.Version, nil
}

// Upload sends the file with POST /api/movies/subtitles or /api/episodes/subtitles. All
// four query parameters are always sent. forced and hi are lower-case "true" or "false":
// Bazarr's upload endpoint sets a flag only on the lower-case string (probe C5f), so
// "True" would silently mean false.
func (b *Bazarr) Upload(ctx context.Context, u Upload) error {
	var path string
	q := url.Values{}
	switch u.App {
	case "radarr":
		path = "/api/movies/subtitles"
		q.Set("radarrid", strconv.Itoa(u.TitleID))
	case "sonarr":
		if u.EpisodeID <= 0 {
			return errors.New("subtitles: an episode upload needs an episode id")
		}
		path = "/api/episodes/subtitles"
		q.Set("seriesid", strconv.Itoa(u.TitleID))
		q.Set("episodeid", strconv.Itoa(u.EpisodeID))
	default:
		return fmt.Errorf("subtitles: unknown app %q", u.App)
	}
	if u.Language == "" {
		return errors.New("subtitles: an upload needs a language")
	}
	q.Set("language", u.Language)
	q.Set("forced", boolParam(u.Forced))
	q.Set("hi", boolParam(u.HI))

	// The history is read first: "written" later means "more entries than now".
	n, err := b.historyCount(ctx, u)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", u.Name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, u.Content); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	if err := b.do(ctx, http.MethodPost, path+"?"+q.Encode(), &body, mw.FormDataContentType(), nil); err != nil {
		return err
	}
	b.mu.Lock()
	b.baseline[uploadKey(u)] = n
	b.mu.Unlock()
	return nil
}

// Written reports whether the title's history has gained an upload entry for this language
// and forced flag since the Upload. Only the history proves a write: with a folder Bazarr
// cannot write to, its queue entry still ends "completed" (probe C5c). The hearing-impaired
// flag is not compared: Bazarr also sets it itself when the cues are bracketed (C5f). The
// written file's name is not predicted either; it would come from the entry (C5c).
func (b *Bazarr) Written(ctx context.Context, u Upload) (bool, error) {
	n, err := b.historyCount(ctx, u)
	if err != nil {
		return false, err
	}
	b.mu.Lock()
	before := b.baseline[uploadKey(u)] // never uploaded here: any matching entry counts
	b.mu.Unlock()
	return n > before, nil
}

func uploadKey(u Upload) string {
	return fmt.Sprintf("%s/%d/%d/%s/%t/%t/%s", u.App, u.TitleID, u.EpisodeID, u.Language, u.Forced, u.HI, u.Name)
}

func boolParam(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// historyCount counts the title's upload entries in the language and forced state of u.
func (b *Bazarr) historyCount(ctx context.Context, u Upload) (int, error) {
	path := fmt.Sprintf("/api/movies/history?radarrid=%d", u.TitleID)
	if u.App == "sonarr" {
		path = fmt.Sprintf("/api/episodes/history?episodeid=%d", u.EpisodeID)
	}
	var res struct {
		Data []struct {
			Action   int    `json:"action"`
			Path     string `json:"subtitles_path"`
			Language struct {
				Code2  string          `json:"code2"`
				Forced json.RawMessage `json:"forced"`
			} `json:"language"`
		} `json:"data"`
	}
	if err := b.do(ctx, http.MethodGet, path, nil, "", &res); err != nil {
		return 0, err
	}
	n := 0
	for _, e := range res.Data {
		// Action 4 is an upload; an entry without a path was not written.
		if e.Action == 4 && e.Path != "" && strings.EqualFold(e.Language.Code2, u.Language) && looseBool(e.Language.Forced) == u.Forced {
			n++
		}
	}
	return n, nil
}

// looseBool reads a JSON bool, or the strings "true" and "True".
func looseBool(raw json.RawMessage) bool {
	return strings.Trim(strings.ToLower(strings.TrimSpace(string(raw))), `"`) == "true"
}

func (b *Bazarr) do(ctx context.Context, method, pathAndQuery string, body io.Reader, contentType string, out any) error {
	where := method + " " + pathOnly(pathAndQuery)
	req, err := http.NewRequestWithContext(ctx, method, b.base+pathAndQuery, body)
	if err != nil {
		return errors.New("subtitles: bad request") // the URL is not echoed back
	}
	req.Header.Set("X-API-KEY", b.key)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drop the URL
		}
		return fmt.Errorf("subtitles: %s: %w", where, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("subtitles: %s: %w", where, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound && method == http.MethodPost:
		return fmt.Errorf("%w (HTTP 404)", ErrUnknownTitle)
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("subtitles: %s: HTTP %d", where, resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("subtitles: %s: unreadable answer", where)
		}
	}
	return nil
}

func pathOnly(p string) string {
	path, _, _ := strings.Cut(p, "?")
	return path
}
