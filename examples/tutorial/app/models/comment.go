package models

import "anetos.dev/anetos/db"

// region: model

// Comment is a reply on an issue.
type Comment struct {
	db.Model
	IssueID  int64  `db:"issue_id"`
	AuthorID int64  `db:"author_id"`
	Body     string `db:"body"`

	Author *User `rel:"belongs_to"` // by AuthorID
}

// CommentAdded is an event, emitted when someone comments on an issue:
// other parts of the app can react to it (events.On).
type CommentAdded struct {
	CommentID int64
}

// endregion
