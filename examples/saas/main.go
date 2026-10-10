// SPDX-License-Identifier: Apache-2.0

// Command saas is a small SaaS app made with anetos new and anetos
// make:auth: accounts with a password, Google or GitHub; a welcome email
// sent by a queue job; plan changes consumed from the billing service's
// pub/sub topic; and a scheduled task that ends trials. One binary runs
// it all, or each part in its own processes:
//
//	go run . migrate
//	go run .                          # everything (go tool anetos dev: with live reload)
//	go run . run --only=web           # the web server
//	go run . run --only=worker        # queue jobs: the welcome email
//	go run . run --only=listener      # pub/sub: billing.subscription_changed
//	go run . run --only=scheduler     # scheduled tasks: end-trials
//	go run . pubsub:publish billing.subscription_changed '{"email":"ada@example.com","plan":"pro"}'
//
// Processes share the database (jobs, cache locks) and, with
// PUBSUB_DRIVER=redis, Redis (the topic). See README.md.
package main

import (
	"context"
	"log"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/redis"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/ext"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/pubsub"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/storage"
	"anetos.dev/anetos/web"

	"anetos.dev/anetos/examples/saas/app/jobs"
	"anetos.dev/anetos/examples/saas/app/listeners"
	"anetos.dev/anetos/examples/saas/app/tasks"
	"anetos.dev/anetos/examples/saas/database/migrations"
	"anetos.dev/anetos/examples/saas/routes"
)

//go:generate go tool templ generate
//go:generate go tool anetos generate

func main() {
	app, err := anetos.New() // reads .env and the environment
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute() // run (default), serve, migrate, route:list, help, …
}

// setup connects the database and adds the migrations, the cache, the
// queue and its workers, the mailer, file storage, pub/sub and its
// listener, the scheduler, the web server, the routes, the accounts and
// the plugins.
func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	// The cache, sessions and jobs tables serve CACHE_DRIVER=database,
	// SESSION_DRIVER=database and QUEUE_DRIVER=database.
	sets := []*migrate.Set{migrations.All, cache.Migrations(""), session.Migrations(""), queue.Migrations("", "")}
	if _, err := migrate.New(app, sets, migrate.WithSeeders(migrations.Seeders...)); err != nil {
		return nil, err
	}
	if _, err := cache.New(app); err != nil {
		return nil, err
	}
	q, err := queue.New(app)
	if err != nil {
		return nil, err
	}
	// The workers run with the app, or alone with `go run . run --only=worker`.
	if err := queue.Register[jobs.SendWelcome](q, queue.Tries(5)); err != nil {
		return nil, err
	}
	if err := q.Work(); err != nil {
		return nil, err
	}
	// Events: events.On(bus, listener); after the queue, for OnQueued.
	if _, err := events.New(app); err != nil {
		return nil, err
	}
	// MAIL_DRIVER: log (development), smtp or memory; after the queue, for
	// mailer.Queue.
	if _, err := mailer.New(app); err != nil {
		return nil, err
	}
	// STORAGE_DRIVER: local (the storage/app directory) or memory.
	if _, err := storage.New(app); err != nil {
		return nil, err
	}
	// PUBSUB_DRIVER: memory (in the process) or redis (REDIS_URL), which
	// listener processes share. The listeners run with the app, or alone
	// with `go run . run --only=listener`.
	ps, err := pubsub.New(app, redis.PubSubDriver())
	if err != nil {
		return nil, err
	}
	err = pubsub.Listen(ps, "billing.subscription_changed", listeners.ChangePlan,
		pubsub.Tries(5), pubsub.DeadLetter("billing.subscription_changed.dlq"))
	if err != nil {
		return nil, err
	}
	s, err := schedule.New(app)
	if err != nil {
		return nil, err
	}
	if err := schedules(s); err != nil {
		return nil, err
	}
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	sessions, err := session.New(app)
	if err != nil {
		return nil, err
	}
	routes.Register(srv.Router(), sessions)
	// Accounts (anetos make:auth): registration, login with a password,
	// Google or GitHub, email verification, password reset and API tokens.
	if _, err := setupAuth(app, srv.Router(), sessions); err != nil {
		return nil, err
	}
	// The plugins in plugins.go (anetos add), last: they use the services
	// above.
	if err := ext.Load(app, plugins()); err != nil {
		return nil, err
	}
	return srv, nil
}

// schedules adds the scheduled tasks. They run with the app, or alone
// with `go run . run --only=scheduler`; `go run . schedule:list` lists
// them. OnOneServer needs a shared cache (CACHE_DRIVER=database) when
// several processes run the scheduler.
func schedules(s *schedule.Scheduler) error {
	return s.Add(schedule.EveryMinute(), "end-trials", tasks.EndTrials,
		schedule.WithoutOverlapping(), schedule.OnOneServer())
}
