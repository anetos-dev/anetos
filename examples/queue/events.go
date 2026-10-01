// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"sync"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/mailer"
)

// region: event
// OrderPlaced is emitted when an order is saved. Each listener gets the
// value (queued listeners, as JSON), so keep it free of pointers they
// could change.
type OrderPlaced struct {
	OrderID int64  `json:"order_id"`
	Item    string `json:"item"`
	Cents   int64  `json:"cents"`
	Email   string `json:"email"`
}

// endregion

// AuditEntry is a line of the audit log.
type AuditEntry struct {
	db.Model
	OrderID int64  `db:"order_id" json:"order_id"`
	Message string `db:"message" json:"message"`
}

// TableName implements db.Tabler.
func (AuditEntry) TableName() string { return "audit_log" }

func init() {
	Migrations.AddFunc("2026_10_01_130100_create_audit_log",
		func(s *migrate.Schema) error {
			return s.Create("audit_log", func(t *migrate.Table) {
				t.ID()
				t.BigInteger("order_id")
				t.String("message", 255)
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error { return s.Drop("audit_log") })
}

// Sales counts the orders placed since the app started, for GET /stats.
type Sales struct {
	mu     sync.Mutex
	Orders int64 `json:"orders"`
	Cents  int64 `json:"cents"`
}

// Snapshot returns the counts.
func (s *Sales) Snapshot() (orders, cents int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Orders, s.Cents
}

// Outbox stands in for a chat channel: it keeps the messages sent.
type Outbox struct {
	mu   sync.Mutex
	sent []string
}

// Sent returns the messages sent.
func (o *Outbox) Sent() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.sent...)
}

var sales = &Sales{}

// region: listeners
// recordAudit writes to the audit log in the order's transaction: if it
// fails, the order isn't placed.
func recordAudit(ctx context.Context, e OrderPlaced) error {
	return db.Create(ctx, &AuditEntry{OrderID: e.OrderID, Message: "placed: " + e.Item})
}

// countSale updates the sales counts in the background, once the order
// is committed. If the process stops first, the count is lost: fine for
// a dashboard.
func (s *Sales) countSale(_ context.Context, e OrderPlaced) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Orders++
	s.Cents += e.Cents
	return nil
}

// emailReceipt sends the receipt, as a queue job: retried if the mail
// server fails, and not lost if the process stops.
func emailReceipt(ctx context.Context, e OrderPlaced) error {
	return mailer.Send(ctx, ReceiptMail{Order: e})
}

// endregion
