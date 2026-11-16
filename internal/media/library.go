package media

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// The library probe (#52): the worker's second, read-only way into the file system. A
// task names a library file as Fukku sees it; the worker reads it only when the path is a
// clean, absolute path inside one of its own paths.libraryRoots, and then only through an
// os.Root of that root, so neither ".." nor a symbolic link (of the file or of a folder on
// the way) can lead outside the root. Nothing is written: the file is opened read-only, and
// the reference deployment mounts the library read-only into the worker as well.

// LibraryPath finds the library root that holds p, the longest one when roots are nested,
// and p relative to it. p must be absolute and clean (no "..", no "//", no trailing
// slash): a path that needs cleaning is refused rather than cleaned, so what is read is
// exactly what was named. ok is false when no root holds p, or p is a root itself.
func LibraryPath(roots []string, p string) (root, rel string, ok bool) {
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return "", "", false
	}
	for _, r := range roots {
		if r == "" || !filepath.IsAbs(r) {
			continue
		}
		r = filepath.Clean(r)
		var rest string
		switch {
		case r == string(filepath.Separator):
			rest = p[1:]
		case strings.HasPrefix(p, r+string(filepath.Separator)):
			rest = p[len(r)+1:]
		default:
			continue
		}
		if rest == "" || !filepath.IsLocal(rest) {
			continue
		}
		if len(r) > len(root) {
			root, rel, ok = r, rest, true
		}
	}
	return root, rel, ok
}

// RunLibrary probes the library file args.Path, confined to roots (paths.libraryRoots).
// A path outside them is refused (outside_library) without touching the disk; one the root
// cannot open (missing, a link that leaves the root, not a regular file) is unreadable.
// The checks after that are the upload probe's, except the extension list: the file is one
// Radarr already imported. An error means the task could not run.
func RunLibrary(ctx context.Context, roots []string, args LibraryProbeArgs, opt Options) (LibraryProbeOutput, error) {
	out := LibraryProbeOutput{Path: args.Path}
	if err := ctx.Err(); err != nil {
		return LibraryProbeOutput{}, err
	}
	root, rel, ok := LibraryPath(roots, args.Path)
	if !ok {
		out.Refusal = RefusalOutsideLibrary
		return out, nil
	}
	lib, err := os.OpenRoot(root)
	if err != nil {
		out.Refusal = RefusalUnreadable // the library is not mounted, say
		return out, nil
	}
	defer func() { _ = lib.Close() }()
	pf := ProbedFile{Path: rel, Kind: "video"}
	formats := formatWhitelist
	if len(opt.LibraryFormats) > 0 {
		formats = strings.Join(opt.LibraryFormats, ",")
	}
	if err := probeVideo(ctx, lib, &pf, opt, formats); err != nil {
		return LibraryProbeOutput{}, err
	}
	out.OK, out.Refusal, out.Media = pf.OK, pf.Refusal, pf.Media
	return out, nil
}
