// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/internal/dbutil"
)

// DatabaseStore keeps items in a table of the app's database, so every
// instance of the app shares them (and their locks) without another
// server. Create the table with [Migrations]. Expiry follows the
// database server's clock, so instances with drifting clocks agree on it.
//
// With PostgreSQL and MySQL it uses its own connections from the pool,
// never a transaction in the context: what it writes inside a
// transaction stays if the transaction rolls back, and a lock taken there
// is visible to others at once. Each cache call made inside a transaction
// therefore needs a second connection: keep DB_MAX_OPEN_CONNS above the
// number of transactions that run at once. SQLite has one writer at a
// time, so there the store joins the context's transaction instead (a
// separate write would wait for it): its writes roll back with it.
type DatabaseStore struct {
	d     *db.DB
	table string

	mu        sync.Mutex
	lastSweep time.Time
}

// NewDatabaseStore returns a store in table (default "cache") of d.
func NewDatabaseStore(d *db.DB, table string) *DatabaseStore {
	if table == "" {
		table = "cache"
	}
	return &DatabaseStore{d: d, table: table}
}

// DatabaseDriver is the database store's driver (CACHE_STORE=database),
// in the table CACHE_TABLE. It uses the app's database: call db.Connect
// before cache.New.
func DatabaseDriver() Driver {
	return Driver{Name: "database", Open: func(app *anetos.App, cfg Config) (Store, error) {
		d, err := anetos.Resolve[*db.DB](app)
		if err != nil {
			return nil, errors.New("the database store needs the app's database: call db.Connect before cache.New")
		}
		return NewDatabaseStore(d, cfg.Table), nil
	}}
}

// Migrations returns the migration creating the database store's table
// (default "cache") with [CreateTable]. Pass it to migrate.New with the
// app's own:
//
//	migrate.New(app, []*migrate.Set{migrations.All, cache.Migrations("")})
func Migrations(table string) *migrate.Set {
	if table == "" {
		table = "cache"
	}
	s := migrate.NewSet("cache")
	s.AddFunc("2026_10_01_000000_create_"+table+"_table",
		func(s *migrate.Schema) error { return CreateTable(s, table) },
		func(s *migrate.Schema) error { return s.Drop(table) })
	return s
}

// CreateTable creates a table for a [DatabaseStore]: key (the primary
// key, up to 255 characters, compared exactly), value and expires_at
// (Unix milliseconds, NULL for never). Other packages that keep items in
// a DatabaseStore (session) use it in their migrations.
func CreateTable(s *migrate.Schema, table string) error {
	if s.Dialect() == "mysql" {
		// Keys compare byte for byte, as in the other stores: text
		// collations ignore case, accents or trailing spaces.
		q := func(n string) string { return "`" + strings.ReplaceAll(n, "`", "``") + "`" }
		return s.Exec("CREATE TABLE " + q(table) + " (`key` VARBINARY(255) NOT NULL PRIMARY KEY, " +
			"`value` LONGBLOB NOT NULL, `expires_at` BIGINT NULL, INDEX " + q(table+"_expires_at_index") + " (`expires_at`))")
	}
	return s.Create(table, func(t *migrate.Table) {
		t.String("key", 255)
		t.Binary("value")
		t.BigInteger("expires_at").Nullable()
		t.Primary("key")
		t.Index("expires_at")
	})
}

// q quotes a name.
func (s *DatabaseStore) q(name string) string { return s.d.Dialect().QuoteIdent(name) }

// conn returns the context the store's queries run with: its database,
// and, except with SQLite, no transaction.
func (s *DatabaseStore) conn(ctx context.Context) context.Context {
	ctx = db.Untracked(db.WithDB(ctx, s.d)) // a cache read per key isn't an N+1
	if s.d.Dialect().Name() == "sqlite" {
		// SQLite has one writer at a time: a write on another connection
		// would wait for the transaction, which may be waiting for it.
		return ctx
	}
	return db.WithoutTx(ctx)
}

