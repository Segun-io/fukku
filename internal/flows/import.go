package flows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/fscheck"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/metrics"
	"github.com/segun-io/fukku/internal/store"
)

// The import (#40). After the user confirms (Confirm), Radarr or Sonarr imports the job's
// rows with a ManualImport command; Fukku submits it, polls it, and then asks the app
// whether the files are there: "imported" is never assumed (plan §9). Then the title's
// monitoring is set, and the post-processing steps run (poststep.go).
//
// The importing step is safe to repeat at every point, because each pass decides from
// what the store and the app say now:
//
//   - Nothing submitted yet: the checks (sentinels and same filesystem, the title not busy,
//     the sources still there), then the submission. The import record says "submitting",
//     with what the app tracks for the title then (the baseline), before the call, and gets
//     the command's id right after it.
//   - A stored command: poll it. Running: wait. Ended (or unknown to the app): ask the app
//     what arrived. A row has arrived when the app tracks a file for it (the film, or each
//     of its episodes) that is not the baseline's, of the job's file's size, and with
//     importMode move the job's file is gone: a file the app had before, or got by itself
//     meanwhile, never counts. Everything there: imported. Else the rows are marked and the
//     job fails with import_failed and the app's message.
//   - Submitted but no command (a crash after the call, before its id was stored; or a
//     retry after a reported failure): what arrived (all: imported), else the app's recent
//     ManualImport commands for the job's folder (the newest is adopted), else a new
//     submission of what is missing.
//
// A failure of a command is reported once: the transition to failed carries a note naming
// the command (failNote), so after the user's retry the same command is not reported again
// and only the rows still missing are submitted. Nothing here deletes or moves a file.

// Job error codes and refusal codes of the import.
const (
	CodeImportFailed       = "import_failed"       // failed: the app did not import every row, or a check refused it
	CodeNeedsOverride      = "needs_override"      // confirm: rows with rejections or warnings not listed in overrides
	CodeIncompleteRows     = "incomplete_rows"     // confirm: rows without a title or an episode; edit or skip them
	CodeNothingToImport    = "nothing_to_import"   // confirm: every row is skipped
	CodeStorageUnavailable = "storage_unavailable" // confirm: a sentinel is missing, or the folders are on different filesystems
)

// Engine is the confirmation, the import and the post-processing. Build one per process
// with NewEngine, on the Proposals the web server and the runner share (it uses their
// app clients, their job locks and their language hook).
type Engine struct {
	p     *Proposals
	log   *slog.Logger
	st    store.Store
	locks *JobLocks
	steps []PostStep
	m     *metrics.Set // import outcomes; nil: none

	// checkStorage runs the filesystem checks of plan §9 before a confirmation and before
	// each submission. Tests replace it.
	checkStorage func() error

	// Second versions (#56, version.go): the post-processing steps of a version job, the
	// filesystem checks of versions.root (tests replace them), and what is told when a job
	// is done (the version keeper groups the film's versions again).
	versionSteps        []PostStep
	checkVersionStorage func() error
	onDone              []func(store.Target)

	mu      sync.Mutex
	polled  map[string]time.Time // pacing only: when each importing job was last looked at
	running map[string]bool      // jobs whose post-processing goroutine runs
	wg      sync.WaitGroup
}

// SetMetrics makes the engine count its imports (submitted, imported, failed) in m. Call it
// before the runner starts.
func (e *Engine) SetMetrics(m *metrics.Set) { e.m = m }

// NewEngine builds the import engine on p.
func NewEngine(p *Proposals) *Engine {
	e := &Engine{
		p: p, log: p.log, st: p.st, locks: p.locks,
		polled: map[string]time.Time{}, running: map[string]bool{},
	}
	e.checkStorage = func() error {
		if err := fscheck.Sentinels(e.p.cfg.Paths); err != nil {
			return err
		}
		return fscheck.SameFilesystem(e.p.cfg)
	}
	e.checkVersionStorage = func() error { return versionStorage(e.p.cfg) }
	return e
}

func (e *Engine) now() time.Time { return e.p.now() }

// cfgMode is the app's import mode: radarr.importMode or sonarr.importMode.
func (e *Engine) cfgMode(app string) string {
	mode := e.p.cfg.Radarr.ImportMode
	if app == AppSonarr {
		mode = e.p.cfg.Sonarr.ImportMode
	}
	if mode == "" {
		mode = "move"
	}
	return mode
}

// due reports whether the job's import should be looked at now: at most every
// server.importPollInterval. Pacing only; no decision depends on it.
func (e *Engine) due(jobID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if last, ok := e.polled[jobID]; ok && now.Sub(last) < e.p.cfg.Server.ImportPollInterval.D() {
		return false
	}
	e.polled[jobID] = now
	return true
}

func (e *Engine) forget(jobID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.polled, jobID)
}

