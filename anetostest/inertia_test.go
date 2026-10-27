// SPDX-License-Identifier: Apache-2.0

package anetostest_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/inertia"
)

type itemsPage struct {
	Items []item `json:"items"`
}

var itemsIndex = inertia.NewPage[itemsPage]("Items/Index")

func inertiaApp(app *anetos.App) (*web.Server, error) {
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	sessions, err := session.New(app)
	if err != nil {
		return nil, err
	}
	root := view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, _ = io.WriteString(w, "<!doctype html><body>")
		return inertia.App().Render(ctx, w)
	})
	if _, err := inertia.New(app, root, inertia.WithVersion("v7")); err != nil {
		return nil, err
	}
	r := srv.Router()
	g := r.Group("", sessions.Middleware, web.CSRF(web.WithXSRFCookie()), inertia.Middleware)
	g.Get("/items", func(c *web.Ctx) error {
		return itemsIndex.Render(c, itemsPage{Items: []item{{1, "Tea"}}})
	}).Name("items")
	g.Post("/items", web.H(func(c *web.Ctx, in itemInput) (web.Responder, error) {
		return web.RedirectRoute("items"), nil
	}))
	g.Get("/plain", func(c *web.Ctx) error { return c.Text(http.StatusOK, "plain") })
	return srv, nil
}

func TestInertia(t *testing.T) {
	app := anetostest.New(t, inertiaApp)
	app.Get("/items").AssertOK().AssertInertia("Items/Index").AssertInertiaProp("items", []item{{1, "Tea"}})
	// A path into a prop: an array's index, an object's key.
	app.Get("/items").AssertInertiaProp("items.0.title", "Tea").AssertInertiaProp("items.0", item{1, "Tea"})
	res := app.AsInertia().Get("/items").AssertOK().AssertHeader("X-Inertia", "true").AssertInertia("Items/Index")
	if p := res.InertiaPage(); p.Version != "v7" || p.URL != "/items" {
		t.Errorf("page %+v", p)
	}
	// A failed post's errors, on the page it is sent back to.
	app.Get("/items")
	app.PostJSON("/items", map[string]string{"title": ""}).AssertRedirect("/items")
	app.Get("/items").AssertInertiaProp("errors", map[string]string{"title": "The title field is required."})

	ft := &fakeT{TB: t}
	fails := anetostest.New(ft, inertiaApp)
	fails.Get("/plain").AssertInertia("Items/Index")
	fails.Get("/items").AssertInertia("Other").AssertInertiaProp("nope", 1).AssertInertiaProp("items", 1).AssertInertiaProp("items", func() {})
	fails.Get("/plain").AssertInertiaProp("items", 1)
	fails.Get("/items").AssertInertiaProp("items.1.title", "x").AssertInertiaProp("items.0.nope", "x").AssertInertiaProp("items.x", "x")
	if len(ft.errs) != 9 || !strings.Contains(ft.errs[0], "no page object") || !strings.Contains(ft.errs[1], `got the Inertia page "Items/Index", want "Other"`) ||
		!strings.Contains(ft.errs[2], `has no prop "nope"`) || !strings.Contains(ft.errs[3], `Inertia prop "items" is [{"id":1,"title":"Tea"}], want 1`) ||
		!strings.Contains(ft.errs[4], "AssertInertiaProp: json: unsupported type") {
		t.Errorf("errors: %q", ft.errs)
	}
	if msg := fatalOf(func() { fails.Get("/plain").InertiaPage() }); !strings.Contains(msg, "no page object") {
		t.Errorf("InertiaPage: %q", msg)
	}
	if msg := fatalOf(func() { anetostest.New(ft, setup).AsInertia() }); !strings.Contains(msg, "AsInertia needs Inertia") {
		t.Errorf("AsInertia without Inertia: %q", msg)
	}
}
