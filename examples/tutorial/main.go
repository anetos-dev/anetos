// Command tracker is an Anetos application.
//
//	go tool anetos dev        run with live reload (http://localhost:8080)
//	go run . migrate          apply database migrations
//	go run . help             list every command
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
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

	"tracker/app/listeners"
	"tracker/database/migrations"
	"tracker/locales"
	"tracker/routes"
)

//go:generate go tool templ generate
//go:generate go tool anetos gen

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
	app.Execute() // run (default), serve, migrate, routes:list, help, …
}

// setup adds the translations and connects the database, then adds the
// migrations, the cache, the queue and its workers, the mailer, file
// storage, the scheduler, the web server, the routes and the plugins.
func setup(app *anetos.App) (*web.Server, error) {
	// Translations: locales/<locale>/*.yaml; APP_LOCALE, LOCALE_URL.
	if _, err := i18n.New(app, locales.FS); err != nil {
		return nil, err
	}
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		return nil, err
	}
	// The cache, sessions and jobs tables serve CACHE_STORE=database,
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
	// Register job types here (queue.Register[jobs.SendWelcome](q)). The
	// workers run with the app, or alone with `go run . run --only=workers`.
	if err := q.Work(); err != nil {
		return nil, err
	}
	// Events: events.On(bus, listener); after the queue, for OnQueued.
	// region: listeners
	bus, err := events.New(app)
	if err != nil {
		return nil, err
	}
	// The author's email about a new comment, sent by a queue worker.
	if err := events.OnQueued(bus, listeners.EmailAuthor); err != nil {
		return nil, err
	}
	// endregion
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
	// Google or GitHub, two-factor sign-in, account settings, email
	// verification, password reset and API tokens.
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

// schedules adds the scheduled tasks, for example
//
//	err := s.Add(schedule.DailyAt("02:00"), "prune-sessions", tasks.PruneSessions, schedule.OnOneServer())
//
// Once there are tasks, they run with the app, or alone with
// `go run . run --only=scheduler`; `go run . schedule:list` lists them.
func schedules(s *schedule.Scheduler) error {
	return nil
}