// planned is one row to import: the row, the app's item as it is sent, and the job's file
// as Fukku sees it.
type planned struct {
	row  store.Row
	ar   arr.Row
	src  string
	size int64 // the job's file's size
}

// work is what one pass of the importing step works on, read under the job's lock.
type work struct {
	t    store.Target
	app  arr.Importer
	rows []store.Row
	plan []planned
	imp  store.Import // zero: no record
	bad  error        // a row that cannot be imported: the job fails
	hist []store.Event
}

// load reads the job's target, rows, files and import record under the job's lock, and
// plans the rows to import.
func (e *Engine) load(ctx context.Context, job store.Job) (work, error) {
	defer e.locks.Lock(job.ID)()
	var w work
	var err error
	if w.t, err = e.st.Target(ctx, job.ID); err != nil {
		return w, err
	}
	w.app = e.p.importer(w.t.App)
	if w.rows, err = e.st.Rows(ctx, job.ID); err != nil {
		return w, err
	}
	files, err := e.st.Files(ctx, job.ID)
	if err != nil {
		return w, err
	}
	if w.imp, err = e.st.Import(ctx, job.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return w, err
	}
	w.plan, w.bad = e.plan(job, w.t, w.rows, files)
	if w.bad == nil && len(w.plan) == 0 {
		w.bad = errors.New("no row to import")
	}
	return w, nil
}

// plan returns the rows to import (those not skipped). Each row's path is rebuilt from the
// job's own file (its FileID), never taken from what an app said, and each row must name
// the job's title: one that names another is refused, so the import cannot land on it.
func (e *Engine) plan(job store.Job, t store.Target, rows []store.Row, files []store.File) ([]planned, error) {
	byID := map[int64]store.File{}
	for _, f := range files {
		byID[f.ID] = f
	}
	// A merge (#55) imports its verified file and nothing else: never an upload under files/,
	// which would replace the film's file with the dub alone.
	isMerge, merged := false, ""
	if c, ok := mergeOf(t); ok {
		isMerge = true
		if c.Output != nil {
			merged = c.Output.Path
		}
	}
	var out []planned
	for _, r := range rows {
		if r.Skip {
			continue
		}
		ar, offered, err := decodeRow(r)
		if err != nil {
			return nil, err
		}
		if !offered {
			return nil, fmt.Errorf("row %s: the app did not offer its file", r.ID)
		}
		f, ok := byID[r.FileID]
		inJob := strings.HasPrefix(f.Path, jobdir.Files+"/")
		if isMerge {
			inJob = merged != "" && f.Path == merged
		}
		if !ok || !inJob || f.Kind != store.KindVideo || f.Status != store.FileOK {
			return nil, fmt.Errorf("row %s: its file is not one of the job's videos", r.ID)
		}
		if err := titleOK(t, ar); err != nil {
			return nil, fmt.Errorf("row %s: %w", r.ID, err)
		}
		ar.Path = e.p.appPath(t.App, job.ID, f.Path)
		src := filepath.Join(jobdir.Dir(e.p.cfg.Paths.IntakeRoot, job.ID), filepath.FromSlash(f.Path))
		out = append(out, planned{row: r, ar: ar, src: src, size: f.Size})
	}
	return out, nil
}

// titleOK checks that an item is for the target title: its movie id (Radarr) or series id
// and episodes (Sonarr), also in the app's own item that is sent back (movie.id, series.id).
func titleOK(t store.Target, ar arr.Row) error {
	switch t.App {
	case AppRadarr:
		if ar.MovieID != t.TitleID || ar.SeriesID != 0 {
			return fmt.Errorf("it is for movie %d / series %d, not movie %d", ar.MovieID, ar.SeriesID, t.TitleID)
		}
	case AppSonarr:
		if ar.SeriesID != t.TitleID || ar.MovieID != 0 {
			return fmt.Errorf("it is for series %d / movie %d, not series %d", ar.SeriesID, ar.MovieID, t.TitleID)
		}
		if len(ar.EpisodeIDs) == 0 {
			return errors.New("it has no episode")
		}
	default:
		return fmt.Errorf("unknown app %q", t.App)
	}
	var raw struct {
		Movie  *struct{ ID int } `json:"movie"`
		Series *struct{ ID int } `json:"series"`
	}
	if len(ar.Raw) > 0 && json.Unmarshal(ar.Raw, &raw) == nil {
		if raw.Movie != nil && raw.Movie.ID != 0 && raw.Movie.ID != ar.MovieID {
			return fmt.Errorf("its item names movie %d", raw.Movie.ID)
		}
		if raw.Series != nil && raw.Series.ID != 0 && raw.Series.ID != ar.SeriesID {
			return fmt.Errorf("its item names series %d", raw.Series.ID)
		}
	}
	return nil
}

