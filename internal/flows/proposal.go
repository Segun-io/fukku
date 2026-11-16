package flows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/pathmap"
	"github.com/segun-io/fukku/internal/store"
)

// The proposal (#38). Once a job has a title (matched), the matched step asks the app what
// it makes of the job's files: GET /manualimport for the job's files folder, never with a
// title id (probes C6, C6-sonarr). Each item is matched to a job file by path, given the
// title (and, for a show, the season and episodes the app or the file name says), and
// re-checked (POST /manualimport). The rows are stored and the job waits for the user
// (awaiting-confirmation). Each edit of a row is re-checked through the app again.
//
// Row ids are derived from the job file ("r<file id>"), so a repeated matched step writes
// the same rows again, and an edit keeps its row's id (PutRows replaces every row).
//
// The warnings Fukku adds are computed from the stored rows, the series' episodes and the
// files' probes whenever the proposal is read (View), so they always describe what the app
// knows now. Only their effect on Skip (existing_file, not_offered) is stored, because the
// import (#40) reads Skip. A film has one file: of several, all but the longest start
// skipped, as if by the user.
//
// The paths sent to the app and stored are always derived from the job's files; an app's
// answer is trusted for its verdict, never for which file a row is.

// Job error codes the proposal adds; a job stalls with CodeArrUnavailable.
const (
	CodeProposalFailed = "proposal_failed" // failed: the app refused the proposal or the re-check
)

// CodeNotFound is the code of a refusal for a row the job does not have.
const CodeNotFound = "not_found"

// Warning codes on a row. The UI has the texts.
const (
	WarnNotOffered       = "not_offered"       // the app did not list the file; it cannot be imported
	WarnExistingFile     = "existing_file"     // an episode of the row already has a file; skipped in M2
	WarnDoubleEpisode    = "double_episode"    // about doubleEpisodeRatio times one episode's runtime
	WarnRuntimeMismatch  = "runtime_mismatch"  // far from the expected runtime, and not a double
	WarnDuplicateEpisode = "duplicate_episode" // another row that is not skipped claims an episode of this one
	WarnLanguage         = "language"          // custom-format score below the profile's minimum (language.go)
	WarnDuplicateFilm    = "duplicate_film"    // another file of the film is not skipped either; a film has one file
)

// HintDVDOrder is the job-level hint when most rows are runtime mismatches.
const HintDVDOrder = "dvd_order"

// Proposal states in the API.
const (
	StatePending = "pending" // the matched step has not finished
	StateReady   = "ready"
)

// The proposal's refusals are *Error like the match flow's, and a failure of an app is
// what arrFailure makes of it (Code says arr_unavailable).

func badRequest(format string, args ...any) error {
	return refuse(CodeBadRequest, format, args...)
}

func rowNotFound() error { return refuse(CodeNotFound, "No such row.") }

func rowsChanged() error {
	return refuse(CodeWrongState, "The rows changed meanwhile. Try again.")
}

// appFailure is a failed call to app, or an answer of it Fukku will not use.
func appFailure(app string, err error) error { return arrFailure(fmt.Errorf("%s: %w", app, err)) }

// Proposals runs the proposal: the runner step for matched, the view and the edits. Build
// one per process with NewProposals and share it between the runner and the web server,
// with the JobLocks the match flow uses. Writes to one job's rows take the job's lock,
// which is never held during a call to an app: each writer reads, asks the app, then takes
// the lock, checks that what it read is still so, and writes with PutProposal, which checks
// the state and the title again in its transaction.
type Proposals struct {
	cfg    config.Config
	log    *slog.Logger
	st     store.Store
	radarr arr.MovieManager  // nil: not configured
	sonarr arr.SeriesManager // nil: not configured
	locks  *JobLocks
	now    func() time.Time
}

// NewProposals builds the proposal flow. locks is shared with the match flow (nil: its own).
func NewProposals(cfg config.Config, log *slog.Logger, st store.Store, radarr arr.MovieManager, sonarr arr.SeriesManager, locks *JobLocks) *Proposals {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if locks == nil {
		locks = NewJobLocks()
	}
	return &Proposals{cfg: cfg, log: log, st: st, radarr: radarr, sonarr: sonarr, locks: locks, now: time.Now}
}

