// SPDX-License-Identifier: Apache-2.0

// Package config loads application configuration from the environment and
// .env files into typed Go structs.
//
// Configuration has two parts:
//
//   - A [Source] supplies raw string values by key. [Load] builds the
//     standard layered source: process environment, then .env.<APP_ENV>,
//     then .env. [Map], [Env] and [Layers] let tests and tools build their
//     own.
//   - [Bind] (or the generic [Get]) copies values into a struct using
//     `env`, `default` and `prefix` tags, converting types and reporting every
//     missing or invalid key at once.
//
// Typical use at startup:
//
//	src, err := config.Load(config.LoadOptions{})
//	if err != nil {
//		log.Fatal(err)
//	}
//	db, err := config.Get[DatabaseConfig](src)
//	if err != nil {
//		log.Fatal(err) // lists every bad key, e.g. "config: DB_PORT (DatabaseConfig.Port): invalid integer "abc""
//	}
//
// Application code should depend on typed config structs, never on raw
// lookups by string key. Binding uses reflection and is intended to run once,
// at startup.
//
// User guide: docs/site/guides/configuration.md.
package config
