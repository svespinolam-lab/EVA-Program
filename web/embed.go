// Package web embeds the front-ends served by EVA Station:
//
//	/        → pc/      EVA Station (PC program, "guy in the chair")
//	/app/    → app/     EVA mobile app (each explorer's phone)
//	/shared/ → shared/  design tokens, base styles, components, icons and logo
//
// The files are compiled into the executable, so eva-station.exe runs
// with no extra files. During development run the server with
// -web ./web to serve them straight from disk and edit them live.
package web

import (
	"embed"
	"io/fs"
)

//go:embed app pc shared
var files embed.FS

// FS returns the embedded web root (contains app/, pc/ and shared/).
func FS() fs.FS { return files }
