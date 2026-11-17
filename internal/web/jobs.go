package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/segun-io/fukku/internal/auth"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/pathmap"
	"github.com/segun-io/fukku/internal/store"
)

// The job API (#11). Jobs are addressed by id only; no request carries a path. A user sees
// and changes their own jobs, an admin everyone's; somebody else's job is a 404.

const (
	defaultJobLimit = 50
	maxJobLimit     = 200

	// manualImportPage is the page of Radarr and Sonarr that has the Manual Import button
	// (Wanted > Missing, in Radarr 6.4 and Sonarr 4.0).
	manualImportPage = "/wanted/missing"
)

var allStates = []store.State{
	store.Uploading, store.Uploaded, store.Probing, store.Probed, store.Matched,
	store.AwaitingConfirmation, store.Importing, store.Imported, store.PostProcessing,
	store.Done, store.Failed, store.Stalled, store.Cancelled,
}

// States a job can be cancelled from, and deleted from.
var (
	cancellable = []store.State{
		store.Uploading, store.Uploaded, store.Probing, store.Probed, store.Matched,
		store.AwaitingConfirmation, store.Failed, store.Stalled,
	}
	deletable = []store.State{store.Cancelled, store.Done, store.Failed}
)

func (s *Server) jobRoutes(mux *http.ServeMux) {
	if s.deps.Store == nil {
		return
	}
	mux.HandleFunc("POST /api/jobs", s.createJob)
	mux.HandleFunc("GET /api/jobs", s.listJobs)
	mux.HandleFunc("GET /api/jobs/{id}", s.getJob)
	mux.HandleFunc("POST /api/jobs/{id}/submit", s.submitJob)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.cancelJob)
	mux.HandleFunc("POST /api/jobs/{id}/retry", s.retryJob)
	mux.HandleFunc("DELETE /api/jobs/{id}", s.deleteJob)
}

// ---- JSON shapes ----------------------------------------------------------------------

type jobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type jobJSON struct {
	ID        string     `json:"id"`
	Owner     string     `json:"owner"`
	State     string     `json:"state"`
	Attempt   int        `json:"attempt"`
	Error     *jobError  `json:"error"`
	Name      *string    `json:"name"`
	FileCount int        `json:"fileCount"`
	TotalSize int64      `json:"totalSize"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Actions   []string   `json:"actions"`
}

type fileJSON struct {
	ID         int64           `json:"id"`
	Path       string          `json:"path"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	Size       int64           `json:"size"`
	Status     string          `json:"status"`
	Reason     *string         `json:"reason"`
	From       *string         `json:"from"`
	Probe      json.RawMessage `json:"probe"`
	Extraction json.RawMessage `json:"extraction"`
}

type uploadJSON struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
}

type eventJSON struct {
	At    time.Time `json:"at"`
	From  *string   `json:"from"`
	To    string    `json:"to"`
	Actor string    `json:"actor"`
	Note  string    `json:"note"`
}

type manualImportJSON struct {
	App    string  `json:"app"`
	Folder string  `json:"folder"`
	URL    *string `json:"url"`
}

type jobDetailJSON struct {
	jobJSON
	Files        []fileJSON         `json:"files"`
	Uploads      []uploadJSON       `json:"uploads"`
	History      []eventJSON        `json:"history"`
	ManualImport []manualImportJSON `json:"manualImport"`
	Target       *targetJSON        `json:"target"` // the title the job is matched to (#37)
	// The import and its result (#40); null before the job is confirmed, and result until
	// the files are imported.
	Import *flows.ImportView `json:"import"`
	Result *flows.ResultView `json:"result"`
	// Flow 2 (#52): the film already has a file; the comparison and what can be done.
	// null for every other job.
	Flow2 *flows.Flow2View `json:"flow2"`
}

