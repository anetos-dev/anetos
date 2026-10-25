// SPDX-License-Identifier: Apache-2.0

// Package clean has code the rules leave alone.
package clean

import (
	"context"
	"math/rand"
	"net/http"
	"net/url"
	"os/exec"
	"slices"
	"strings"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/rbac"
	"anetos.dev/anetos/web"
)

type Post struct {
	db.Model
	Title string
}

type Posts struct{}

func (h *Posts) Show(c *web.Ctx) error {
	if err := rbac.Authorize(c.Request().Context(), "posts.view"); err != nil {
		return err
	}
	_, _ = db.Find[Post](c.Request().Context(), c.Param("id"))
	return nil
}

const order = "CASE priority WHEN 'urgent' THEN 0 ELSE 1 END"

func list(ctx context.Context, title string) {
	_, _ = db.Raw[Post](ctx, "SELECT * FROM posts WHERE title = ? ORDER BY "+order, title)
}

// helper isn't a typed handler: its project isn't the client's.
func helper(c *web.Ctx, p Post) (string, error) {
	_, err := db.Raw[Post](c.Request().Context(), "SELECT * FROM posts WHERE id = ?", p.ID)
	return p.Title, err
}

type Note struct {
	db.Model
	UserID int64
}

// Edit compares the note's owner with the user.
func (h *Posts) Edit(c *web.Ctx) error {
	note, err := db.Find[Note](c.Request().Context(), c.Param("id"))
	if err != nil || note.UserID != auth.CurrentID(c.Request().Context()) {
		return err
	}
	return nil
}

// Index chooses the sort from lists of constants.
func (h *Posts) Index(c *web.Ctx) error {
	ctx := c.Request().Context()
	sort := c.Query("sort")
	switch sort {
	case "title", "created_at":
	default:
		sort = "created_at"
	}
	_, _ = db.Raw[Post](ctx, "SELECT * FROM posts ORDER BY "+sort)
	dir := c.Query("dir")
	if !slices.Contains([]string{"asc", "desc"}, dir) {
		dir = "asc"
	}
	_, _ = db.Raw[Post](ctx, "SELECT * FROM posts ORDER BY title "+dir)
	return nil
}

type Back struct{ Next, Q string }

// Redirects to own paths, or to URLs it checked.
func (h *Posts) Redirects(c *web.Ctx, in Back) (web.Responder, error) {
	if c.Query("a") != "" {
		return nil, c.Redirect(303, "/posts/"+c.Param("id"))
	}
	if c.Query("b") != "" {
		return web.Redirect("/posts?q=" + url.QueryEscape(in.Q)), nil
	}
	if c.Query("c") != "" {
		u, err := url.Parse(c.Query("next"))
		if err != nil || u.IsAbs() || u.Host != "" {
			return web.Redirect("/"), nil
		}
		return nil, c.Redirect(303, u.RequestURI())
	}
	if !strings.HasPrefix(in.Next, "/") || strings.HasPrefix(in.Next, "//") {
		in.Next = "/"
	}
	return web.Redirect(in.Next), nil
}

func (h *Posts) Grep(c *web.Ctx) error {
	return exec.Command("grep", "--", c.Query("q"), "notes.txt").Run()
}

func (h *Posts) Outgoing(c *web.Ctx) error {
	req, _ := http.NewRequest("GET", "https://api.example.com/x", nil)
	return c.Redirect(303, req.URL.String())
}

func jitter() {
	footprintSize := rand.Intn(5)
	sessionCount := rand.Intn(10)
	_, _ = footprintSize, sessionCount
}
