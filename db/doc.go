// SPDX-License-Identifier: Apache-2.0

// Package db is Anetos's data layer: connections, a typed query builder,
// model CRUD with timestamps, soft deletes and hooks, transactions, raw SQL
// scanned into structs, and pagination. It is built on database/sql and
// works with PostgreSQL, MySQL/MariaDB and SQLite through the driver
// modules (anetos.dev/anetos/drivers/postgres, …/mysql, …/sqlite).
//
// Connect once at startup; the connection is then available in every
// context the app creates (handlers, jobs, app.Go tasks):
//
//	database, err := db.Connect(ctx, app, sqlite.Driver(), postgres.Driver())
//
// Models are plain structs:
//
//	type Post struct {
//		db.Model        // id, created_at, updated_at
//		db.SoftDeletes  // deleted_at
//		Title    string `db:"title" json:"title"`
//		AuthorID int64  `db:"author_id" json:"author_id"`
//	}
//
//	err := db.Create(ctx, &post)
//	post, err := db.Find[Post](ctx, id)          // ErrNotFound → 404
//	posts, err := db.Query[Post](ctx).
//		Where(db.C("author_id").Eq(id)).
//		Latest().
//		Paginate(page, 20)
//
// Columns are the db tags, or the snake_case field names; the table is the
// snake_case plural of the type name unless a TableName method says
// otherwise. Struct, pointer-to-struct and slice-of-struct fields without a
// db tag are ignored (they are reserved for relations).
//
// # Transactions
//
// [Tx] runs a function in a transaction carried by its context, so
// everything called with that context joins it; nested calls use
// savepoints.
//
// # Raw SQL
//
// [Raw] scans any query into structs or values, and [Exec] runs
// statements. Placeholders are written ? on every database, or :name with
// [Named].
//
// # Validation
//
// Importing db registers the unique and exists validation rules, which
// query the database in the request's context.
package db