// Lock takes the job's lock (shared with the match flow) and returns the unlock. Hold it
// only around store reads and writes, never across a call to an app. The confirmation
// (#40) should take it to read the rows it imports.
func (p *Proposals) Lock(jobID string) func() { return p.locks.Lock(jobID) }

// importer returns the app's client, or nil when it is not configured.
func (p *Proposals) importer(app string) arr.Importer {
	switch {
	case app == AppRadarr && p.radarr != nil:
		return p.radarr
	case app == AppSonarr && p.sonarr != nil:
		return p.sonarr
	}
	return nil
}

// forTarget refuses rows whose app item names another title than t.
func forTarget(rows []store.Row, t store.Target) error {
	for _, r := range rows {
		ar, offered, err := decodeRow(r)
		if err != nil {
			return err
		}
		ok := ar.MovieID == t.TitleID && ar.SeriesID == 0
		if t.App == AppSonarr {
			ok = ar.SeriesID == t.TitleID && ar.MovieID == 0
		}
		if offered && !ok {
			return fmt.Errorf("row %s is for movie %d / series %d, not %s %d", r.ID, ar.MovieID, ar.SeriesID, t.App, t.TitleID)
		}
	}
	return nil
}

const actor = "system"

// RowID is the id of the row of a job file: stable however often the proposal is made.
func RowID(fileID int64) string { return "r" + strconv.FormatInt(fileID, 10) }

// Matched is the runner step for matched jobs (#29's Step). It is safe to repeat at every
// point: it only reads from the app, PutRows replaces the job's rows in one transaction
// with rows whose ids derive from the files, and the transition comes last. The app is
// asked without the job's lock; the lock is taken to check that the job is still matched
// to the same title and to write.
func (p *Proposals) Matched(ctx context.Context, job store.Job) error {
	t, err := p.st.Target(ctx, job.ID)
	if errors.Is(err, store.ErrNotFound) {
		return p.move(ctx, job, nil, store.Probed, store.Change{Note: "no title chosen"})
	}
	if err != nil {
		return err
	}
	imp := p.importer(t.App)
	if imp == nil {
		return p.move(ctx, job, &t, store.Stalled, store.Change{
			ErrorCode: CodeArrUnavailable, ErrorText: t.App + " is not configured",
		})
	}
	files, err := p.st.Files(ctx, job.ID)
	if err != nil {
		return err
	}
	items, err := imp.Proposal(ctx, p.folder(t.App, job.ID))
	if err != nil {
		return p.appFailed(ctx, job, t, "proposal", err)
	}
	var eps []arr.Episode
	if t.App == AppSonarr {
		if eps, err = p.sonarr.Episodes(ctx, t.TitleID); err != nil {
			return p.appFailed(ctx, job, t, "episodes", err)
		}
	}
	byPath := map[string]arr.Row{}
	for _, it := range items {
		byPath[path.Clean(it.Path)] = it
	}
	var (
		rows    []store.Row
		send    []arr.Row
		sendIdx []int // index in rows of each row sent
		quals   []arr.Quality
	)
	for _, f := range offerable(files) {
		row := store.Row{ID: RowID(f.ID), FileID: f.ID, Edit: json.RawMessage(`{}`)}
		appPath := p.appPath(t.App, job.ID, f.Path)
		it, ok := byPath[path.Clean(appPath)]
		if !ok {
			row.Skip = true // not_offered
			rows = append(rows, row)
			continue
		}
		it.Path = appPath // what Fukku derives from the file, never what an app said
		switch t.App {
		case AppRadarr:
			it.MovieID, it.SeriesID = t.TitleID, 0
			if unknownQuality(it.Quality) {
				if quals == nil {
					if quals, err = imp.Qualities(ctx); err != nil {
						return p.appFailed(ctx, job, t, "qualities", err)
					}
				}
				if q, ok := defaultQuality(probeOf(f), p.cfg.Match.DefaultSource, quals); ok {
					it.Quality = q
				}
			}
		case AppSonarr:
			it = assignEpisodes(it, t.TitleID, eps, path.Base(f.Path))
			it.MovieID = 0
		}
		sendIdx = append(sendIdx, len(rows))
		send = append(send, it)
		rows = append(rows, row)
	}
	if len(send) > 0 {
		fresh, err := imp.Recheck(ctx, send)
		if err != nil {
			return p.appFailed(ctx, job, t, "re-check", err)
		}
		merged, err := mergeAnswers(send, fresh)
		if err != nil {
			return p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: CodeProposalFailed, ErrorText: t.App + " (re-check): " + err.Error()})
		}
		epByID := episodeIndex(eps)
		for i, ar := range merged {
			r := &rows[sendIdx[i]]
			if r.Proposal, err = json.Marshal(ar); err != nil {
				return err
			}
			r.Skip = hasExistingFile(ar, epByID)
		}
	}
	if t.App == AppRadarr {
		keepLongestFilm(rows, files)
	}

	if err := forTarget(rows, t); err != nil {
		return p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: CodeProposalFailed, ErrorText: t.App + " (re-check): " + err.Error()})
	}

	// One write, under the job's lock, which the match flow holds for its own: the store
	// checks again that the job is still matched to the same title (the user may have
	// chosen another one, or cancelled, meanwhile; the next pass sees it).
	defer p.Lock(job.ID)()
	next, err := p.st.PutProposal(ctx, job.ID, store.Matched, store.AwaitingConfirmation, t, rows, store.Change{
		Actor: actor, Note: fmt.Sprintf("%d row(s) proposed by %s", len(rows), t.App),
	})
	if errors.Is(err, store.ErrConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	p.log.Info("job_state", "job", job.ID, "from", job.State, "to", next.State, "attempt", next.Attempt)
	return nil
}

