// SPDX-License-Identifier: Apache-2.0

// Command audit is a JSON API of documents whose every change is in an
// audit log (package audit): who created, edited, archived, deleted and
// restored each one, field by field, and who exported them. Users sign in
// with API tokens; seed creates one.
//
//	anetos key:generate >> .env   # APP_KEY, once
//	export APP_ENV=development HTTP_ADDR=:8080
//	go run . migrate
//	go run . seed
//	go run .
package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/web"
)

// Handlers serves the API.
type Handlers struct{}

// IDPath reads {id} from the path.
type IDPath struct {
	ID int64 `path:"id"`
}

// NewDocument is a document to create.
type NewDocument struct {
	Slug  string `json:"slug" validate:"required|max:100|alpha_dash|unique_live:documents,slug"`
	Title string `json:"title" validate:"required|max:255"`
	Body  string `json:"body"`
}

// CreateDocument creates a draft owned by the user.
func (Handlers) CreateDocument(c *web.Ctx, in NewDocument) (web.Responder, error) {
	u, err := auth.Current[*User](c)
	if err != nil {
		return nil, err
	}
	d := Document{OwnerID: u.ID, Slug: in.Slug, Title: in.Title, Body: in.Body, Status: "draft", ShareToken: rand.Text()}
	if err := db.Create(c, &d); err != nil { // logged: "created", with the values
		return nil, err
	}
	return web.Created(d), nil
}

// Show returns a document and counts the view (a column the log leaves
// out, so reading doesn't fill the log).
func (Handlers) Show(c *web.Ctx, in IDPath) (web.Responder, error) {
	d, err := db.Find[Document](c, in.ID)
	if err != nil {
		return nil, err
	}
	if _, err := db.Query[Document](c).WhereKeys(d.ID).Update(colViews.SetRaw("view_count + 1")); err != nil {
		return nil, err
	}
	return web.JSON(http.StatusOK, d), nil
}

// DocumentChanges are the fields to change; those left out stay.
type DocumentChanges struct {
	ID     int64   `path:"id"`
	Title  *string `json:"title" validate:"max:255"`
	Body   *string `json:"body"`
	Status *string `json:"status" validate:"in:draft,published,archived"`
}

// region: update
// UpdateDocument changes a document. The log records the fields that
// changed, from what to what, and who changed them.
func (Handlers) UpdateDocument(c *web.Ctx, in DocumentChanges) (web.Responder, error) {
	d, err := db.Find[Document](c, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Title != nil {
		d.Title = *in.Title
	}
	if in.Body != nil {
		d.Body = *in.Body
	}
	if in.Status != nil {
		d.Status = *in.Status
	}
	if err := db.Update(c, &d); err != nil {
		return nil, err
	}
	return web.JSON(http.StatusOK, d), nil
}

// endregion

// Delete soft-deletes a document.
func (Handlers) Delete(c *web.Ctx, in IDPath) (web.Responder, error) {
	d, err := db.Find[Document](c, in.ID)
	if err != nil {
		return nil, err
	}
	if err := db.Delete(c, &d); err != nil { // "deleted": the row stays, restorable
		return nil, err
	}
	return web.NoContent(), nil
}

// Restore undeletes a document.
func (Handlers) Restore(c *web.Ctx, in IDPath) (web.Responder, error) {
	d, err := db.Query[Document](c).OnlyTrashed().Find(in.ID)
	if err != nil {
		return nil, err
	}
	if err := db.Restore(c, &d); err != nil {
		return nil, err
	}
	return web.JSON(http.StatusOK, d), nil
}

// region: archive
// Archive archives the user's published documents in one statement: one
// bulk entry in the log, with every document's key.
func (Handlers) Archive(c *web.Ctx) error {
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	n, err := db.Query[Document](c).
		Where(colOwnerID.Eq(u.ID), colStatus.Eq("published")).
		Update(colStatus.Set("archived"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]int64{"archived": n})
}

// endregion

// region: export
// Export returns the user's documents, and records that they did: an
// event of the app's own, not a write.
func (Handlers) Export(c *web.Ctx) error {
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	docs, err := db.Query[Document](c).Where(colOwnerID.Eq(u.ID)).Get()
	if err != nil {
		return err
	}
	err = audit.Record(c, "documents.exported", audit.Subject{Type: "users", ID: u.AuthID()},
		map[string]any{"documents": len(docs)})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, docs)
}

