// SPDX-License-Identifier: Apache-2.0

package queue

import (
	"context"
	"errors"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/internal/dbutil"
)

// DatabaseStore keeps jobs in a table of the app's database (and failed
// jobs in another), so every instance of the app shares them without
// another server. Create the tables with [Migrations]. Delays and leases
// follow the database server's clock.
//
// [Store.Push] runs in the context's transaction, if there is one: the
// job is dispatched if and only if the transaction commits, and workers
// see it only then. The workers' queries use their own connections
// (except with SQLite, which has one writer at a time). Workers reserve
// jobs with SELECT … FOR UPDATE SKIP LOCKED, which needs PostgreSQL,
// MySQL 8.0 or MariaDB 10.6 or later.
type DatabaseStore struct {
	d      *db.DB
	table  string
	failed string
}

// NewDatabaseStore returns a store in the tables table (default "jobs")
// and failedTable (default "failed_jobs") of d.
func NewDatabaseStore(d *db.DB, table, failedTable string) *DatabaseStore {
	if table == "" {
		table = "jobs"
	}
	if failedTable == "" {
		failedTable = "failed_jobs"
	}
	return &DatabaseStore{d: d, table: table, failed: failedTable}
}

// DatabaseDriver is the database store's driver (QUEUE_DRIVER=database),
// in the tables QUEUE_TABLE and QUEUE_FAILED_TABLE. It uses the app's
// database: call db.Connect before queue.New.
func DatabaseDriver() Driver {
	return Driver{Name: "database", Open: func(app *anetos.App, cfg Config) (Store, error) {
		d, err := anetos.Resolve[*db.DB](app)
		if err != nil {
			return nil, errors.New("the database driver needs the app's database: call db.Connect before queue.New")
		}
		return NewDatabaseStore(d, cfg.Table, cfg.FailedTable), nil
	}}
}

// Migrations returns the migration creating the database driver's tables
// (default "jobs" and "failed_jobs"; pass the QUEUE_TABLE and
// QUEUE_FAILED_TABLE values if you set them) with [CreateTables]. Pass it
// to migrate.New with the app's own:
//
//	migrate.New(app, []*migrate.Set{migrations.All, queue.Migrations("", "")})
func Migrations(table, failedTable string) *migrate.Set {
	if table == "" {
		table = "jobs"
	}
	if failedTable == "" {
		failedTable = "failed_jobs"
	}
	s := migrate.NewSet("queue")
	s.AddFunc("2026_10_01_000400_create_"+table+"_tables",
		func(s *migrate.Schema) error { return CreateTables(s, table, failedTable) },
		func(s *migrate.Schema) error { return errors.Join(s.Drop(failedTable), s.Drop(table)) })
	return s
}

// CreateTables creates the tables of a [DatabaseStore]: table for the
// jobs and failedTable for the failed ones. Times are Unix milliseconds.
func CreateTables(s *migrate.Schema, table, failedTable string) error {
	err := s.Create(table, func(t *migrate.Table) {
		t.String("id", 36)
		t.String("queue", 100)
		t.LongText("payload")
		t.Integer("attempts").Default(0)
		t.BigInteger("available_at") // reserved jobs: when the lease ends
		t.String("token", 64).Default("")
		t.BigInteger("created_at")
		t.Primary("id")
		t.Index("queue", "available_at")
	})
	if err != nil {
		return err
	}
	return s.Create(failedTable, func(t *migrate.Table) {
		t.String("id", 36)
		t.String("queue", 100)
		t.LongText("payload")
		t.LongText("error")
		t.Integer("attempts").Default(0)
		t.BigInteger("failed_at")
		t.Primary("id")
		t.Index("failed_at")
	})
}

func (s *DatabaseStore) q(name string) string { return s.d.Dialect().QuoteIdent(name) }

func (s *DatabaseStore) dialect() string { return s.d.Dialect().Name() }

func (s *DatabaseStore) now() string { return dbutil.NowMillis(s.dialect()) }

