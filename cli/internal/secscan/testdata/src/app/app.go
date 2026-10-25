// SPDX-License-Identifier: Apache-2.0

// Package app has what the rules find, each with the finding it wants.
package app

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"html/template"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"
	"github.com/a-h/templ"
)

type Post struct {
	db.Model
	Title string
}

type Search struct {
	Term string
	Sort string
}

type Posts struct{ sql *sql.DB }

func (h *Posts) Search(c *web.Ctx) error {
	q := c.Query("q")
	_, _ = db.Raw[Post](c.Request().Context(), "SELECT * FROM posts WHERE title LIKE '%"+q+"%'") // want `source.sql problem`
	_, _ = db.Raw[Post](c.Request().Context(), "SELECT * FROM posts WHERE title LIKE ?", "%"+q+"%")
	order := fmt.Sprintf("ORDER BY %s", c.Query("sort"))
	_, _ = h.sql.Query("SELECT * FROM posts " + order) // want `source.sql problem`
	return nil
}

// Index is a typed handler: its input is the client's.
func (h *Posts) Index(c *web.Ctx, in Search) ([]Post, error) {
	var b strings.Builder
	b.WriteString("SELECT * FROM posts ORDER BY ")
	b.WriteString(in.Sort)
	return db.Raw[Post](c.Request().Context(), b.String()) // want `source.sql problem`
}

func byColumn(ctx context.Context, column string) {
	_, _ = db.Exec(ctx, "UPDATE posts SET "+column+" = NULL") // want `source.sql warning`
	_, _ = db.Exec(ctx, "UPDATE posts SET n = "+strconv.Itoa(len(column)))
}

func (h *Posts) Page(c *web.Ctx) error {
	name := c.Query("name")
	_ = templ.Raw("<b>" + name + "</b>")            // want `source.xss problem`
	_ = template.HTML(c.Request().FormValue("bio")) // want `source.xss problem`
	_ = templ.Raw("<b>fixed</b>")
	return c.HTML(200, "<p>"+template.HTMLEscapeString(name)+"</p>")
}

func (h *Posts) Login(c *web.Ctx) error {
	next := c.Query("next")
	if c.Query("safe") != "" {
		return c.Redirect(303, "/home")
	}
	return c.Redirect(303, next) // want `source.redirect problem`
}

func (h *Posts) LoginChecked(c *web.Ctx) error {
	next := c.Query("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	return c.Redirect(303, next)
}

func Legacy(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, r.URL.Query().Get("to"), http.StatusFound)     // want `source.redirect problem`
	_ = exec.Command("sh", "-c", "convert "+r.FormValue("file")).Run() // want `source.exec problem`
	_ = exec.Command("convert", r.FormValue("file")).Run()             // want `source.exec warning`
	_ = exec.Command(r.FormValue("prog")).Run()                        // want `source.exec problem`
	_ = exec.Command("ls", "-l").Run()
	_, _ = os.ReadFile(filepath.Join("uploads", r.FormValue("name"))) // want `source.path warning`
	_, _ = os.ReadFile(filepath.Join("uploads", filepath.Base(r.FormValue("name"))))
	_, _ = http.Get(r.FormValue("url")) // want `source.ssrf warning`
}

var client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // want `source.tls problem`

func secure() *tls.Config {
	cfg := &tls.Config{}
	cfg.InsecureSkipVerify = false
	return cfg
}

func resetToken() string {
	return strconv.Itoa(rand.Int()) // want `source.rand warning`
}

func session() {
	sessionID := rand.Int63() // want `source.rand warning`
	_ = sessionID
	shuffle := rand.Perm(3)
	_ = shuffle
}

func (h *Posts) Show(c *web.Ctx) error {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	_, _ = db.Find[Post](c.Request().Context(), id) // want `source.owner warning`
	return nil
}

func (h *Posts) ShowMine(c *web.Ctx) error {
	_, _ = db.Query[Post](c.Request().Context()).Where("owner").Find(c.Param("id"))
	return nil
}

func routes() {
	_ = web.H(func(c *web.Ctx, in Post) (Post, error) { return in, nil }) // want `source.mass-assign warning`
	_ = web.H(func(c *web.Ctx, in Search) ([]Post, error) { return nil, nil })
}

// ShowNote loads the user, and doesn't compare it with the note's owner.
func (h *Posts) ShowNote(c *web.Ctx) error {
	_ = auth.CurrentID(c.Request().Context())
	_, _ = db.Find[Post](c.Request().Context(), c.Param("id")) // want `source.owner warning`
	return nil
}

func NewToken() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // want `source.rand warning`
	return b
}

func (h *Posts) HalfChecked(c *web.Ctx) error {
	next := c.Query("next")
	if !strings.HasPrefix(next, "/") {
		next = "/"
	}
	return c.Redirect(303, next) // want `source.redirect problem`
}
