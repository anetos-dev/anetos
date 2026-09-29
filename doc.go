// SPDX-License-Identifier: Apache-2.0

// Package anetos is a batteries-included web framework for Go.
//
// Anetos is pre-alpha: APIs will change before v1.0. See
// docs/planning/roadmap.md for the plan and docs/design/design.md for the
// architecture.
//
// An [App] ties together configuration ([AppConfig], package config), a
// structured logger, a small typed service container ([Provide],
// [Resolve]), [Provider]s that wire in functionality, and a supervised
// runtime (package supervisor) that runs long-lived components as
// goroutines and shuts them down gracefully.
//
//	app, err := anetos.New()
//	if err != nil {
//		log.Fatal(err)
//	}
//	app.Use(&database.Provider{}) // illustrative
//	_ = app.Go("cache-warmer", warmCache)
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//	defer stop()
//	if err := app.Run(ctx); err != nil {
//		log.Fatal(err)
//	}
//
// Concept guide: docs/site/concepts/application-lifecycle.md.
package anetos
