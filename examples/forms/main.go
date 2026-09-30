// SPDX-License-Identifier: Apache-2.0

// Command forms is a small notes app with server-rendered HTML: templ
// views with a layout, forms with CSRF protection, validation errors and
// old input after a redirect, flash messages, sessions, hashed static
// assets and htmx.
//
//	(cd ../../cli && go run ./cmd/anetos key:generate) >> .env   # APP_KEY, once
//	export APP_ENV=development HTTP_ADDR=:8080
//	go generate ./...   # templ generate, after changing a .templ file
//	go run .
//
// Then open http://localhost:8080.
package main

//go:generate go tool templ generate

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"anetos.dev/anetos"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/view/htmx"
	"anetos.dev/anetos/web"
)

// region: assets
// public holds the files of the public directory.
//
//go:embed public
var public embed.FS

// assets serves public/ and the bundled htmx at /assets, with hashed URLs.
var assets = mustAssets()

func mustAssets() *view.Assets {
	files, err := fs.Sub(public, "public")
	if err != nil {
		panic(err)
	}
	a, err := view.NewAssets("/assets", files, htmx.FS)
	if err != nil {
		panic(err)
	}
	return a
}

// endregion

// NoteInput is the form for creating and editing notes.
type NoteInput struct {
	Title string `json:"title" validate:"required|max:100"`
	Body  string `json:"body" validate:"required|max:2000"`
}

// NoteID reads the {id} path parameter.
type NoteID struct {
	ID int64 `path:"id"`
}

// UpdateNote is the input of PUT /notes/{id}.
type UpdateNote struct {
	NoteID
	NoteInput
}

// Notes holds the handlers.
type Notes struct {
	store *Store
}

func (h Notes) Index(c *web.Ctx) error {
	return c.Render(http.StatusOK, NotesPage(h.store.All()))
}

func (h Notes) New(c *web.Ctx) error {
	return c.Render(http.StatusOK, NoteFormPage(Note{}))
}

// region: create
func (h Notes) Create(c *web.Ctx, in NoteInput) (web.Responder, error) {
	// Invalid input never gets here: the browser is sent back to the form,
	// which shows the errors and the submitted values.
	h.store.Add(in.Title, in.Body)
	c.Session().Flash("status", "Note created.")
	return web.RedirectRoute("notes.index"), nil
}

// endregion

func (h Notes) Edit(c *web.Ctx, in NoteID) (web.Responder, error) {
	n, ok := h.store.Get(in.ID)
	if !ok {
		return nil, web.Error(http.StatusNotFound, "note not found")
	}
	return web.View(NoteFormPage(n)), nil
}

func (h Notes) Update(c *web.Ctx, in UpdateNote) (web.Responder, error) {
	if !h.store.Update(in.ID, in.Title, in.Body) {
		return nil, web.Error(http.StatusNotFound, "note not found")
	}
	c.Session().Flash("status", "Note saved.")
	return web.RedirectRoute("notes.index"), nil
}

// region: delete
func (h Notes) Delete(c *web.Ctx, in NoteID) (web.Responder, error) {
	h.store.Delete(in.ID)
	if c.IsHTMX() {
		// htmx replaces the note's list item with this empty response.
		return web.Text(http.StatusOK, ""), nil
	}
	c.Session().Flash("status", "Note deleted.")
	return web.RedirectRoute("notes.index"), nil
}

// endregion

// setup adds the web server and the routes to app.
func setup(app *anetos.App) (*web.Server, error) {
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	// region: routes
	sessions, err := session.ForApp(app) // SESSION_* settings; needs APP_KEY
	if err != nil {
		return nil, err
	}
	r := srv.Router()
	r.UseGlobal(web.MethodOverride) // forms can send PUT and DELETE with _method
	r.HandleStd(http.MethodGet, "/assets/{path...}", assets)

	h := Notes{store: NewStore()}
	pages := r.Group("", sessions.Middleware, web.CSRF())
	pages.Get("/", func(c *web.Ctx) error { return c.RedirectRoute("notes.index") })
	pages.Get("/notes", h.Index).Name("notes.index")
	pages.Get("/notes/new", h.New).Name("notes.new")
	pages.Post("/notes", web.H(h.Create)).Name("notes.store")
	pages.Get("/notes/{id}/edit", web.H(h.Edit)).Name("notes.edit")
	pages.Put("/notes/{id}", web.H(h.Update)).Name("notes.update")
	pages.Delete("/notes/{id}", web.H(h.Delete)).Name("notes.delete")
	// endregion
	return srv, nil
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app, err := anetos.New()
	if err != nil {
		return err
	}
	if _, err := setup(app); err != nil {
		return err
	}
	return app.Run(ctx)
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
