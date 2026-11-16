// Package jobdir names the directories under paths.intakeRoot. It never opens media files.
package jobdir

import (
	"os"
	"path/filepath"
	"regexp"
)

// Layout under the intake root:
//
//	.incoming/<upload id>[.info]   unfinished tus uploads (web process only)
//	jobs/<job id>/files/           what is offered to Radarr or Sonarr: accepted loose
//	                               uploads and extracted archive entries
//	jobs/<job id>/archives/        uploaded archives; never offered to the *arr apps
//	jobs/<job id>/work/            worker scratch space (M3: preview clips, merge output)
const (
	Incoming = ".incoming"
	Jobs     = "jobs"
	Files    = "files"
	Archives = "archives"
	Work     = "work"
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

// Remove deletes a job's directory and everything in it.
func Remove(intakeRoot, id string) error { return os.RemoveAll(Dir(intakeRoot, id)) }

// RemoveUpload deletes an unfinished upload's data and info files. Upload ids have the
// same form as job ids.
func RemoveUpload(intakeRoot, uploadID string) error {
	if !ValidID(uploadID) {
		panic("jobdir: malformed upload id")
	}
	base := filepath.Join(intakeRoot, Incoming, uploadID)
	for _, p := range []string{base, base + ".info"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
