// Package extract unpacks untrusted archives under a job directory (worker only).
package extract

// Args are the args of a spool task of kind "extract".
type Args struct {
	Archive string `json:"archive"` // job-relative, e.g. "archives/Season 01.zip"
	Dest    string `json:"dest"`    // job-relative directory to create, e.g. "files/Season 01"
}

// Output is its output.
type Output struct {
	// Refused is set when the whole archive was refused; nothing of it is then left on disk.
	// Codes: not_a_zip, too_many_entries, too_large, ratio, free_space.
	Refused string    `json:"refused,omitempty"`
	Files   []Entry   `json:"files"`   // what was written
	Skipped []Skipped `json:"skipped"` // entries that were not written
}

type Entry struct {
	Path string `json:"path"` // job-relative, under Dest, after name cleaning
	Size int64  `json:"size"`
}

type Skipped struct {
	Name string `json:"name"` // the entry's name, made printable and cut to 200 bytes
	// Reason codes: traversal, absolute, drive_letter, backslash, symlink, special,
	// nested_archive, extension, name_too_long, duplicate, encrypted.
	Reason string `json:"reason"`
}
