// SPDX-License-Identifier: Apache-2.0

// Command queue takes orders over HTTP and charges them in the
// background, with a job: retried when the (fake) payment gateway fails,
// marked failed when the card is declined. Jobs are kept where
// QUEUE_DRIVER says: sync (run at once), memory, database (SQLite here)
// or redis (REDIS_URL).
//
//	go tool anetos key:generate >> .env   # APP_KEY, once
//	export APP_ENV=development HTTP_ADDR=:8080 QUEUE_DRIVER=database
//	go run . migrate
//	go run .                              # the server and the workers
//	curl -d '{"item":"Book","cents":1500}' localhost:8080/orders
//	go run . queue:failed                 # jobs that failed for good
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/redis"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/queue"
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
	app.Execute() // run: the HTTP server and the workers; run --only=workers: the workers
}

// newGateway returns the payment gateway; tests replace it.
var newGateway = func() Gateway { return &FakeGateway{} }

func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	if _, err := migrate.ForApp(app, []*migrate.Set{Migrations, queue.Migrations("", "")}); err != nil {
		return nil, err
	}
	app.AddContextValue(gatewayKey{}, newGateway()) // jobs and handlers find it in their context
	// region: setup
	q, err := queue.ForApp(app, redis.QueueDriver()) // QUEUE_DRIVER: sync, memory, database or redis
	if err != nil {
		return nil, err
	}
	if err := queue.Register[ChargeOrder](q, queue.Tries(5), queue.Timeout(30*time.Second)); err != nil {
		return nil, err
	}
	// Workers for the payments queue first, then the default one.
	if err := q.Work(queue.Queues("payments", "default"), queue.Concurrency(4)); err != nil {
		return nil, err
	}
	// endregion
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	r := srv.Router()
	r.Post("/orders", web.H(PlaceOrder))
	r.Get("/orders/{id}", web.H(ShowOrder))
	return srv, nil
}

// OrderInput is a new order.
type OrderInput struct {
	Item  string `json:"item" validate:"required|max:100"`
	Cents int64  `json:"cents" validate:"required|min:1"`
}

// region: dispatch
// PlaceOrder saves the order and dispatches the job that charges it.
func PlaceOrder(c *web.Ctx, in OrderInput) (web.Responder, error) {
	o := &Order{Item: in.Item, Cents: in.Cents, Status: "pending"}
	err := db.Tx(c, func(ctx context.Context) error {
		if err := db.Create(ctx, o); err != nil {
			return err
		}
		// AfterCommit: no charge for an order that isn't saved. (The
		// database driver writes the job in the transaction instead.)
		return queue.Dispatch(ctx, ChargeOrder{OrderID: o.ID}, queue.OnQueue("payments"), queue.AfterCommit())
	})
	if err != nil {
		return nil, err
	}
	return web.JSON(http.StatusAccepted, o), nil // 202: it is being charged
}

// endregion

// OrderID is the order in the path.
type OrderID struct {
	ID int64 `path:"id"`
}

// ShowOrder returns an order, with its status.
func ShowOrder(c *web.Ctx, in OrderID) (*Order, error) {
	o, err := db.Find[Order](c, in.ID)
	return &o, err
}
