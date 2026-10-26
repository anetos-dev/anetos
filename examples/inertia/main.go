// SPDX-License-Identifier: Apache-2.0

// Command inertia is a notes app whose pages are React components that
// Inertia's client renders, while the routes, handlers, validation,
// sessions and redirects are Go's (package web/inertia): a list that
// loads more notes and its statistics after the page shows, a form whose
// errors come back from the server, a flash message after saving, and
// the frontend's error page.
//
//	cd frontend && npm install && cd ..
//	go tool anetos key:generate           # APP_KEY, once
//	go tool anetos dev                    # the app and Vite, on http://localhost:8080
//
// The pages' props are TypeScript types in frontend/src/types, which
// go run . types writes from the Go types (anetos dev does as you edit).
//
// Its tests (go test) need neither Node nor a build of the frontend.
package main

//go:generate go tool templ generate

import (
	"context"
	"log"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/examples/inertia/public"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view/vite"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/inertia"
	"anetos.dev/anetos/web/typescript"
)

// Note is a note, as the pages get it.
type Note struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// region: pages
// NotesIndex are the props of the list: a page of notes, which the
// client appends to the ones it shows when it loads more, and the
// statistics, which it asks for after the page shows.
type NotesIndex struct {
	Notes inertia.Prop[[]Note] `json:"notes"`
	Next  int                  `json:"next"` // the next page, 0 after the last
	Stats inertia.Prop[Stats]  `json:"stats"`
}

// Stats are the list's statistics, slow to compute.
type Stats struct {
	Count int `json:"count"`
	Words int `json:"words"`
}

// NotesCreate are the props of the form.
type NotesCreate struct {
	MaxTitle int `json:"maxTitle"`
}

var (
	notesIndex  = inertia.NewPage[NotesIndex]("Notes/Index")
	notesCreate = inertia.NewPage[NotesCreate]("Notes/Create")
)

// endregion

// Shared are the props of every page.
type Shared struct {
	AppName string `json:"appName"`
}

// NoteInput is the form's input.
type NoteInput struct {
	Title string `json:"title" validate:"required|max:60"`
	Body  string `json:"body" validate:"max:2000"`
}

// store keeps the notes in memory.
type store struct {
	mu    sync.Mutex
	notes []Note
	next  int
}

func newStore() *store {
	s := &store{}
	for i := 1; i <= 23; i++ {
		s.add(NoteInput{Title: "Note " + strconv.Itoa(i), Body: "The body of note " + strconv.Itoa(i) + "."})
	}
	return s
}

func (s *store) add(in NoteInput) Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	n := Note{ID: s.next, Title: in.Title, Body: in.Body}
	s.notes = append([]Note{n}, s.notes...)
	return n
}

// page returns page p (from 1) of the newest notes, and the next page's
// number (0 after the last).
func (s *store) page(p, size int) ([]Note, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := min((p-1)*size, len(s.notes))
	end := min(start+size, len(s.notes))
	next := 0
	if end < len(s.notes) {
		next = p + 1
	}
	return slices.Clone(s.notes[start:end]), next
}

func (s *store) delete(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes = slices.DeleteFunc(s.notes, func(n Note) bool { return n.ID == id })
}

func (s *store) stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{Count: len(s.notes)}
	for _, n := range s.notes {
		st.Words += len(splitWords(n.Body))
	}
	return st
}

func splitWords(s string) []string {
	var words []string
	start := -1
	for i, r := range s + " " {
		switch {
		case r == ' ' && start >= 0:
			words = append(words, s[start:i])
			start = -1
		case r != ' ' && start < 0:
			start = i
		}
	}
	return words
}

// notes are the pages' handlers.
type notes struct{ store *store }

// region: handlers
func (h notes) index(c *web.Ctx) error {
	p, _ := strconv.Atoi(c.Query("page"))
	list, next := h.store.page(max(p, 1), 10)
	return notesIndex.Render(c, NotesIndex{
		Notes: inertia.Merge(list).MatchOn("id"),
		Next:  next,
		Stats: inertia.Defer(func(ctx context.Context) (Stats, error) {
			time.Sleep(300 * time.Millisecond) // a slow query
			return h.store.stats(), nil
		}),
	})
}

func (h notes) create(c *web.Ctx) error {
	return notesCreate.Render(c, NotesCreate{MaxTitle: 60})
}

// save saves a note; an invalid one is sent back to the form with its
// errors, which the page gets in its errors prop.
func (h notes) save(c *web.Ctx, in NoteInput) (web.Responder, error) {
	n := h.store.add(in)
	inertia.Flash(c, "toast", "Saved “"+n.Title+"”.")
	return web.RedirectRoute("notes.index"), nil
}

// endregion

func (h notes) delete(c *web.Ctx) error {
	id, _ := strconv.Atoi(c.Param("id"))
	h.store.delete(id)
	inertia.Flash(c, "toast", "Deleted.")
	return c.Redirect(http.StatusFound, "/notes") // 303 for Inertia's client: a DELETE
}

// region: setup
func setup(app *anetos.App) (*web.Server, error) {
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	sessions, err := session.New(app)
	if err != nil {
		return nil, err
	}
	if _, err := vite.New(app, public.Files, vite.WithReactRefresh()); err != nil {
		return nil, err
	}
	shared := func(*web.Ctx) (Shared, error) { return Shared{AppName: app.Config().Name}, nil }
	in, err := inertia.New(app, root(), inertia.WithShared(shared), inertia.WithPages(notesIndex, notesCreate))
	if err != nil {
		return nil, err
	}

	r := srv.Router()
	r.ErrorPages(inertia.ErrorPages("Error"))
	r.Get("/build/{path...}", web.WrapHandler(vite.Handler()))
	h := notes{store: newStore()}
	g := r.Group("", sessions.Middleware, web.CSRF(web.WithXSRFCookie()), inertia.Middleware)
	g.Get("/", func(c *web.Ctx) error { return c.RedirectRoute("notes.index") })
	g.Get("/notes", h.index).Name("notes.index")
	g.Get("/notes/new", h.create).Name("notes.create")
	g.Post("/notes", web.H(h.save))
	g.Delete("/notes/{id}", h.delete)
	g.Get("/boom", func(*web.Ctx) error { return web.Error(http.StatusTeapot, "This page is a teapot.") })

	// go run . types writes the pages' props as TypeScript, in
	// frontend/src/types (anetos dev does after each build).
	if err := typescript.Register(app, srv, types(in)); err != nil {
		return nil, err
	}
	return srv, nil
}

// types is where the frontend's TypeScript types come from.
func types(in *inertia.Inertia) typescript.Config {
	return typescript.Config{Inertia: in}
}

// endregion

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