// endregion

// Event is one line of a document's history, for the API.
type Event struct {
	At      time.Time      `json:"at"`
	Actor   string         `json:"actor"`
	Action  string         `json:"action"`
	Via     string         `json:"via,omitempty"`
	Changes *audit.Changes `json:"changes,omitempty"`
	Rows    int64          `json:"rows,omitempty"` // a bulk write: how many documents it touched
}

// HistoryQuery reads {id} and an optional ?cursor= from the previous
// page, to page back.
type HistoryQuery struct {
	ID     int64  `path:"id"`
	Cursor string `query:"cursor"`
}

// HistoryPage is a page of a document's history.
type HistoryPage struct {
	Events []Event `json:"events"`
	Next   string  `json:"next,omitempty"` // the cursor of the next (older) page
}

// region: history
// History returns what happened to a document, newest first: its own
// entries and the bulk writes that touched it.
func (Handlers) History(c *web.Ctx, in HistoryQuery) (web.Responder, error) {
	d, err := db.Query[Document](c).WithTrashed().Find(in.ID)
	if err != nil {
		return nil, err
	}
	subject, err := audit.SubjectOf(&d)
	if err != nil {
		return nil, err
	}
	events, next, err := audit.History(c, subject, 50, in.Cursor)
	if err != nil {
		return nil, err
	}
	out := make([]Event, len(events))
	for i, e := range events {
		out[i] = Event{At: e.At(), Actor: e.Actor().String(), Action: e.Action()}
		if e.Entry != nil {
			out[i].Via = e.Entry.ViaName
			out[i].Changes = &e.Entry.Changes
		} else {
			out[i].Via = e.Bulk.ViaName
			out[i].Rows = e.Bulk.RowCount
		}
	}
	return web.JSON(http.StatusOK, HistoryPage{out, next}), nil
}

// endregion

func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	sets := []*migrate.Set{Migrations, auth.Migrations(), audit.Migrations()}
	if _, err := migrate.New(app, sets); err != nil {
		return nil, err
	}
	if _, err := cache.New(app); err != nil { // auth's login throttling
		return nil, err
	}
	a, err := auth.New(app, users)
	if err != nil {
		return nil, err
	}
	// region: setup
	trail, err := audit.New(app) // after db.Connect
	if err != nil {
		return nil, err
	}
	if err := audit.Track[Document](trail, audit.Except("view_count")); err != nil {
		return nil, err
	}
	// Documents deleted more than 30 days ago go for good: db:prune-trashed.
	if err := db.PruneTrashed[Document](app, 30*24*time.Hour); err != nil {
		return nil, err
	}
	// endregion
	app.Command("seed", "Create a user and an API token to try the API with", func(ctx context.Context, args *cmd.Args) error {
		return seed(ctx, a, args)
	})
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	routes(srv.Router(), a)
	return srv, nil
}

func routes(r *web.Router, a *auth.Auth[*User]) {
	var h Handlers
	api := r.Group("/api", a.TokenMiddleware, a.Require) // Authorization: Bearer <token>
	api.Post("/documents", web.H(h.CreateDocument))
	api.Post("/documents/archive", h.Archive)
	api.Get("/documents/export", h.Export)
	api.Get("/documents/{id}", web.H(h.Show))
	api.Patch("/documents/{id}", web.H(h.UpdateDocument))
	api.Delete("/documents/{id}", web.H(h.Delete))
	api.Post("/documents/{id}/restore", web.H(h.Restore))
	api.Get("/documents/{id}/history", web.H(h.History))
}

// seed creates a user and an API token.
func seed(ctx context.Context, a *auth.Auth[*User], args *cmd.Args) error {
	u := User{Name: "Ada", Email: "ada@example.com"}
	if err := db.Create(ctx, &u); err != nil {
		return err
	}
	token, _, err := a.CreateToken(ctx, &u, "seed", []string{"*"}, 0)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(args.Stdout, "Ada's API token: %s\n", token)
	return err
}

func main() {
	app, err := anetos.New()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute()
}
