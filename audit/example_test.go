// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"context"
	"log"

	"anetos.dev/anetos"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/db"
)

// Invoice is a model whose changes the log tracks.
type Invoice struct {
	db.Model
	db.SoftDeletes
	Number string `db:"number"`
	Total  int64  `db:"total"`
	Notes  string `db:"notes"`
}

// Track a model after db.Connect: every create, change, delete and
// restore of an invoice is logged from then on.
func ExampleTrack() {
	var app *anetos.App // from anetos.New, after db.Connect
	trail, err := audit.ForApp(app)
	if err != nil {
		log.Fatal(err)
	}
	if err := audit.Track[Invoice](trail, audit.Redact("notes")); err != nil {
		log.Fatal(err)
	}
}

// Record an event that isn't a write, with details.
func ExampleRecord() {
	ctx := context.Background() // a request's, a job's
	invoice := Invoice{Number: "2026-0042"}
	subject, err := audit.SubjectOf(&invoice)
	if err != nil {
		log.Fatal(err)
	}
	if err := audit.Record(ctx, "invoice.sent", subject, map[string]any{"to": "billing@example.com"}); err != nil {
		log.Fatal(err)
	}
}

// Attribute changes to a service rather than to a user: a webhook's
// handler, say.
func ExampleWithActor() {
	ctx := audit.WithActor(context.Background(), audit.Actor{Type: "service", ID: "stripe"})
	_ = ctx // db.Update(ctx, &invoice) is now logged as service:stripe
}