// keepLongestFilm leaves one film file to import: a film has one file, so with several
// videos the longest one (by the probe; the first by path on a tie) stays and the others
// are skipped as if by the user, who can undo it (then duplicate_film warns).
func keepLongestFilm(rows []store.Row, files []store.File) {
	byID := map[int64]store.File{}
	for _, f := range files {
		byID[f.ID] = f
	}
	offered := func(r store.Row) bool { return len(r.Proposal) > 0 && string(r.Proposal) != "null" }
	keep := -1
	for i, r := range rows {
		if offered(r) && (keep < 0 || probeOf(byID[r.FileID]).DurationMs > probeOf(byID[rows[keep].FileID]).DurationMs) {
			keep = i
		}
	}
	for i := range rows {
		if offered(rows[i]) && i != keep {
			rows[i].Skip = true
			rows[i].Edit = json.RawMessage(`{"skip":true}`)
		}
	}
}

// AppAnswers is the runner's resume check for jobs stalled with arr_unavailable: the
// job's app answers its status call, and the job has waited server.arrStallBackoff,
// doubled for each earlier stall in a row in the same state.
func (p *Proposals) AppAnswers(ctx context.Context, job store.Job) (bool, error) {
	t, err := p.st.Target(ctx, job.ID)
	if errors.Is(err, store.ErrNotFound) {
		return true, nil // nothing to wait for: the step decides what comes next
	}
	if err != nil {
		return false, err
	}
	n, err := p.stalls(ctx, job.ID, job.Resume, t.MatchedAt)
	if err != nil {
		return false, err
	}
	if wait := backoff(p.cfg.Server.ArrStallBackoff.D(), n); p.now().Sub(job.UpdatedAt) < wait {
		return false, nil
	}
	imp := p.importer(t.App)
	if imp == nil {
		return false, nil
	}
	if _, err := imp.Status(ctx); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		p.log.Debug("arr_still_unavailable", "job", job.ID, "app", t.App, "error", err)
		return false, nil
	}
	return true, nil
}

// backoff is the wait after the nth stall in a row: base, doubled per stall, at most 16×.
func backoff(base time.Duration, n int) time.Duration {
	if n < 1 {
		n = 1
	}
	return base << min(n-1, 4)
}

