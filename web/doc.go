// SPDX-License-Identifier: Apache-2.0

// Package web is Anetos's HTTP layer: a router on top of net/http's
// ServeMux, a request context, typed handlers with automatic binding,
// consistent error responses, standard middleware and a supervised server.
//
// Everything is net/http-compatible: routes can serve any http.Handler,
// middleware is func(http.Handler) http.Handler, and the [Router] itself is
// an http.Handler.
//
// A typical setup:
//
//	app, err := anetos.New()
//	if err != nil {
//		log.Fatal(err)
//	}
//	srv, err := web.NewServer(app) // reads HTTP_* config, adds the "http" component
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	r := srv.Router()
//	r.Get("/", home).Name("home")
//	r.Get("/posts/{id}", web.H(posts.Show)).Name("posts.show")
//	r.Post("/posts", web.H(posts.Create)).Name("posts.create")
//
//	// … then app.Run(ctx)
//
// Handlers come in three forms: [HandlerFunc] (func(*Ctx) error), typed
// handlers wrapped with [H], and plain http.Handlers wrapped with
// [WrapHandler]. Errors returned by handlers become JSON problem
// details or HTML error pages (see [DefaultErrorHandler]).
//
// Guides: docs/site/guides/routing.md and docs/site/guides/handlers.md.
package web
