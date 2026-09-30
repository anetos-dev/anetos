// SPDX-License-Identifier: Apache-2.0

// Package fixture holds models covering the column rules of the db
// package. models_gen.go is written by anetos gen; the modelgen tests
// check that it is up to date and that it matches db.Columns.
package fixture

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"net/netip"
	"time"

	"anetos.dev/anetos/db"
)

// Author embeds db.Model, so it is a model.
type Author struct {
	db.Model
	Name  string `db:"name"`
	Email string // untagged: email
}

// Post covers most field kinds.
type Post struct {
	db.Model
	db.SoftDeletes
	AuthorID    int64             // author_id
	Title       string            `db:"title"`
	Body        sql.NullString    // a value type: a column
	Meta        map[string]string `db:"meta,json"`
	Tags        []string          `db:",json"`
	Raw         json.RawMessage   // []byte: a column
	Data        []byte            `db:"data"`
	Score       *float64          // nullable
	Status      Status            // a named string type
	Code        Code              // a Valuer
	Point       Point             // a Scanner struct: one column
	Address     Address           `db:"address,json"` // a tagged struct: one column
	Views       int               `db:"views,readonly"`
	PublishedAt *time.Time        `db:"published_at"`
	Checked     time.Time         // time.Time is a value
	IP          netip.Addr        // an untagged struct that doesn't scan: not a column
	Author      *Author           // relation: not a column
	Comments    []Comment         // relation: not a column
	Ignored     string            `db:"-"`
	secret      string            // unexported: not a column
}

// Comment has a TableName method, so it is a model.
type Comment struct {
	ID     string `db:"id,pk"`
	PostID int64
	Body   string
}

// TableName implements db.Tabler.
func (Comment) TableName() string { return "post_comments" }

// Base is shared by models; it isn't one itself.
//
//anetos:skip
type Base struct {
	db.Model
	TenantID int64
}

// Invoice embeds Base, which embeds db.Model.
type Invoice struct {
	Base
	*Extra
	Total int64 `db:"total_cents"`
}

// Extra is embedded through a pointer.
type Extra struct {
	Note string
}

// Setting is a plain struct marked as a model.
//
//anetos:model
type Setting struct {
	Key   string `db:"key,pk"`
	Value string
}

// auditEntry is an unexported model.
type auditEntry struct {
	db.Timestamps
	Action string
}

// NotAModel is a plain struct: no columns are generated.
type NotAModel struct {
	Name string
}

// Page is generic, so it is never a model.
type Page[T any] struct {
	db.Model
	Items []T
}

// Status is a named string type.
type Status string

// Code is stored through driver.Valuer.
type Code struct{ v string }

// Value implements driver.Valuer.
func (c Code) Value() (driver.Value, error) { return c.v, nil }

// Point scans itself from one column.
type Point struct{ X, Y float64 }

// Scan implements sql.Scanner.
func (p *Point) Scan(any) error { return nil }

// Address is stored as JSON.
type Address struct {
	City string
}

var (
	_ = Post{secret: ""}
	_ = auditEntry{}
)