// stalls counts the job's stalls out of state since it last entered that state other than
// by resuming, and since its title was chosen (since): a user's retry or a new match starts
// again from zero.
func (p *Proposals) stalls(ctx context.Context, jobID string, state store.State, since time.Time) (int, error) {
	h, err := p.st.History(ctx, jobID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range h {
		switch {
		case e.At.Before(since):
		case e.To == state && e.From != store.Stalled:
			n = 0
		case e.From == state && e.To == store.Stalled:
			n++
		}
	}
	return n, nil
}

// appFailed ends a pass whose call to the app for title t failed: stalled when the app does
// not answer (it resumes when it does), failed when it refused, and failed too once the job
// has stalled server.arrStallLimit times in a row for this title, so that an app which
// answers its status but keeps failing this call does not keep the job cycling. If the user
// chose another title meanwhile, nothing happens: the next pass works for that one.
func (p *Proposals) appFailed(ctx context.Context, job store.Job, t store.Target, call string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	text := fmt.Sprintf("%s (%s): %v", t.App, call, err)
	if !Unavailable(err) {
		return p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: CodeProposalFailed, ErrorText: text})
	}
	n, herr := p.stalls(ctx, job.ID, job.State, t.MatchedAt)
	if herr != nil {
		return herr
	}
	if n >= p.cfg.Server.ArrStallLimit {
		return p.move(ctx, job, &t, store.Failed, store.Change{
			ErrorCode: CodeArrUnavailable, ErrorText: fmt.Sprintf("%s; stalled %d times in a row", text, n),
		})
	}
	return p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: text})
}

// Unavailable reports whether err from an app call means the app does not answer: the
// request did not reach it, timed out, or got a 5xx, 401, 403 or 429 (down, overloaded,
// or the key is wrong: an operator can fix that and the job goes on). Anything else (an
// answer Fukku cannot read, a 4xx refusal, a method the client lacks) is a failure.
func Unavailable(err error) bool {
	if errors.Is(err, arr.ErrUnreachable) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch s := arr.StatusOf(err); {
	case s >= 500:
		return true
	case s == http.StatusUnauthorized, s == http.StatusForbidden, s == http.StatusTooManyRequests:
		return true
	}
	return false
}

// move makes one transition as the system. With a target, only while the job still has
// that target (TransitionFor): a step that worked for one title never moves the job once the
// user has chosen another. A job that changed state or title meanwhile is left alone: the
// next pass sees it as it is now.
func (p *Proposals) move(ctx context.Context, job store.Job, t *store.Target, to store.State, c store.Change) error {
	c.Actor = actor
	var (
		j   store.Job
		err error
	)
	if t != nil {
		j, err = p.st.TransitionFor(ctx, job.ID, *t, job.State, to, c)
	} else {
		j, err = p.st.Transition(ctx, job.ID, job.State, to, c)
	}
	if errors.Is(err, store.ErrConflict) {
		p.log.Debug("job_moved", "job", job.ID, "from", job.State, "to", to, "note", "the job changed state or title meanwhile")
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s → %s: %w", job.State, to, err)
	}
	attrs := []any{"job", job.ID, "from", job.State, "to", j.State, "attempt", j.Attempt}
	if c.ErrorCode != "" {
		attrs = append(attrs, "code", c.ErrorCode, "detail", c.ErrorText)
	}
	p.log.Info("job_state", attrs...)
	return nil
}

// folder is the job's files folder as the app sees it.
func (p *Proposals) folder(app, jobID string) string {
	dir := filepath.ToSlash(filepath.Join(jobdir.Dir(p.cfg.Paths.IntakeRoot, jobID), jobdir.Files))
	return pathmap.To(p.mappings(app), dir)
}

// appPath is a job file (a job-relative path) as the app sees it.
func (p *Proposals) appPath(app, jobID, rel string) string {
	full := filepath.ToSlash(filepath.Join(jobdir.Dir(p.cfg.Paths.IntakeRoot, jobID), filepath.FromSlash(rel)))
	return pathmap.To(p.mappings(app), full)
}

func (p *Proposals) mappings(app string) []config.PathMapping {
	if app == AppSonarr {
		return p.cfg.Paths.Mappings.Sonarr
	}
	return p.cfg.Paths.Mappings.Radarr
}

// offerable are the files a proposal has rows for: accepted videos under files/, in path
// order. Subtitles go to the subtitle manager (#42); archives are never offered.
func offerable(files []store.File) []store.File {
	var out []store.File
	for _, f := range files {
		if f.Kind == store.KindVideo && f.Status == store.FileOK && strings.HasPrefix(f.Path, jobdir.Files+"/") {
			out = append(out, f)
		}
	}
	return out
}
