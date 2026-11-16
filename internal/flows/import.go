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
	"github.com/segun-io/fukku/internal/store"
)

// The import (#40). After the user confirms (Confirm), Radarr or Sonarr imports the job's
// rows with a ManualImport command; Fukku submits it, polls it, and then asks the app
// whether the files are there: "imported" is never assumed (plan §9). Then the title's
// monitoring is set, and the post-processing steps run (poststep.go).
//
// The importing step is safe to repeat at every point, because each pass decides from
// what the store and the app say now, in this order:
//
//  1. Ask first: are the rows' files where the app tracks them? (A film: Movie(id).File is
//     set; a show: every episode of the row has a file; with importMode move, the job's
//     file is gone too.) All there: imported.
//  2. The stored command: poll it. Running: wait. Ended without every file: the rows are
//     marked and the job fails with import_failed and the app's message.
//  3. No stored id after a submission (a crash after the call, before its id was stored):
//     the app's recent ManualImport commands for the job's folder; the newest is adopted.
//  4. Otherwise the checks (sentinels and same filesystem, the title not busy, the sources
//     still there), then the submission: the import record says "submitting" before the
//     call, and gets the command's id right after it.
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

	// checkStorage runs the filesystem checks of plan §9 before a confirmation and before
	// each submission. Tests replace it.
	checkStorage func() error

	mu      sync.Mutex
	polled  map[string]time.Time // pacing only: when each importing job was last looked at
	running map[string]bool      // jobs whose post-processing goroutine runs
	wg      sync.WaitGroup
}

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
	row store.Row
	ar  arr.Row
	src string
}

