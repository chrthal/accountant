// Package web embeds the HTML templates and static assets so the app ships as
// a single binary with no files to deploy alongside it (ADR 0001).
package web

import (
	"embed"
	"io/fs"
)

//go:embed templates/*.html templates/help/*.html
var Templates embed.FS

//go:embed static
var staticFS embed.FS

// Static serves the files under web/static/ rooted at the directory itself, so
// handlers can mount it at /static/ without the path appearing twice.
var Static = must(fs.Sub(staticFS, "static"))

func must(f fs.FS, err error) fs.FS {
	if err != nil {
		panic(err)
	}
	return f
}
