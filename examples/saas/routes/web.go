// SPDX-License-Identifier: Apache-2.0

// Package routes maps URLs to handlers.
package routes

import (
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/saas/app/handlers"
	"anetos.dev/anetos/examples/saas/public"
)

// Register adds the application's routes.
func Register(r *web.Router, sessions *session.Manager) {
	r.UseGlobal(web.MethodOverride) // HTML forms can send PUT and DELETE with _method
	r.Get("/assets/{path...}", web.WrapHandler(public.Assets))
	r.Static("/", public.Files) // robots.txt, .well-known/…: after the routes

	// Pages: sessions, flash messages and CSRF protection.
	pages := r.Group("", sessions.Middleware, web.CSRF())
	pages.Get("/", handlers.Home{}.Show).Name("home")
}
