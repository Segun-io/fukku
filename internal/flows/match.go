// Package flows holds the rules of the user-facing flows (plan section 8) that sit between
// the web handlers and the stores and apps they use. Nothing here knows about HTTP.
package flows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/store"
)

// Apps, as the job's Target.App and the API's "app" field name them.
const (
	AppRadarr = "radarr"
	AppSonarr = "sonarr"
)

// Codes of the errors the match flow returns.
const (
	CodeBadRequest       = "bad_request"
	CodeWrongState       = "wrong_state"
	CodeAlreadyInLibrary = "already_in_library"
	CodeTitleInUse       = "title_in_use"
	CodeTitleBusy        = "title_busy"
	CodeArrUnavailable   = "arr_unavailable"
	CodeAppNotConfigured = "app_not_configured"
	CodeTitleNotFound    = "title_not_found"
)

// Error is a refusal the caller reports to the user. Code is stable; Message is English.
// Rows names the rows a refusal is about (the confirmation's needs_override and
// incomplete_rows), in the job's row order.
type Error struct {
	Code, Message string
	Rows          []string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func refuse(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// unavailable is what every failure of an app becomes. Its text is for logs: the message
// shown to a user never carries the app's own words.
type unavailable struct{ cause error }

func (u *unavailable) Error() string { return "an app did not answer: " + u.cause.Error() }
func (u *unavailable) Unwrap() error { return u.cause }

// Code returns the stable code of err: an *Error's own, CodeArrUnavailable for a failure
// of an app, "" for anything else (an internal error).
func Code(err error) string {
	var e *Error
	var u *unavailable
	switch {
	case errors.As(err, &e):
		return e.Code
	case errors.As(err, &u):
		return CodeArrUnavailable
	}
	return ""
}

func arrFailure(err error) error { return &unavailable{err} }

// seriesTypes are the types Sonarr knows; the first is the default.
var seriesTypes = []string{"standard", "anime", "daily"}

const (
	maxTermLen      = 200
	maxHits         = 25
	maxCacheEntries = 256
)

// Matcher implements the match step: lookups, the options and the match itself.
type Matcher struct {
	cfg    config.Config
	store  store.Store
	radarr arr.MovieManager  // nil: not configured
	sonarr arr.SeriesManager // nil: not configured
	now    func() time.Time

	// locks has a lock per title (around asking the app and adding, so that a repeated
	// request finds the title the first one added, and two jobs cannot both take a free
	// one) and per job (around the final write). Other steps that write a job's target,
	// rows or state share it through NewMatcher.
	locks *JobLocks
	// writeTimeout limits the store writes that follow a change in an app.
	writeTimeout time.Duration

	cacheMu sync.Mutex
	cache   map[string]cached
}

type cached struct {
	at   time.Time
	hits any
}

// NewMatcher returns a Matcher. An app is available when its URL is set and its client is
// not nil. locks is shared with the other writers of a job (nil: its own).
func NewMatcher(cfg config.Config, st store.Store, radarr arr.MovieManager, sonarr arr.SeriesManager, locks *JobLocks, now func() time.Time) *Matcher {
	if now == nil {
		now = time.Now
	}
	if locks == nil {
		locks = NewJobLocks()
	}
	m := &Matcher{cfg: cfg, store: st, now: now, cache: map[string]cached{}, locks: locks, writeTimeout: writeTimeout}
	if cfg.Radarr.URL != "" {
		m.radarr = radarr
	}
	if cfg.Sonarr.URL != "" {
		m.sonarr = sonarr
	}
	return m
}

// ---- Apps -----------------------------------------------------------------------------

type ProfileInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type RootInfo struct {
	Path string `json:"path"`
}

// Defaults are what the match form starts with. QualityProfileID and RootFolder are null
// when the settings name nothing usable (the user picks, or there is no root folder).
type Defaults struct {
	QualityProfileID *int    `json:"qualityProfileId"`
	RootFolder       *string `json:"rootFolder"`
	Monitored        *bool   `json:"monitored,omitempty"`  // Radarr
	SeriesType       string  `json:"seriesType,omitempty"` // Sonarr
}

// AppInfo describes one configured app. Unavailable is true when the app did not answer:
// the lists are then empty.
type AppInfo struct {
	QualityProfiles []ProfileInfo `json:"qualityProfiles"`
	RootFolders     []RootInfo    `json:"rootFolders"`
	SeriesTypes     []string      `json:"seriesTypes,omitempty"`
	Defaults        Defaults      `json:"defaults"`
	Unavailable     bool          `json:"unavailable,omitempty"`
}

// Apps is the answer of GET /api/apps: an app that is not configured is left out.
type Apps struct {
	Radarr *AppInfo `json:"radarr,omitempty"`
	Sonarr *AppInfo `json:"sonarr,omitempty"`
}

// Apps lists the configured apps with their profiles, root folders and defaults. A failure
// of an app is reported in its entry and returned in errs, to be logged.
func (m *Matcher) Apps(ctx context.Context) (apps Apps, errs []error) {
	if m.radarr != nil {
		info, err := m.appInfo(ctx, AppRadarr, m.radarr, m.cfg.Radarr)
		apps.Radarr = &info
		if err != nil {
			errs = append(errs, err)
		}
	}
	if m.sonarr != nil {
		info, err := m.appInfo(ctx, AppSonarr, m.sonarr, m.cfg.Sonarr)
		apps.Sonarr = &info
		if err != nil {
			errs = append(errs, err)
		}
	}
	return apps, errs
}

func (m *Matcher) appInfo(ctx context.Context, app string, imp arr.Importer, c config.Arr) (AppInfo, error) {
	info := AppInfo{QualityProfiles: []ProfileInfo{}, RootFolders: []RootInfo{}}
	if app == AppSonarr {
		info.SeriesTypes = slices.Clone(seriesTypes)
		info.Defaults.SeriesType = seriesTypes[0]
	} else {
		monitored := c.MonitorAfterImport
		info.Defaults.Monitored = &monitored
	}
	ctx, cancel := withTimeout(ctx, c)
	defer cancel()
	profiles, roots, err := listOptions(ctx, imp)
	if err != nil {
		info.Unavailable = true
		return info, fmt.Errorf("%s: %w", app, err)
	}
	for _, p := range profiles {
		info.QualityProfiles = append(info.QualityProfiles, ProfileInfo{ID: p.ID, Name: p.Name})
	}
	for _, r := range roots {
		info.RootFolders = append(info.RootFolders, RootInfo{Path: r.Path})
	}
	if id, ok := defaultProfile(profiles, c.QualityProfile); ok {
		info.Defaults.QualityProfileID = &id
	}
	if root, ok := defaultRoot(roots, c.RootFolder); ok {
		info.Defaults.RootFolder = &root
	}
	return info, nil
}

func listOptions(ctx context.Context, imp arr.Importer) ([]arr.Profile, []arr.RootFolder, error) {
	profiles, err := imp.Profiles(ctx)
	if err != nil {
		return nil, nil, err
	}
	roots, err := imp.RootFolders(ctx)
	if err != nil {
		return nil, nil, err
	}
	return profiles, roots, nil
}

// defaultProfile finds the profile the setting names (case-insensitive); none when the
// setting is empty or names no profile.
func defaultProfile(profiles []arr.Profile, name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	for _, p := range profiles {
		if strings.EqualFold(p.Name, name) {
			return p.ID, true
		}
	}
	return 0, false
}

// defaultRoot is the configured root folder when the app lists it, else the first one.
func defaultRoot(roots []arr.RootFolder, configured string) (string, bool) {
	if len(roots) == 0 {
		return "", false
	}
	if configured != "" {
		for _, r := range roots {
			if strings.TrimRight(r.Path, "/\\") == strings.TrimRight(configured, "/\\") {
				return r.Path, true
			}
		}
	}
	return roots[0].Path, true
}

func withTimeout(ctx context.Context, c config.Arr) (context.Context, context.CancelFunc) {
	if t := c.RequestTimeout.D(); t > 0 {
		return context.WithTimeout(ctx, t)
	}
	return ctx, func() {}
}

// ---- Lookups --------------------------------------------------------------------------

// MovieHit is one film of a lookup. There are no poster URLs: the browser may not load
// images from other origins.
type MovieHit struct {
	TMDBID    int    `json:"tmdbId"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Overview  string `json:"overview"`
	InLibrary bool   `json:"inLibrary"`
	HasFile   bool   `json:"hasFile"`
}

// SeriesHit is one show of a lookup.
type SeriesHit struct {
	TVDBID    int    `json:"tvdbId"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Overview  string `json:"overview"`
	InLibrary bool   `json:"inLibrary"`
	Seasons   []int  `json:"seasons"`
}

func cleanTerm(term string) (string, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return "", refuse(CodeBadRequest, "term is required.")
	}
	if len(term) > maxTermLen {
		return "", refuse(CodeBadRequest, "term is longer than %d bytes.", maxTermLen)
	}
	return term, nil
}

// LookupMovies searches Radarr. An answer is reused for match.lookupCacheTtl per term.
func (m *Matcher) LookupMovies(ctx context.Context, term string) ([]MovieHit, error) {
	if m.radarr == nil {
		return nil, refuse(CodeAppNotConfigured, "Radarr is not configured.")
	}
	term, err := cleanTerm(term)
	if err != nil {
		return nil, err
	}
	v, err := m.cachedLookup(ctx, AppRadarr, term, m.cfg.Radarr, func(ctx context.Context) (any, error) {
		found, err := m.radarr.Lookup(ctx, term)
		if err != nil {
			return nil, err
		}
		hits := []MovieHit{}
		for _, f := range found {
			if f.TMDBID == 0 || len(hits) == maxHits {
				continue
			}
			hits = append(hits, MovieHit{
				TMDBID: f.TMDBID, Title: f.Title, Year: f.Year, Overview: f.Overview,
				InLibrary: f.ID != 0, HasFile: f.ID != 0 && (f.HasFile || f.File != nil),
			})
		}
		return hits, nil
	})
	if err != nil {
		return nil, err
	}
	return slices.Clone(v.([]MovieHit)), nil
}

// LookupSeries searches Sonarr, like LookupMovies.
func (m *Matcher) LookupSeries(ctx context.Context, term string) ([]SeriesHit, error) {
	if m.sonarr == nil {
		return nil, refuse(CodeAppNotConfigured, "Sonarr is not configured.")
	}
	term, err := cleanTerm(term)
	if err != nil {
		return nil, err
	}
	v, err := m.cachedLookup(ctx, AppSonarr, term, m.cfg.Sonarr, func(ctx context.Context) (any, error) {
		found, err := m.sonarr.Lookup(ctx, term)
		if err != nil {
			return nil, err
		}
		hits := []SeriesHit{}
		for _, f := range found {
			if f.TVDBID == 0 || len(hits) == maxHits {
				continue
			}
			seasons := f.Seasons
			if seasons == nil {
				seasons = []int{}
			}
			hits = append(hits, SeriesHit{
				TVDBID: f.TVDBID, Title: f.Title, Year: f.Year, Overview: f.Overview,
				InLibrary: f.ID != 0, Seasons: seasons,
			})
		}
		return hits, nil
	})
	if err != nil {
		return nil, err
	}
	return slices.Clone(v.([]SeriesHit)), nil
}

// cachedLookup returns the cached hits for (app, term) or runs fetch under the app's
// request timeout. A failure is not cached.
func (m *Matcher) cachedLookup(ctx context.Context, app, term string, c config.Arr, fetch func(context.Context) (any, error)) (any, error) {
	key := app + "\x00" + strings.ToLower(term)
	ttl := m.cfg.Match.LookupCacheTTL.D()
	now := m.now()
	if ttl > 0 {
		m.cacheMu.Lock()
		e, ok := m.cache[key]
		m.cacheMu.Unlock()
		if ok && now.Sub(e.at) < ttl {
			return e.hits, nil
		}
	}
	ctx, cancel := withTimeout(ctx, c)
	defer cancel()
	hits, err := fetch(ctx)
	if err != nil {
		return nil, arrFailure(fmt.Errorf("%s lookup: %w", app, err))
	}
	if ttl > 0 {
		m.cacheMu.Lock()
		if len(m.cache) >= maxCacheEntries {
			for k, e := range m.cache {
				if now.Sub(e.at) >= ttl {
					delete(m.cache, k)
				}
			}
			if len(m.cache) >= maxCacheEntries {
				clear(m.cache)
			}
		}
		m.cache[key] = cached{at: now, hits: hits}
		m.cacheMu.Unlock()
	}
	return hits, nil
}

// ---- Match ----------------------------------------------------------------------------

// MatchRequest is the body of POST /api/jobs/{id}/match. Zero values mean "the default
// from the settings".
type MatchRequest struct {
	App              string `json:"app"`
	TMDBID           int    `json:"tmdbId"`
	TVDBID           int    `json:"tvdbId"`
	QualityProfileID int    `json:"qualityProfileId"`
	RootFolder       string `json:"rootFolder"`
	Monitored        *bool  `json:"monitored"`
	SeriesType       string `json:"seriesType"`
}

// options is what the job's Target.Options holds: what was applied when the title was
// added. A title that was in the library already is left as it was (Added false).
type options struct {
	Added            bool   `json:"added"`
	QualityProfileID int    `json:"qualityProfileId,omitempty"`
	RootFolder       string `json:"rootFolder,omitempty"`
	Monitored        *bool  `json:"monitored,omitempty"`
	SeriesType       string `json:"seriesType,omitempty"`
}

// matchable are the states a job can be matched from; from the last two, choosing again
// replaces the target.
var matchable = []store.State{store.Probed, store.Matched, store.AwaitingConfirmation}

// title is a title as found on the server, before anything is changed.
type title struct {
	id   int // 0: not in the library
	name string
	year int
}

// Match points the job at the title and returns the job in state matched. The caller has
// checked that the user may see the job. Errors are *Error, or failures of an app (Code
// says arr_unavailable); anything else is internal.
func (m *Matcher) Match(ctx context.Context, user string, job store.Job, req MatchRequest) (store.Job, error) {
	// Refuse early, before the app is asked; the state is checked again at the write.
	job, err := m.store.Job(ctx, job.ID)
	if err != nil {
		return store.Job{}, err
	}
	if !slices.Contains(matchable, job.State) {
		return store.Job{}, wrongState()
	}
	var (
		t    title
		opts options
		app  string
	)
	switch req.App {
	case AppRadarr:
		if m.radarr == nil {
			return store.Job{}, refuse(CodeAppNotConfigured, "Radarr is not configured.")
		}
		if req.TMDBID <= 0 || req.TVDBID != 0 {
			return store.Job{}, refuse(CodeBadRequest, "A film is chosen with tmdbId.")
		}
		app = AppRadarr
		defer m.locks.Lock("title:radarr:" + strconv.Itoa(req.TMDBID))()
		t, opts, err = m.matchMovie(ctx, job, req)
	case AppSonarr:
		if m.sonarr == nil {
			return store.Job{}, refuse(CodeAppNotConfigured, "Sonarr is not configured.")
		}
		if req.TVDBID <= 0 || req.TMDBID != 0 {
			return store.Job{}, refuse(CodeBadRequest, "A show is chosen with tvdbId.")
		}
		app = AppSonarr
		defer m.locks.Lock("title:sonarr:" + strconv.Itoa(req.TVDBID))()
		t, opts, err = m.matchSeries(ctx, job, req)
	default:
		return store.Job{}, refuse(CodeBadRequest, "app must be %q or %q.", AppRadarr, AppSonarr)
	}
	if err != nil {
		return store.Job{}, err
	}
	raw, err := json.Marshal(opts)
	if err != nil {
		return store.Job{}, err
	}

	// The job's lock covers the read and the write: whoever else changes the job's state,
	// target or rows (the proposal step) holds it too. The write itself checks the state.
	defer m.locks.Lock(job.ID)()

	// The title may be in the app by now: the write must not be lost to a client that went
	// away, so it does not depend on the request's context. Its limit starts after the
	// wait for the lock, which ignores the context.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.writeTimeout)
	defer cancel()
	job, err = m.store.Job(wctx, job.ID)
	if err != nil {
		return store.Job{}, err
	}
	if !slices.Contains(matchable, job.State) {
		return store.Job{}, wrongState()
	}
	// Choosing again replaces the target and, with it, the rows. The same title again keeps
	// the record of how it came into the library.
	if old, err := m.store.Target(wctx, job.ID); err == nil {
		if old.App == app && old.TitleID == t.id && !opts.Added {
			raw = old.Options
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Job{}, err
	}
	next, err := m.store.ReplaceTarget(wctx, job.ID, job.State, store.Matched, &store.Target{
		App: app, TitleID: t.id, Title: t.name, Year: t.year, Options: raw, MatchedBy: user,
	}, store.Change{Actor: user})
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrInvalidTransition) {
		return store.Job{}, wrongState()
	}
	return next, err
}

// writeTimeout is the default of Matcher.writeTimeout.
const writeTimeout = 30 * time.Second

func wrongState() error {
	return refuse(CodeWrongState, "The job is not in a state that allows this.")
}

// Unmatch removes the job's target and its rows and returns the job to probed. The title
// stays in the app, if Fukku added it.
func (m *Matcher) Unmatch(ctx context.Context, user string, job store.Job) (store.Job, error) {
	defer m.locks.Lock(job.ID)()
	job, err := m.store.Job(ctx, job.ID)
	if err != nil {
		return store.Job{}, err
	}
	if job.State != store.Matched && job.State != store.AwaitingConfirmation {
		return store.Job{}, wrongState()
	}
	next, err := m.store.ReplaceTarget(ctx, job.ID, job.State, store.Probed, nil, store.Change{Actor: user})
	if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrInvalidTransition) {
		return store.Job{}, wrongState()
	}
	return next, err
}

