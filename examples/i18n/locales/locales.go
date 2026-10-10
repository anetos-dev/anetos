// SPDX-License-Identifier: Apache-2.0

// Package locales holds the example's translations: en.yaml, and the
// files of the bn folder.
package locales

import "embed"

// FS holds the catalogs, for i18n.New.
//
//go:embed *
var FS embed.FS
