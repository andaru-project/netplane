// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build embedui

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// DistFS returns the embedded production SPA filesystem rooted at dist/.
func DistFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
