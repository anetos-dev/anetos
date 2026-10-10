// Package public is the web root: its files are served at / (robots.txt
// at /robots.txt, .well-known/ at /.well-known/), and those of static/
// at /assets/, with content-hashed URLs.
package public

import (
	"embed"
	"io/fs"

	"anetos.dev/anetos/view"
	"anetos.dev/anetos/view/htmx"
)

// Files are the folder's files, which routes/web.go serves at / with
// r.Static (Go files and other hidden files aren't served).
//
//go:embed *
var Files embed.FS

// Assets serves the files of the static directory and the bundled htmx,
// with content-hashed URLs: Assets.URL("app.css").
var Assets = mustAssets()

func mustAssets() *view.Assets {
	static, err := fs.Sub(Files, "static")
	if err != nil {
		panic(err)
	}
	a, err := view.NewAssets("/assets", static, htmx.FS)
	if err != nil {
		panic(err)
	}
	return a
}
