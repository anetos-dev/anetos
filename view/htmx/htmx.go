// SPDX-License-Identifier: Apache-2.0

// Package htmx bundles htmx (https://htmx.org), so pages get interactivity
// without a JavaScript build step, and its server-sent events extension
// (for streamed AI answers, say). Serve them with the other static files:
//
//	assets, err := view.NewAssets("/assets", public.Files, htmx.FS)
//
//	<script src={ assets.URL("htmx.min.js") }></script>
//	<script src={ assets.URL("htmx-ext-sse.min.js") }></script>
//
// htmx and the extension are distributed under the Zero-Clause BSD
// license (see LICENSE and LICENSE-sse in this directory).
package htmx

import "embed"

// Version is the bundled htmx release.
const Version = "2.0.11"

// SSEVersion is the bundled release of the server-sent events extension
// (htmx-ext-sse).
const SSEVersion = "2.2.4"

// FS holds htmx.min.js and htmx-ext-sse.min.js at its root.
//
//go:embed htmx.min.js htmx-ext-sse.min.js
var FS embed.FS