// Importing is the runner step for importing jobs (see the top of this file).
func (e *Engine) Importing(ctx context.Context, job store.Job) error {
	// subtitlesOnly (#57): when the confirmation skipped every row (every episode has a file),
	// nothing is imported; the post-processing hands the subtitles over.
	t, terr := e.st.Target(ctx, job.ID)
	if terr != nil && !errors.Is(terr, store.ErrNotFound) {
		return terr
	}
	if terr == nil {
		if v, ok := versionOf(t); ok { // a second version (#56): placed by Fukku, not imported
			return e.placeVersion(ctx, job, t, v)
		}
		if c, ok := seasonDubOf(t); ok && c.Policy == PolicySubtitlesOnly {
			rows, err := e.st.Rows(ctx, job.ID)
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(rows, func(r store.Row) bool { return !r.Skip }) {
				e.forget(job.ID)
				return e.p.move(ctx, job, &t, store.Imported, store.Change{Note: "nothing imported: the season dub policy is subtitlesOnly"})
			}
		}
	}
	if !e.due(job.ID) {
		return nil
	}
	// A merge (#55, merge.go): the merge task and the merged file's row come first.
	if c, ok := mergeOf(t); ok && terr == nil {
		ready, err := e.mergeStep(ctx, job, t, c)
		if err != nil || !ready {
			return err
		}
	}
	w, err := e.load(ctx, job)
	if errors.Is(err, store.ErrNotFound) { // no target: nothing can be imported
		e.forget(job.ID)
		return e.p.move(ctx, job, nil, store.Failed, store.Change{ErrorCode: CodeImportFailed, ErrorText: "the job has no title"})
	}
	if err != nil {
		return err
	}
	if w.app == nil {
		return e.p.move(ctx, job, &w.t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: w.t.App + " is not configured"})
	}
	if w.bad != nil {
		return e.fail(ctx, job, w.t, CodeImportFailed, "refused before the import: "+w.bad.Error())
	}
	if w.imp.SubmittedAt.IsZero() {
		return e.submit(ctx, job, w, make([]bool, len(w.plan))) // nothing of ours can have arrived
	}
	base := baselineOf(w.imp)
	hist, err := e.st.History(ctx, job.ID)
	if err != nil {
		return err
	}
	w.hist = hist
	reported := reportedCommands(hist)
	cur := w.imp.CommandID
	if reported[cur] {
		cur = 0 // reported to the user, who retried: what is missing is submitted again
	}

	// 1. The stored command first: while it runs there is nothing to decide; once it has
	// ended, the app is asked what arrived, and that decides.
	if cur != 0 {
		cmd, err := w.app.Command(ctx, cur)
		gone := arr.StatusOf(err) == http.StatusNotFound
		if err != nil && !gone {
			return e.appError(ctx, job, w, "command", err, true)
		}
		if !gone && (cmd.Status == "queued" || cmd.Status == "started" || cmd.Status == "") {
			return e.answered(ctx, w, job.State)
		}
		got, err := e.arrived(ctx, w.t, w.plan, base)
		if err != nil {
			return e.appError(ctx, job, w, "files", err, false)
		}
		if !slices.Contains(got, false) {
			return e.finish(ctx, job, w)
		}
		why := cmd.Message
		switch {
		case !base.recorded && e.cfgMode(w.t.App) != "move":
			why = "the import has no baseline (an earlier version submitted it), so with importMode copy its files cannot be told from the app's own: check the title in the app"
		case gone:
			why = "the app no longer knows the import command (it may have restarted)"
		case cmd.Status == "completed":
			why = "the import finished, but not every file arrived"
		case why == "":
			why = "the import " + cmd.Status
		}
		return e.failed(ctx, job, w, got, cur, why)
	}

	// 2. Submitted, but no command to poll (a crash lost its id, or the user retried after
	// a reported failure): what has arrived, then a command the app knows for the job's
	// folder, then a new submission of what is missing.
	got, err := e.arrived(ctx, w.t, w.plan, base)
	if err != nil {
		return e.appError(ctx, job, w, "files", err, true)
	}
	if !slices.Contains(got, false) {
		return e.finish(ctx, job, w)
	}
	found, err := e.findCommand(ctx, w.app, job, w.t, reported)
	if err != nil {
		return e.appError(ctx, job, w, "commands", err, true)
	}
	if found != 0 {
		w.imp.JobID, w.imp.CommandID, w.imp.FinishedAt = job.ID, found, time.Time{}
		w.imp.RefusalsFrom = len(w.hist) // the app answered the whole pass
		if err := e.st.PutImport(ctx, w.imp); err != nil {
			return err
		}
		e.log.Info("import_command_found", "job", job.ID, "app", w.t.App, "command", found)
		return nil // polled from the next pass on
	}
	return e.submit(ctx, job, w, got)
}

