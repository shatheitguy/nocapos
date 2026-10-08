// Package web embeds the browser UI so alfad ships as a single binary.
//
// dist/ holds the built frontend. Today it is a dependency-free console; the
// React Web OS build output will replace it at the same path.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Assets returns the UI rooted at dist/.
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at compile time
	}
	return sub
}
