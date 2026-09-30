// SPDX-License-Identifier: Apache-2.0

// Command forms is a small notes app with server-rendered HTML: templ
// views with a layout, a paginated list, forms with CSRF protection,
// validation errors and old input after a redirect, flash messages,
// sessions, hashed static assets and htmx, over a SQLite database.
//
//	go tool anetos key:generate >> .env   # APP_KEY, once
//	export APP_ENV=development HTTP_ADDR=:8080
//	go generate ./...   # templ generate and anetos gen, after changing a .templ file or a model
//	go run . migrate    # and go run . db:seed for sample notes
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

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/sqlite"
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

// ListNotes is the input of GET /notes.
type ListNotes struct {
	Page int `query:"page"` // 1-based; missing or 0 is the first page
}

// Notes holds the handlers. They find the database in the request context.
type Notes struct{}

// region: index
func (Notes) Index(c *web.Ctx, in ListNotes) (web.Responder, error) {
	// Newest first; the ID breaks ties between notes created in the same
	// instant, so no note shows on two pages.
	page, err := db.Query[Note](c).OrderBy(NoteCols.CreatedAt.Desc(), NoteCols.ID.Desc()).Paginate(in.Page, 10)
	if err != nil {
		return nil, err
	}
	return web.View(NotesPage(page)), nil
}

// endregion

func (Notes) New(c *web.Ctx) error {
	return c.Render(http.StatusOK, NoteFormPage(Note{}))
}

// region: create
func (Notes) Create(c *web.Ctx, in NoteInput) (web.Responder, error) {
	// Invalid input never gets here: the browser is sent back to the form,
	// which shows the errors and the submitted values.
	if err := db.Create(c, &Note{Title: in.Title, Body: in.Body}); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Note created.")
	return web.RedirectRoute("notes.index"), nil
}

// endregion

func (Notes) Edit(c *web.Ctx, in NoteID) (web.Responder, error) {
	n, err := db.Find[Note](c, in.ID) // db.ErrNotFound becomes a 404
	if err != nil {
		return nil, err
	}
	return web.View(NoteFormPage(n)), nil
}

func (Notes) Update(c *web.Ctx, in UpdateNote) (web.Responder, error) {
	n, err := db.Find[Note](c, in.ID)
	if err != nil {
		return nil, err
	}
	n.Title, n.Body = in.Title, in.Body
	if err := db.Update(c, &n); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Note saved.")
	return web.RedirectRoute("notes.index"), nil
}

// region: delete
func (Notes) Delete(c *web.Ctx, in NoteID) (web.Responder, error) {
	n, err := db.Find[Note](c, in.ID)
	if err != nil {
		return nil, err
	}
	if err := db.Delete(c, &n); err != nil {
		return nil, err
	}
	if c.IsHTMX() {
		// htmx replaces the note's list item with this empty response.
		return web.Text(http.StatusOK, ""), nil
	}
	c.Session().Flash("status", "Note deleted.")
	return web.RedirectRoute("notes.index"), nil
}

// endregion

// setup connects the database (DB_* settings; default SQLite
// database/app.db) and adds the migrations, the web server and the routes
// to app. The tests call it too.
func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	if _, err := migrate.ForApp(app, []*migrate.Set{Migrations}, migrate.WithSeeders(Seeders...)); err != nil {
		return nil, err
	}
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

	var h Notes
	pages := r.Group("", sessions.Middleware, web.CSRF())
	pages.Get("/", func(c *web.Ctx) error { return c.RedirectRoute("notes.index") })
	pages.Get("/notes", web.H(h.Index)).Name("notes.index")
	pages.Get("/notes/new", h.New).Name("notes.new")
	pages.Post("/notes", web.H(h.Create)).Name("notes.store")
	pages.Get("/notes/{id}/edit", web.H(h.Edit)).Name("notes.edit")
	pages.Put("/notes/{id}", web.H(h.Update)).Name("notes.update")
	pages.Delete("/notes/{id}", web.H(h.Delete)).Name("notes.delete")
	// endregion
	return srv, nil
}

func main() {
	app, err := anetos.New()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute() // serves by default; also migrate, db:seed, routes:list, help
}
