// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/queue"
)

func init() {
	extra = append(extra, test{"Events", testBusEvents})
}

type stBusEvent struct {
	Text string `json:"text"`
}

// testBusEvents checks how listeners meet transactions: On listeners run in
// Emit's transaction; OnAsync ones run after it commits, and never if it
// rolls back; OnQueued jobs are written in it (database queue driver).
func testBusEvents(t *testing.T, ctx context.Context) {
	store := queueTables(t, ctx)
	q := queue.NewWithStore(store, queue.Config{})
	logs := &syncBuffer{}
	b := events.NewBus(events.WithQueue(q), events.WithLogger(slog.New(slog.NewTextHandler(logs, nil))))
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	check(t, events.On(b, func(ctx context.Context, e stBusEvent) error {
		return db.Create(ctx, &stNote{Text: e.Text})
	}))
	var async atomic.Int32
	check(t, events.OnAsync(b, func(context.Context, stBusEvent) error { async.Add(1); return nil }))
	check(t, events.OnQueued(b, func(context.Context, stBusEvent) error { return nil }, events.Name("st-queued")))
	ctx = events.WithBus(ctx, b)
	count := func() (notes, jobs int64) {
		t.Helper()
		var err error
		notes, err = db.Query[stNote](ctx).Count()
		check(t, err)
		jobs, err = store.Size(ctx, "default")
		check(t, err)
		return notes, jobs
	}

	errRollback := errors.New("rollback")
	err := db.Tx(ctx, func(ctx context.Context) error {
		if err := events.Emit(ctx, stBusEvent{Text: "rolled back"}); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("Tx = %v", err)
	}
	check(t, b.Wait(ctx))
	if notes, jobs := count(); notes != 0 || jobs != 0 || async.Load() != 0 {
		t.Errorf("after a rollback: %d notes, %d jobs, %d async runs; want none", notes, jobs, async.Load())
	}

	check(t, db.Tx(ctx, func(ctx context.Context) error {
		if err := events.Emit(ctx, stBusEvent{Text: "committed"}); err != nil {
			return err
		}
		if err := b.Wait(ctx); err != nil {
			return err
		}
		if async.Load() != 0 {
			t.Error("the async listener ran before the commit")
		}
		return nil
	}))
	check(t, b.Wait(ctx))
	if notes, jobs := count(); notes != 1 || jobs != 1 || async.Load() != 1 {
		t.Errorf("after the commit: %d notes, %d jobs, %d async runs; want 1 each", notes, jobs, async.Load())
	}

	// A nested transaction that rolls back takes its event with it.
	check(t, db.Tx(ctx, func(ctx context.Context) error {
		err := db.Tx(ctx, func(ctx context.Context) error {
			if err := events.Emit(ctx, stBusEvent{Text: "nested"}); err != nil {
				return err
			}
			return errRollback
		})
		if !errors.Is(err, errRollback) {
			return err
		}
		return nil
	}))
	check(t, b.Wait(ctx))
	if notes, jobs := count(); notes != 1 || jobs != 1 || async.Load() != 1 {
		t.Errorf("after a nested rollback: %d notes, %d jobs, %d async runs; want 1 each", notes, jobs, async.Load())
	}

	// In a test's transaction (anetostest), work at its level counts as
	// committed.
	if d(ctx).Dialect().Name() != "sqlite" { // in-memory SQLite: one connection, which the test's transaction would hold
		tx, err := d(ctx).SQL().BeginTx(ctx, nil)
		check(t, err)
		defer func() { _ = tx.Rollback() }()
		tctx, err := db.WithTestTx(ctx, tx)
		check(t, err)
		check(t, events.Emit(tctx, stBusEvent{Text: "test, direct"}))
		check(t, b.Wait(ctx))
		if async.Load() != 2 {
			t.Errorf("directly in a test's transaction, async ran %d times, want 2", async.Load())
		}
		check(t, db.Tx(tctx, func(ctx context.Context) error { return events.Emit(ctx, stBusEvent{Text: "test, in Tx"}) }))
		check(t, b.Wait(ctx))
		if async.Load() != 3 {
			t.Errorf("in a Tx in a test's transaction, async ran %d times, want 3", async.Load())
		}
		check(t, tx.Rollback())
	}

	// Handing an event to a closed bus after the commit is logged.
	check(t, db.Tx(ctx, func(ctx context.Context) error {
		if err := events.Emit(ctx, stBusEvent{Text: "closing"}); err != nil {
			return err
		}
		return b.Close(context.Background())
	}))
	if !strings.Contains(logs.String(), "the bus is closed") {
		t.Errorf("logs:\n%s", logs.String())
	}
}

// syncBuffer collects logs.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
