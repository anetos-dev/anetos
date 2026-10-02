// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/queue"
)

// Order is something to charge for.
type Order struct {
	db.Model
	Item   string `db:"item" json:"item"`
	Cents  int64  `db:"cents" json:"cents"`
	Email  string `db:"email" json:"email"`   // the customer's, for the receipt
	Status string `db:"status" json:"status"` // pending, paid or failed
}

var (
	colID     = db.Col[int64]("id")
	colStatus = db.Col[string]("status")
)

// setStatus moves an order from pending to status.
func setStatus(ctx context.Context, id int64, status string) error {
	_, err := db.Query[Order](ctx).Where(colID.Eq(id), colStatus.Eq("pending")).Update(colStatus.Set(status))
	return err
}

// Migrations creates the orders table; the queue's tables come from
// queue.Migrations.
var Migrations = migrate.NewSet("app")

func init() {
	Migrations.AddFunc("2026_10_01_130000_create_orders",
		func(s *migrate.Schema) error {
			return s.Create("orders", func(t *migrate.Table) {
				t.ID()
				t.String("item", 100)
				t.BigInteger("cents")
				t.String("email", 254)
				t.String("status", 20).Default("pending")
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error { return s.Drop("orders") })
}

// Gateway charges cards. ChargeOrder finds it in its context.
type Gateway interface {
	// Charge charges cents once per key: charging again with the same key
	// does nothing.
	Charge(ctx context.Context, key string, cents int64) error
}

// ErrDeclined is a charge the card's bank refused: retrying won't help.
var ErrDeclined = errors.New("card declined")

type gatewayKey struct{}

// gateway returns the gateway in ctx (setup adds it to the app's
// contexts).
func gateway(ctx context.Context) Gateway { return ctx.Value(gatewayKey{}).(Gateway) }

// FakeGateway records charges, declines orders over $1,000, and fails
// the first attempt of each charge when Flaky is set.
type FakeGateway struct {
	Flaky   bool
	mu      sync.Mutex
	charged map[string]int64
	tries   map[string]int
}

// Charge implements Gateway.
func (g *FakeGateway) Charge(_ context.Context, key string, cents int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.charged == nil {
		g.charged, g.tries = map[string]int64{}, map[string]int{}
	}
	g.tries[key]++
	switch {
	case cents > 100_000:
		return ErrDeclined
	case g.Flaky && g.tries[key] == 1:
		return errors.New("gateway: 503 Service Unavailable")
	}
	g.charged[key] = cents
	return nil
}

// Charges returns the number of charges made.
func (g *FakeGateway) Charges() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.charged)
}

// region: job
// ChargeOrder charges an order. Its fields are what the worker gets: the
// order's ID, not the order.
type ChargeOrder struct {
	OrderID int64 `json:"order_id"`
}

// Handle charges the order. A job may run more than once, so it is
// idempotent: the job's ID is the gateway's idempotency key, and a paid
// order isn't charged again.
func (j ChargeOrder) Handle(ctx context.Context) error {
	o, err := db.Find[Order](ctx, j.OrderID)
	if errors.Is(err, db.ErrNotFound) {
		return queue.Permanent(err) // retrying won't make it appear
	}
	if err != nil {
		return err
	}
	if o.Status != "pending" {
		return nil
	}
	job, _ := queue.Current(ctx)
	switch err := gateway(ctx).Charge(ctx, job.ID, o.Cents); {
	case errors.Is(err, ErrDeclined):
		return queue.Permanent(err) // fail now: Failed marks the order
	case err != nil:
		return fmt.Errorf("charge order %d: %w", o.ID, err) // retried later
	}
	return setStatus(ctx, o.ID, "paid")
}

// Failed runs when the job fails for good: declined, or out of tries.
func (j ChargeOrder) Failed(ctx context.Context, err error) {
	anetos.Logger(ctx).InfoContext(ctx, "order not charged", "order", j.OrderID, "error", err)
	if err := setStatus(ctx, j.OrderID, "failed"); err != nil {
		anetos.Logger(ctx).ErrorContext(ctx, "mark the order failed", "order", j.OrderID, "error", err)
	}
}

// endregion
