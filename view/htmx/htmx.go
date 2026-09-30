// SPDX-License-Identifier: Apache-2.0

// Package htmx bundles htmx (https://htmx.org), so pages get interactivity
// without a JavaScript build step. Serve it with the other static files:
//
//	assets, err := view.NewAssets("/assets", public.Files, htmx.FS)
//
//	<script src={ assets.URL("htmx.min.js") }></script>
//
// htmx is distributed under the Zero-Clause BSD license (see LICENSE in
// this directory).
package htmx

import "embed"

// Version is the bundled htmx release.
const Version = "2.0.11"

// FS holds htmx.min.js at its root.
//
//go:embed htmx.min.js
var FS embed.FS
