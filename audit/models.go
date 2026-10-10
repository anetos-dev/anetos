// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"errors"
	"slices"
	"strings"
	"time"

	"anetos.dev/anetos/db/migrate"
)

// Actor is who did something: a user ("user", "42"), the app itself
// ("system", ""), or what the app names with [WithActor] ("service",
// "stripe").
type Actor struct {
	// Type is "user", "system", or the app's own: lowercase letters,
	// digits, _ and -, up to 50 characters.
	Type string `json:"type"`
	// ID identifies the actor within its type, up to 100 bytes; "" for
	// system.
	ID string `json:"id,omitempty"`
}

// System is the actor of work no user or named actor started: a command,
// a scheduled task, a job dispatched by one of those.
var System = Actor{Type: "system"}

// User returns the actor for the user with id (an auth.Authenticatable's
// AuthID).
func User(id string) Actor { return Actor{Type: "user", ID: id} }

// String returns "user:42", or "system".
func (a Actor) String() string {
	if a.ID == "" {
		return a.Type
	}
	return a.Type + ":" + a.ID
}

// parseActor reads an Actor written by String.
func parseActor(s string) (Actor, bool) {
	typ, id, _ := strings.Cut(s, ":")
	a := Actor{typ, id}
	return a, a.check() == nil
}

func (a Actor) check() error {
	if !kindRe.MatchString(a.Type) || len(a.ID) > 100 {
		return errors.New("audit: an actor's type is lowercase letters, digits, _ and - (up to 50), and its ID up to 100 bytes")
	}
	return nil
}

// Subject is what an entry is about: a row (the table and its primary
// key, as text), or what the app names in [Record].
type Subject struct {
	// Type is the table, or the app's name for the kind of thing, up to
	// 100 bytes.
	Type string `json:"type"`
	// ID is the row's primary key as text (fmt.Sprint), up to 255 bytes.
	ID string `json:"id"`
}

// Changes are an entry's column values: New for a create, Old and New of
// the changed columns for an update, Old for a permanent delete.
// Redacted columns have the value "[redacted]".
type Changes struct {
	// Old are the values before, by column.
	Old map[string]any `json:"old,omitempty"`
	// New are the values after, by column.
	New map[string]any `json:"new,omitempty"`
}

