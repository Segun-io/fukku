package uploads

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	tusd "github.com/tus/tusd/v2/pkg/handler"

	"github.com/segun-io/fukku/internal/auth"
	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/safename"
	"github.com/segun-io/fukku/internal/store"
)

// creation is an upload that passed the creation checks, on its way to tusd's pre-create
// hook through the request context.
type creation struct {
	id, jobID, owner, name string
	size                   int64
	stored                 bool // the row has been written
}

type creationKey struct{}

// create handles POST: the checks of the contract, in its order, then tusd creates the
// upload. createMu is held throughout, so the limits hold under concurrent creations.
func (h *Handler) create(w http.ResponseWriter, r *http.Request, user auth.User) {
	if r.ContentLength > 0 || r.Header.Get("Content-Type") == "application/offset+octet-stream" {
		writeError(w, errBadRequest("Create the upload without data, then send the data with PATCH."))
		return
	}
	if r.Header.Get("Upload-Concat") != "" {
		writeError(w, errBadRequest("Concatenation is not supported."))
		return
	}

	h.createMu.Lock()
	defer h.createMu.Unlock()

	c, e := h.checkCreate(r.Context(), r, user)
	if e != nil {
		writeError(w, e)
		return
	}

	iw := newInterceptor(w, func(status int, hdr http.Header) {
		if status == http.StatusCreated {
			hdr.Set("Location", h.location(c.id))
			hdr.Set("Upload-Expires", h.expires(h.now()))
		}
	})
	req := withPath(r.WithContext(context.WithValue(r.Context(), creationKey{}, c)), "")
	h.tusMW.ServeHTTP(iw, req)
	iw.finish(h.log)

	if iw.status != http.StatusCreated {
		if c.stored {
			h.forget(r.Context(), c.id, "creation_failed")
		}
		return
	}
	h.log.Info("upload_created", "upload", c.id, "job", c.jobID, "user", c.owner, "name", c.name, "size", c.size)
}

