// Package uploads is the resumable upload endpoint, /api/uploads/: tus 1.0 with the
// creation, termination and expiration extensions, built on tusd. Each upload names its job;
// the data is kept in <intakeRoot>/.incoming/ while it arrives, and a finished upload is
// moved into the job directory and becomes one of the job's files (pending until the worker
// has checked it).
//
// The web process is meant to be the only writer of .incoming/; it never takes a path from a
// file there (incomingStore). Every unfinished upload is a
// store.Upload row, so the limits (per user, in flight, free space) are decided from the
// database, and a restart finds every upload again.
package uploads

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tusd "github.com/tus/tusd/v2/pkg/handler"
	"github.com/tus/tusd/v2/pkg/memorylocker"

	"github.com/segun-io/fukku/internal/auth"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/fscheck"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/metrics"
	"github.com/segun-io/fukku/internal/store"
)

// unsupportedHeaders are request headers the endpoint removes before tusd sees them:
// X-HTTP-Method-Override, with which tusd lets a POST stand in for PATCH or DELETE (the
// checks here go by r.Method), and the headers of the IETF resumable-upload drafts, which
// tusd reads in part even with its experimental protocol off and which would reach
// extensions the endpoint does not have (a deferred length). It speaks tus 1.0 only.
var unsupportedHeaders = []string{
	"X-HTTP-Method-Override",
	"Upload-Draft-Interop-Version", "Upload-Complete", "Upload-Incomplete",
}

// dropUnsupportedHeaders removes unsupportedHeaders from h.
func dropUnsupportedHeaders(h http.Header) {
	for _, k := range unsupportedHeaders {
		h.Del(k)
	}
}

// BasePath is where the endpoint is mounted.
const BasePath = "/api/uploads/"

// Extensions are the tus extensions the endpoint supports (the Tus-Extension header).
const Extensions = "creation,termination,expiration"

const (
	// progressEvery is how often an arriving chunk updates the upload's row at most.
	progressEvery = 2 * time.Second
	// lockTimeout is how long a request waits for another request on the same upload to stop.
	lockTimeout = 20 * time.Second
	// settleTime is how long a request waits for the previous request on the same upload to
	// end by itself before it has tusd interrupt that one (see turn).
	settleTime = time.Second
	dirMode    = 0o755
	fileMode   = 0o644
)

// Options configure a Handler.
type Options struct {
	Config config.Config
	Log    *slog.Logger
	Store  store.Store
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// FreeSpace returns the bytes available on the filesystem that holds dir; nil means the
	// operating system's answer. Tests replace it.
	FreeSpace func(dir string) (int64, error)
	// Metrics counts the bytes received; nil: not counted.
	Metrics *metrics.Set
}

// Handler serves /api/uploads/. It is safe for concurrent use.
type Handler struct {
	cfg        config.Upload
	paths      config.Paths
	publicBase string
	maxName    int
	root       string // paths.intakeRoot
	incoming   string // <root>/.incoming
	log        *slog.Logger
	st         store.Store
	now        func() time.Time
	freeSpace  func(string) (int64, error)
	metrics    *metrics.Set

	tus    *tusd.UnroutedHandler
	tusMW  http.Handler
	locker *memorylocker.MemoryLocker

	// createMu makes the creation checks and the new row one step, so two uploads created
	// at the same time cannot both take the last slot.
	createMu sync.Mutex
	// finishMu serialises moving finished uploads and terminating uploads.
	finishMu sync.Mutex
	// rowMu orders updates of upload rows with their deletion, so a late progress update
	// never brings a finished upload's row back.
	rowMu sync.Mutex

	readyMu sync.Mutex
	ready   atomic.Bool

	turnsMu sync.Mutex
	turns   map[string]chan struct{}

	// done remembers finished uploads for upload.idleUploadExpiry, so a client whose answer
	// to the last PATCH was lost still sees its upload as complete (see tombstone).
	doneMu sync.Mutex
	done   map[string]tombstone
}

// tombstone is what is left of a finished upload: enough to answer its owner's HEAD or a
// repeated last PATCH.
type tombstone struct {
	owner string
	size  int64
	until time.Time
}

