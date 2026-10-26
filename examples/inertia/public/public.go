// SPDX-License-Identifier: Apache-2.0

// Package public is the web root: robots.txt, and build/, where Vite
// writes the frontend (npm run build in frontend/).
package public

import "embed"

// Files are the folder's files.
//
//go:embed *
var Files embed.FS