// tracked is what the app tracks for the rows' title: the film's file, or each episode's
// file id (0: none). The one taken before the first submission is the import's baseline.
type tracked struct {
	Movie    *trackedFile   `json:"movie,omitempty"`
	Episodes map[string]int `json:"episodes,omitempty"`
	// recorded: read from an import's baseline (baselineOf). False for an import whose
	// baseline was not recorded; with importMode copy nothing then counts as arrived.
	recorded bool
}

type trackedFile struct {
	ID   int   `json:"id"`
	Size int64 `json:"size"`
}

// baselineOf reads an import's baseline. One that was not recorded (an import submitted by
// an earlier version), or cannot be read, is "nothing tracked" and not recorded: with
// importMode move the job's file being gone still tells the import's file apart; with
// copy nothing can, so arrived counts nothing (it fails safe).
func baselineOf(imp store.Import) tracked {
	var b tracked
	if len(imp.Baseline) > 0 && json.Unmarshal(imp.Baseline, &b) == nil {
		b.recorded = true
	} else {
		b = tracked{}
	}
	return b
}

// look asks the app what it tracks for the title now.
func (e *Engine) look(ctx context.Context, t store.Target) (tracked, error) {
	var now tracked
	switch t.App {
	case AppRadarr:
		m, err := e.p.radarr.Movie(ctx, t.TitleID)
		if err != nil {
			return now, err
		}
		if m.File != nil {
			now.Movie = &trackedFile{ID: m.File.ID, Size: m.File.Size}
		}
	case AppSonarr:
		eps, err := e.p.sonarr.Episodes(ctx, t.TitleID)
		if err != nil {
			return now, err
		}
		now.Episodes = map[string]int{}
		for _, ep := range eps {
			now.Episodes[strconv.Itoa(ep.ID)] = ep.FileID
		}
	}
	return now, nil
}

// arrived reports, for each planned row, whether the import brought its file: the app
// tracks a file for it (the film, or every episode of the row) that it did not track before
// the first submission (base), of the job's file's size, and with importMode move the job's
// file is gone. A file the app had already, or got by itself meanwhile (a download of
// another release), does not count.
//
// With importMode move a size the app reports as 0 (unknown) is not held against the file:
// the job's file being gone tells it apart. With copy the job's file stays, so the size
// and the baseline are all there is: a size of 0 is "not arrived", and an import without a
// recorded baseline has nothing arrived (it fails safe rather than taking the app's own
// file for the import's).
func (e *Engine) arrived(ctx context.Context, t store.Target, plan []planned, base tracked) ([]bool, error) {
	move := e.cfgMode(t.App) == "move"
	out := make([]bool, len(plan))
	if !move && !base.recorded {
		return out, nil
	}
	now, err := e.look(ctx, t)
	if err != nil {
		return nil, err
	}
	gone := func(p string) bool {
		_, err := os.Lstat(p) // the directory entry only: the web process never opens media
		return errors.Is(err, fs.ErrNotExist)
	}
	sameSize := func(app, ours int64) bool { return app == ours || (move && app == 0) }
	switch t.App {
	case AppRadarr:
		f := now.Movie
		fresh := f != nil && (base.Movie == nil || f.ID != base.Movie.ID)
		for i, pl := range plan {
			out[i] = fresh && sameSize(f.Size, pl.size) && (!move || gone(pl.src))
		}
	case AppSonarr:
		var sizes map[int]int64 // episode file id → size, read when a candidate needs it
		for i, pl := range plan {
			ok := len(pl.ar.EpisodeIDs) > 0 && (!move || gone(pl.src))
			for _, id := range pl.ar.EpisodeIDs {
				key := strconv.Itoa(id)
				fid := now.Episodes[key]
				if !ok || fid == 0 || fid == base.Episodes[key] {
					ok = false
					break
				}
				if sizes == nil {
					files, err := e.p.sonarr.EpisodeFiles(ctx, t.TitleID)
					if err != nil {
						return nil, err
					}
					sizes = map[int]int64{}
					for _, f := range files {
						sizes[f.ID] = f.Size
					}
				}
				if !sameSize(sizes[fid], pl.size) {
					ok = false
				}
			}
			out[i] = ok
		}
	}
	return out, nil
}

// findCommand returns the newest ManualImport the app knows for the job's files folder
// that has not been reported already, or 0.
func (e *Engine) findCommand(ctx context.Context, app arr.Importer, job store.Job, t store.Target, reported map[int]bool) (int, error) {
	cmds, err := app.Commands(ctx)
	if err != nil {
		return 0, err
	}
	folder := path.Clean(e.p.folder(t.App, job.ID)) + "/"
	if c, ok := mergeOf(t); ok && c.Output != nil { // a merge imports from its merged/<attempt>/
		folder = path.Dir(e.p.appPath(t.App, job.ID, c.Output.Path)) + "/"
	}
	best := 0
	for _, c := range cmds {
		if c.Name != "ManualImport" || reported[c.ID] || len(c.Files) == 0 || c.ID <= best {
			continue
		}
		if !slices.ContainsFunc(c.Files, func(p string) bool { return !strings.HasPrefix(path.Clean(p), folder) }) {
			best = c.ID
		}
	}
	return best, nil
}