// New builds the handler. It does not touch the disk; Start (or the first request) creates
// the directories and reconciles what a crash left behind.
func New(opts Options) (*Handler, error) {
	cfg := opts.Config
	h := &Handler{
		cfg:        cfg.Upload,
		paths:      cfg.Paths,
		publicBase: strings.TrimRight(cfg.Server.PublicBaseURL, "/"),
		maxName:    cfg.Extract.MaxNameBytes,
		root:       cfg.Paths.IntakeRoot,
		incoming:   filepath.Join(cfg.Paths.IntakeRoot, jobdir.Incoming),
		log:        opts.Log,
		st:         opts.Store,
		now:        opts.Now,
		freeSpace:  opts.FreeSpace,
		locker:     memorylocker.New(),
		turns:      map[string]chan struct{}{},
		done:       map[string]tombstone{},
	}
	if h.log == nil {
		h.log = slog.New(slog.DiscardHandler)
	}
	if h.now == nil {
		h.now = time.Now
	}
	if h.freeSpace == nil {
		h.freeSpace = freeSpace
	}
	if h.st == nil {
		return nil, errors.New("uploads: no store")
	}

	composer := tusd.NewStoreComposer()
	composer.UseCore(incomingStore{intakeRoot: h.root, fileMode: fileMode, sizeOf: h.rowSize})
	h.locker.UseIn(composer)

	tus, err := tusd.NewUnroutedHandler(tusd.Config{
		StoreComposer:             composer,
		BasePath:                  BasePath,
		MaxSize:                   int64(h.cfg.MaxFileSize),
		DisableDownload:           true,
		DisableTermination:        true, // DELETE is answered here, see terminate
		DisableConcatenation:      true,
		Cors:                      &tusd.CorsConfig{Disable: true}, // same origin only (#5)
		Logger:                    tusLogger(h.log),
		PreUploadCreateCallback:   h.preCreate,
		PreFinishResponseCallback: h.preFinish,
		AcquireLockTimeout:        lockTimeout,
		NetworkTimeout:            h.cfg.RequestIdleTimeout.D(),
	})
	if err != nil {
		return nil, fmt.Errorf("uploads: %w", err)
	}
	h.tus = tus
	h.tusMW = tus.Middleware(http.HandlerFunc(h.route))
	return h, nil
}

// Start prepares the intake root: it creates .incoming/ and jobs/ and reconciles the upload
// rows with the disk. It does nothing while a sentinel file is missing (nothing is ever
// created under a missing mount); the first request after the sentinels are back does it.
func (h *Handler) Start(ctx context.Context) {
	if err := h.prepare(ctx); err != nil {
		h.log.Warn("uploads_unavailable", "error", err)
	}
}

func (h *Handler) prepare(ctx context.Context) error {
	if h.ready.Load() {
		return nil
	}
	h.readyMu.Lock()
	defer h.readyMu.Unlock()
	if h.ready.Load() {
		return nil
	}
	if err := fscheck.Sentinels(h.paths); err != nil {
		return err
	}
	// worker/ too: the worker container mounts it on its own (jobdir.Prepare).
	for _, dir := range []string{h.incoming, filepath.Join(h.root, jobdir.Jobs), filepath.Join(h.root, jobdir.Worker)} {
		if err := mkdirAll(dir); err != nil {
			return err
		}
	}
	// A symlinked .incoming is refused here, at once, and by every open (incomingStore).
	r, err := openIncoming(h.root)
	if err != nil {
		return err
	}
	_ = r.Close()
	if err := h.reconcile(ctx); err != nil {
		return err
	}
	h.ready.Store(true)
	return nil
}

// ServeHTTP answers the tus requests under BasePath.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Tus-Resumable", "1.0.0")
	dropUnsupportedHeaders(r.Header)

	rest, ok := strings.CutPrefix(r.URL.Path, BasePath)
	if !ok {
		writeError(w, errNotFound("No such upload."))
		return
	}
	if r.Method == http.MethodOptions && rest == "" {
		iw := newInterceptor(w, func(int, http.Header) { w.Header().Set("Tus-Extension", Extensions) })
		h.tusMW.ServeHTTP(iw, withPath(r, ""))
		iw.finish(h.log)
		return
	}

	user, ok := auth.FromContext(r.Context())
	if !ok || user.Name == "" {
		writeError(w, &apiError{http.StatusForbidden, "forbidden", "Not signed in."})
		return
	}
	if err := fscheck.Sentinels(h.paths); err != nil {
		h.log.Warn("uploads_unavailable", "error", err)
		writeError(w, errStorage())
		return
	}
	if err := h.prepare(r.Context()); err != nil {
		h.log.Warn("uploads_unavailable", "error", err)
		writeError(w, errStorage())
		return
	}

	switch {
	case rest == "" && r.Method == http.MethodPost:
		h.create(w, r, user)
	case rest == "":
		w.Header().Set("Allow", "POST, OPTIONS")
		writeError(w, &apiError{http.StatusMethodNotAllowed, "bad_request", "Use POST to create an upload."})
	case !jobdir.ValidID(rest):
		writeError(w, errNotFound("No such upload."))
	case r.Method == http.MethodHead || r.Method == http.MethodPatch || r.Method == http.MethodDelete:
		h.existing(w, r, user, rest)
	default:
		w.Header().Set("Allow", "HEAD, PATCH, DELETE")
		writeError(w, &apiError{http.StatusMethodNotAllowed, "bad_request", "Use HEAD, PATCH or DELETE on an upload."})
	}
}

