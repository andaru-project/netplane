// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build embedui

package main

import (
	"io/fs"

	"github.com/andrebrait/netplane/web"
)

func embeddedWebFS() fs.FS {
	return web.DistFS()
}
