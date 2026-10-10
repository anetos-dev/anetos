// SPDX-License-Identifier: Apache-2.0

// Package migrate changes database schemas with versioned migrations
// written in Go (or SQL), compiled into the app's binary, and fills them
// with seeders.
//
// A migration creates or changes tables with the schema builder:
//
//	type createPosts struct{}
//
//	func (createPosts) Up(s *migrate.Schema) error {
//		return s.Create("posts", func(t *migrate.Table) {
//			t.ID()
//			t.ForeignID("author_id").Constrained().CascadeOnDelete()
//			t.String("title", 200)
//			t.Text("body")
//			t.Timestamps()
//			t.SoftDeletes()
//			t.Index("author_id", "created_at")
//		})
//	}
//
//	func (createPosts) Down(s *migrate.Schema) error { return s.Drop("posts") }
//
// Migrations live in a [Set], each added under an ID that starts with a
// timestamp, and a [Runner] applies them:
//
//	var All = migrate.NewSet("app")
//	func init() { All.Add("2026_10_01_120000_create_posts", createPosts{}) }
//
//	runner, err := migrate.New(app, []*migrate.Set{All})
//	results, err := runner.Up(ctx)
//
// Applied migrations are recorded in the migrations table, with the batch
// they ran in so [Runner.Rollback] can undo the last deploy's changes. On
// PostgreSQL and SQLite each migration runs in a transaction, so a failed
// one leaves no trace; MySQL commits every schema change immediately.
// [Runner.Command] provides the migrate, migrate:rollback, migrate:reset,
// migrate:fresh, migrate:status and db:seed commands.
package migrate
