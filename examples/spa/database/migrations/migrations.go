// SPDX-License-Identifier: Apache-2.0

// Package migrations holds the database migrations and seeders. Add a
// migration with `go tool anetos make:migration create_posts_table`; each
// file adds itself to All.
package migrations

import "anetos.dev/anetos/db/migrate"

// All is the application's migration set.
var All = migrate.NewSet("app")

// Seeders fill the database with data (`go run . db:seed`).
var Seeders = []migrate.Seeder{}
