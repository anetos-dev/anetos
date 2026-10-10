// SPDX-License-Identifier: Apache-2.0

// Package anetos is a batteries-included web framework for Go: routing,
// data, accounts, queues, mail, storage, search, AI and translations, built
// on net/http, in one binary.
//
// Anetos is pre-release; APIs may change until 1.0. The documentation is
// at https://docs.anetos.dev, and [Getting started] builds a small app in
// about fifteen minutes. The [glossary] lists the words these packages use.
//
// # An app
//
// "anetos new" writes a project whose main.go has three steps.
// [New] reads the settings from .env and the environment. A setup
// function builds the services the app uses, in order: db.Connect for the
// database, then cache.New, queue.New, mailer.New, storage.New… and
// web.NewServer with the routes. [App.Execute] runs the command line it
// was given: "run" (the default) serves HTTP and runs the queue workers,
// the scheduler and the pub/sub listeners until the process is stopped, while
// "migrate", "route:list", "doctor" and the others do one thing and exit.
// The example shows the shape.
//
// # What the App holds
//
//   - Settings: [AppConfig] (APP_*), and [App.Source] for a package's own
//     (package config binds them to a struct).
//   - A small service container ([Provide], [Resolve]), to hand services
//     from one [Provider] to another at boot.
//   - The logger ([App.Logger], [Logger]) and the clock ([Now]), which
//     tests can freeze.
//   - Operations: each request, job, async listener call, pub/sub message,
//     scheduled task or AI tool call runs as an [Operation];
//     [App.AroundOperations] wraps them.
//   - Background components: long-running work that the supervisor
//     starts, restarts and stops in stages ([App.Go], [App.Component]).
//     [ProcessTypes] declares which process types (web, worker,
//     scheduler, listener) a component belongs to, so "run
//     --only=worker" can pick them.
//   - Carriers: context values that move with queued work ([Carrier]).
//   - Commands ([App.Command], [App.AddCommand]) and doctor checks
//     ([App.AddCheck]).
//
// # The packages
//
//   - Web: web (router, typed handlers, middleware, responses), view and
//     view/htmx (templ pages), session, web/ratelimit, web/openapi, validate.
//   - Data: db (models, query builder, relations, transactions, search),
//     db/migrate, db/factory.
//   - The app itself: config (settings into structs), cmd (commands),
//     supervisor.
//   - Background work: queue, events, pubsub, schedule.
//   - Accounts and security: auth, auth/social, auth/rbac, auth/password,
//     audit, encryption, qr (QR codes for two-factor setup).
//   - Mail, files and cache: mailer, storage, cache.
//   - Languages and time: i18n, and here [Date] and the app's time zone
//     ([Location]).
//   - AI: ai.
//   - Testing: anetostest, and the helpers in aitest, cachetest, dbtest,
//     pubsubtest, queuetest and storagetest.
//   - Plugins: ext.
//
// The admin panel (anetos.dev/anetos/admin) and the drivers
// (anetos.dev/anetos/drivers/...: SQLite, PostgreSQL, MySQL, Redis, S3,
// Google Cloud, Anthropic, OpenAI, Gemini) are modules of their own, as
// is the Postmark plugin (anetos.dev/anetos/plugins/postmark).
//
// [Getting started]: https://docs.anetos.dev/getting-started/
// [glossary]: https://docs.anetos.dev/concepts/glossary/
package anetos
