// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/cache/cachetest"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

func init() {
	extra = append(extra, test{"CacheStore", testCacheStore}, test{"WithoutTx", testWithoutTx})
}

// testCacheStore runs the cache store conformance suite on the database
// store, in a table created by cache.Migrations.
func testCacheStore(t *testing.T, ctx context.Context) {
	r, err := migrate.NewRunner(d(ctx), []*migrate.Set{cache.Migrations("st_cache")}, migrate.WithTable("st_cache_migrations"))
	check(t, err)
	_, err = r.Up(ctx)
	check(t, err)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS st_cache")
		_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS st_cache_migrations")
	})
	store := cache.NewDatabaseStore(d(ctx), "st_cache")

	cachetest.Run(t, func(*testing.T) cache.Store { return store })

	// With PostgreSQL and MySQL the store doesn't join the context's
	// transaction: an item written in a transaction that rolls back
	// stays. With SQLite (one writer at a time) it joins it.
	c := cache.New(store, "tx:")
	errRollback := errors.New("rollback")
	err = db.Tx(ctx, func(ctx context.Context) error {
		if err := cache.Set(cache.WithCache(ctx, c), "k", 1, time.Minute); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("Tx = %v", err)
	}
	_, ok, err := store.Get(ctx, "tx:k")
	check(t, err)
	if sqlite := d(ctx).Dialect().Name() == "sqlite"; ok == sqlite {
		t.Errorf("after the rollback, the item is there: %v", ok)
	}
	check(t, store.Flush(ctx, "tx:"))

	// The migration rolls back.
	_, err = r.Reset(ctx)
	check(t, err)
	if _, _, err := store.Get(ctx, "k"); err == nil {
		t.Error("the table is still there after a reset")
	}
}

// testWithoutTx checks that db.WithoutTx leaves the transaction: its
// writes stay after a rollback.
func testWithoutTx(t *testing.T, ctx context.Context) {
	if d(ctx).Dialect().Name() == "sqlite" {
		t.Skip("SQLite: a write on another connection waits for the transaction")
	}
	errRollback := errors.New("rollback")
	err := db.Tx(ctx, func(ctx context.Context) error {
		check(t, db.Create(ctx, &stNote{Text: "in"}))
		if !db.InTx(ctx) || db.InTx(db.WithoutTx(ctx)) {
			t.Error("InTx doesn't follow WithoutTx")
		}
		check(t, db.Create(db.WithoutTx(ctx), &stNote{Text: "out"}))
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("Tx = %v", err)
	}
	notes, err := db.Query[stNote](ctx).Get()
	check(t, err)
	if len(notes) != 1 || notes[0].Text != "out" {
		t.Errorf("after the rollback: %+v", notes)
	}
	if db.WithoutTx(ctx) != ctx {
		t.Error("WithoutTx changed a context with no transaction")
	}
}
