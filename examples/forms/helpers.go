// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"

	"anetos.dev/anetos/web"
)

func formTitle(n Note) string {
	if n.ID == 0 {
		return "New note"
	}
	return "Edit note"
}

// formAction is where the note form posts: the create route for a new note,
// the update route (with _method=PUT) for an existing one.
func formAction(ctx context.Context, n Note) (string, error) {
	if n.ID == 0 {
		return web.URL(ctx, "notes.create")
	}
	return web.URL(ctx, "notes.update", n.ID)
}