func ptr[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

// view builds the Job JSON of a job from its files and unfinished uploads.
func (s *Server) view(ctx context.Context, u auth.User, j store.Job, files []store.File, uploads []store.Upload) jobJSON {
	v := jobJSON{
		ID: j.ID, Owner: j.Owner, State: string(j.State), Attempt: j.Attempt,
		CreatedAt: j.CreatedAt.UTC(), UpdatedAt: j.UpdatedAt.UTC(),
	}
	if !j.ExpiresAt.IsZero() {
		t := j.ExpiresAt.UTC()
		v.ExpiresAt = &t
	}
	if j.ErrorCode != "" {
		v.Error = &jobError{Code: j.ErrorCode, Message: errorMessage(u, j)}
	}
	v.Name = jobName(files, uploads)
	for _, f := range files {
		if f.Status != store.FileRefused { // a refused file is no longer on disk
			v.FileCount++
			v.TotalSize += f.Size
		}
	}
	pre, err := s.beforeImport(ctx, j)
	if err != nil {
		s.log.Error("job_api", "job", j.ID, "error", err)
	}
	v.Actions = s.actions(j, u.Admin, pre, len(files) > 0 && len(uploads) == 0)
	return v
}

// errorMessage is English text for the error of a failed or stalled job. The technical
// detail is for admins only: it may name paths or services.
func errorMessage(u auth.User, j store.Job) string {
	if u.Admin && j.ErrorText != "" {
		return j.ErrorText
	}
	if j.State == store.Stalled {
		return "The job is waiting for something outside Fukku and will continue by itself."
	}
	return "The job failed."
}

// jobName is the first archive's name, else the first video's, else the first unfinished
// upload's, else nil. files are ordered by path.
func jobName(files []store.File, uploads []store.Upload) *string {
	for _, kind := range []store.FileKind{store.KindArchive, store.KindVideo} {
		for _, f := range files {
			if f.Kind == kind {
				return ptr(path.Base(f.Path))
			}
		}
	}
	if len(uploads) > 0 {
		return ptr(uploads[0].Name)
	}
	return nil
}

// actions is what a user may do with a job now. canSubmit: the job has a file and no
// unfinished upload.
func (s *Server) actions(j store.Job, admin, beforeImport, canSubmit bool) []string {
	state := j.State
	out := []string{}
	if beforeImport || s.canCancel(j, admin) {
		out = append(out, "cancel")
	}
	if state == store.Failed {
		out = append(out, "retry")
	}
	if slices.Contains(deletable, state) {
		out = append(out, "delete")
	}
	if state == store.Uploading && canSubmit {
		out = append(out, "submit")
	}
	return out
}

// canCancel reports whether a job may be cancelled. A job stalled on its way through the
// import may be cancelled by an admin only: an import command of it may still be running
// in the app, and cancelling removes the job's files. So may an importing job that has
// been importing for longer than server.jobStuckAfter (a command that never ends); a job
// that is importing for less is not cancelled by anyone. (A job that failed there may be
// cancelled by its user: its command has ended.)
func (s *Server) canCancel(j store.Job, admin bool) bool {
	if s.abandons(j) {
		return admin && (j.State != store.Importing || s.stuckImport(j))
	}
	return slices.Contains(cancellable, j.State)
}

// beforeImport reports whether j is a merge job (#55) in importing, or stalled on its way
// through the import, whose import has not been submitted (it has no import record, or
// one with a zero SubmittedAt). Nothing has been sent to the app then, so its owner may
// cancel it (cancelJob; the store checks it again, in the move's transaction). An error
// reads as false: the job then follows the rules of any import.
func (s *Server) beforeImport(ctx context.Context, j store.Job) (bool, error) {
	if !s.abandons(j) {
		return false, nil
	}
	t, err := s.deps.Store.Target(ctx, j.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !flows.IsMerge(t)) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	imp, err := s.deps.Store.Import(ctx, j.ID)
	if errors.Is(err, store.ErrNotFound) {
		return true, nil
	}
	return err == nil && imp.SubmittedAt.IsZero(), err
}

// abandons reports whether cancelling j gives up on an import that may still be running in
// the app: j is importing, or stalled on its way through the import.
func (s *Server) abandons(j store.Job) bool {
	return j.State == store.Importing || stalledImport(j)
}

// stuckImport reports whether j has been importing for longer than server.jobStuckAfter.
func (s *Server) stuckImport(j store.Job) bool {
	return j.State == store.Importing && s.now().Sub(j.UpdatedAt) > s.cfg.Server.JobStuckAfter.D()
}

// stalledImport reports whether j is stalled while importing or after it.
func stalledImport(j store.Job) bool {
	return j.State == store.Stalled && slices.Contains([]store.State{store.Importing, store.Imported, store.PostProcessing}, j.Resume)
}

func (s *Server) fileView(f store.File) fileJSON {
	v := fileJSON{
		ID: f.ID, Path: f.Path, Name: path.Base(f.Path), Kind: string(f.Kind), Size: f.Size,
		Status: string(f.Status), Reason: ptr(f.Reason), From: ptr(f.From),
	}
	hasReport := len(f.Report) > 0 && string(f.Report) != "null"
	switch {
	case f.Kind == store.KindVideo && f.Status == store.FileOK && hasReport:
		v.Probe = f.Report
	case f.Kind == store.KindArchive && f.Status != store.FilePending && hasReport:
		v.Extraction = f.Report
	}
	return v
}

// manualImport lists, for each configured app, the job's files folder as that app sees it
// and a link to the app, when its public address is set.
func (s *Server) manualImport(j store.Job) []manualImportJSON {
	out := []manualImportJSON{}
	folder := filepath.ToSlash(filepath.Join(jobdir.Dir(s.cfg.Paths.IntakeRoot, j.ID), jobdir.Files))
	for _, app := range []struct {
		name string
		cfg  config.Arr
		maps []config.PathMapping
	}{
		{"radarr", s.cfg.Radarr, s.cfg.Paths.Mappings.Radarr},
		{"sonarr", s.cfg.Sonarr, s.cfg.Paths.Mappings.Sonarr},
	} {
		if app.cfg.URL == "" {
			continue
		}
		m := manualImportJSON{App: app.name, Folder: pathmap.To(app.maps, folder)}
		if app.cfg.PublicURL != "" {
			m.URL = ptr(strings.TrimRight(app.cfg.PublicURL, "/") + manualImportPage)
		}
		out = append(out, m)
	}
	return out
}

// ---- Helpers --------------------------------------------------------------------------

// user returns the signed-in user, or answers 403 (the route is outside the middleware).
func (s *Server) user(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := auth.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusForbidden, "forbidden", "Not signed in.")
	}
	return u, ok
}

func notFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "No such job.")
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("job_api", "method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
}

// ownJob loads the job named in the URL. A malformed id (checked before anything touches
// the disk), an unknown id and somebody else's job all answer 404.
func (s *Server) ownJob(w http.ResponseWriter, r *http.Request) (auth.User, store.Job, bool) {
	u, ok := s.user(w, r)
	if !ok {
		return u, store.Job{}, false
	}
	id := r.PathValue("id")
	if !jobdir.ValidID(id) {
		notFound(w)
		return u, store.Job{}, false
	}
	j, err := s.deps.Store.Job(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		notFound(w)
		return u, store.Job{}, false
	case err != nil:
		s.internalError(w, r, err)
		return u, store.Job{}, false
	case j.Owner != u.Name && !u.Admin:
		notFound(w)
		return u, store.Job{}, false
	}
	return u, j, true
}

// respondJob answers 200 with the job as it is now.
func (s *Server) respondJob(w http.ResponseWriter, r *http.Request, u auth.User, j store.Job, status int) {
	files, uploads, err := s.parts(r.Context(), j)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, status, s.view(r.Context(), u, j, files, uploads))
}

// parts loads a job's files and its unfinished uploads.
func (s *Server) parts(ctx context.Context, j store.Job) ([]store.File, []store.Upload, error) {
	files, err := s.deps.Store.Files(ctx, j.ID)
	if err != nil {
		return nil, nil, err
	}
	uploads, err := s.deps.Store.Uploads(ctx, "", j.ID)
	if err != nil {
		return nil, nil, err
	}
	return files, uploads, nil
}

func (s *Server) audit(ctx context.Context, u auth.User, action, jobID string, detail map[string]string) {
	e := store.AuditEntry{Actor: u.Name, Email: u.Email, Action: action, JobID: jobID, Detail: detail}
	if err := s.deps.Store.Audit(ctx, e); err != nil {
		s.log.Error("audit_failed", "action", action, "job", jobID, "error", err)
	}
}

// move changes a job's state as the user and answers with the job, or maps the store's
// refusal to 409 wrong_state.
func (s *Server) move(w http.ResponseWriter, r *http.Request, u auth.User, j store.Job, to store.State, action string) {
	s.moveNoted(w, r, u, j, to, action, "", nil)
}

