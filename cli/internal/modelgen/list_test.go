// SPDX-License-Identifier: Apache-2.0

package modelgen

import (
	"slices"
	"testing"
)

// List describes the fixture's models: tables (a TableName method's
// too), columns with their typed columns' fields, relations.
func TestList(t *testing.T) {
	models, err := List(".", "./internal/fixture")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ModelInfo{}
	for _, m := range models {
		byName[m.Name] = m
	}
	post, comment := byName["Post"], byName["Comment"]
	if post.Table != "posts" || post.TableMethod || post.File != "internal/fixture/models.go" || post.Package != "anetos.dev/anetos/cli/internal/modelgen/internal/fixture" {
		t.Errorf("Post: %+v", post)
	}
	if comment.Table != "post_comments" || !comment.TableMethod {
		t.Errorf("Comment's table: %q %v", comment.Table, comment.TableMethod)
	}
	if _, ok := byName["NotAModel"]; ok {
		t.Error("NotAModel is listed")
	}
	if !slices.ContainsFunc(post.Columns, func(c ColumnInfo) bool { return c.Name == "id" && c.Field == "ID" && c.Type == "int64" }) {
		t.Errorf("Post's columns: %+v", post.Columns)
	}
	if !slices.Contains(post.Relations, RelationInfo{Field: "Author", Kind: "belongs_to", Model: "Author"}) ||
		!slices.Contains(post.Relations, RelationInfo{Field: "Comments", Kind: "has_many", Model: "Comment"}) {
		t.Errorf("Post's relations: %+v", post.Relations)
	}
	if slices.ContainsFunc(post.Columns, func(c ColumnInfo) bool { return c.Field == "Author" || c.Field == "Comments" }) {
		t.Error("a relation is listed as a column")
	}
}

// A TableName method promoted from an embedded struct, or returning a
// constant, names the table as the db package reads it.
func TestListTableMethods(t *testing.T) {
	dir := module(t, map[string]string{"models/models.go": `package models

import "anetos.dev/anetos/db"

type Base struct {
	db.Model
}

func (Base) TableName() string { return "bases" }

// Person's table is Base's: db asserts TableName on *Person.
type Person struct {
	Base
	Name string
}

const tbl = "the_orders"

type Order struct {
	db.Model
}

func (Order) TableName() string { return tbl }

type Item struct {
	db.Model
}

func (i Item) TableName() string { return "items_" + i.kind() }

func (Item) kind() string { return "x" }
`})
	models, err := List(dir, "./models")
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]string{}
	for _, m := range models {
		tables[m.Name] = m.Table
		if !m.TableMethod {
			t.Errorf("%s: no TableMethod", m.Name)
		}
	}
	if tables["Person"] != "bases" || tables["Order"] != "the_orders" || tables["Item"] != "" {
		t.Errorf("tables = %v", tables)
	}
}
