// SPDX-License-Identifier: Apache-2.0

// Command tracker is an issue tracker, the reference app of Anetos:
// projects with members and roles, issues with labels, comments, files
// and their history, search, emails from queue jobs, a morning digest,
// an admin, and a JSON API. See README.md.
//
//	go tool anetos dev        run with live reload (http://localhost:8080)
//	go run . migrate          apply database migrations
//	go run . db:seed          sample users, projects and issues
//	go run . help             list every command
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"anetos.dev/anetos"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/mysql"
	"anetos.dev/anetos/drivers/postgres"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/ext"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/storage"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/openapi"

	"anetos.dev/anetos/examples/tracker/app/jobs"
	"anetos.dev/anetos/examples/tracker/app/models"
	"anetos.dev/anetos/examples/tracker/app/tasks"
	"anetos.dev/anetos/examples/tracker/database/migrations"
	"anetos.dev/anetos/examples/tracker/locales"
	"anetos.dev/anetos/examples/tracker/routes"
)

//go:generate go tool templ generate
//go:generate go tool anetos generate

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Print(anetos.VersionText()) // without the settings setup needs
		return
	}
	app, err := anetos.New() // reads .env and the environment
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute() // run (default), serve, migrate, route:list, help, …
}

// setup adds the translations and connects the database, then adds the
// migrations, the cache, the queue and its workers, the mailer, file
// storage, the scheduler, the web server, the routes and the plugins.
func setup(app *anetos.App) (*web.Server, error) {
	// Translations: locales/<locale>/*.yaml; APP_LOCALE, APP_LOCALE_STRATEGY.
	if _, err := i18n.New(app, locales.FS); err != nil {
		return nil, err
	}
	// DB_DRIVER picks one: the tracker runs on SQLite (the default),
	// PostgreSQL and MySQL or MariaDB.
	if _, err := db.Connect(context.Background(), app, sqlite.Driver(), postgres.Driver(), mysql.Driver()); err != nil {
		return nil, err
	}
	// The cache, sessions and jobs tables serve CACHE_DRIVER=database,
	// SESSION_DRIVER=database and QUEUE_DRIVER=database; the audit log
	// keeps the history of projects and issues.
	sets := []*migrate.Set{migrations.All, cache.Migrations(""), session.Migrations(""), queue.Migrations("", ""), audit.Migrations()}
	if _, err := migrate.New(app, sets, migrate.WithSeeders(migrations.Seeders...)); err != nil {
		return nil, err
	}
	if _, err := cache.New(app); err != nil {
		return nil, err
	}
	// The audit log: every change to an issue, with who made it; the
	// issue's page shows its history, the admin all of it.
	trail, err := audit.New(app)
	if err != nil {
		return nil, err
	}
	if err := audit.Track[models.Issue](trail); err != nil {
		return nil, err
	}
	q, err := queue.New(app)
	if err != nil {
		return nil, err
	}
	// The emails about issues. The workers run with the app, or alone
	// with `go run . run --only=worker`.
	if err := queue.Register[jobs.NotifyAssigned](q, queue.Tries(5)); err != nil {
		return nil, err
	}
	if err := queue.Register[jobs.NotifyComment](q, queue.Tries(5)); err != nil {
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
	a, err := setupAuth(app, srv.Router(), sessions)
	if err != nil {
		return nil, err
	}
	// The tracker's pages and API.
	routes.Tracker(srv.Router(), sessions, a)
	// region: openapi
	// The API's description: `go run . openapi`, GET /api/openapi.json.
	if err := openapi.Register(app, srv, routes.OpenAPI); err != nil {
		return nil, err
	}
	// endregion
	// The admin interface (anetos make:admin) at ADMIN_PATH (/admin).
	if err := setupAdmin(app, srv.Router(), sessions, a); err != nil {
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
// them.
func schedules(s *schedule.Scheduler) error {
	// Weekday mornings at 8 (SCHEDULE_TIMEZONE, else APP_TIMEZONE), on
	// one server when several run the scheduler.
	return s.Add(schedule.Cron("0 8 * * 1-5"), "send-digests", tasks.SendDigests, schedule.OnOneServer())
}