// checkFree refuses a title another active job holds or the app is downloading. The job's
// own earlier choice of the same title does not count.
func (m *Matcher) checkFree(ctx context.Context, app string, titleID int, self string, busy func(context.Context) (bool, error)) error {
	jobs, err := m.store.ActiveJobsFor(ctx, app, titleID)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.ID != self {
			return refuse(CodeTitleInUse, "Another job is already working on this title.")
		}
	}
	b, err := busy(ctx)
	if err != nil {
		return arrFailure(fmt.Errorf("%s busy check: %w", app, err))
	}
	if b {
		return refuse(CodeTitleBusy, "The app is downloading this title right now.")
	}
	return nil
}

func (m *Matcher) matchMovie(ctx context.Context, job store.Job, req MatchRequest) (title, options, error) {
	c := m.cfg.Radarr
	ctx, cancel := withTimeout(ctx, c)
	defer cancel()
	var t title
	var opts options

	// The title is looked up again, by id: nothing about it comes from the browser.
	found, err := m.radarr.Lookup(ctx, "tmdb:"+strconv.Itoa(req.TMDBID))
	if err != nil {
		return t, opts, arrFailure(fmt.Errorf("radarr lookup: %w", err))
	}
	var hit *arr.Movie
	for i := range found {
		if found[i].TMDBID == req.TMDBID {
			hit = &found[i]
			break
		}
	}
	if hit == nil {
		return t, opts, refuse(CodeTitleNotFound, "Radarr does not know a film with that tmdbId.")
	}
	t = title{id: hit.ID, name: hit.Title, year: hit.Year}

	if hit.ID != 0 {
		film, err := m.radarr.Movie(ctx, hit.ID)
		if err != nil {
			return t, opts, arrFailure(fmt.Errorf("radarr movie: %w", err))
		}
		if film.File != nil {
			return t, opts, refuse(CodeAlreadyInLibrary, "The film already has a file in the library.")
		}
		if err := m.checkFree(ctx, AppRadarr, hit.ID, job.ID, func(ctx context.Context) (bool, error) {
			return m.radarr.Busy(ctx, hit.ID)
		}); err != nil {
			return t, opts, err
		}
		return t, options{}, nil
	}

	profiles, roots, err := listOptions(ctx, m.radarr)
	if err != nil {
		return t, opts, arrFailure(fmt.Errorf("radarr options: %w", err))
	}
	profile, root, err := resolve(profiles, roots, c, req)
	if err != nil {
		return t, opts, err
	}
	monitored := c.MonitorAfterImport
	if req.Monitored != nil {
		monitored = *req.Monitored
	}
	added, err := m.radarr.Add(ctx, arr.AddMovie{
		TMDBID: req.TMDBID, QualityProfileID: profile, RootFolder: root, Monitored: monitored, Tag: c.Tag,
	})
	if err != nil {
		return t, opts, arrFailure(fmt.Errorf("radarr add: %w", err))
	}
	t.id = added.ID
	if added.Title != "" {
		t.name, t.year = added.Title, added.Year
	}
	return t, options{Added: true, QualityProfileID: profile, RootFolder: root, Monitored: &monitored}, nil
}

