// Package ui embeds the built single-page app (ui/dist, produced by `npm run build`).
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built app. It holds only .gitkeep when the UI has not been built.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embed pattern guarantees the directory exists
	}
	return sub
}
