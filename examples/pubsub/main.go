// SPDX-License-Identifier: Apache-2.0

// Command pubsub is a billing service: it listens to the "orders.created"
// topic another service publishes to, creates an invoice for each order,
// and publishes "invoices.created". Orders it can't bill go to the
// "orders.created.dlq" topic. The broker is PUBSUB_DRIVER's: memory (in
// the process), redis (REDIS_URL) or gcp (PUBSUB_GCP_PROJECT).
//
//	go tool anetos key:generate >> .env   # APP_KEY, once
//	export APP_ENV=development HTTP_ADDR=:8080 PUBSUB_DRIVER=redis
//	go run . migrate
//	go run .                              # the server and the listener
//	go run . pubsub:publish orders.created '{"order_id":1,"customer":"Ada","cents":1500}'
//	curl localhost:8080/invoices
package main

import (
	"context"
	"log"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/gcppubsub"
	"anetos.dev/anetos/drivers/redis"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/pubsub"
	"anetos.dev/anetos/web"
)

func main() {
	app, err := anetos.New()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute() // run: the server and the listener; run --only=listeners: the listener
}

func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	if _, err := migrate.ForApp(app, []*migrate.Set{Migrations}); err != nil {
		return nil, err
	}
	// region: setup
	ps, err := pubsub.ForApp(app, redis.PubSubDriver(), gcppubsub.Driver()) // PUBSUB_DRIVER: memory, redis or gcp
	if err != nil {
		return nil, err
	}
	err = pubsub.Listen(ps, "orders.created", CreateInvoice,
		pubsub.Concurrency(8),                   // messages at once, in each process
		pubsub.MaxAttempts(5),                   // then...
		pubsub.DeadLetter("orders.created.dlq"), // ...to this topic
	)
	if err != nil {
		return nil, err
	}
	// endregion
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	srv.Router().Get("/invoices", web.H(ListInvoices))
	return srv, nil
}

// ListInvoices lists the invoices.
func ListInvoices(c *web.Ctx, _ struct{}) ([]Invoice, error) {
	return db.Query[Invoice](c).OrderBy(db.Col[int64]("id").Asc()).Get()
}
