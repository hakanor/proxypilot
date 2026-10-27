// Package api serves the control API and the embedded web UI.
package api

import (
	"embed"
	"io/fs"
)

//go:embed all:webdist
var webdist embed.FS

// WebFS returns the built web UI rooted at webdist/.
func WebFS() fs.FS {
	sub, err := fs.Sub(webdist, "webdist")
	if err != nil {
		panic(err)
	}
	return sub
}
