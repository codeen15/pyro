// Package web embeds the built single-page app (web/dist) into the pyro binary.
// Run `make ui` (or `npm run build` in web/) before `go build` to refresh it.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built UI rooted at dist/.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