// Fields returns the columns that have a value, sorted.
func (c Changes) Fields() []string {
	var out []string
	for k := range c.Old {
		out = append(out, k)
	}
	for k := range c.New {
		if _, ok := c.Old[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// Redacted is the value recorded for redacted columns.
const Redacted = "[redacted]"

// The actions of tracked models' writes. Entries the app records with
// [Record] have actions of its own.
const (
	// Created: a row was inserted.
	Created = "created"
	// Updated: columns of a row changed.
	Updated = "updated"
	// Deleted: a row was soft-deleted; it is still in the database.
	Deleted = "deleted"
	// Restored: a soft-deleted row was restored.
	Restored = "restored"
	// ForceDeleted: a row was removed from the database (ForceDelete, or
	// Delete on a model without SoftDeletes).
	ForceDeleted = "force_deleted"
	// Upserted: rows were inserted or updated by an upsert (bulk only).
	Upserted = "upserted"
)

// Entry is one entry of the log (table audit_log): a single-row write of
// a tracked model, or an event recorded with [Record]. Query entries with
// db.Query[audit.Entry].
type Entry struct {
	// ID is the entry's key.
	ID int64 `db:"id,pk" json:"id"`
	// OccurredAt is when it happened (the app's clock, UTC).
	OccurredAt time.Time `db:"occurred_at" json:"occurred_at"`
	// ActorType and ActorID are who did it ([Entry.Actor]).
	ActorType string `db:"actor_type" json:"actor_type"`
	// ActorID identifies the actor within its type.
	ActorID string `db:"actor_id" json:"actor_id"`
	// ActingAs is the user the actor was acting as (auth.Impersonate),
	// "user:42"; "" if none.
	ActingAs string `db:"acting_as" json:"acting_as,omitempty"`
	// Action is what happened: [Created], [Updated]… or the app's own.
	Action string `db:"action" json:"action"`
	// SubjectType and SubjectID are what it happened to.
	SubjectType string `db:"subject_type" json:"subject_type"`
	// SubjectID is the subject's key, as text.
	SubjectID string `db:"subject_id" json:"subject_id"`
	// Changes are the column values involved.
	Changes Changes `db:"changes,json" json:"changes"`
	// Properties are the app's details of an event it recorded.
	Properties map[string]any `db:"properties,json" json:"properties,omitempty"`
	// ViaKind and ViaName are the work that did it: a unit of work
	// ("request", "GET /posts/7"; "job", "send-welcome"; "listener",
	// "task", "message", "tool") or "command" and its name; "" if none.
	ViaKind string `db:"via_kind" json:"via_kind"`
	// ViaName names the work.
	ViaName string `db:"via_name" json:"via_name"`
	// RequestID is the HTTP request's ID (web.RequestID), "" outside one.
	RequestID string `db:"request_id" json:"request_id"`
	// IP is the client's address, masked or not as AUDIT_IP says; "" by
	// default.
	IP string `db:"ip" json:"ip,omitempty"`
}

// TableName is audit_log.
func (Entry) TableName() string { return "audit_log" }

// Actor returns who did it.
func (e Entry) Actor() Actor { return Actor{e.ActorType, e.ActorID} }

// Subject returns what it was done to.
func (e Entry) Subject() Subject { return Subject{e.SubjectType, e.SubjectID} }

// Row is a row's values in a bulk entry.
type Row struct {
	// Key is the row's primary key, as text.
	Key string `json:"key"`
	// Values are its column values.
	Values map[string]any `json:"values"`
}

// Condition is the condition a bulk write chose its rows by.
type Condition struct {
	// SQL is the condition, in the database's SQL ("" for creates and
	// upserts, and for writes to every row).
	SQL string `json:"sql,omitempty"`
	// Args are its arguments.
	Args []any `json:"args,omitempty"`
}

// BulkOp is one write to many rows of a tracked model (table audit_bulk):
// a query's Update, Delete, ForceDelete or Restore, a CreateMany or an
// Upsert. The rows it touched are in audit_bulk_items ([BulkItem]).
type BulkOp struct {
	// ID is the entry's key.
	ID int64 `db:"id,pk" json:"id"`
	// OccurredAt is when it happened.
	OccurredAt time.Time `db:"occurred_at" json:"occurred_at"`
	// ActorType and ActorID are who did it.
	ActorType string `db:"actor_type" json:"actor_type"`
	// ActorID identifies the actor within its type.
	ActorID string `db:"actor_id" json:"actor_id"`
	// ActingAs is the user the actor was acting as (auth.Impersonate),
	// "user:42"; "" if none.
	ActingAs string `db:"acting_as" json:"acting_as,omitempty"`
	// Action is [Created], [Updated], [Deleted], [Restored],
	// [ForceDeleted] or [Upserted].
	Action string `db:"action" json:"action"`
	// SubjectType is the table.
	SubjectType string `db:"subject_type" json:"subject_type"`
	// RowCount is how many rows were written.
	RowCount int64 `db:"row_count" json:"row_count"`
	// Condition is how the rows were chosen.
	Condition Condition `db:"where_clause,json" json:"condition"`
	// Assignments are an update's new values by column; an expression is
	// {"sql": …, "args": […]}.
	Assignments map[string]any `db:"assignments,json" json:"assignments,omitempty"`
	// Before are rows' values before the write, up to
	// AUDIT_BULK_MAX_VALUES rows.
	Before []Row `db:"values_before,json" json:"before,omitempty"`
	// After are rows' values after a create or an upsert, within the same
	// limit.
	After []Row `db:"values_after,json" json:"after,omitempty"`
	// Complete reports whether Before and After hold every row's values.
	Complete bool `db:"values_complete" json:"complete"`
	// ViaKind and ViaName are the work that did it, as in [Entry].
	ViaKind string `db:"via_kind" json:"via_kind"`
	// ViaName names the work.
	ViaName string `db:"via_name" json:"via_name"`
	// RequestID is the HTTP request's ID, "" outside one.
	RequestID string `db:"request_id" json:"request_id"`
	// IP is the client's address, as AUDIT_IP says.
	IP string `db:"ip" json:"ip,omitempty"`
}

// TableName is audit_bulk.
func (BulkOp) TableName() string { return "audit_bulk" }

// Actor returns who did it.
func (b BulkOp) Actor() Actor { return Actor{b.ActorType, b.ActorID} }

// BulkItem is a row a bulk write touched (table audit_bulk_items).
type BulkItem struct {
	// BulkID is the [BulkOp]'s ID.
	BulkID int64 `db:"bulk_id" json:"bulk_id"`
	// SubjectType is the table.
	SubjectType string `db:"subject_type" json:"subject_type"`
	// SubjectID is the row's key, as text.
	SubjectID string `db:"subject_id" json:"subject_id"`
}

// TableName is audit_bulk_items.
func (BulkItem) TableName() string { return "audit_bulk_items" }

// Migrations returns the migrations creating the log's tables (audit_log,
// audit_bulk, audit_bulk_items), for migrate.New. Entries are written
// in the database of the change they describe: an app tracking models of
// another database runs these there too.
func Migrations() *migrate.Set {
	s := migrate.NewSet("audit")
	s.AddFunc("2026_10_06_000100_create_audit_tables",
		func(s *migrate.Schema) error {
			who := func(t *migrate.Table) {
				t.Timestamp("occurred_at")
				t.String("actor_type", 50)
				t.String("actor_id", 100)
				t.String("action", 100)
				t.String("subject_type", 100)
			}
			via := func(t *migrate.Table) {
				t.String("via_kind", 20)
				t.String("via_name", 255)
				t.String("request_id", 100)
				t.String("ip", 45)
			}
			if err := s.Create("audit_log", func(t *migrate.Table) {
				t.ID()
				who(t)
				t.String("subject_id", 255)
				t.JSON("changes")
				t.JSON("properties").Nullable()
				via(t)
				t.Index("subject_type", "subject_id", "id")
				t.Index("actor_type", "actor_id", "id")
				t.Index("occurred_at")
			}); err != nil {
				return err
			}
			if err := s.Create("audit_bulk", func(t *migrate.Table) {
				t.ID()
				who(t)
				t.BigInteger("row_count")
				t.JSON("where_clause")
				t.JSON("assignments").Nullable()
				t.JSON("values_before").Nullable()
				t.JSON("values_after").Nullable()
				t.Boolean("values_complete")
				via(t)
				t.Index("subject_type", "id")
				t.Index("actor_type", "actor_id", "id")
				t.Index("occurred_at")
			}); err != nil {
				return err
			}
			if err := s.Create("audit_bulk_items", func(t *migrate.Table) {
				t.ForeignID("bulk_id").References("audit_bulk").CascadeOnDelete()
				t.String("subject_type", 100)
				t.String("subject_id", 255)
				t.Primary("bulk_id", "subject_id")
				t.Index("subject_type", "subject_id")
			}); err != nil {
				return err
			}
			if s.Dialect() == "mysql" {
				// Keys and actor IDs compare byte for byte, as elsewhere:
				// MySQL's and MariaDB's text collations ignore case, accents
				// and trailing spaces.
				for _, stmt := range []string{
					"ALTER TABLE audit_log MODIFY actor_id VARBINARY(400) NOT NULL, MODIFY subject_id VARBINARY(1020) NOT NULL",
					"ALTER TABLE audit_bulk MODIFY actor_id VARBINARY(400) NOT NULL",
					"ALTER TABLE audit_bulk_items MODIFY subject_id VARBINARY(1020) NOT NULL",
				} {
					if err := s.Exec(stmt); err != nil {
						return err
					}
				}
			}
			return nil
		},
		func(s *migrate.Schema) error {
			return errors.Join(s.Drop("audit_bulk_items"), s.Drop("audit_bulk"), s.Drop("audit_log"))
		})
	s.AddFunc("2026_10_07_000100_add_acting_as_to_audit_tables",
		func(s *migrate.Schema) error {
			for _, table := range []string{"audit_log", "audit_bulk"} {
				if err := s.Alter(table, func(t *migrate.Table) { t.String("acting_as", 160).Default("") }); err != nil {
					return err
				}
				if s.Dialect() == "mysql" {
					// Compared byte for byte, as actor_id (Anonymize).
					if err := s.Exec("ALTER TABLE " + table + " MODIFY acting_as VARBINARY(640) NOT NULL DEFAULT ''"); err != nil {
						return err
					}
				}
			}
			return nil
		},
		func(s *migrate.Schema) error {
			for _, table := range []string{"audit_log", "audit_bulk"} {
				if err := s.Alter(table, func(t *migrate.Table) { t.DropColumn("acting_as") }); err != nil {
					return err
				}
			}
			return nil
		})
	return s
}
