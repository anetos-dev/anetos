// SPDX-License-Identifier: Apache-2.0

// Package docs holds the pages of Anetos's documentation, the Markdown
// in docs/site that docs.anetos.dev is built from, for tools: the anetos
// tool's docs command prints them at the version of Anetos a project
// uses, without a network. An app doesn't import it.
package docs

import (
	"embed"
	"io/fs"
)

//go:embed site/*.md site/*/*.md site/*/*/*.md
var site embed.FS

// Site is the documentation's pages, by their path in docs/site:
// "README.md", "guides/queues.md", "getting-started/tutorial/README.md".
var Site fs.FS = sub()

func sub() fs.FS {
	s, err := fs.Sub(site, "site")
	if err != nil {
		panic(err) // a constant, valid directory name
	}
	return s
}
