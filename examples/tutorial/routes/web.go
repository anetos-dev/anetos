// Package routes maps URLs to handlers.
package routes

import (
	"net/http"

	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"

	"tracker/app/handlers"
	"tracker/public"
	"tracker/views"
)

// Register adds the application's routes.
func Register(r *web.Router, sessions *session.Manager) {
	r.UseGlobal(web.MethodOverride) // HTML forms can send PUT and DELETE with _method
	r.ErrorPages(func(_ *web.Ctx, e web.ErrorPage) view.Component { return views.ErrorPage(e) })
	r.HandleStd(http.MethodGet, "/assets/{path...}", public.Assets)
	r.Static("/", public.Files) // robots.txt, .well-known/…: after the routes

	// Pages: sessions, flash messages and CSRF protection.
	pages := r.Group("", sessions.Middleware, web.CSRF())
	pages.Get("/", handlers.Home{}.Show).Name("home")
}