// conn returns the context the workers' queries run with: the store's
// database, and, except with SQLite, no transaction.
func (s *DatabaseStore) conn(ctx context.Context) context.Context {
	ctx = db.AllowRepeatedQueries(db.WithDB(ctx, s.d))
	if s.dialect() == "sqlite" {
		return ctx // one writer at a time: another connection would wait for the transaction
	}
	return db.WithoutTx(ctx)
}

// exec runs a statement and returns the rows it changed. Outside a
// transaction, it runs it again after a deadlock (inside one, the
// deadlock has ended the transaction: the caller's retry runs it again).
func (s *DatabaseStore) exec(ctx context.Context, query string, args ...any) (int64, error) {
	var n int64
	retry := dbutil.Retry
	if db.InTx(ctx) {
		retry = func(_ context.Context, fn func() error) error { return fn() }
	}
	err := retry(ctx, func() error {
		res, err := db.Exec(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// tx runs fn in a transaction, again after a deadlock.
func (s *DatabaseStore) tx(ctx context.Context, fn func(ctx context.Context) error) error {
	return dbutil.Retry(ctx, func() error { return db.Tx(ctx, fn) })
}

func ms(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return max(d.Milliseconds(), 1)
}

// joinsTx reports whether Push writes in a transaction of ctx: one on
// the store's database.
func (s *DatabaseStore) joinsTx(ctx context.Context) bool {
	return db.InTx(db.WithDB(ctx, s.d))
}

// Push implements [Store]. It joins the context's transaction.
func (s *DatabaseStore) Push(ctx context.Context, m Message, delay time.Duration) error {
	ctx = db.AllowRepeatedQueries(db.WithDB(ctx, s.d)) // dispatching jobs in a loop is fine
	_, err := s.exec(ctx, "INSERT INTO "+s.q(s.table)+" ("+s.q("id")+", "+s.q("queue")+", "+s.q("payload")+", "+s.q("attempts")+", "+
		s.q("available_at")+", "+s.q("token")+", "+s.q("created_at")+") VALUES (?, ?, ?, 0, "+s.now()+" + ?, '', "+s.now()+")",
		m.ID, m.Queue, string(m.Payload), ms(delay))
	return err
}

type reserved struct {
	ID       string `db:"id"`
	Payload  string `db:"payload"`
	Attempts int    `db:"attempts"`
}

// Reserve implements [Store].
func (s *DatabaseStore) Reserve(ctx context.Context, queue string, lease time.Duration) (*Reservation, error) {
	ctx = s.conn(ctx)
	token := newToken()
	avail := s.q("available_at") + " <= " + s.now()
	next := "SELECT " + s.q("id") + " FROM " + s.q(s.table) + " WHERE " + s.q("queue") + " = ? AND " + avail +
		" ORDER BY " + s.q("available_at") + ", " + s.q("id") + " LIMIT 1"
	set := "UPDATE " + s.q(s.table) + " SET " + s.q("attempts") + " = " + s.q("attempts") + " + 1, " +
		s.q("available_at") + " = " + s.now() + " + ?, " + s.q("token") + " = ?"
	returning := " RETURNING " + s.q("id") + ", " + s.q("payload") + ", " + s.q("attempts")
	var row reserved
	var err error
	switch s.dialect() {
	case "postgres":
		// The subquery locks the row it picks, skipping those other
		// workers are reserving.
		err = dbutil.Retry(ctx, func() error {
			row, err = db.RawFirst[reserved](ctx, set+" WHERE "+s.q("id")+" = ("+next+" FOR UPDATE SKIP LOCKED)"+returning,
				ms(lease), token, queue)
			return err
		})
	case "mysql":
		err = s.tx(ctx, func(ctx context.Context) error {
			row, err = db.RawFirst[reserved](ctx, "SELECT "+s.q("id")+", "+s.q("payload")+", "+s.q("attempts")+" FROM "+s.q(s.table)+
				" WHERE "+s.q("queue")+" = ? AND "+avail+" ORDER BY "+s.q("available_at")+", "+s.q("id")+" LIMIT 1 FOR UPDATE SKIP LOCKED", queue)
			if err != nil {
				return err
			}
			row.Attempts++
			_, err = db.Exec(ctx, set+" WHERE "+s.q("id")+" = ?", ms(lease), token, row.ID)
			return err
		})
	default:
		// SQLite has one writer at a time, so the update is atomic. Look
		// first, so idle workers don't take the write lock.
		if _, err = db.RawFirst[string](ctx, next, queue); err == nil {
			row, err = db.RawFirst[reserved](ctx, set+" WHERE "+s.q("id")+" = ("+next+")"+returning, ms(lease), token, queue)
		}
	}
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &Reservation{ID: row.ID, Queue: queue, Payload: []byte(row.Payload), Attempts: row.Attempts, Token: token}, nil
}

// Delete implements [Store].
func (s *DatabaseStore) Delete(ctx context.Context, r *Reservation) error {
	n, err := s.exec(s.conn(ctx), "DELETE FROM "+s.q(s.table)+" WHERE "+s.q("id")+" = ? AND "+s.q("token")+" = ?", r.ID, r.Token)
	if err == nil && n == 0 {
		err = ErrLeaseLost
	}
	return err
}

// Release implements [Store].
func (s *DatabaseStore) Release(ctx context.Context, r *Reservation, delay time.Duration, refund bool) error {
	sub := 0
	if refund {
		sub = 1
	}
	n, err := s.exec(s.conn(ctx), "UPDATE "+s.q(s.table)+" SET "+s.q("available_at")+" = "+s.now()+" + ?, "+s.q("token")+" = '', "+
		s.q("attempts")+" = "+s.q("attempts")+" - ? WHERE "+s.q("id")+" = ? AND "+s.q("token")+" = ?", ms(delay), sub, r.ID, r.Token)
	if err == nil && n == 0 {
		err = ErrLeaseLost
	}
	return err
}

// Fail implements [Store].
func (s *DatabaseStore) Fail(ctx context.Context, r *Reservation, errMsg string) error {
	return s.tx(s.conn(ctx), func(ctx context.Context) error {
		res, err := db.Exec(ctx, "DELETE FROM "+s.q(s.table)+" WHERE "+s.q("id")+" = ? AND "+s.q("token")+" = ?", r.ID, r.Token)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.Join(err, ErrLeaseLost)
		}
		if _, err := db.Exec(ctx, "DELETE FROM "+s.q(s.failed)+" WHERE "+s.q("id")+" = ?", r.ID); err != nil {
			return err
		}
		_, err = db.Exec(ctx, "INSERT INTO "+s.q(s.failed)+" ("+s.q("id")+", "+s.q("queue")+", "+s.q("payload")+", "+s.q("error")+", "+
			s.q("attempts")+", "+s.q("failed_at")+") VALUES (?, ?, ?, ?, ?, "+s.now()+")",
			r.ID, r.Queue, string(r.Payload), errMsg, r.Attempts)
		return err
	})
}

// Size implements [Store].
func (s *DatabaseStore) Size(ctx context.Context, queue string) (int64, error) {
	return db.RawFirst[int64](s.conn(ctx), "SELECT COUNT(*) FROM "+s.q(s.table)+" WHERE "+s.q("queue")+" = ?", queue)
}

// Clear implements [Store].
func (s *DatabaseStore) Clear(ctx context.Context, queue string) (int64, error) {
	return s.exec(s.conn(ctx), "DELETE FROM "+s.q(s.table)+" WHERE "+s.q("queue")+" = ?", queue)
}

type failedRow struct {
	ID       string `db:"id"`
	Queue    string `db:"queue"`
	Payload  string `db:"payload"`
	Error    string `db:"error"`
	Attempts int    `db:"attempts"`
	FailedAt int64  `db:"failed_at"`
}

// Failed implements [Store].
func (s *DatabaseStore) Failed(ctx context.Context, offset, limit int) ([]FailedJob, error) {
	rows, err := db.Raw[failedRow](s.conn(ctx), "SELECT "+s.q("id")+", "+s.q("queue")+", "+s.q("payload")+", "+s.q("error")+", "+
		s.q("attempts")+", "+s.q("failed_at")+" FROM "+s.q(s.failed)+" ORDER BY "+s.q("failed_at")+" DESC, "+s.q("id")+" DESC LIMIT ? OFFSET ?", max(limit, 0), max(offset, 0))
	if err != nil {
		return nil, err
	}
	out := make([]FailedJob, len(rows))
	for i, r := range rows {
		out[i] = FailedJob{ID: r.ID, Queue: r.Queue, Payload: []byte(r.Payload), Error: r.Error, Attempts: r.Attempts, FailedAt: time.UnixMilli(r.FailedAt)}
	}
	return out, nil
}

// CountFailed implements [FailedCounter].
func (s *DatabaseStore) CountFailed(ctx context.Context) (int64, error) {
	type count struct {
		N int64 `db:"n"`
	}
	c, err := db.RawFirst[count](s.conn(ctx), "SELECT COUNT(*) AS n FROM "+s.q(s.failed))
	return c.N, err
}

// FindFailed implements [FailedFinder].
func (s *DatabaseStore) FindFailed(ctx context.Context, id string) (FailedJob, bool, error) {
	r, err := db.RawFirst[failedRow](s.conn(ctx), "SELECT "+s.q("id")+", "+s.q("queue")+", "+s.q("payload")+", "+s.q("error")+", "+
		s.q("attempts")+", "+s.q("failed_at")+" FROM "+s.q(s.failed)+" WHERE "+s.q("id")+" = ?", id)
	if errors.Is(err, db.ErrNotFound) || err == nil && r.ID != id {
		return FailedJob{}, false, nil // MySQL compares without case
	}
	if err != nil {
		return FailedJob{}, false, err
	}
	return FailedJob{ID: r.ID, Queue: r.Queue, Payload: []byte(r.Payload), Error: r.Error, Attempts: r.Attempts, FailedAt: time.UnixMilli(r.FailedAt)}, true, nil
}

// Retry implements [Store].
func (s *DatabaseStore) Retry(ctx context.Context, id string) (bool, error) {
	found := false
	err := s.tx(s.conn(ctx), func(ctx context.Context) error {
		found = false
		f, err := db.RawFirst[failedRow](ctx, "SELECT "+s.q("queue")+", "+s.q("payload")+" FROM "+s.q(s.failed)+" WHERE "+s.q("id")+" = ? "+
			s.d.Dialect().LockClause(false), id)
		if errors.Is(err, db.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := db.Exec(ctx, "DELETE FROM "+s.q(s.failed)+" WHERE "+s.q("id")+" = ?", id); err != nil {
			return err
		}
		// A job with this ID still queued (a retry racing a dispatch with
		// the same ID can't happen: IDs are random) is replaced.
		if _, err := db.Exec(ctx, "DELETE FROM "+s.q(s.table)+" WHERE "+s.q("id")+" = ?", id); err != nil {
			return err
		}
		if err := s.Push(ctx, Message{ID: id, Queue: f.Queue, Payload: []byte(f.Payload)}, 0); err != nil {
			return err
		}
		found = true
		return nil
	})
	return found, err
}

// Forget implements [Store].
func (s *DatabaseStore) Forget(ctx context.Context, id string) (bool, error) {
	n, err := s.exec(s.conn(ctx), "DELETE FROM "+s.q(s.failed)+" WHERE "+s.q("id")+" = ?", id)
	return n > 0, err
}

// Flush implements [Store].
func (s *DatabaseStore) Flush(ctx context.Context) (int64, error) {
	return s.exec(s.conn(ctx), "DELETE FROM "+s.q(s.failed))
}

// Close implements [Store]; the database belongs to the app, so it does
// nothing.
func (s *DatabaseStore) Close() error { return nil }
