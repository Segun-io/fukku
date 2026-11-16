// Package version holds build information, set with -ldflags at build time.
package version

// Version is the release version, e.g. 0.1.0; "dev" for local builds.
var Version = "dev"

// Commit is the source commit the binary was built from.
var Commit = "unknown"