func (s *DatabaseStore) exec(ctx context.Context, query string, args ...any) (int64, error) {
	var n int64
	err := dbutil.Retry(ctx, func() error {
		res, err := db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// now is the database server's time in Unix milliseconds, in SQL.
func (s *DatabaseStore) now() string { return dbutil.NowMillis(s.d.Dialect().Name()) }

// expiry returns the SQL for the expiry after ttl, and its arguments.
func (s *DatabaseStore) expiry(ttl time.Duration) (string, []any) {
	if ttl <= 0 {
		return "NULL", nil
	}
	return "(" + s.now() + " + ?)", []any{max(ttl.Milliseconds(), 1)}
}

// live is the condition for an item that hasn't expired.
func (s *DatabaseStore) live() string {
	return "(" + s.q("expires_at") + " IS NULL OR " + s.q("expires_at") + " > " + s.now() + ")"
}

// Get implements [Store].
func (s *DatabaseStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := db.RawFirst[[]byte](s.conn(ctx), "SELECT "+s.q("value")+" FROM "+s.q(s.table)+
		" WHERE "+s.q("key")+" = ? AND "+s.live(), key)
	if errors.Is(err, db.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if v == nil {
		v = []byte{}
	}
	return v, true, nil
}

// Set implements [Store].
func (s *DatabaseStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	ctx = s.conn(ctx)
	exp, args := s.expiry(ttl)
	cols := []string{s.q("value"), s.q("expires_at")}
	_, err := s.exec(ctx, "INSERT INTO "+s.q(s.table)+" ("+s.q("key")+", "+s.q("value")+", "+s.q("expires_at")+") VALUES (?, ?, "+exp+") "+
		s.d.Dialect().Upsert([]string{s.q("key")}, cols), append([]any{key, nonNil(value)}, args...)...)
	if err == nil {
		s.sweep(ctx)
	}
	return err
}

func nonNil(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

// Add implements [Store]: an insert that does nothing if the key is
// there; if it was, but expired, the row is deleted and the insert
// retried.
func (s *DatabaseStore) Add(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	ctx = s.conn(ctx)
	exp, args := s.expiry(ttl)
	insert := "INSERT INTO "
	var ignore string
	if s.d.Dialect().Name() == "mysql" {
		insert = "INSERT IGNORE INTO "
	} else {
		ignore = " ON CONFLICT (" + s.q("key") + ") DO NOTHING"
	}
	insert += s.q(s.table) + " (" + s.q("key") + ", " + s.q("value") + ", " + s.q("expires_at") + ") VALUES (?, ?, " + exp + ")" + ignore
	args = append([]any{key, nonNil(value)}, args...)
	for range 3 {
		n, err := s.exec(ctx, insert, args...)
		if err != nil {
			return false, err
		}
		if n == 1 {
			s.sweep(ctx)
			return true, nil
		}
		// The key is there: if it expired, remove it and try again.
		n, err = s.exec(ctx, "DELETE FROM "+s.q(s.table)+" WHERE "+s.q("key")+" = ? AND "+s.q("expires_at")+" <= "+s.now(), key)
		if err != nil || n == 0 {
			return false, err
		}
	}
	return false, nil // each time, someone else replaced the expired key first
}

// Replace implements [Store].
func (s *DatabaseStore) Replace(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	ctx = s.conn(ctx)
	exp, args := s.expiry(ttl)
	n, err := s.exec(ctx, "UPDATE "+s.q(s.table)+" SET "+s.q("value")+" = ?, "+s.q("expires_at")+" = "+exp+" WHERE "+s.q("key")+" = ? AND "+s.live(),
		append(append([]any{nonNil(value)}, args...), key)...)
	return n == 1, err
}

// Delete implements [Store].
func (s *DatabaseStore) Delete(ctx context.Context, key string) error {
	_, err := s.exec(s.conn(ctx), "DELETE FROM "+s.q(s.table)+" WHERE "+s.q("key")+" = ?", key)
	return err
}

// Increment implements [Store]: it locks the row in a transaction (or
// adds it when missing), so concurrent increments wait for each other.
func (s *DatabaseStore) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	ctx = s.conn(ctx)
	for range 100 {
		var n int64
		found := false
		err := dbutil.Retry(ctx, func() error {
			return db.Tx(ctx, func(ctx context.Context) error {
				found = false
				old, err := db.RawFirst[[]byte](ctx, "SELECT "+s.q("value")+" FROM "+s.q(s.table)+
					" WHERE "+s.q("key")+" = ? AND "+s.live()+" "+s.d.Dialect().LockClause(false), key)
				if errors.Is(err, db.ErrNotFound) {
					return nil
				}
				if err != nil {
					return err
				}
				found = true
				if n, err = addInt(key, old, delta); err != nil {
					return err
				}
				_, err = db.Exec(ctx, "UPDATE "+s.q(s.table)+" SET "+s.q("value")+" = ? WHERE "+s.q("key")+" = ?",
					strconv.AppendInt(nil, n, 10), key)
				return err
			})
		})
		if err != nil {
			return 0, err
		}
		if found {
			s.sweep(ctx)
			return n, nil
		}
		added, err := s.Add(ctx, key, strconv.AppendInt(nil, delta, 10), ttl)
		if err != nil {
			return 0, err
		}
		if added {
			return delta, nil
		}
		// Someone created it first: increment theirs.
	}
	return 0, fmt.Errorf("cache: increment %s: too much contention", key)
}

// DeleteIf implements [Store].
func (s *DatabaseStore) DeleteIf(ctx context.Context, key string, value []byte) (bool, error) {
	n, err := s.exec(s.conn(ctx), "DELETE FROM "+s.q(s.table)+" WHERE "+s.q("key")+" = ? AND "+s.q("value")+" = ? AND "+s.live(),
		key, nonNil(value))
	return n == 1, err
}

// ExpireIf implements [Store].
func (s *DatabaseStore) ExpireIf(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, fmt.Errorf("cache: ExpireIf needs a positive ttl, got %s", ttl)
	}
	exp, args := s.expiry(ttl)
	n, err := s.exec(s.conn(ctx), "UPDATE "+s.q(s.table)+" SET "+s.q("expires_at")+" = "+exp+" WHERE "+s.q("key")+" = ? AND "+s.q("value")+" = ? AND "+s.live(),
		append(args, key, nonNil(value))...)
	return n == 1, err
}

// Flush implements [Store].
func (s *DatabaseStore) Flush(ctx context.Context, prefix string) error {
	ctx = s.conn(ctx)
	if prefix == "" {
		_, err := s.exec(ctx, "DELETE FROM "+s.q(s.table))
		return err
	}
	n := len([]rune(prefix)) // characters
	if s.d.Dialect().Name() == "mysql" {
		n = len(prefix) // VARBINARY: bytes
	}
	_, err := s.exec(ctx, "DELETE FROM "+s.q(s.table)+" WHERE SUBSTR("+s.q("key")+", 1, ?) = ?", n, prefix)
	return err
}

// Close implements [Store]; the database belongs to the app, so it does
// nothing.
func (s *DatabaseStore) Close() error { return nil }

// sweep deletes expired items every few minutes, after a write.
func (s *DatabaseStore) sweep(ctx context.Context) {
	s.mu.Lock()
	now := time.Now()
	due := now.Sub(s.lastSweep) >= 5*time.Minute
	if due {
		s.lastSweep = now
	}
	s.mu.Unlock()
	if due {
		_, _ = s.exec(ctx, "DELETE FROM "+s.q(s.table)+" WHERE "+s.q("expires_at")+" <= "+s.now())
	}
}