// abandon cancels a job whose import may still be running in the app, as an admin. The
// app may still finish its ManualImport command and put the files in the library after
// the job's files are gone, and Fukku would not see it: the transition's note and the
// import.abandoned audit entry name the app and the command (when it is known), so that
// an admin can check the title in the app.
func (s *Server) abandon(w http.ResponseWriter, r *http.Request, u auth.User, j store.Job) {
	ctx := r.Context()
	detail := map[string]string{}
	app := "the app"
	if t, err := s.deps.Store.Target(ctx, j.ID); err == nil {
		detail["app"], app = t.App, t.App
	} else if !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	imp, err := s.deps.Store.Import(ctx, j.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	note := "import abandoned by an admin"
	switch {
	case imp.CommandID != 0 && !imp.FinishedAt.IsZero():
		detail["command"] = strconv.Itoa(imp.CommandID)
		note = fmt.Sprintf("%s; ManualImport command %d had ended", note, imp.CommandID)
	case imp.CommandID != 0:
		detail["command"] = strconv.Itoa(imp.CommandID)
		note = fmt.Sprintf("%s; %s may still finish ManualImport command %d", note, app, imp.CommandID)
	case !imp.SubmittedAt.IsZero():
		detail["command"] = "unknown"
		note = fmt.Sprintf("%s; %s may still finish a ManualImport submitted for it (its id is not known)", note, app)
	default:
		note += "; nothing was submitted to " + app
	}
	s.moveNoted(w, r, u, j, store.Cancelled, "import.abandoned", note, detail)
}

// moveNoted is move with a note on the transition and more detail in the audit entry.
func (s *Server) moveNoted(w http.ResponseWriter, r *http.Request, u auth.User, j store.Job, to store.State, action, note string, detail map[string]string) {
	next, err := s.deps.Store.Transition(r.Context(), j.ID, j.State, to, store.Change{Actor: u.Name, Note: note})
	switch {
	case errors.Is(err, store.ErrNotFound):
		notFound(w)
		return
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrInvalidTransition):
		writeError(w, http.StatusConflict, "wrong_state", "The job is not in a state that allows this.")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	audit := map[string]string{"from": string(j.State), "to": string(next.State)}
	maps.Copy(audit, detail)
	s.audit(r.Context(), u, action, j.ID, audit)
	s.respondJob(w, r, u, next, http.StatusOK)
}

func wrongState(w http.ResponseWriter) {
	writeError(w, http.StatusConflict, "wrong_state", "The job is not in a state that allows this.")
}

// ---- Handlers -------------------------------------------------------------------------

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	u, ok := s.user(w, r)
	if !ok {
		return
	}
	j, err := s.deps.Store.CreateJob(r.Context(), u.Name)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.audit(r.Context(), u, "job.created", j.ID, nil)
	writeJSON(w, http.StatusCreated, s.view(r.Context(), u, j, nil, nil))
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	u, ok := s.user(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := store.JobFilter{Limit: defaultJobLimit}
	for _, raw := range q["state"] {
		st := store.State(raw)
		if !slices.Contains(allStates, st) {
			writeError(w, http.StatusBadRequest, "bad_request", "Unknown state "+strconv.Quote(raw)+".")
			return
		}
		f.States = append(f.States, st)
	}
	mine := false
	if raw := q.Get("mine"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "mine must be true or false.")
			return
		}
		mine = b
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxJobLimit {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be from 1 to "+strconv.Itoa(maxJobLimit)+".")
			return
		}
		f.Limit = n
	}
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "offset must be 0 or more.")
			return
		}
		f.Offset = n
	}
	if !u.Admin || mine {
		f.Owner = u.Name
	}
	jobs, err := s.deps.Store.Jobs(r.Context(), f)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]jobJSON, 0, len(jobs))
	for _, j := range jobs {
		files, uploads, err := s.parts(r.Context(), j)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		out = append(out, s.view(r.Context(), u, j, files, uploads))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	files, uploads, err := s.parts(r.Context(), j)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	history, err := s.deps.Store.History(r.Context(), j.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	d := jobDetailJSON{
		jobJSON: s.view(r.Context(), u, j, files, uploads),
		Files:   make([]fileJSON, 0, len(files)),
		Uploads: []uploadJSON{}, History: make([]eventJSON, 0, len(history)), ManualImport: []manualImportJSON{},
	}
	for _, f := range files {
		d.Files = append(d.Files, s.fileView(f))
	}
	if j.State == store.Uploading {
		for _, up := range uploads {
			d.Uploads = append(d.Uploads, uploadJSON{ID: up.ID, Name: up.Name, Size: up.Size, Offset: up.Offset})
		}
	}
	// The store records transitions only; the history starts with the job's creation.
	if len(history) == 0 || history[0].From != "" {
		d.History = append(d.History, eventJSON{At: j.CreatedAt.UTC(), To: string(store.Uploading), Actor: j.Owner})
	}
	for _, e := range history {
		d.History = append(d.History, eventJSON{At: e.At.UTC(), From: ptr(string(e.From)), To: string(e.To), Actor: e.Actor, Note: e.Note})
	}
	if t, err := s.deps.Store.Target(r.Context(), j.ID); err == nil {
		d.Target = targetView(t)
		d.Flow2 = flows.ViewFlow2(s.cfg, t, files)
	} else if !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	if j.State == store.Probed {
		d.ManualImport = s.manualImport(j)
	}
	if s.deps.Imports != nil {
		if d.Import, d.Result, err = s.deps.Imports.Detail(r.Context(), j, u.Admin); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) submitJob(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	if j.State != store.Uploading {
		wrongState(w)
		return
	}
	files, uploads, err := s.parts(r.Context(), j)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if len(uploads) > 0 {
		writeError(w, http.StatusConflict, "uploads_unfinished", "Some uploads are not finished.")
		return
	}
	if len(files) == 0 {
		writeError(w, http.StatusConflict, "no_files", "The job has no files.")
		return
	}
	s.move(w, r, u, j, store.Uploaded, "job.submitted")
}

// cancelJob only changes the state; stopping the tasks and removing the files is the
// job runner's work (#29).
func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	pre, err := s.beforeImport(r.Context(), j)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if pre {
		s.cancelBeforeImport(w, r, u, j)
		return
	}
	if !s.canCancel(j, u.Admin) {
		wrongState(w)
		return
	}
	if s.abandons(j) {
		s.abandon(w, r, u, j)
		return
	}
	s.move(w, r, u, j, store.Cancelled, "job.cancelled")
}

// cancelBeforeImport cancels a merge job whose import is not submitted (#92). The store
// checks that, and makes the move, in one transaction: against the engine's submission
// (Store.BeginImport) whichever commits first wins, so an import that has been submitted
// is never cancelled this way (it is then refused, as any import is). Nothing of the
// job is in the library, so there is nothing to undo; the runner stops the merge task and
// removes the job's files.
func (s *Server) cancelBeforeImport(w http.ResponseWriter, r *http.Request, u auth.User, j store.Job) {
	next, err := s.deps.Store.CancelUnsubmitted(r.Context(), j.ID, j.State, store.Change{Actor: u.Name, Note: "cancelled before the import was submitted"})
	switch {
	case errors.Is(err, store.ErrNotFound):
		notFound(w)
		return
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrInvalidTransition):
		wrongState(w)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	s.audit(r.Context(), u, "job.cancelled", j.ID, map[string]string{"from": string(j.State), "to": string(next.State)})
	s.respondJob(w, r, u, next, http.StatusOK)
}

func (s *Server) retryJob(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	if j.State != store.Failed || j.Resume == "" {
		wrongState(w)
		return
	}
	s.move(w, r, u, j, j.Resume, "job.retried")
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) {
	u, j, ok := s.ownJob(w, r)
	if !ok {
		return
	}
	if !slices.Contains(deletable, j.State) {
		writeError(w, http.StatusConflict, "job_active", "The job is still active.")
		return
	}
	ctx := r.Context()
	uploads, err := s.deps.Store.Uploads(ctx, "", j.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	root := s.cfg.Paths.IntakeRoot
	if err := jobdir.Remove(root, j.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, up := range uploads {
		if !jobdir.ValidID(up.ID) {
			s.log.Warn("job_api", "note", "upload with a malformed id skipped", "job", j.ID)
			continue
		}
		if err := jobdir.RemoveUpload(root, up.ID); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	if err := s.deps.Store.DeleteJob(ctx, j.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, r, err)
		return
	}
	s.audit(ctx, u, "job.deleted", j.ID, map[string]string{"state": string(j.State)})
	w.WriteHeader(http.StatusNoContent)
}
