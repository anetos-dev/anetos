// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

// Note is a note. NoteCols, its typed columns, are in models_gen.go.
//
//go:generate go tool anetos generate
type Note struct {
	db.Model        // id, created_at, updated_at
	Title    string `db:"title"`
	Body     string `db:"body"`
}

// Migrations creates the notes table.
var Migrations = migrate.NewSet("notes")

func init() {
	Migrations.AddFunc("2026_10_01_120000_create_notes",
		func(s *migrate.Schema) error {
			return s.Create("notes", func(t *migrate.Table) {
				t.ID()
				t.String("title", 100)
				t.Text("body")
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error { return s.Drop("notes") })
	// region: search-index
	Migrations.AddFunc("2026_10_02_120000_add_search_to_notes",
		func(s *migrate.Schema) error {
			return s.Alter("notes", func(t *migrate.Table) {
				t.SearchIndex("title", "body") // titles weigh more
			})
		},
		func(s *migrate.Schema) error {
			return s.Alter("notes", func(t *migrate.Table) { t.DropSearchIndex() })
		})
	// endregion
}

// region: seeders
// Seeders add sample notes: go run . db:seed
var Seeders = []migrate.Seeder{
	{Name: "notes", Run: func(ctx context.Context) error {
		_, err := NoteFactory.CreateMany(ctx, 25) // see factories.go
		return err
	}},
}

// endregion