// checkCreate runs the creation checks after the sentinel check (done by ServeHTTP).
func (h *Handler) checkCreate(ctx context.Context, r *http.Request, user auth.User) (*creation, *apiError) {
	meta := tusd.ParseMetadataHeader(r.Header.Get("Upload-Metadata"))

	// The job: it exists, belongs to this user and is still taking uploads.
	jobID, ok := meta["jobId"]
	if !ok {
		return nil, errBadRequest("Upload-Metadata needs jobId.")
	}
	if !jobdir.ValidID(jobID) {
		return nil, errNotFound("No such job.")
	}
	job, err := h.st.Job(ctx, jobID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, errNotFound("No such job.")
	case err != nil:
		return nil, h.internal("job_lookup", err)
	case job.Owner != user.Name:
		return nil, errNotFound("No such job.")
	case job.State != store.Uploading:
		return nil, errJobNotUploading(job.State)
	}

	// The file name: one clean path element, an accepted extension, not yet in the job.
	raw, ok := meta["filename"]
	if !ok {
		return nil, errBadRequest("Upload-Metadata needs filename.")
	}
	name, err := safename.Clean(raw, h.maxName)
	if err != nil {
		return nil, &apiError{http.StatusBadRequest, "bad_name", "The file name is empty, has a path separator or is too long."}
	}
	if strings.HasPrefix(name, ".") {
		// Names starting with a dot are hidden, and the intake root uses them for its own
		// temporary files.
		return nil, &apiError{http.StatusBadRequest, "bad_name", "The file name must not start with a dot."}
	}
	if kind, _ := h.kindOf(name); kind == "" {
		return nil, &apiError{http.StatusUnsupportedMediaType, "extension_not_allowed",
			fmt.Sprintf("Files ending in %q are not accepted.", strings.ToLower(filepath.Ext(name)))}
	}
	files, err := h.st.Files(ctx, jobID)
	if err != nil {
		return nil, h.internal("files_lookup", err)
	}
	for _, f := range files {
		if _, base, ok := strings.Cut(f.Path, "/"); ok && !strings.Contains(base, "/") && strings.EqualFold(base, name) {
			return nil, errDuplicate(name)
		}
	}
	pending, err := h.st.Uploads(ctx, "", jobID)
	if err != nil {
		return nil, h.internal("uploads_lookup", err)
	}
	for _, u := range pending {
		if strings.EqualFold(u.Name, name) {
			return nil, errDuplicate(name)
		}
	}

	// The length: given up front, and within upload.maxFileSize.
	if r.Header.Get("Upload-Defer-Length") != "" {
		return nil, errBadRequest("Upload-Length is required; deferred length is not supported.")
	}
	size, err := strconv.ParseInt(r.Header.Get("Upload-Length"), 10, 64)
	if err != nil || size < 0 {
		return nil, errBadRequest("Upload-Length is missing or invalid.")
	}
	if size > int64(h.cfg.MaxFileSize) {
		return nil, &apiError{http.StatusRequestEntityTooLarge, "file_too_large",
			fmt.Sprintf("Files may be at most %d bytes (upload.maxFileSize).", int64(h.cfg.MaxFileSize))}
	}

	// The limits, over every unfinished upload.
	all, err := h.st.Uploads(ctx, "", "")
	if err != nil {
		return nil, h.internal("uploads_lookup", err)
	}
	var mine int
	var inFlight, stillNeeded int64
	for _, u := range all {
		if u.Owner == user.Name {
			mine++
		}
		inFlight += u.Size
		stillNeeded += max(u.Size-u.Offset, 0)
	}
	if mine >= h.cfg.MaxParallelPerUser {
		return nil, &apiError{http.StatusTooManyRequests, "too_many_uploads",
			fmt.Sprintf("You can run at most %d uploads at once (upload.maxParallelPerUser).", h.cfg.MaxParallelPerUser)}
	}
	if inFlight+size > int64(h.cfg.MaxInFlight) {
		return nil, &apiError{http.StatusInsufficientStorage, "in_flight_limit",
			"Too much is being uploaded right now (upload.maxInFlight); try again later."}
	}
	free, err := h.freeSpace(h.root)
	if err != nil {
		return nil, h.internal("free_space", err)
	}
	if free >= 0 && free-stillNeeded < size+int64(h.cfg.FreeSpaceMargin) {
		return nil, &apiError{http.StatusInsufficientStorage, "insufficient_space",
			"Not enough free space for this file (upload.freeSpaceMargin)."}
	}

	return &creation{id: newID(), jobID: jobID, owner: user.Name, name: name, size: size}, nil
}

// preCreate is tusd's pre-create hook: it writes the upload's row and fixes the id and the
// stored metadata. The checks ran before, in create.
func (h *Handler) preCreate(ev tusd.HookEvent) (tusd.HTTPResponse, tusd.FileInfoChanges, error) {
	c, ok := ev.Context.Value(creationKey{}).(*creation)
	if !ok || ev.Upload.Size != c.size || ev.Upload.SizeIsDeferred {
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, errBadRequest("Upload-Length is missing or invalid.").tus()
	}
	now := h.now().UTC()
	u := store.Upload{
		ID: c.id, JobID: c.jobID, Owner: c.owner, Name: c.name, Size: c.size,
		CreatedAt: now, ProgressAt: now,
	}
	if err := h.st.PutUpload(context.WithoutCancel(ev.Context), u); err != nil {
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, h.internal("upload_create", err).tus()
	}
	c.stored = true
	return tusd.HTTPResponse{}, tusd.FileInfoChanges{
		ID:       c.id,
		MetaData: tusd.MetaData{"filename": c.name, "jobId": c.jobID},
	}, nil
}

// kindOf says what a file is by its extension, and the job directory it goes to.
func (h *Handler) kindOf(name string) (store.FileKind, string) {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case slices.Contains(h.cfg.VideoExtensions, ext):
		return store.KindVideo, jobdir.Files
	case slices.Contains(h.cfg.SubtitleExtensions, ext):
		return store.KindSubtitle, jobdir.Files
	case slices.Contains(h.cfg.ArchiveExtensions, ext):
		return store.KindArchive, jobdir.Archives
	}
	return "", ""
}
