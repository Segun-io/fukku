package uploads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	tusd "github.com/tus/tusd/v2/pkg/handler"

	"github.com/segun-io/fukku/internal/jobdir"
)

// maxInfoSize is the largest .info file read: the upload's metadata is two short fields.
const maxInfoSize = 64 << 10

// errIncomingUnsafe is a .incoming/ that is not a real directory of the intake root, or an
// upload file there that is not a regular file of its own name.
var errIncomingUnsafe = errors.New("uploads: .incoming or an upload file in it is not what the web process created")

// incomingStore is tusd's data store for .incoming/. It keeps tusd's filestore layout
// (<id> for the data, <id>.info for the JSON FileInfo, the same Storage keys), so uploads
// in progress survive an upgrade, but it never takes a path from a file: the worker can
// write the intake root and is assumed compromised (docs/threat-model.md, B4), and tusd's
// own filestore opens whatever path an .info file names. Here every path is computed from
// the upload id, every open goes through an os.Root of .incoming, which must be a real
// directory of the intake root (not a symbolic link), and an upload file must be a regular
// file that is not a link.
type incomingStore struct {
	intakeRoot string
	fileMode   fs.FileMode
}

// dir is .incoming as the Storage keys of an .info file name it (tusd's filestore wrote
// absolute paths).
func (s incomingStore) dir() string { return filepath.Join(s.intakeRoot, jobdir.Incoming) }

// openIncoming opens .incoming as an os.Root, refusing it unless it is a directory of the
// intake root itself: a symbolic link there (even one to another directory of the intake
// root) is refused, also when it is put in place between the check and the open.
func openIncoming(intakeRoot string) (*os.Root, error) {
	intake, err := os.OpenRoot(intakeRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = intake.Close() }()
	li, err := intake.Lstat(jobdir.Incoming)
	if err != nil {
		return nil, err
	}
	if !li.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", errIncomingUnsafe, jobdir.Incoming)
	}
	r, err := intake.OpenRoot(jobdir.Incoming)
	if err != nil {
		return nil, err
	}
	fi, err := r.Stat(".")
	if err != nil || !os.SameFile(li, fi) {
		_ = r.Close()
		return nil, fmt.Errorf("%w: %s changed while it was opened", errIncomingUnsafe, jobdir.Incoming)
	}
	return r, nil
}

// openRegular opens name in r only if it is a regular file and not a symbolic link: the
// name is checked with Lstat, and the open file must be the same file (a link swapped in
// between is refused). O_NONBLOCK keeps a FIFO from hanging the request.
func openRegular(r *os.Root, name string, flag int) (*os.File, error) {
	li, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !li.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", errIncomingUnsafe, name)
	}
	f, err := r.OpenFile(name, flag|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || !os.SameFile(li, fi) {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s changed while it was opened", errIncomingUnsafe, name)
	}
	return f, nil
}

// dataSize is the size of an upload's data, if it is there and safe to use.
func dataSize(intakeRoot, id string) (int64, bool) {
	if !jobdir.ValidID(id) {
		return 0, false
	}
	r, err := openIncoming(intakeRoot)
	if err != nil {
		return 0, false
	}
	defer func() { _ = r.Close() }()
	fi, err := r.Lstat(id)
	if err != nil || !fi.Mode().IsRegular() {
		return 0, false
	}
	return fi.Size(), true
}

// NewUpload creates <id> and <id>.info. The id is the one preCreate chose; a file of that
// name is never reused.
func (s incomingStore) NewUpload(_ context.Context, info tusd.FileInfo) (tusd.Upload, error) {
	if !jobdir.ValidID(info.ID) {
		return nil, errors.New("uploads: malformed upload id")
	}
	r, err := openIncoming(s.intakeRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	f, err := r.OpenFile(info.ID, os.O_CREATE|os.O_EXCL|os.O_WRONLY, s.fileMode)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	info.Storage = map[string]string{
		"Type":               "filestore",
		filestoreKeyPath:     filepath.Join(s.dir(), info.ID),
		filestoreKeyInfoPath: filepath.Join(s.dir(), info.ID+".info"),
	}
	u := &incomingUpload{store: s, info: info}
	if err := u.writeInfo(r); err != nil {
		return nil, err
	}
	return u, nil
}

// The Storage keys of tusd's filestore (filestore.StorageKeyPath, StorageKeyInfoPath).
const (
	filestoreKeyPath     = "Path"
	filestoreKeyInfoPath = "InfoPath"
)

// GetUpload reads <id>.info and the size of <id>. An .info file for another id, or whose
// Storage names any other place than .incoming/<id> and .incoming/<id>.info, is refused:
// the paths are never taken from it.
func (s incomingStore) GetUpload(_ context.Context, id string) (tusd.Upload, error) {
	if !jobdir.ValidID(id) {
		return nil, tusd.ErrNotFound
	}
	r, err := openIncoming(s.intakeRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	f, err := openRegular(r, id+".info", os.O_RDONLY)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, tusd.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxInfoSize+1))
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	if len(data) > maxInfoSize {
		return nil, fmt.Errorf("%w: %s.info is too large", errIncomingUnsafe, id)
	}
	var info tusd.FileInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("%w: %s.info: %w", errIncomingUnsafe, id, err)
	}
	if info.ID != id {
		return nil, fmt.Errorf("%w: %s.info names another upload", errIncomingUnsafe, id)
	}
	if !s.ownPath(info.Storage[filestoreKeyPath], id) || !s.ownPath(info.Storage[filestoreKeyInfoPath], id+".info") {
		return nil, fmt.Errorf("%w: %s.info names a path outside .incoming/%s", errIncomingUnsafe, id, id)
	}
	fi, err := r.Lstat(id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, tusd.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", errIncomingUnsafe, id)
	}
	info.Offset = fi.Size()
	return &incomingUpload{store: s, info: info}, nil
}

// ownPath reports whether a Storage path of an .info file is name in .incoming: unset, the
// bare name, or the absolute path tusd's filestore and NewUpload write.
func (s incomingStore) ownPath(p, name string) bool {
	return p == "" || p == name || p == filepath.Join(s.dir(), name)
}

// incomingUpload is one upload of incomingStore.
type incomingUpload struct {
	store incomingStore
	info  tusd.FileInfo
}

func (u *incomingUpload) GetInfo(context.Context) (tusd.FileInfo, error) { return u.info, nil }

// WriteChunk appends src to the upload's data (tusd has checked the offset and limits the
// length).
func (u *incomingUpload) WriteChunk(_ context.Context, _ int64, src io.Reader) (int64, error) {
	r, err := openIncoming(u.store.intakeRoot)
	if err != nil {
		return 0, err
	}
	defer func() { _ = r.Close() }()
	f, err := openRegular(r, u.info.ID, os.O_WRONLY|os.O_APPEND)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, src)
	u.info.Offset += n
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// GetReader is never used (downloads are disabled), but tusd's interface needs it.
func (u *incomingUpload) GetReader(context.Context) (io.ReadCloser, error) {
	r, err := openIncoming(u.store.intakeRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return openRegular(r, u.info.ID, os.O_RDONLY)
}

func (u *incomingUpload) FinishUpload(context.Context) error { return nil }

// writeInfo writes <id>.info once, when the upload is created (no extension of the
// endpoint changes it later); an existing file of that name is never replaced.
func (u *incomingUpload) writeInfo(r *os.Root) error {
	data, err := json.Marshal(u.info)
	if err != nil {
		return err
	}
	f, err := r.OpenFile(u.info.ID+".info", os.O_CREATE|os.O_EXCL|os.O_WRONLY, u.store.fileMode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
