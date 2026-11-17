package uploads

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/segun-io/fukku/internal/jobdir"
	"github.com/segun-io/fukku/internal/store"
)

// link makes dst a second name of src; tests replace it to simulate a filesystem without
// hard links.
var link = func(root *os.Root, src, dst string) error { return root.Link(src, dst) }

var (
	errTargetTaken = errors.New("uploads: the job already has a file with this name")
	errIncomplete  = errors.New("uploads: the upload's data is not complete")
	errDataGone    = errors.New("uploads: the upload's data is gone")
)

// finish makes a complete upload part of its job, unless the job has stopped taking uploads.
// It is safe to call again, and concurrently with itself.
func (h *Handler) finish(ctx context.Context, id string) *apiError {
	ctx = context.WithoutCancel(ctx)
	h.finishMu.Lock()
	defer h.finishMu.Unlock()

	row, err := h.st.Upload(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return errNotFound("No such upload.")
	} else if err != nil {
		return h.internal("upload_lookup", err)
	}
	job, err := h.st.Job(ctx, row.JobID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errNotFound("No such job.")
	case err != nil:
		return h.internal("job_lookup", err)
	case job.State != store.Uploading:
		return errJobNotUploading(job.State)
	}
	return h.complete(ctx, row)
}

// complete moves a finished upload's data into the job directory by rename, never replacing
// anything, records the file (status pending) and deletes the upload's row. Every step can
// be repeated, so a crash at any point is repaired by running it again (reconcile).
// The caller holds finishMu.
func (h *Handler) complete(ctx context.Context, row store.Upload) *apiError {
	kind, dir := h.kindOf(row.Name)
	if kind == "" {
		return &apiError{http.StatusUnsupportedMediaType, "extension_not_allowed", "Files of this type are no longer accepted."}
	}
	if !jobdir.ValidID(row.ID) || !jobdir.ValidID(row.JobID) {
		return h.internal("upload_complete", errors.New("malformed id in upload row"))
	}
	inc, err := openIncoming(h.root) // refuses a symlinked .incoming
	if err != nil {
		return h.internal("upload_move", err)
	}
	_ = inc.Close()
	root, err := os.OpenRoot(h.root)
	if err != nil {
		return h.internal("upload_move", err)
	}
	defer func() { _ = root.Close() }()
	src, dst := h.relPaths(row, dir)

	switch err := moveNoReplace(root, src, dst, row.Size); {
	case errors.Is(err, errTargetTaken):
		return errDuplicate(row.Name)
	case errors.Is(err, errDataGone):
		return errNotFound("The upload's data is gone.")
	case err != nil:
		return h.internal("upload_move", err)
	}

	f := store.File{JobID: row.JobID, Path: dir + "/" + row.Name, Kind: kind, Size: row.Size, Status: store.FilePending}
	if _, err := h.st.AddFile(ctx, f); err != nil && !errors.Is(err, store.ErrExists) {
		return h.internal("file_add", err)
	}
	h.remember(row)
	if err := h.deleteRow(ctx, row.ID); err != nil {
		return h.internal("upload_delete", err)
	}
	if err := jobdir.RemoveUpload(h.root, row.ID); err != nil {
		// Only the .info file is left; retention sweeps it.
		h.log.Warn("upload_remove_failed", "upload", row.ID, "error", err)
	}
	h.log.Info("upload_completed", "upload", row.ID, "job", row.JobID, "user", row.Owner,
		"name", row.Name, "kind", string(kind), "size", row.Size)
	return nil
}

// relPaths are an upload's data in .incoming/ and its place in the job, relative to the
// intake root. dir is jobdir.Files or jobdir.Archives.
func (h *Handler) relPaths(row store.Upload, dir string) (src, dst string) {
	return filepath.Join(jobdir.Incoming, row.ID), filepath.Join(jobdir.Jobs, row.JobID, dir, row.Name)
}

