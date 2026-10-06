// SPDX-License-Identifier: Apache-2.0

// Package routes maps URLs to handlers.
package routes

import (
	"net/http"

	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/tracker/public"
)

// Register adds the routes every page needs: method override and the
// static files. The tracker's pages are in tracker.go, the accounts' in
// auth.go.
func Register(r *web.Router, sessions *session.Manager) {
	r.UseGlobal(web.MethodOverride) // HTML forms can send PUT and DELETE with _method
	r.HandleStd(http.MethodGet, "/assets/{path...}", public.Assets)
}
