// SPDX-License-Identifier: Apache-2.0

package anetos_test

import (
	"log"
	"net/http"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/web"
)

// The shape of every app's main.go, as `anetos new` writes it (shortened).
func Example() {
	app, err := anetos.New() // reads .env and the environment
	if err != nil {
		log.Fatal(err)
	}
	if err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute() // run (the default), serve, migrate, route:list, help…
}

// setup builds the app's services, in order. A real app connects its
// database first: db.Connect(ctx, app, sqlite.Driver()).
func setup(app *anetos.App) error {
	if _, err := cache.New(app); err != nil { // CACHE_DRIVER
		return err
	}
	q, err := queue.New(app) // QUEUE_DRIVER
	if err != nil {
		return err
	}
	if err := q.Work(); err != nil { // the workers run with the app
		return err
	}
	if _, err := mailer.New(app); err != nil { // MAIL_DRIVER
		return err
	}
	srv, err := web.NewServer(app) // HTTP_ADDR or PORT
	if err != nil {
		return err
	}
	srv.Router().Get("/", func(c *web.Ctx) error {
		return c.Text(http.StatusOK, "Hello")
	}).Name("home")
	return nil
}
