// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/pubsub"
)

// region: messages
// OrderCreated is what the shop publishes to "orders.created".
type OrderCreated struct {
	OrderID  int64  `json:"order_id"`
	Customer string `json:"customer"`
	Cents    int64  `json:"cents"`
}

// InvoiceCreated is what billing publishes to "invoices.created".
type InvoiceCreated struct {
	InvoiceID int64 `json:"invoice_id"`
	OrderID   int64 `json:"order_id"`
}

// endregion

// Invoice bills an order.
type Invoice struct {
	db.Model
	OrderID  int64  `db:"order_id" json:"order_id"`
	Customer string `db:"customer" json:"customer"`
	Cents    int64  `db:"cents" json:"cents"`
}

var colOrderID = db.Col[int64]("order_id")

// Migrations creates the invoices table.
var Migrations = migrate.NewSet("app")

func init() {
	Migrations.AddFunc("2026_10_01_140000_create_invoices",
		func(s *migrate.Schema) error {
			return s.Create("invoices", func(t *migrate.Table) {
				t.ID()
				t.BigInteger("order_id").Unique() // one invoice per order, even with concurrent deliveries
				t.String("customer", 100)
				t.BigInteger("cents")
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error { return s.Drop("invoices") })
}

// region: listener
// CreateInvoice bills an order. A message may arrive more than once, so
// an order that has its invoice is skipped.
func CreateInvoice(ctx context.Context, o OrderCreated) error {
	if o.OrderID <= 0 || o.Cents <= 0 {
		return pubsub.Permanent(fmt.Errorf("invalid order: %+v", o)) // straight to the dead-letter topic
	}
	return db.Tx(ctx, func(ctx context.Context) error {
		billed, err := db.Query[Invoice](ctx).Where(colOrderID.Eq(o.OrderID)).Exists()
		if err != nil || billed {
			return err
		}
		inv := &Invoice{OrderID: o.OrderID, Customer: o.Customer, Cents: o.Cents}
		if err := db.Create(ctx, inv); err != nil {
			return err // another delivery won the race: retried, then skipped
		}
		return pubsub.Publish(ctx, "invoices.created", InvoiceCreated{InvoiceID: inv.ID, OrderID: o.OrderID}, pubsub.AfterCommit())
	})
}

// endregion
