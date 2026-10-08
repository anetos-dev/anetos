// SPDX-License-Identifier: Apache-2.0

package models

import (
	"anetos.dev/anetos/db"
)

// region: model

// Bookmark is a row of the bookmarks table (anetos make:crud). `go tool anetos
// gen` writes its typed columns (BookmarkCols) to models_gen.go.
type Bookmark struct {
	db.Model        // id, created_at, updated_at
	UserID   int64  `db:"user_id"` // whose bookmark
	URL      string `db:"url"`
	Title    string `db:"title"`
	Notes    string `db:"notes"`
	Archived bool   `db:"archived"`
}

// endregion
