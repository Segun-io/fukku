// Package mediaserver tells the media server (Jellyfin) that the library changed and finds
// the new title there (plan §6, Media server refresh). Only the web process uses it.
package mediaserver

import (
	"context"
	"net/url"
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
	// Scan starts a scan of every library on the server (the fallback of
	// mediaServer.refreshFallback). It does not start one while a scan is running, and then
	// reports started false.
	Scan(ctx context.Context) (started bool, err error)
	// Find returns the server's item id for a path, or "" while it does not know it yet.
	Find(ctx context.Context, path string) (itemID string, err error)
}

// Grouper is implemented by a media server that can show two files as versions of one
// title (#56, decision #20): Jellyfin's merge-versions call. Second versions of films are
// grouped with one.
type Grouper interface {
	// MergeVersions groups the items (two or more) as versions of one title.
	MergeVersions(ctx context.Context, itemIDs []string) error
	// Sources returns the paths (as the server sees them) of the versions the server shows
	// for the item: its own file and the files grouped with it. Empty when the server does
	// not list the item.
	Sources(ctx context.Context, itemID string) ([]string, error)
}

// TitleLink fills the {itemId} placeholder of mediaServer.titleLinkTemplate. An empty
// template or item id gives "": no link. The id is URL-escaped (it comes from the media
// server and may hold characters that mean something in a URL), so it stays one value
// wherever the template puts it: in the path, a query or the fragment.
func TitleLink(template, itemID string) string {
	if template == "" || itemID == "" {
		return ""
	}
	escaped := strings.ReplaceAll(url.QueryEscape(itemID), "+", "%20")
	return strings.ReplaceAll(template, "{itemId}", escaped)
}