// submit runs the checks and submits the rows that have not arrived. The first submission
// records what the app tracks for the title before it (the baseline); a later one (after
// a reported failure) keeps it.
func (e *Engine) submit(ctx context.Context, job store.Job, w work, got []bool) error {
	if err := e.checkStorage(); err != nil {
		return e.fail(ctx, job, w.t, CodeImportFailed, "refused before the import: "+err.Error())
	}
	if IsMerge(w.t) { // Radarr's file must still be the one the merge was made from (#55)
		if stop, err := e.mergeStillTracked(ctx, job, w); stop || err != nil {
			return err
		}
	}
	var busy bool
	var err error
	switch w.t.App {
	case AppRadarr:
		busy, err = e.p.radarr.Busy(ctx, w.t.TitleID)
	case AppSonarr:
		busy, err = e.p.sonarr.Busy(ctx, w.t.TitleID)
	}
	if err != nil {
		return e.appError(ctx, job, w, "busy check", err, false)
	}
	if busy {
		return e.fail(ctx, job, w.t, CodeTitleBusy, "the app is downloading this title; import it when the download is done")
	}
	var send []arr.Row
	for i, pl := range w.plan {
		if got[i] {
			continue
		}
		info, err := os.Lstat(pl.src)
		if err != nil || !info.Mode().IsRegular() {
			return e.fail(ctx, job, w.t, CodeImportFailed, fmt.Sprintf("refused before the import: %s is gone", path.Base(pl.ar.Path)))
		}
		send = append(send, pl.ar)
	}
	baseline := w.imp.Baseline
	if w.imp.SubmittedAt.IsZero() || len(baseline) == 0 {
		now, err := e.look(ctx, w.t)
		if err != nil {
			return e.appError(ctx, job, w, "files", err, false)
		}
		if baseline, err = json.Marshal(now); err != nil {
			return err
		}
	}
	// "Submitting" is stored before the call, so that after a crash the next pass looks for
	// the command among the app's before it submits again.
	// The app has answered every call of this pass: its refusals so far are not counted.
	imp := store.Import{JobID: job.ID, SubmittedAt: e.now(), Baseline: baseline, RefusalsFrom: len(w.hist)}
	if err := e.st.PutImport(ctx, imp); err != nil {
		return err
	}
	mode := e.cfgMode(w.t.App)
	id, err := w.app.Import(ctx, send, mode)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		text := fmt.Sprintf("%s (import): %v", w.t.App, err)
		if Unavailable(err) { // the command may exist: the next pass looks for it first
			return e.p.move(ctx, job, &w.t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: text})
		}
		// The app answered and refused: there is no command.
		e.forget(job.ID)
		e.m.ObserveImport(w.t.App, metrics.ImportFailed)
		return e.p.move(ctx, job, &w.t, store.Failed, store.Change{ErrorCode: CodeImportFailed, ErrorText: text})
	}
	imp.CommandID = id
	if err := e.st.PutImport(ctx, imp); err != nil {
		return err
	}
	e.m.ObserveImport(w.t.App, metrics.ImportSubmitted)
	e.log.Info("import_submitted", "job", job.ID, "app", w.t.App, "command", id, "files", len(send), "mode", mode)
	return nil
}

// finish marks every row imported and moves the job to imported.
func (e *Engine) finish(ctx context.Context, job store.Job, w work) error {
	all := make([]bool, len(w.plan))
	for i := range all {
		all[i] = true
	}
	if err := e.markRows(ctx, job, w.plan, all, false); err != nil {
		return err
	}
	imp := w.imp
	imp.JobID = job.ID
	if imp.FinishedAt.IsZero() {
		imp.FinishedAt = e.now()
		if err := e.st.PutImport(ctx, imp); err != nil {
			return err
		}
	}
	e.forget(job.ID)
	e.m.ObserveImport(w.t.App, metrics.ImportImported)
	return e.p.move(ctx, job, &w.t, store.Imported, store.Change{Note: fmt.Sprintf("%d file(s) imported by %s", len(w.plan), w.t.App)})
}

