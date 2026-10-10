// SPDX-License-Identifier: Apache-2.0

// Command queue takes orders over HTTP and charges them in the
// background, with a job: retried when the (fake) payment gateway fails,
// marked failed when the card is declined. Placing an order emits an
// OrderPlaced event, whose listeners write the audit log, count sales
// and email a receipt. Jobs are kept where
// QUEUE_DRIVER says: sync (run at once), memory, database (SQLite here)
// or redis (REDIS_URL). Receipts are emailed with the mailer (MAIL_DRIVER:
// log, smtp, memory or postmark). A scheduler prunes the audit log every night and
// dispatches an hourly sales report; its locks are in the cache
// (CACHE_DRIVER: memory, database or redis).
//
//	go tool anetos key:generate >> .env   # APP_KEY, once
//	export APP_ENV=development HTTP_ADDR=:8080 QUEUE_DRIVER=database
//	go run . migrate
//	go run .                              # the server, the workers and the scheduler
//	curl -d '{"item":"Book","cents":1500,"email":"ada@example.com"}' localhost:8080/orders
//	open http://localhost:8080/dev/mail/receipt   # the receipt email (development)
//	go run . queue:failed                 # jobs that failed for good
//	go run . schedule:list                # the scheduled tasks
//	go run . plugins:list                 # the plugins (postmark: a webhook for bounces)
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/redis"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/ext"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/plugins/postmark"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
	"anetos.dev/anetos/web"
)

//go:generate go tool templ generate

func main() {
	app, err := anetos.New()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute() // run: the HTTP server, the workers and the scheduler; run --only=workers: the workers
}

// newGateway returns the payment gateway; tests replace it.
var newGateway = func() Gateway { return &FakeGateway{} }

func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	if _, err := migrate.New(app, []*migrate.Set{Migrations, queue.Migrations("", ""), cache.Migrations("")}); err != nil {
		return nil, err
	}
	app.AddContextValue(gatewayKey{}, newGateway()) // jobs and handlers find it in their context
	// region: setup
	q, err := queue.New(app, redis.QueueDriver()) // QUEUE_DRIVER: sync, memory, database or redis
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
	// region: mail-setup
	// With the queue, mailer.Queue sends from a queue job.
	if _, err := mailer.New(app, postmark.Driver()); err != nil { // MAIL_DRIVER: log, smtp, memory or postmark
		return nil, err
	}
	// endregion
	// region: events-setup
	bus, err := events.New(app) // after queue.New: queued listeners use the app's queue
	if err != nil {
		return nil, err
	}
	if err := events.On(bus, recordAudit); err != nil {
		return nil, err
	}
	if err := events.OnAsync(bus, sales.countSale, events.Name("count-sale")); err != nil {
		return nil, err
	}
	if err := events.OnQueued(bus, emailReceipt, events.Job(queue.Tries(10))); err != nil {
		return nil, err
	}
	// endregion
	// region: schedule-setup
	// The scheduler's locks (WithoutOverlapping, OnOneServer) are in the
	// cache: with several instances, use a store they share.
	if _, err := cache.New(app, redis.CacheDriver()); err != nil { // CACHE_DRIVER: memory, database or redis
		return nil, err
	}
	s, err := schedule.New(app) // SCHEDULE_TIMEZONE, default APP_TIMEZONE (UTC)
	if err != nil {
		return nil, err
	}
	if err := s.Add(schedule.DailyAt("03:00"), "prune-audit-log", pruneAuditLog,
		schedule.WithoutOverlapping(), schedule.OnOneServer(), schedule.Timeout(10*time.Minute)); err != nil {
		return nil, err
	}
	// Hourly, the scheduler dispatches a job; a worker runs it.
	if err := queue.Register[SalesReport](q, queue.Tries(3)); err != nil {
		return nil, err
	}
	if err := s.Add(schedule.Hourly(), "sales-report", schedule.Dispatch(SalesReport{}), schedule.OnOneServer()); err != nil {
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
	r.Post("/orders/{id}/receipt", web.H(ResendReceipt))
	// region: preview
	if app.Config().Env.IsDevelopment() {
		r.HandleStd(http.MethodGet, "/dev/mail/receipt", mailer.Preview(previewReceipt))
	}
	// endregion
	r.Get("/stats", func(c *web.Ctx) error {
		orders, cents := sales.Snapshot()
		return c.JSON(http.StatusOK, map[string]int64{"orders": orders, "cents": cents})
	})
	// region: plugins
	// The plugins in plugins.go (anetos add), last: they use the services
	// above. The postmark plugin's webhook is POST /postmark/webhook.
	if err := ext.Load(app, plugins()); err != nil {
		return nil, err
	}
	// endregion
	return srv, nil
}

// OrderInput is a new order.
type OrderInput struct {
	Item  string `json:"item" validate:"required|max:100"`
	Cents int64  `json:"cents" validate:"required|min:1"`
	Email string `json:"email" validate:"required|email|max:254"`
}

// region: dispatch
// PlaceOrder saves the order and dispatches the job that charges it.
func PlaceOrder(c *web.Ctx, in OrderInput) (web.Responder, error) {
	o := &Order{Item: in.Item, Cents: in.Cents, Email: in.Email, Status: "pending"}
	err := db.Tx(c, func(ctx context.Context) error {
		if err := db.Create(ctx, o); err != nil {
			return err
		}
		if err := events.Emit(ctx, OrderPlaced{OrderID: o.ID, Item: o.Item, Cents: o.Cents, Email: o.Email}); err != nil {
			return err // an On listener failed: no order
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
