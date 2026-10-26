// SPDX-License-Identifier: Apache-2.0

// Package vite connects an app to a frontend that Vite (https://vite.dev)
// builds: JavaScript or TypeScript, CSS, and the frameworks Vite
// compiles. The frontend lives in the project's frontend/ folder; `vite
// build` writes it into public/build/, which the app embeds and [Handler]
// serves under /build/, with Vite's manifest (build/manifest.json)
// saying which files each entry needs.
//
// [New] sets it up in setup, from the app's web root:
//
//	if _, err := vite.New(app, public.Files); err != nil {
//		return nil, err
//	}
//
// routes/web.go serves the build:
//
//	r.Get("/build/{path...}", web.WrapHandler(vite.Handler()))
//
// and the layout loads an entry:
//
//	<head>
//		@vite.Tags("src/main.ts")
//	</head>
//
// In development, anetos dev runs Vite's development server beside the
// app, forwards /build/ to it, and sets VITE_DEV_URL=/build/: the tags
// then load the sources from Vite, which updates the page when they
// change (hot module replacement). VITE_DEV_URL is refused where the app
// is deployed (APP_ENV production or staging), and so is a missing
// manifest; elsewhere, without one, the tags are an HTML comment, so
// tests run without building the frontend.
//
// See the guide "Bundle JavaScript and CSS with Vite"
// (https://docs.anetos.dev/guides/vite/).
package vite
