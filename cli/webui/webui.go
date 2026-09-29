// Package webui embeds the built web UI. `make web` (vite build) writes
// the bundle into dist/. Only dist/.gitkeep is checked in, so the embed
// pattern matches on a fresh clone; vite restores it on every build from
// web/public/.gitkeep.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built web UI, or nil if it wasn't built before compiling.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