// route passes a request that went through tusd's middleware to tusd.
func (h *Handler) route(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.tus.PostFile(w, r)
	case http.MethodHead:
		h.tus.HeadFile(w, r)
	case http.MethodPatch:
		h.tus.PatchFile(w, r)
	default:
		writeError(w, &apiError{http.StatusMethodNotAllowed, "bad_request", "Method not allowed."})
	}
}

// existing handles HEAD, PATCH and DELETE on an upload. Only its owner may use it; anyone
// else gets 404, as if it did not exist.
func (h *Handler) existing(w http.ResponseWriter, r *http.Request, user auth.User, id string) {
	ctx := r.Context()
	// Look the upload up before and again after waiting for the previous request on it,
	// which may have finished it meanwhile.
	if _, ok := h.lookup(w, r, user, id); !ok {
		return
	}
	defer h.turn(ctx, id)()
	row, ok := h.lookup(w, r, user, id)
	if !ok {
		return
	}

	if r.Method == http.MethodDelete {
		h.terminate(w, r, row)
		return
	}
	if r.Method == http.MethodPatch {
		job, err := h.st.Job(ctx, row.JobID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, errNotFound("No such job."))
			return
		case err != nil:
			writeError(w, h.internal("job_lookup", err))
			return
		case job.State != store.Uploading:
			writeError(w, errJobNotUploading(job.State))
			return
		}
	}

	// All the data is here but the upload was not finished: the answer to the last PATCH was
	// lost, or moving the file failed. Finish it now, so the client sees a complete upload.
	if size, ok := h.dataSize(id); ok && size == row.Size {
		h.answerComplete(w, r, row)
		return
	}

	if r.Method == http.MethodPatch {
		limit := int64(h.cfg.ChunkSize)
		if r.ContentLength > limit {
			writeError(w, &apiError{http.StatusRequestEntityTooLarge, "too_large",
				fmt.Sprintf("One request may carry at most %d bytes (upload.chunkSize).", limit)})
			return
		}
		body := r.Body
		if r.ContentLength < 0 {
			body = http.MaxBytesReader(w, body, limit)
		}
		start, _ := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
		r.Body = &progressBody{ReadCloser: body, h: h, id: id, start: start, last: h.now()}
	}

	iw := newInterceptor(w, func(status int, hdr http.Header) {
		if status != http.StatusOK && status != http.StatusNoContent {
			return
		}
		progressAt := row.ProgressAt
		if off, err := strconv.ParseInt(hdr.Get("Upload-Offset"), 10, 64); err == nil && off > row.Offset {
			progressAt = h.now()
		}
		if off := hdr.Get("Upload-Offset"); off != strconv.FormatInt(row.Size, 10) {
			hdr.Set("Upload-Expires", h.expires(progressAt))
		}
	})
	h.tusMW.ServeHTTP(iw, withPath(r, id))

	if iw.status == http.StatusNotFound {
		h.notFoundByTus(w, r, iw, user, id)
		return
	}
	iw.finish(h.log)
	if size, ok := h.dataSize(id); ok {
		h.noteProgress(ctx, id, size)
	}
}

// lookup returns the owner's upload, or answers the request: as complete for a finished
// upload (HEAD, a repeated last PATCH), else 404.
func (h *Handler) lookup(w http.ResponseWriter, r *http.Request, user auth.User, id string) (store.Upload, bool) {
	row, err := h.st.Upload(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if t, ok := h.completed(id); ok && t.owner == user.Name && r.Method != http.MethodDelete {
			h.answerDone(w, r, t.size)
			return row, false
		}
		writeError(w, errNotFound("No such upload."))
		return row, false
	case err != nil:
		writeError(w, h.internal("upload_lookup", err))
		return row, false
	case row.Owner != user.Name:
		writeError(w, errNotFound("No such upload."))
		return row, false
	}
	return row, true
}

