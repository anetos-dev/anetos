// SPDX-License-Identifier: Apache-2.0

// Command notes is a small JSON API built with Anetos's HTTP layer: routing,
// named routes, typed handlers with binding and validation, and error
// responses. Notes are kept in memory.
//
//	APP_ENV=development HTTP_ADDR=:8080 go run ./examples/notes
//
//	curl -s localhost:8080/notes -H 'Content-Type: application/json' -d '{"title":"Hello","body":"First note","tags":["intro"]}'
//	curl -s localhost:8080/notes -H 'Content-Type: application/json' -d '{"title":"","tags":["a","a"]}'   # 422
//	curl -s localhost:8080/notes?limit=10
//	curl -s localhost:8080/notes/1
//	curl -s -X DELETE localhost:8080/notes/1
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"
)

// region: types
// Note is what the API returns.
type Note struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateNote is the input for POST /notes. It binds from JSON or a form,
// and the validate rules run before the handler.
type CreateNote struct {
	Title string   `json:"title" validate:"required|max:200"`
	Body  string   `json:"body" validate:"max:10000"`
	Tags  []string `json:"tags" validate:"max:5|distinct|alpha_dash"`
}

// Validate runs after the tag rules pass, for checks tags can't express.
func (in CreateNote) Validate(context.Context) error {
	if strings.EqualFold(strings.TrimSpace(in.Title), "untitled") {
		return validate.Fail("title", "Please give the note a real title.")
	}
	return nil
}

// ListNotes reads its input from the query string.
type ListNotes struct {
	Limit int `query:"limit" validate:"min:0|max:100"`
}

// NoteID reads the {id} path parameter.
type NoteID struct {
	ID int `path:"id"`
}

// endregion

// region: handlers
// Notes holds the handlers and their dependencies.
type Notes struct {
	mu     sync.Mutex
	notes  []Note
	nextID int
}

func (h *Notes) List(c *web.Ctx, in ListNotes) ([]Note, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := slices.Clone(h.notes)
	if in.Limit > 0 && in.Limit < len(out) {
		out = out[:in.Limit]
	}
	return out, nil
}

func (h *Notes) Show(c *web.Ctx, in NoteID) (Note, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, n := range h.notes {
		if n.ID == in.ID {
			return n, nil
		}
	}
	return Note{}, web.Error(http.StatusNotFound, "note not found")
}

// Create answers the new note: 201, the route's Status.
func (h *Notes) Create(c *web.Ctx, in CreateNote) (Note, error) {
	h.mu.Lock()
	h.nextID++
	n := Note{ID: h.nextID, Title: in.Title, Body: in.Body, Tags: in.Tags, CreatedAt: anetos.Now(c).UTC()}
	h.notes = append(h.notes, n)
	h.mu.Unlock()

	c.Logger().Info("note created", "id", n.ID)
	url, err := c.URL("notes.show", n.ID)
	if err != nil {
		return Note{}, err
	}
	c.SetHeader("Location", url)
	return n, nil
}

// Delete answers 204 No Content: web.Empty has no body.
func (h *Notes) Delete(c *web.Ctx, in NoteID) (web.Empty, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := slices.IndexFunc(h.notes, func(n Note) bool { return n.ID == in.ID })
	if i < 0 {
		return web.Empty{}, web.Error(http.StatusNotFound, "note not found")
	}
	h.notes = slices.Delete(h.notes, i, i+1)
	return web.Empty{}, nil
}

// endregion

// region: routes
func routes(r *web.Router, notes *Notes) {
	r.Get("/", func(c *web.Ctx) error {
		return c.RedirectRoute("notes.index")
	}).Name("home")

	api := r.Group("/notes").As("notes.")
	api.Get("", web.H(notes.List)).Name("index")
	api.Post("", web.H(notes.Create)).Name("create").Status(http.StatusCreated)
	api.Get("/{id}", web.H(notes.Show)).Name("show")
	api.Delete("/{id}", web.H(notes.Delete)).Name("delete")
}

// endregion

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// region: main
func run() error {
	app, err := anetos.New()
	if err != nil {
		return err
	}
	srv, err := web.NewServer(app) // reads HTTP_*, registers the "http" component
	if err != nil {
		return err
	}
	routes(srv.Router(), &Notes{})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx)
}

// endregion
