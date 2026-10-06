// Package routes maps URLs to handlers.
package routes

import (
	"net/http"

	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"

	"tracker/app/handlers"
	"tracker/public"
)

// Register adds the application's routes.
func Register(r *web.Router, sessions *session.Manager) {
	r.UseGlobal(web.MethodOverride) // HTML forms can send PUT and DELETE with _method
	r.HandleStd(http.MethodGet, "/assets/{path...}", public.Assets)

	// Pages: sessions, flash messages and CSRF protection.
	pages := r.Group("", sessions.Middleware, web.CSRF())
	pages.Get("/", handlers.Home{}.Show).Name("home")
}
