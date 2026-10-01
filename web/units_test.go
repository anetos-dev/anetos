// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/web"
)

type unitCtxKey struct{}

// unitName answers the name of the request's unit, from its context.
func unitName(c *web.Ctx) error {
	name, _ := c.Request().Context().Value(unitCtxKey{}).(string)
	return c.Text(http.StatusOK, name)
}

// Each request is a unit of work, whose context its handler gets.
func TestRequestUnits(t *testing.T) {
	app := newApp(t, config.Map{"APP_ENV": "testing"})
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	srv.Router().Get("/posts/{id}", unitName)
	get := func(path string) string {
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Body.String()
	}
	if got := get("/posts/7"); got != "" {
		t.Errorf("without AroundUnits: %q", got)
	}
	var units []anetos.Unit
	ended := 0
	app.AroundUnits(func(ctx context.Context, u anetos.Unit) (context.Context, func()) {
		units = append(units, u)
		return context.WithValue(ctx, unitCtxKey{}, u.Name), func() { ended++ }
	})
	if got := get("/posts/7?draft=1"); got != "GET /posts/7" || len(units) != 1 || units[0].Kind != "request" || ended != 1 {
		t.Errorf("body %q, units %+v, ended %d", got, units, ended)
	}
}
