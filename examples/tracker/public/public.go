// SPDX-License-Identifier: Apache-2.0

// Package public holds the static files served under /assets.
package public

import (
	"embed"
	"io/fs"

	"anetos.dev/anetos/view"
	"anetos.dev/anetos/view/htmx"
)

//go:embed static
var files embed.FS

// Assets serves the files of the static directory and the bundled htmx,
// with content-hashed URLs: Assets.URL("app.css").
var Assets = mustAssets()

func mustAssets() *view.Assets {
	static, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	a, err := view.NewAssets("/assets", static, htmx.FS)
	if err != nil {
		panic(err)
	}
	return a
}