// work is what one pass of the importing step works on, read under the job's lock.
type work struct {
	t    store.Target
	app  arr.Importer
	rows []store.Row
	plan []planned
	imp  store.Import // zero: no record
	bad  error        // a row that cannot be imported: the job fails
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
		if !ok || !strings.HasPrefix(f.Path, jobdir.Files+"/") || f.Kind != store.KindVideo || f.Status != store.FileOK {
			return nil, fmt.Errorf("row %s: its file is not one of the job's videos", r.ID)
		}
		if err := titleOK(t, ar); err != nil {
			return nil, fmt.Errorf("row %s: %w", r.ID, err)
		}
		ar.Path = e.p.appPath(t.App, job.ID, f.Path)
		src := filepath.Join(jobdir.Dir(e.p.cfg.Paths.IntakeRoot, job.ID), filepath.FromSlash(f.Path))
		out = append(out, planned{row: r, ar: ar, src: src})
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
	if !e.due(job.ID) {
		return nil
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

	// 1. Ask first.
	got, err := e.arrived(ctx, w.t, w.plan)
	if err != nil {
		return e.appError(ctx, job, w, "files", err)
	}
	if !slices.Contains(got, false) {
		return e.finish(ctx, job, w)
	}

	// 2 and 3. The command: the stored one, or one a crash lost the id of.
	hist, err := e.st.History(ctx, job.ID)
	if err != nil {
		return err
	}
	reported := reportedCommands(hist)
	cur := w.imp.CommandID
	if reported[cur] {
		cur = 0 // reported to the user, who retried: what is missing is submitted again
	}
	if cur == 0 && !w.imp.SubmittedAt.IsZero() {
		found, err := e.findCommand(ctx, w.app, job, w.t, reported)
		if err != nil {
			return e.appError(ctx, job, w, "commands", err)
		}
		if found != 0 {
			w.imp.JobID, w.imp.CommandID, w.imp.FinishedAt = job.ID, found, time.Time{}
			if err := e.st.PutImport(ctx, w.imp); err != nil {
				return err
			}
			e.log.Info("import_command_found", "job", job.ID, "app", w.t.App, "command", found)
			cur = found
		}
	}
	if cur != 0 {
		cmd, err := w.app.Command(ctx, cur)
		if arr.StatusOf(err) == http.StatusNotFound {
			return e.failed(ctx, job, w, got, cur, "the app no longer knows the import command (it may have restarted)")
		}
		if err != nil {
			return e.appError(ctx, job, w, "command", err)
		}
		switch cmd.Status {
		case "queued", "started", "":
			return nil
		case "completed":
			return e.failed(ctx, job, w, got, cur, "the import finished, but not every file arrived")
		}
		msg := cmd.Message
		if msg == "" {
			msg = "the import " + cmd.Status
		}
		return e.failed(ctx, job, w, got, cur, msg)
	}

	// 4. Submit.
	return e.submit(ctx, job, w, got)
}

// arrived reports, for each planned row, whether the app tracks its file now (see 1.).
func (e *Engine) arrived(ctx context.Context, t store.Target, plan []planned) ([]bool, error) {
	move := e.cfgMode(t.App) == "move"
	gone := func(p string) bool {
		_, err := os.Lstat(p) // the directory entry only: the web process never opens media
		return errors.Is(err, fs.ErrNotExist)
	}
	out := make([]bool, len(plan))
	switch t.App {
	case AppRadarr:
		m, err := e.p.radarr.Movie(ctx, t.TitleID)
		if err != nil {
			return nil, err
		}
		for i, pl := range plan {
			out[i] = m.File != nil && (!move || gone(pl.src))
		}
	case AppSonarr:
		eps, err := e.p.sonarr.Episodes(ctx, t.TitleID)
		if err != nil {
			return nil, err
		}
		idx := episodeIndex(eps)
		for i, pl := range plan {
			ok := len(pl.ar.EpisodeIDs) > 0
			for _, id := range pl.ar.EpisodeIDs {
				if idx[id].FileID == 0 {
					ok = false
				}
			}
			out[i] = ok && (!move || gone(pl.src))
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

// submit runs the checks and submits the rows that have not arrived.
func (e *Engine) submit(ctx context.Context, job store.Job, w work, got []bool) error {
	if err := e.checkStorage(); err != nil {
		return e.fail(ctx, job, w.t, CodeImportFailed, "refused before the import: "+err.Error())
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
		return e.appError(ctx, job, w, "busy check", err)
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
	// "Submitting" is stored before the call, so that after a crash the next pass looks for
	// the command among the app's before it submits again.
	imp := store.Import{JobID: job.ID, SubmittedAt: e.now()}
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
		return e.p.move(ctx, job, &w.t, store.Failed, store.Change{ErrorCode: CodeImportFailed, ErrorText: text})
	}
	imp.CommandID = id
	if err := e.st.PutImport(ctx, imp); err != nil {
		return err
	}
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
	return e.p.move(ctx, job, &t, store.Failed, store.Change{ErrorCode: code, ErrorText: t.App + ": " + text})
}

// appError ends a pass whose call to the app failed. The job stalls when the app does not
// answer, and also when an import may be running (submitted, not finished): a job is never
// given up on halfway, whatever the app answers. Otherwise an answer that refuses the call
// fails the job with import_failed.
func (e *Engine) appError(ctx context.Context, job store.Job, w work, call string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	text := fmt.Sprintf("%s (%s): %v", w.t.App, call, err)
	running := !w.imp.SubmittedAt.IsZero() && w.imp.FinishedAt.IsZero()
	if !Unavailable(err) && !running {
		e.forget(job.ID)
		return e.p.move(ctx, job, &w.t, store.Failed, store.Change{ErrorCode: CodeImportFailed, ErrorText: text})
	}
	return e.p.move(ctx, job, &w.t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: text})
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
// dub cannot replace them (B6). Each call sets a state, so it is safe to repeat.
func (e *Engine) Imported(ctx context.Context, job store.Job) error {
	t, err := e.st.Target(ctx, job.ID)
	if err != nil {
		return err
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
	switch t.App {
	case AppRadarr:
		if e.p.radarr == nil {
			return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: "radarr is not configured"})
		}
		var want *bool
		if opts.Added && opts.Monitored != nil {
			want = opts.Monitored
			note = fmt.Sprintf("monitored: %t, as chosen", *want)
		}
		if dubRule && (want == nil || *want) {
			m, err := e.p.radarr.Movie(ctx, t.TitleID)
			if err != nil {
				return e.monitorFailed(ctx, job, t, err)
			}
			if slices.ContainsFunc(imported, func(ar arr.Row) bool { return dubOnly(m.OriginalLanguage, ar.Languages) }) {
				off := false
				want = &off
				note = "unmonitored: the file has only a dub (merge.unmonitorAfterDubOnlyImport)"
			}
		}
		if want != nil {
			if err := e.p.radarr.SetMonitored(ctx, t.TitleID, *want); err != nil {
				return e.monitorFailed(ctx, job, t, err)
			}
		}
	case AppSonarr:
		if e.p.sonarr == nil {
			return e.p.move(ctx, job, &t, store.Stalled, store.Change{ErrorCode: CodeArrUnavailable, ErrorText: "sonarr is not configured"})
		}
		if dubRule {
			s, err := e.p.sonarr.Series(ctx, t.TitleID)
			if err != nil {
				return e.monitorFailed(ctx, job, t, err)
			}
			var ids []int
			for _, ar := range imported {
				if dubOnly(s.OriginalLanguage, ar.Languages) {
					ids = append(ids, ar.EpisodeIDs...)
				}
			}
			if len(ids) > 0 {
				if err := e.p.sonarr.SetEpisodesMonitored(ctx, ids, false); err != nil {
					return e.monitorFailed(ctx, job, t, err)
				}
				note = fmt.Sprintf("%d episode(s) unmonitored: their file has only a dub (merge.unmonitorAfterDubOnlyImport)", len(ids))
			}
		}
	}
	return e.p.move(ctx, job, &t, store.PostProcessing, store.Change{Note: note})
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
