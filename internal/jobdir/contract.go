// Package jobdir names the directories under paths.intakeRoot. It never opens media files.
package jobdir

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// Layout under the intake root:
//
//	.incoming/<upload id>[.info]   unfinished tus uploads (web process only; the worker has
//	                               no view of it)
//	jobs/<job id>/                 written by the web process only; the worker reads it
//	                               through a read-only mount (docs/install.md)
//	jobs/<job id>/files/           what is offered to Radarr or Sonarr: accepted loose
//	                               uploads and extracted archive entries
//	jobs/<job id>/archives/        uploaded archives; never offered to the *arr apps
//	jobs/<job id>/merged/<name>/   verified merge output (flow 2a, #54): one directory per
//	                               merge, which no upload or extraction can create
//	jobs/<job id>/versions/<row>/  an episode's second version under its library name, a hard
//	                               link to the upload, for Sonarr's verdict before it is
//	                               placed (#58); never imported
//	worker/<job id>/               everything the worker writes for a job, the only place it
//	                               can write (#113, #114, #138): its scratch space work/
//	                               (preview clips, extraction and merge scratch) and the
//	                               extracted files/ and merged/ that the web process then
//	                               moves into jobs/<job id>/ one regular file at a time
//	                               (Seal), so that nothing the worker can still change is
//	                               handed to an app or linked into the library
const (
	Incoming = ".incoming"
	Jobs     = "jobs"
	Worker   = "worker"
	Files    = "files"
	Archives = "archives"
	Work     = "work"
	Merged   = "merged"
	Versions = "versions"
)

var idRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidID reports whether id is a well-formed job id or upload id.
func ValidID(id string) bool { return idRE.MatchString(id) }

// Dir returns <intakeRoot>/jobs/<id>. It panics on a malformed id, so no caller can build
// a path from unchecked input.
func Dir(intakeRoot, id string) string {
	if !ValidID(id) {
		panic("jobdir: malformed job id")
	}
	return filepath.Join(intakeRoot, Jobs, id)
}

// Open opens a job's directory as an os.Root, from an os.Root of the intake root, so a
// symbolic link at jobs/ or jobs/<id> that leads out of the intake root is refused instead of
// followed (os.OpenRoot of the joined path would follow it). The worker is assumed
// compromised (docs/threat-model.md, B4) and, in a deployment that does not follow
// docs/install.md, may be able to write the intake root, so every operation on a job's
// directory starts here. A missing directory is an error wrapping fs.ErrNotExist. It panics
// on a malformed id, as Dir does.
func Open(intakeRoot, id string) (*os.Root, error) {
	if !ValidID(id) {
		panic("jobdir: malformed job id")
	}
	intake, err := os.OpenRoot(intakeRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = intake.Close() }()
	return intake.OpenRoot(filepath.Join(Jobs, id))
}

// OpenWorker opens the worker's directory of a job, worker/<id>, as an os.Root, from an
// os.Root of the intake root, like Open. The web process reads what the worker wrote there
// (preview clips, and the files Seal moves into the job) and never hands anything in it to
// an app. A missing directory is an error wrapping fs.ErrNotExist. It panics on a malformed
// id.
func OpenWorker(intakeRoot, id string) (*os.Root, error) {
	if !ValidID(id) {
		panic("jobdir: malformed job id")
	}
	intake, err := os.OpenRoot(intakeRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = intake.Close() }()
	return intake.OpenRoot(filepath.Join(Worker, id))
}

// MakeWorker is OpenWorker for the worker: it creates worker/<id> first when it is missing.
// worker/ itself is the worker's one writable mount and exists already, except in tests.
func MakeWorker(intakeRoot, id string) (*os.Root, error) {
	if !ValidID(id) {
		panic("jobdir: malformed job id")
	}
	intake, err := os.OpenRoot(intakeRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = intake.Close() }()
	if err := intake.MkdirAll(filepath.Join(Worker, id), 0o755); err != nil {
		return nil, err
	}
	return intake.OpenRoot(filepath.Join(Worker, id))
}

// Prepare creates the folders of the intake root that the two containers mount separately
// (.incoming/, jobs/ and worker/) when they are missing, as the web process's user. A
// container runtime creates a missing bind-mount source as root, and neither process could
// write it then (#138): the web process calls this at start-up, and docs/install.md has the
// folders made before the first start. The caller checks the sentinels first, so nothing is
// created under a missing mount.
func Prepare(intakeRoot string) error {
	for _, d := range []string{Incoming, Jobs, Worker} {
		if err := os.MkdirAll(filepath.Join(intakeRoot, d), 0o755); err != nil { //nolint:gosec // paths.intakeRoot, from the configuration
			return err
		}
	}
	return nil
}

// Remove deletes a job's directories and everything in them, the worker's (worker/<id>)
// first and jobs/<id> last, so that a job whose jobs/<id> is gone has nothing left. It goes
// through an os.Root of the intake root: a symbolic link at jobs/ or worker/ that leads out
// of the intake root is refused, and one at jobs/<id> or worker/<id> is removed itself,
// never followed. A missing intake root or job directory is not an error.
func Remove(intakeRoot, id string) error {
	if !ValidID(id) {
		panic("jobdir: malformed job id")
	}
	r, err := os.OpenRoot(intakeRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if err := r.RemoveAll(filepath.Join(Worker, id)); err != nil {
		return err
	}
	return r.RemoveAll(filepath.Join(Jobs, id))
}

// RemoveUpload deletes an unfinished upload's data and info files, through an os.Root of
// the intake root (a symbolic link at .incoming/ leading out of it is refused). Upload ids
// have the same form as job ids.
func RemoveUpload(intakeRoot, uploadID string) error {
	if !ValidID(uploadID) {
		panic("jobdir: malformed upload id")
	}
	r, err := os.OpenRoot(intakeRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	base := filepath.Join(Incoming, uploadID)
	for _, p := range []string{base, base + ".info"} {
		if err := r.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
