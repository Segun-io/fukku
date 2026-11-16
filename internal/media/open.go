package media

import (
	"os"
)

// What the other worker tasks that run ffmpeg on uploads and library files (the preview
// clips, #53) share with the probe, so that every tool is confined in the same way.

// FormatWhitelist is the demuxers the worker lets ffmpeg and ffprobe use on an upload
// (the containers of upload.videoExtensions), as an ffmpeg option value.
const FormatWhitelist = formatWhitelist

// ProtocolWhitelist is the only protocol the worker lets ffmpeg and ffprobe open: the
// inherited descriptor ("fd"), so no other file and no URL.
const ProtocolWhitelist = protocolWhitelist

// OpenRegular opens p through root, read-only, without blocking on a FIFO, and requires a
// regular file. ok is false when it cannot be opened or is not a regular file; the caller
// refuses it as unreadable.
func OpenRegular(root *os.Root, p string) (f *os.File, size int64, ok bool) {
	f, size = openRegular(root, p)
	return f, size, f != nil
}

// OpenLibrary opens the library file p read-only, as the library probe does: only when p
// is a clean path inside one of roots (paths.libraryRoots; else the refusal is
// RefusalOutsideLibrary, and the disk is not touched), and only through an os.Root of that
// root, so that neither ".." nor a link leads out of it (else RefusalUnreadable).
func OpenLibrary(roots []string, p string) (f *os.File, size int64, refusal string) {
	root, rel, ok := LibraryPath(roots, p)
	if !ok {
		return nil, 0, RefusalOutsideLibrary
	}
	lib, err := os.OpenRoot(root)
	if err != nil {
		return nil, 0, RefusalUnreadable
	}
	defer func() { _ = lib.Close() }()
	f, size, ok = OpenRegular(lib, rel)
	if !ok {
		return nil, 0, RefusalUnreadable
	}
	return f, size, ""
}