// failed ends an import command that did not import every row: the rows are marked, the
// command is recorded as finished, and the job fails with a note naming the command.
func (e *Engine) failed(ctx context.Context, job store.Job, w work, got []bool, cmdID int, why string) error {
	if err := e.markRows(ctx, job, w.plan, got, true); err != nil {
		return err
	}
	var missing []string
	for i, pl := range w.plan {
		if !got[i] {
			missing = append(missing, path.Base(pl.ar.Path))
		}
	}
	w.imp.JobID, w.imp.CommandID = job.ID, cmdID
	if w.imp.SubmittedAt.IsZero() {
		w.imp.SubmittedAt = e.now()
	}
	w.imp.FinishedAt = e.now()
	if err := e.st.PutImport(ctx, w.imp); err != nil {
		return err
	}
	e.forget(job.ID)
	e.m.ObserveImport(w.t.App, metrics.ImportFailed)
	return e.p.move(ctx, job, &w.t, store.Failed, store.Change{
		ErrorCode: CodeImportFailed,
		ErrorText: fmt.Sprintf("%s: %s; not imported: %s", w.t.App, why, strings.Join(missing, ", ")),
		Note:      failNote(cmdID, len(missing)),
	})
}

// markRows stores each planned row's result: imported where got says so, else failed when
// final, else none.
func (e *Engine) markRows(ctx context.Context, job store.Job, plan []planned, got []bool, final bool) error {
	defer e.locks.Lock(job.ID)()
	for i, pl := range plan {
		want := store.ResultNone
		switch {
		case got[i]:
			want = store.ResultImported
		case final:
			want = store.ResultFailed
		}
		if pl.row.Result == want {
			continue
		}
		r := pl.row
		r.Result = want
		if err := e.st.UpdateRow(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// fail fails the job (a refusal, before the app was asked to import).
func (e *Engine) fail(ctx context.Context, job store.Job, t store.Target, code, text string) error {
	e.forget(job.ID)
	e.m.ObserveImport(t.App, metrics.ImportFailed)
	return e.p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: code, ErrorText: t.App + ": " + text})
}

// answered ends a pass in which the app answered every call and the command still runs.
// When refusals were counted before it, the import records that the run of refusals is
// broken (Import.RefusalsFrom), so that refusals with a successful poll between them never
// add up to server.arrStallLimit. Otherwise nothing is written: polls are frequent.
func (e *Engine) answered(ctx context.Context, w work, state store.State) error {
	if w.refusals(state) == 0 {
		return nil
	}
	w.imp.RefusalsFrom = len(w.hist)
	return e.st.PutImport(ctx, w.imp)
}

// refusals counts the app's refusals in a row while the import may be running: those of
// the history since the app last answered a whole pass (Import.RefusalsFrom).
func (w work) refusals(state store.State) int {
	return refusals(w.hist[min(max(w.imp.RefusalsFrom, 0), len(w.hist)):], state)
}

// appError ends a pass whose call to the app failed. An app that does not answer stalls
// the job (arr_unavailable), whatever the state of the import. An answer that refuses the
// call (a 4xx other than 401, 403 and 429: the film was deleted, say) fails it with
// import_failed, unless an import may still be running (running): then the job stalls too,
// so that a command half way is not given up on at the first refusal, but the refusals in a
// row are counted, and at server.arrStallLimit the job fails with a message that says so.
// A pass the app answers throughout breaks the run (answered).
func (e *Engine) appError(ctx context.Context, job store.Job, w work, call string, err error, running bool) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	text := fmt.Sprintf("%s (%s): %v", w.t.App, call, err)
	if Unavailable(err) {
		return e.p.move(ctx, job, &w.t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: text})
	}
	if !running {
		e.forget(job.ID)
		return e.p.move(ctx, job, &w.t, store.Failed, store.Change{ErrorCode: CodeImportFailed, ErrorText: text})
	}
	if n := w.refusals(job.State) + 1; n >= max(e.p.cfg.Server.ArrStallLimit, 1) {
		e.forget(job.ID)
		return e.p.move(ctx, job, &w.t, store.Failed, store.Change{
			ErrorCode: CodeImportFailed,
			ErrorText: fmt.Sprintf("%s; refused %d times in a row while an import may be running: check the title in %s", text, n, w.t.App),
		})
	}
	return e.p.move(ctx, job, &w.t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: text, Note: refusalNote + call})
}

// refusalNote starts the note of a stall for a refusal while an import may be running.
const refusalNote = "the app refused: "

// refusals counts the stalls for a refusal in a row out of state at the end of the history:
// resuming from them does not interrupt the run; a stall for another reason or entering
// the state otherwise does.
func refusals(h []store.Event, state store.State) int {
	n := 0
	for i := len(h) - 1; i >= 0; i-- {
		ev := h[i]
		switch {
		case ev.From == store.Stalled && ev.To == state:
		case ev.From == state && ev.To == store.Stalled && strings.HasPrefix(ev.Note, refusalNote):
			n++
		default:
			return n
		}
	}
	return n
}

// failNote is the note of the transition that reports command id's failure.
func failNote(id, missing int) string {
	return fmt.Sprintf("%s%d did not import %d file(s)", failNotePrefix, id, missing)
}