func (m *Matcher) matchSeries(ctx context.Context, job store.Job, req MatchRequest) (title, options, error) {
	c := m.cfg.Sonarr
	ctx, cancel := withTimeout(ctx, c)
	defer cancel()
	var t title
	var opts options

	found, err := m.sonarr.Lookup(ctx, "tvdb:"+strconv.Itoa(req.TVDBID))
	if err != nil {
		return t, opts, arrFailure(fmt.Errorf("sonarr lookup: %w", err))
	}
	var hit *arr.Series
	for i := range found {
		if found[i].TVDBID == req.TVDBID {
			hit = &found[i]
			break
		}
	}
	if hit == nil {
		return t, opts, refuse(CodeTitleNotFound, "Sonarr does not know a show with that tvdbId.")
	}
	t = title{id: hit.ID, name: hit.Title, year: hit.Year}

	if hit.ID != 0 {
		if err := m.checkFree(ctx, AppSonarr, hit.ID, job.ID, func(ctx context.Context) (bool, error) {
			return m.sonarr.Busy(ctx, hit.ID)
		}); err != nil {
			return t, opts, err
		}
		return t, options{}, nil
	}

	typ := req.SeriesType
	if typ == "" {
		typ = seriesTypes[0]
	}
	if !slices.Contains(seriesTypes, typ) {
		return t, opts, refuse(CodeBadRequest, "seriesType must be one of %s.", strings.Join(seriesTypes, ", "))
	}
	profiles, roots, err := listOptions(ctx, m.sonarr)
	if err != nil {
		return t, opts, arrFailure(fmt.Errorf("sonarr options: %w", err))
	}
	profile, root, err := resolve(profiles, roots, c, req)
	if err != nil {
		return t, opts, err
	}
	added, err := m.sonarr.Add(ctx, arr.AddSeries{
		TVDBID: req.TVDBID, QualityProfileID: profile, RootFolder: root, Type: typ, Tag: c.Tag,
	})
	if err != nil {
		return t, opts, arrFailure(fmt.Errorf("sonarr add: %w", err))
	}
	t.id = added.ID
	if added.Title != "" {
		t.name, t.year = added.Title, added.Year
	}
	return t, options{Added: true, QualityProfileID: profile, RootFolder: root, SeriesType: typ}, nil
}

// resolve turns the request's quality profile and root folder into ones the app lists,
// filling gaps from the settings. A value the app does not list is refused: neither a
// path nor an id reaches the app unless the app named it.
func resolve(profiles []arr.Profile, roots []arr.RootFolder, c config.Arr, req MatchRequest) (profile int, root string, err error) {
	profile = req.QualityProfileID
	if profile == 0 {
		id, ok := defaultProfile(profiles, c.QualityProfile)
		if !ok {
			return 0, "", refuse(CodeBadRequest, "qualityProfileId is required: no default profile is set.")
		}
		profile = id
	} else if !slices.ContainsFunc(profiles, func(p arr.Profile) bool { return p.ID == profile }) {
		return 0, "", refuse(CodeBadRequest, "qualityProfileId is not one of the app's quality profiles.")
	}
	if req.RootFolder == "" {
		r, ok := defaultRoot(roots, c.RootFolder)
		if !ok {
			return 0, "", refuse(CodeBadRequest, "The app has no root folder.")
		}
		return profile, r, nil
	}
	for _, r := range roots {
		if r.Path == req.RootFolder {
			return profile, r.Path, nil
		}
	}
	return 0, "", refuse(CodeBadRequest, "rootFolder is not one of the app's root folders.")
}