// notFoundByTus decides what tusd's 404 for an upload that has a row means. The upload may
// have finished meanwhile (another request completed it), the mount may have gone, or the
// data is really gone; only then is the upload forgotten.
func (h *Handler) notFoundByTus(w http.ResponseWriter, r *http.Request, iw *interceptor, user auth.User, id string) {
	if t, ok := h.completed(id); ok && t.owner == user.Name {
		iw.discard()
		h.answerDone(w, r, t.size)
		return
	}
	if err := fscheck.Sentinels(h.paths); err != nil {
		h.log.Warn("uploads_unavailable", "error", err)
		iw.discard()
		writeError(w, errStorage())
		return
	}
	if h.dataGone(id) {
		h.forget(r.Context(), id, "data_missing")
	} else {
		h.log.Warn("upload_data_unreadable", "upload", id)
	}
	iw.finish(h.log)
}

// rowSize is the size an upload's row was created with (incomingStore.sizeOf).
func (h *Handler) rowSize(ctx context.Context, id string) (int64, error) {
	row, err := h.st.Upload(ctx, id)
	return row.Size, err
}

// dataGone reports whether neither the data nor the .info file of an upload exists.
func (h *Handler) dataGone(id string) bool {
	r, err := openIncoming(h.root)
	if err != nil {
		return false // cannot tell: keep the upload
	}
	defer func() { _ = r.Close() }()
	for _, p := range []string{id, id + ".info"} {
		if _, err := r.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			return false
		}
	}
	return true
}

// remember keeps a tombstone for a finished upload until upload.idleUploadExpiry, and
// drops expired ones.
func (h *Handler) remember(row store.Upload) {
	now := h.now()
	h.doneMu.Lock()
	defer h.doneMu.Unlock()
	for id, t := range h.done {
		if now.After(t.until) {
			delete(h.done, id)
		}
	}
	h.done[row.ID] = tombstone{owner: row.Owner, size: row.Size, until: now.Add(h.cfg.IdleUploadExpiry.D())}
}

// completed returns the tombstone of a finished upload.
func (h *Handler) completed(id string) (tombstone, bool) {
	h.doneMu.Lock()
	defer h.doneMu.Unlock()
	t, ok := h.done[id]
	if ok && h.now().After(t.until) {
		delete(h.done, id)
		return tombstone{}, false
	}
	return t, ok
}

// turn lets a request on an upload wait, up to settleTime, for the previous request on the
// same upload to end, and returns the function that ends this request's turn. A client
// sends its requests for one upload one after another, but its next request can arrive
// while the server is still finishing the previous one; tusd would interrupt that one.
// After settleTime the request goes ahead anyway: the previous one is stuck (a connection
// that died without a word), and tusd stops it, as it should.
func (h *Handler) turn(ctx context.Context, id string) func() {
	h.turnsMu.Lock()
	ch, ok := h.turns[id]
	if !ok {
		ch = make(chan struct{}, 1)
		h.turns[id] = ch
	}
	h.turnsMu.Unlock()
	t := time.NewTimer(settleTime)
	defer t.Stop()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }
	case <-t.C:
	case <-ctx.Done():
	}
	return func() {}
}

// answerComplete finishes an upload whose data is complete and answers HEAD or PATCH as tus
// does for a complete upload.
func (h *Handler) answerComplete(w http.ResponseWriter, r *http.Request, row store.Upload) {
	if e := h.finish(r.Context(), row.ID); e != nil {
		if _, ok := h.completed(row.ID); !ok {
			writeError(w, e)
			return
		}
	}
	h.answerDone(w, r, row.Size)
}