const failNotePrefix = "ManualImport command "

// reportedCommands are the commands whose failure a transition to failed has reported.
func reportedCommands(h []store.Event) map[int]bool {
	out := map[int]bool{}
	for _, ev := range h {
		if ev.From != store.Importing || ev.To != store.Failed {
			continue
		}
		rest, ok := strings.CutPrefix(ev.Note, failNotePrefix)
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		if id, err := strconv.Atoi(f[0]); err == nil && id > 0 {
			out[id] = true
		}
	}
	return out
}

// ---- imported ---------------------------------------------------------------------------

// Imported is the runner step for imported jobs: the title's monitoring, then
// post-processing. A film is monitored as chosen at the match (a film that was in the
// library already is left as it was); a show's monitoring is not changed. With
// merge.unmonitorAfterDubOnlyImport, a film or episodes whose imported file has only other
// languages than the title's original one are unmonitored, so that an upgrade without the
// dub cannot replace them (B6). What came of it (monitored, and why not) is stored in the
// target's options for the job's result. Each call sets a state, so it is safe to repeat.
func (e *Engine) Imported(ctx context.Context, job store.Job) error {
	t, err := e.st.Target(ctx, job.ID)
	if err != nil {
		return err
	}
	if IsVersion(t) { // a second version (#56): Radarr's film and its monitoring are untouched
		return e.p.move(ctx, job, &t, store.PostProcessing, store.Change{Note: "monitoring unchanged: a second version is not Radarr's file"})
	}
	if IsMerge(t) { // a merge (#55): merge.unmonitorAfterMerge decides
		return e.mergeImported(ctx, job, t)
	}
	rows, err := e.st.Rows(ctx, job.ID)
	if err != nil {
		return err
	}
	var opts options
	if len(t.Options) > 0 {
		_ = json.Unmarshal(t.Options, &opts)
	}
	var imported []arr.Row
	for _, r := range rows {
		if r.Result != store.ResultImported {
			continue
		}
		if ar, ok, err := decodeRow(r); err == nil && ok {
			imported = append(imported, ar)
		}
	}
	dubRule := e.p.cfg.Merge.UnmonitorAfterDubOnlyImport
	note := "monitoring unchanged"
	choice, _ := seasonDubOf(t)
	if choice.Policy == PolicySubtitlesOnly && len(imported) == 0 { // nothing was imported: nothing to set
		return e.p.move(ctx, job, &t, store.PostProcessing, store.Change{Note: "nothing imported: the subtitles are handed over"})
	}
	var mon monitoring
	switch t.App {
	case AppRadarr:
		if e.p.radarr == nil {
			return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: "radarr is not configured"})
		}
		var want *bool
		if opts.Added && opts.Monitored != nil {
			want = opts.Monitored
			note = fmt.Sprintf("monitored: %t, as chosen", *want)
			if !*want {
				mon.Reason = UnmonitorChosen
			}
		}
		if dubRule || want == nil {
			m, err := e.p.radarr.Movie(ctx, t.TitleID)
			if err != nil {
				return e.monitorFailed(ctx, job, t, err)
			}
			if want == nil {
				current := m.Monitored
				want = &current // left as it is; recorded as it is
			}
			if dubRule && *want && slices.ContainsFunc(imported, func(ar arr.Row) bool { return dubOnly(m.OriginalLanguage, ar.Languages) }) {
				off := false
				want = &off
				mon.Reason = UnmonitorDubOnly
				note = "unmonitored: the file has only a dub (merge.unmonitorAfterDubOnlyImport)"
			}
		}
		if opts.Added || mon.Reason == UnmonitorDubOnly {
			if err := e.p.radarr.SetMonitored(ctx, t.TitleID, *want); err != nil {
				return e.monitorFailed(ctx, job, t, err)
			}
		}
		mon.Monitored = *want
	case AppSonarr:
		if e.p.sonarr == nil {
			return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: "sonarr is not configured"})
		}
		var off []int
		if dubRule {
			s, err := e.p.sonarr.Series(ctx, t.TitleID)
			if err != nil {
				return e.monitorFailed(ctx, job, t, err)
			}
			for _, ar := range imported {
				if dubOnly(s.OriginalLanguage, ar.Languages) {
					off = append(off, ar.EpisodeIDs...)
				}
			}
			if len(off) > 0 {
				if err := e.p.sonarr.SetEpisodesMonitored(ctx, off, false); err != nil {
					return e.monitorFailed(ctx, job, t, err)
				}
				mon.Reason = UnmonitorDubOnly
				note = fmt.Sprintf("%d episode(s) unmonitored: their file has only a dub (merge.unmonitorAfterDubOnlyImport)", len(off))
			}
			// A season replaced by a dub is unmonitored as a whole: its own flag, which
			// the episode call does not touch (C3-sonarr), so that the app's searches
			// leave it alone (B6-sonarr).
			if seasons, err := e.dubSeasons(ctx, t, choice, imported, s.OriginalLanguage); err != nil {
				return e.monitorFailed(ctx, job, t, err)
			} else if len(seasons) > 0 {
				for _, n := range seasons {
					if err := e.p.sonarr.SetSeasonMonitored(ctx, t.TitleID, n, false); err != nil {
						return e.monitorFailed(ctx, job, t, err)
					}
				}
				mon.Reason = UnmonitorDubOnly
				note = fmt.Sprintf("%s unmonitored: replaced by a dub (merge.unmonitorAfterDubOnlyImport)", seasonsText(seasons))
			}
		}
		// Monitored: every imported episode is, as Sonarr says now.
		eps, err := e.p.sonarr.Episodes(ctx, t.TitleID)
		if err != nil {
			return e.monitorFailed(ctx, job, t, err)
		}
		idx := episodeIndex(eps)
		mon.Monitored = len(imported) > 0
		for _, ar := range imported {
			for _, id := range ar.EpisodeIDs {
				if !idx[id].Monitored || slices.Contains(off, id) {
					mon.Monitored = false
				}
			}
		}
	}
	if mon.Monitored {
		mon.Reason = ""
	}
	if err := e.recordMonitoring(ctx, job, t, mon); err != nil {
		return err
	}
	return e.p.move(ctx, job, &t, store.PostProcessing, store.Change{Note: note})
}

