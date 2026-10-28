// SPDX-License-Identifier: Apache-2.0

package inertia_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/inertia"
)

// With APP_LOCALE_STRATEGY=prefix, a page's URL keeps the locale's
// prefix, which routing takes off the request's path: the client puts
// the page's URL in the address bar.
func TestPageURLLocalePrefix(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_KEY": encryption.GenerateKey(), "APP_LOCALE_STRATEGY": "prefix"}),
		anetos.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := i18n.New(app, fstest.MapFS{"en.yaml": {Data: []byte("hello: Hello\n")}, "bn.yaml": {Data: []byte("hello: Hi\n")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := inertia.New(app, root, inertia.WithVersion("v1")); err != nil {
		t.Fatal(err)
	}
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	r := srv.Router()
	r.Group("", inertia.Middleware).Get("/posts", func(c *web.Ctx) error { return inertia.Render(c, "Posts", nil) })
	if err := app.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	hs := httptest.NewUnstartedServer(r)
	hs.Config.BaseContext = func(net.Listener) context.Context { return app.Context(context.Background()) }
	hs.Start()
	t.Cleanup(hs.Close)
	for path, want := range map[string]string{"/bn/posts?page=2": "/bn/posts?page=2", "/posts": "/posts"} {
		req, _ := http.NewRequest(http.MethodGet, hs.URL+path, nil)
		req.Header.Set("X-Inertia", "true")
		req.Header.Set("X-Inertia-Version", "v1")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		var pg struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(b, &pg); err != nil || pg.URL != want {
			t.Errorf("%s: url %q (%v), want %q:\n%s", path, pg.URL, err, want, b)
		}
	}
}