// answerDone answers HEAD or PATCH for a complete upload of size bytes. A PATCH at any
// offset up to size is the repeat of a request whose answer was lost: its data is already
// here, so it is answered with the final offset and its body is not read.
func (h *Handler) answerDone(w http.ResponseWriter, r *http.Request, size int64) {
	n := strconv.FormatInt(size, 10)
	if r.Method == http.MethodPatch {
		off, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
		if err != nil || off < 0 || off > size {
			writeError(w, &apiError{http.StatusConflict, "offset_mismatch", "Upload-Offset does not match the upload's offset; send HEAD first."})
			return
		}
	}
	hdr := w.Header()
	hdr.Del("Content-Length")
	hdr.Del("Content-Type")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Upload-Offset", n)
	if r.Method == http.MethodHead {
		hdr.Set("Upload-Length", n)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// terminate deletes an unfinished upload (tus termination). A request still sending data to
// it is stopped first.
func (h *Handler) terminate(w http.ResponseWriter, r *http.Request, row store.Upload) {
	lock, err := h.locker.NewLock(row.ID)
	if err != nil {
		writeError(w, h.internal("upload_lock", err))
		return
	}
	lctx, cancel := context.WithTimeout(r.Context(), lockTimeout)
	defer cancel()
	if err := lock.Lock(lctx, func() {}); err != nil {
		writeError(w, &apiError{http.StatusLocked, "upload_busy", "Another request is still working on this upload; try again."})
		return
	}
	defer func() { _ = lock.Unlock() }()

	h.finishMu.Lock()
	defer h.finishMu.Unlock()
	ctx := r.Context()
	if _, err := h.st.Upload(ctx, row.ID); errors.Is(err, store.ErrNotFound) {
		writeError(w, errNotFound("No such upload."))
		return
	} else if err != nil {
		writeError(w, h.internal("upload_lookup", err))
		return
	}
	if err := jobdir.RemoveUpload(h.root, row.ID); err != nil {
		writeError(w, h.internal("upload_remove", err))
		return
	}
	if err := h.deleteRow(ctx, row.ID); err != nil {
		writeError(w, h.internal("upload_remove", err))
		return
	}
	h.log.Info("upload_deleted", "upload", row.ID, "job", row.JobID, "user", row.Owner, "name", row.Name)
	w.WriteHeader(http.StatusNoContent)
}

// preFinish runs before tusd answers the PATCH that completes an upload: the file becomes
// part of the job before the client hears that the upload is complete.
func (h *Handler) preFinish(ev tusd.HookEvent) (tusd.HTTPResponse, error) {
	if e := h.finish(ev.Context, ev.Upload.ID); e != nil {
		return tusd.HTTPResponse{}, e.tus()
	}
	return tusd.HTTPResponse{}, nil
}

// noteProgress records that an upload's data has reached offset, if that is more than its
// row says.
func (h *Handler) noteProgress(ctx context.Context, id string, offset int64) {
	ctx = context.WithoutCancel(ctx)
	h.rowMu.Lock()
	defer h.rowMu.Unlock()
	row, err := h.st.Upload(ctx, id)
	if err != nil || offset <= row.Offset {
		return
	}
	row.Offset = offset
	row.ProgressAt = h.now().UTC()
	if err := h.st.PutUpload(ctx, row); err != nil {
		h.log.Warn("upload_progress_not_saved", "upload", id, "error", err)
	}
}

func (h *Handler) deleteRow(ctx context.Context, id string) error {
	h.turnsMu.Lock()
	delete(h.turns, id)
	h.turnsMu.Unlock()
	h.rowMu.Lock()
	defer h.rowMu.Unlock()
	err := h.st.DeleteUpload(context.WithoutCancel(ctx), id)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

// forget drops an upload whose data is gone: its row and whatever is left in .incoming/.
func (h *Handler) forget(ctx context.Context, id, reason string) {
	if err := jobdir.RemoveUpload(h.root, id); err != nil {
		h.log.Warn("upload_remove_failed", "upload", id, "error", err)
	}
	if err := h.deleteRow(ctx, id); err != nil {
		h.log.Warn("upload_remove_failed", "upload", id, "error", err)
		return
	}
	h.log.Warn("upload_dropped", "upload", id, "reason", reason)
}

// expires is the Upload-Expires value for an upload last advanced at progressAt.
func (h *Handler) expires(progressAt time.Time) string {
	return progressAt.Add(h.cfg.IdleUploadExpiry.D()).UTC().Format(http.TimeFormat)
}

// location is the URL of an upload: a path, or an absolute URL when server.publicBaseUrl is
// set. Never built from the request, so it stays right behind a proxy that ends TLS.
func (h *Handler) location(id string) string {
	return h.publicBase + BasePath + id
}

func (h *Handler) internal(op string, err error) *apiError {
	h.log.Error("upload_error", "op", op, "error", err)
	return &apiError{http.StatusInternalServerError, "internal", "Internal error."}
}

// withPath returns r with its path cut down to the upload id, as tusd expects.
func withPath(r *http.Request, id string) *http.Request {
	r2 := new(http.Request)
	*r2 = *r
	u := *r.URL
	u.Path = "/" + id
	u.RawPath = ""
	r2.URL = &u
	return r2
}

// newID returns a fresh upload id: 16 random bytes as 32 lower-case hex characters.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// progressBody counts the bytes of a PATCH body and records them every progressEvery.
type progressBody struct {
	io.ReadCloser
	h     *Handler
	id    string
	start int64
	n     int64
	last  time.Time
}

func (b *progressBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n += int64(n)
	b.h.metrics.AddBytesReceived(int64(n))
	if now := b.h.now(); now.Sub(b.last) >= progressEvery {
		b.last = now
		b.h.noteProgress(context.Background(), b.id, b.start+b.n)
	}
	return n, err
}