// dubSeasons are the seasons whose own flag is set off after a replace (#57): those the
// confirmation replaced files in, when imported dub-only files cover every episode of the
// season. The flag reaches every episode of the season (C3-sonarr), so with a partial
// season only the imported episodes are unmonitored (by the caller), never the others.
func (e *Engine) dubSeasons(ctx context.Context, t store.Target, choice seasonDubChoice, imported []arr.Row, original string) ([]int, error) {
	if choice.Policy != PolicyReplace || len(choice.Seasons) == 0 {
		return nil, nil
	}
	eps, err := e.p.sonarr.Episodes(ctx, t.TitleID)
	if err != nil {
		return nil, err
	}
	dub := map[int]bool{} // episodes with an imported dub-only file
	for _, ar := range imported {
		if dubOnly(original, ar.Languages) {
			for _, id := range ar.EpisodeIDs {
				dub[id] = true
			}
		}
	}
	var out []int
	for _, n := range choice.Seasons {
		covered, seen := true, false
		for _, ep := range eps {
			if ep.SeasonNumber != n {
				continue
			}
			seen = true
			covered = covered && dub[ep.ID]
		}
		if seen && covered {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out, nil
}

// recordMonitoring stores the monitoring outcome in the target's options, under the job's
// lock; the target's identity does not change.
func (e *Engine) recordMonitoring(ctx context.Context, job store.Job, t store.Target, mon monitoring) error {
	defer e.locks.Lock(job.ID)()
	cur, err := e.st.Target(ctx, job.ID)
	if err != nil {
		return err
	}
	if cur.App != t.App || cur.TitleID != t.TitleID || !cur.MatchedAt.Equal(t.MatchedAt) {
		return nil // another title meanwhile: the transition will not happen either
	}
	if old, ok := monitoringOf(cur); ok && old == mon {
		return nil
	}
	if cur.Options, err = withOption(cur.Options, "monitoring", mon); err != nil {
		return err
	}
	return e.st.SetTarget(ctx, job.ID, cur)
}

// dubOnly reports whether languages (of an imported file) are known and none is the title's
// original language.
func dubOnly(original string, languages []arr.Language) bool {
	if original == "" {
		return false
	}
	known := false
	for _, l := range languages {
		if l.Name == "" || strings.EqualFold(l.Name, "unknown") {
			continue
		}
		if strings.EqualFold(l.Name, original) {
			return false
		}
		known = true
	}
	return known
}

// monitorFailed ends an imported pass whose call to the app failed: stalled when the app
// does not answer (failed once it has stalled server.arrStallLimit times in a row), failed
// with import_failed when it refused. A retry sets the monitoring again.
func (e *Engine) monitorFailed(ctx context.Context, job store.Job, t store.Target, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	text := fmt.Sprintf("%s (monitoring): %v", t.App, err)
	if !Unavailable(err) {
		return e.p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: CodeImportFailed, ErrorText: text})
	}
	n, herr := e.p.stalls(ctx, job.ID, job.State, t.MatchedAt)
	if herr != nil {
		return herr
	}
	if n >= e.p.cfg.Server.ArrStallLimit {
		return e.p.move(ctx, job, &t, store.Failed, store.Change{
			ErrorCode: CodeArrUnavailable, ErrorText: fmt.Sprintf("%s; stalled %d times in a row", text, n),
		})
	}
	return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: text})
}
