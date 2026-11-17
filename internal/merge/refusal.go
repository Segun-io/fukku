package merge

// Class says what a refusal of the merge task means for the job that asked for it (#95).
type Class int

const (
	// Transient: the refusal comes from a fault that may pass (free space, a busy
	// destination, a library or an upload that cannot be read for now, a library file that
	// changed). Asking again with the same choice may work, so the job fails and a retry
	// runs the merge again (library_changed and busy have their own handling in flows).
	Transient Class = iota
	// Repeatable: the refusal comes from the choice itself (the tracks, the offset, what
	// mkvmerge makes of them), so the same choice fails the same way. The job goes back to
	// its confirmation with the refusal shown, and the user chooses again.
	Repeatable
)

func (c Class) String() string {
	if c == Repeatable {
		return "repeatable"
	}
	return "transient"
}

// Classes classifies every Refusal code, in this one place. A code that is not listed here
// fails TestEveryRefusalIsClassified, and Classify treats an unknown code as Transient (a
// retry, never a loop back to the confirmation).
var Classes = map[string]Class{
	RefusalTrackNotFound: Repeatable, // the chosen index is not an audio or subtitle track of the upload
	RefusalMuxFailed:     Repeatable, // mkvmerge refused this combination of tracks and offset
	RefusalVerifyFailed:  Repeatable, // the output of this choice failed a verification rule

	RefusalFreeSpace:         Transient, // the space can be freed
	RefusalBusy:              Transient, // another merge into the destination ends
	RefusalLibraryChanged:    Transient, // the film is matched again (back to probed)
	RefusalLibraryUnreadable: Transient, // a mount or a permission can be fixed
	RefusalUploadUnreadable:  Transient, // the same for the upload's storage
	RefusalOutsideLibrary:    Transient, // paths.libraryRoots or the path mappings can be fixed
}

// Classify returns the class of a refusal code; an unknown code is Transient.
func Classify(code string) Class { return Classes[code] }

// IsRepeatable reports whether the same choice would be refused again.
func IsRepeatable(code string) bool { return Classify(code) == Repeatable }