// moveNoReplace moves src to dst inside root, keeping the inode, and fails with
// errTargetTaken when dst exists and is not src. It links dst to src, then removes src: a
// crash in between leaves two names for one inode, which the next call recognises.
func moveNoReplace(root *os.Root, src, dst string, size int64) error {
	if err := root.MkdirAll(filepath.Dir(dst), dirMode); err != nil {
		return err
	}
	si, serr := root.Lstat(src)
	di, derr := root.Lstat(dst)
	if serr != nil && !errors.Is(serr, fs.ErrNotExist) {
		return serr
	}
	if serr == nil && !si.Mode().IsRegular() {
		// Only the web process creates upload data, as regular files; anything else there
		// (a link put in by the worker) is never moved into a job.
		return errIncomingUnsafe
	}
	if derr != nil && !errors.Is(derr, fs.ErrNotExist) {
		return derr
	}

	switch {
	case serr == nil && derr == nil:
		if !os.SameFile(si, di) {
			return errTargetTaken
		}
		// Linked before a crash; only the old name is left to remove.
	case serr == nil:
		if si.Size() != size {
			return errIncomplete
		}
		if lerr := link(root, src, dst); lerr != nil {
			if errors.Is(lerr, fs.ErrExist) {
				return errTargetTaken
			}
			// A filesystem without hard links: rename, but only one that refuses to replace
			// (renameat2 RENAME_NOREPLACE on Linux; elsewhere the move fails).
			if err := renameNoReplace(root, src, dst); errors.Is(err, fs.ErrExist) {
				return errTargetTaken
			} else if err != nil {
				return fmt.Errorf("cannot move without replacing: link: %w; rename: %w", lerr, err)
			}
			return nil
		}
	case derr == nil:
		// Moved before a crash.
		if !di.Mode().IsRegular() || di.Size() != size {
			return errTargetTaken
		}
		return nil
	default:
		return errDataGone
	}
	if err := root.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// reconcile repairs, at start-up, what a crash left: for every upload row it finishes an
// upload whose data is complete (moved or not, with or without its file row), drops rows
// whose data is gone, and corrects the offset of the others.
func (h *Handler) reconcile(ctx context.Context) error {
	rows, err := h.st.Uploads(ctx, "", "")
	if err != nil {
		return err
	}
	h.finishMu.Lock()
	defer h.finishMu.Unlock()
	for _, row := range rows {
		h.reconcileOne(ctx, row)
	}
	return nil
}

func (h *Handler) reconcileOne(ctx context.Context, row store.Upload) {
	if !jobdir.ValidID(row.ID) || !jobdir.ValidID(row.JobID) {
		if err := h.deleteRow(ctx, row.ID); err == nil {
			h.log.Warn("upload_dropped", "upload", row.ID, "reason", "malformed_id")
		}
		return
	}
	job, err := h.st.Job(ctx, row.JobID)
	if errors.Is(err, store.ErrNotFound) {
		h.forget(ctx, row.ID, "job_gone")
		return
	} else if err != nil {
		h.log.Warn("upload_reconcile_failed", "upload", row.ID, "error", err)
		return
	}

	root, err := os.OpenRoot(h.root)
	if err != nil {
		h.log.Warn("upload_reconcile_failed", "upload", row.ID, "error", err)
		return
	}
	defer func() { _ = root.Close() }()
	_, dir := h.kindOf(row.Name)
	src, dst := h.relPaths(row, dir)
	si, serr := root.Lstat(src)
	_, ierr := root.Lstat(src + ".info")
	moved := false
	if dir != "" {
		di, derr := root.Lstat(dst)
		moved = derr == nil && (serr != nil || os.SameFile(si, di))
	}
	complete := serr == nil && si.Size() == row.Size

	switch {
	case moved || complete && job.State == store.Uploading:
		if e := h.complete(ctx, row); e != nil {
			if serr != nil {
				// What has the upload's name in the job is something else.
				h.forget(ctx, row.ID, "data_missing")
				return
			}
			h.log.Warn("upload_reconcile_failed", "upload", row.ID, "error", e.message)
			return
		}
		h.log.Info("upload_reconciled", "upload", row.ID, "job", row.JobID, "action", "completed")
	case serr != nil || ierr != nil:
		h.forget(ctx, row.ID, "data_missing")
	case si.Size() != row.Offset:
		row.Offset = si.Size()
		if t := si.ModTime(); t.After(row.ProgressAt) {
			row.ProgressAt = t.UTC()
		}
		if err := h.st.PutUpload(ctx, row); err != nil {
			h.log.Warn("upload_reconcile_failed", "upload", row.ID, "error", err)
		}
	}
}

// dataSize is the size of an upload's data in .incoming/, if it is there.
func (h *Handler) dataSize(id string) (int64, bool) { return dataSize(h.root, id) }

func mkdirAll(dir string) error {
	return os.MkdirAll(dir, dirMode) //nolint:gosec // directories under paths.intakeRoot, from the configuration
}
