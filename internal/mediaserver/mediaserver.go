// Package mediaserver tells the media server (Jellyfin) that the library changed and finds
// the new title there (plan §6, Media server refresh). Only the web process uses it.
package mediaserver

import (
	"context"
	"strings"
)

// Refresh methods (mediaServer.refreshMethod).
const (
	// MethodLibraryRefresh refreshes the library item that holds the path
	// (POST /Items/{libraryId}/Refresh). The title is visible in under a second (decision
	// #22, probe J1b). The default.
	MethodLibraryRefresh = "libraryRefresh"
	// MethodPathUpdate reports the path (POST /Library/Media/Updated). The server acts on
	// it only after its LibraryMonitorDelay, 60 s by default (probe J1).
	MethodPathUpdate = "pathUpdate"
	// MethodLibraryScan starts a full library scan (POST /Library/Refresh).
	MethodLibraryScan = "libraryScan"
	// MethodNone does not tell the server anything.
	MethodNone = "none"
)

// MediaServer is what Fukku needs from a media server. Every path is as the server sees it.
type MediaServer interface {
	Version(ctx context.Context) (string, error)
	// Refresh tells the server that paths (as the server sees them) changed.
	Refresh(ctx context.Context, paths []string) error
	// Find returns the server's item id for a path, or "" while it does not know it yet.
	Find(ctx context.Context, path string) (itemID string, err error)
}

// TitleLink fills the {itemId} placeholder of mediaServer.titleLinkTemplate. An empty
// template or item id gives "": no link.
func TitleLink(template, itemID string) string {
	if template == "" || itemID == "" {
		return ""
	}
	return strings.ReplaceAll(template, "{itemId}", itemID)
}
