// SPDX-License-Identifier: Apache-2.0

// Package locales holds the app's translations, embedded in the binary:
// a folder per locale (en/app.yaml, en/auth.yaml), or a file
// (bn.yaml). Add a language by adding its folder; keys are looked up
// with i18n.T(ctx, "home.title"), and `go run . lang:check` reports what
// a language is missing.
package locales

import "embed"

// FS holds the catalogs, for i18n.New.
//
//go:embed *
var FS embed.FS
