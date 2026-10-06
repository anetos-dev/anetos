package models

import "anetos.dev/anetos/db"

// region: model

// Issue is a bug or a task. `go tool anetos gen` writes its typed columns
// (IssueCols) and relations (IssueRels) to models_gen.go.
type Issue struct {
	db.Model        // id, created_at, updated_at
	Title    string `db:"title"`
	Body     string `db:"body"`
	Status   string `db:"status"` // "open" or "closed"
	AuthorID int64  `db:"author_id"`

	Author *User `rel:"belongs_to"` // by AuthorID
}

// endregion
