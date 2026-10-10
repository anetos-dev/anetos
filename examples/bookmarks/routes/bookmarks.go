// SPDX-License-Identifier: Apache-2.0

package routes

import (
	"net/http"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/web"

	"bookmarks/app/handlers"
)

// region: routes

// Bookmarks adds the bookmarks API: routes/auth.go calls it with its me
// group, so every request has a token. Reading needs the token's
// bookmarks:read ability, changing bookmarks bookmarks:write; a login's
// token has every ability.
func Bookmarks(r *web.Router) {
	var h handlers.Bookmarks
	read := r.With(auth.RequireAbilities("bookmarks:read"))
	write := r.With(auth.RequireAbilities("bookmarks:write"))
	read.Get("/bookmarks", web.H(h.Index)).Name("bookmarks.index")
	write.Post("/bookmarks", web.H(h.Create)).Name("bookmarks.create").Status(http.StatusCreated)
	read.Get("/bookmarks/{id}", web.H(h.Show)).Name("bookmarks.show")
	write.Put("/bookmarks/{id}", web.H(h.Update)).Name("bookmarks.update")
	write.Delete("/bookmarks/{id}", web.H(h.Delete)).Name("bookmarks.delete")
	write.Post("/bookmarks/{id}/archive", web.H(h.Archive)).Name("bookmarks.archive")
}

// endregion
