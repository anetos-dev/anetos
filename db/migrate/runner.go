// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"cmp"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
)

// DefaultTable is where applied migrations are recorded.
const DefaultTable = "migrations"

// Runner applies and rolls back the migrations of one or more sets, and
// runs seeders. It is safe for concurrent use: on PostgreSQL and MySQL a
// database lock keeps two processes (say, two instances starting during a
// deploy) from migrating at once, which needs a pool of at least two
// connections.
type Runner struct {
	d       *db.DB
	dialect string
	entries []entry
	seeders []Seeder
	table   string
	env     anetos.Environment
	log     *slog.Logger
	mu      sync.Mutex
}

// Option configures a [Runner].
type Option func(*Runner)

// WithSeeders sets the seeders [Runner.Seed] runs, in order.
func WithSeeders(seeders ...Seeder) Option {
	return func(r *Runner) { r.seeders = append(r.seeders, seeders...) }
}

// WithTable records applied migrations in another table (default
// "migrations").
func WithTable(name string) Option { return func(r *Runner) { r.table = name } }

// WithEnvironment tells the runner which environment it runs in. Fresh
// only works in development and testing; without this option the
// environment is treated as production.
func WithEnvironment(env anetos.Environment) Option { return func(r *Runner) { r.env = env } }

// WithLogger logs each applied and rolled-back migration and each seeder
// at debug level.
func WithLogger(l *slog.Logger) Option { return func(r *Runner) { r.log = l } }

// NewRunner returns a runner for the migrations in sets on d. It returns an
// error if two sets share a name or two seeders share a name.
func NewRunner(d *db.DB, sets []*Set, opts ...Option) (*Runner, error) {
	r := &Runner{d: d, dialect: d.Dialect().Name(), table: DefaultTable, log: slog.New(slog.DiscardHandler)}
	switch r.dialect {
	case "postgres", "mysql", "sqlite":
	default:
		return nil, fmt.Errorf("migrate: dialect %q isn't supported", r.dialect)
	}
	sources := map[string]bool{}
	for _, s := range sets {
		if sources[s.source] {
			return nil, fmt.Errorf("migrate: two sets are named %q", s.source)
		}
		sources[s.source] = true
		r.entries = append(r.entries, s.entries...)
	}
	slices.SortStableFunc(r.entries, func(a, b entry) int {
		return cmp.Or(cmp.Compare(a.id, b.id), cmp.Compare(a.source, b.source))
	})
	for _, o := range opts {
		o(r)
	}
	if err := checkName(r.table); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, s := range r.seeders {
		if s.Name == "" || s.Run == nil || names[s.Name] {
			return nil, fmt.Errorf("migrate: seeder %q is unnamed, has no Run, or is listed twice", s.Name)
		}
		names[s.Name] = true
	}
	return r, nil
}

// ForApp returns a runner for the app's database (from db.Connect) that
// knows the app's environment and logs with its logger.
func ForApp(app *anetos.App, sets []*Set, opts ...Option) (*Runner, error) {
	d, err := anetos.Resolve[*db.DB](app)
	if err != nil {
		return nil, fmt.Errorf("migrate: %w (call db.Connect first)", err)
	}
	opts = append([]Option{
		WithEnvironment(app.Config().Env),
		WithLogger(app.Logger().With("component", "migrate")),
	}, opts...)
	return NewRunner(d, sets, opts...)
}

// Result is one migration applied or rolled back.
type Result struct {
	Source string
	ID     string
	Took   time.Duration
}

// Status is the state of one migration.
type Status struct {
	Source    string
	ID        string
	Applied   bool
	Batch     int       // 0 if pending
	AppliedAt time.Time // zero if pending
	// Missing is set for a migration recorded as applied that no set
	// contains any more; it can't be rolled back.
	Missing bool
}

type record struct {
	PK        int64     `db:"id"`
	Source    string    `db:"source"`
	ID        string    `db:"migration"`
	Batch     int       `db:"batch"`
	AppliedAt time.Time `db:"applied_at"`
}

func (r *Runner) q(name string) string { return quoteName(r.d.Dialect(), name) }

func (r *Runner) ensureTable(ctx context.Context) error {
	var ddl string
	switch r.dialect {
	case "postgres":
		ddl = "CREATE TABLE IF NOT EXISTS %s (id BIGSERIAL PRIMARY KEY, source VARCHAR(100) NOT NULL, migration VARCHAR(255) NOT NULL, batch INTEGER NOT NULL, applied_at TIMESTAMPTZ NOT NULL, UNIQUE (source, migration))"
	case "mysql":
		ddl = "CREATE TABLE IF NOT EXISTS %s (id BIGINT AUTO_INCREMENT PRIMARY KEY, source VARCHAR(100) NOT NULL, migration VARCHAR(255) NOT NULL, batch INT NOT NULL, applied_at DATETIME(6) NOT NULL, UNIQUE (source, migration))"
	default:
		ddl = "CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, source VARCHAR(100) NOT NULL, migration VARCHAR(255) NOT NULL, batch INTEGER NOT NULL, applied_at DATETIME NOT NULL, UNIQUE (source, migration))"
	}
	_, err := db.Exec(ctx, fmt.Sprintf(ddl, r.q(r.table)))
	return err
}

func (r *Runner) records(ctx context.Context) ([]record, error) {
	return db.Raw[record](ctx, "SELECT id, source, migration, batch, applied_at FROM "+r.q(r.table)+" ORDER BY id")
}

// lock serializes migration runs: in this process with a mutex, and across
// processes with a PostgreSQL advisory lock or a MySQL named lock (per
// database), held on a dedicated connection. SQLite needs no more than the
// mutex: it is used by one server.
func (r *Runner) lock(ctx context.Context) (func(), error) {
	r.mu.Lock()
	if r.dialect == "sqlite" {
		return r.mu.Unlock, nil
	}
	if r.d.SQL().Stats().MaxOpenConnections == 1 {
		r.mu.Unlock()
		return nil, errors.New("migrate: the runner needs at least 2 connections (DB_MAX_OPEN_CONNS): one holds the lock that keeps other instances from migrating at the same time")
	}
	conn, err := r.d.SQL().Conn(ctx)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	fail := func(err error) (func(), error) {
		conn.Close()
		r.mu.Unlock()
		return nil, fmt.Errorf("migrate: lock: %w", err)
	}
	var unlockSQL string
	var unlockArg any
	switch r.dialect {
	case "postgres":
		// Advisory locks are per database already.
		key := int64(hash64("anetos-migrate:"+r.table) >> 1)
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
			return fail(err)
		}
		unlockSQL, unlockArg = "SELECT pg_advisory_unlock($1)", key
	case "mysql":
		// Named locks are server-wide: include the database.
		var dbName sql.NullString
		if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&dbName); err != nil {
			return fail(err)
		}
		name := fmt.Sprintf("anetos_migrate_%x", hash64(dbName.String+"."+r.table))
		var got sql.NullInt64
		if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", name, 600).Scan(&got); err != nil {
			return fail(err)
		}
		if got.Int64 != 1 {
			return fail(errors.New("timed out waiting for another migration run to finish"))
		}
		unlockSQL, unlockArg = "SELECT RELEASE_LOCK(?)", name
	}
	return func() {
		if _, err := conn.ExecContext(context.WithoutCancel(ctx), unlockSQL, unlockArg); err != nil {
			// Don't return a connection that may still hold the lock to
			// the pool: mark it bad so it is closed.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
		r.mu.Unlock()
	}, nil
}

func hash64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// prepare puts the runner's database in ctx, takes the lock and creates
// the migrations table.
func (r *Runner) prepare(ctx context.Context) (context.Context, func(), error) {
	ctx = db.WithDB(ctx, r.d)
	unlock, err := r.lock(ctx)
	if err != nil {
		return nil, nil, err
	}
	// Under the lock: concurrent CREATE TABLE IF NOT EXISTS isn't safe on
	// PostgreSQL.
	if err := r.ensureTable(ctx); err != nil {
		unlock()
		return nil, nil, fmt.Errorf("migrate: create %s table: %w", r.table, err)
	}
	return ctx, unlock, nil
}

// Up applies every pending migration, in ID order, as one batch. It stops
// at the first failure and returns the migrations applied before it.
func (r *Runner) Up(ctx context.Context) ([]Result, error) {
	ctx, unlock, err := r.prepare(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return r.up(ctx)
}

func (r *Runner) up(ctx context.Context) ([]Result, error) {
	recs, err := r.records(ctx)
	if err != nil {
		return nil, err
	}
	applied := map[[2]string]bool{}
	batch := 0
	for _, rec := range recs {
		applied[[2]string{rec.Source, rec.ID}] = true
		batch = max(batch, rec.Batch)
	}
	batch++
	var done []Result
	for _, e := range r.entries {
		if applied[[2]string{e.source, e.id}] {
			continue
		}
		start := time.Now()
		if err := r.apply(ctx, e, true, batch); err != nil {
			return done, fmt.Errorf("migrate: %s %s: %w", e.source, e.id, err)
		}
		res := Result{e.source, e.id, time.Since(start)}
		r.log.DebugContext(ctx, "migrated", "source", e.source, "migration", e.id, "took", res.Took)
		done = append(done, res)
	}
	return done, nil
}

// apply runs one migration up or down and updates its record, in a
// transaction where the database supports transactional DDL.
func (r *Runner) apply(ctx context.Context, e entry, up bool, batch int) error {
	work := func(ctx context.Context) error {
		s := &Schema{ctx: ctx, d: r.d, dialect: r.dialect}
		if up {
			if err := e.m.Up(s); err != nil {
				return err
			}
			_, err := db.Exec(ctx, "INSERT INTO "+r.q(r.table)+" (source, migration, batch, applied_at) VALUES (?, ?, ?, ?)",
				e.source, e.id, batch, time.Now().UTC().Truncate(time.Microsecond))
			return err
		}
		if err := e.m.Down(s); err != nil {
			return err
		}
		_, err := db.Exec(ctx, "DELETE FROM "+r.q(r.table)+" WHERE source = ? AND migration = ?", e.source, e.id)
		return err
	}
	switch {
	case e.noTx || r.dialect == "mysql": // MySQL commits after every DDL statement anyway
		return work(ctx)
	case r.dialect == "sqlite":
		return r.sqliteTx(ctx, work)
	}
	return db.Tx(ctx, work)
}

// sqliteTx runs work in a transaction with foreign key enforcement off, so
// a migration can rebuild a table (create, copy, drop, rename) without the
// drop cascading to other tables. SQLite ignores that PRAGMA inside a
// transaction, so it is set on a dedicated connection first; before
// committing, PRAGMA foreign_key_check makes sure the result is
// consistent.
func (r *Runner) sqliteTx(ctx context.Context, work func(context.Context) error) (err error) {
	conn, err := r.d.SQL().Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer func() {
		if _, onErr := conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON"); onErr != nil && err == nil {
			err = onErr
		}
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	txCtx, err := db.WithTx(ctx, tx)
	if err != nil {
		return err
	}
	if err := work(txCtx); err != nil {
		return err
	}
	var violations []string
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	for rows.Next() {
		var table, parent sql.NullString
		var rowid, fkid sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			rows.Close()
			return err
		}
		violations = append(violations, fmt.Sprintf("%s row %d → %s", table.String, rowid.Int64, parent.String))
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(violations) > 0 {
		return fmt.Errorf("foreign keys would be broken: %s", strings.Join(violations, "; "))
	}
	done = true
	return tx.Commit()
}

// Rollback undoes the last batches applied (at least 1), newest first.
func (r *Runner) Rollback(ctx context.Context, batches int) ([]Result, error) {
	ctx, unlock, err := r.prepare(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return r.rollback(ctx, max(batches, 1))
}

// Reset rolls back every applied migration.
func (r *Runner) Reset(ctx context.Context) ([]Result, error) {
	ctx, unlock, err := r.prepare(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return r.rollback(ctx, -1)
}

func (r *Runner) rollback(ctx context.Context, batches int) ([]Result, error) {
	recs, err := r.records(ctx)
	if err != nil {
		return nil, err
	}
	slices.Reverse(recs) // newest first
	var numbers []int
	for _, rec := range recs {
		if !slices.Contains(numbers, rec.Batch) {
			numbers = append(numbers, rec.Batch)
		}
	}
	slices.SortFunc(numbers, func(a, b int) int { return cmp.Compare(b, a) })
	if batches >= 0 && len(numbers) > batches {
		numbers = numbers[:batches]
	}
	slices.SortStableFunc(recs, func(a, b record) int { return cmp.Or(cmp.Compare(b.Batch, a.Batch), cmp.Compare(b.PK, a.PK)) })
	var todo []record
	var targets []entry
	for _, rec := range recs {
		if !slices.Contains(numbers, rec.Batch) {
			continue
		}
		i := slices.IndexFunc(r.entries, func(e entry) bool { return e.source == rec.Source && e.id == rec.ID })
		if i < 0 {
			// Checked before rolling anything back.
			return nil, fmt.Errorf("migrate: %s %s (batch %d) is applied but not registered, so it can't be rolled back", rec.Source, rec.ID, rec.Batch)
		}
		todo, targets = append(todo, rec), append(targets, r.entries[i])
	}
	var done []Result
	for k, rec := range todo {
		start := time.Now()
		if err := r.apply(ctx, targets[k], false, rec.Batch); err != nil {
			return done, fmt.Errorf("migrate: roll back %s %s: %w", rec.Source, rec.ID, err)
		}
		res := Result{rec.Source, rec.ID, time.Since(start)}
		r.log.DebugContext(ctx, "rolled back", "source", rec.Source, "migration", rec.ID, "took", res.Took)
		done = append(done, res)
	}
	return done, nil
}

// ErrNotAllowed is returned by Fresh outside development and testing.
var ErrNotAllowed = errors.New("migrate: fresh drops every table and only runs in development and testing (APP_ENV)")

// Fresh drops every table and view in the database (on PostgreSQL also
// materialized views and enum types; objects owned by extensions stay),
// then applies all migrations. Stored procedures, functions and triggers
// defined outside tables are not dropped. It refuses to run unless the environment is development or
// testing (see [WithEnvironment]).
func (r *Runner) Fresh(ctx context.Context) ([]Result, error) {
	if r.env != anetos.Development && r.env != anetos.Testing {
		return nil, ErrNotAllowed
	}
	ctx = db.WithDB(ctx, r.d)
	unlock, err := r.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := r.dropAll(ctx); err != nil {
		return nil, fmt.Errorf("migrate: drop tables: %w", err)
	}
	if err := r.ensureTable(ctx); err != nil {
		return nil, err
	}
	return r.up(ctx)
}

// dropAll drops every table and view on one connection, with foreign key
// checks off where they would get in the way.
func (r *Runner) dropAll(ctx context.Context) error {
	conn, err := r.d.SQL().Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var list string
	switch r.dialect {
	case "postgres":
		// Tables, views, materialized views and enum types of the current
		// schema, except objects that belong to extensions.
		list = `SELECT c.relname, CASE c.relkind WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized view' ELSE 'table' END
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p', 'v', 'm')
			AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype = 'e')
			UNION ALL
			SELECT t.typname, 'type' FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
			WHERE n.nspname = current_schema() AND t.typtype = 'e'
			AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = t.oid AND d.deptype = 'e')`
	case "mysql":
		list = "SELECT table_name, CASE table_type WHEN 'VIEW' THEN 'view' ELSE 'table' END FROM information_schema.tables WHERE table_schema = DATABASE()"
		if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0"); err != nil {
			return err
		}
		defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "SET FOREIGN_KEY_CHECKS = 1") }()
	default:
		list = "SELECT name, type FROM sqlite_master WHERE type IN ('table', 'view') AND substr(name, 1, 7) <> 'sqlite_'"
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return err
		}
		defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON") }()
	}
	rows, err := conn.QueryContext(ctx, list)
	if err != nil {
		return err
	}
	type object struct{ name, kind string }
	var objects []object
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.name, &o.kind); err != nil {
			rows.Close()
			return err
		}
		objects = append(objects, o)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	order := map[string]int{"view": 0, "materialized view": 1, "table": 2, "type": 3}
	slices.SortStableFunc(objects, func(a, b object) int { return cmp.Compare(order[a.kind], order[b.kind]) })
	for _, o := range objects {
		stmt := "DROP " + strings.ToUpper(o.kind) + " IF EXISTS " + r.q(o.name)
		if r.dialect == "postgres" {
			stmt += " CASCADE"
		}
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// Status lists every migration: applied ones in the order they ran, then
// pending ones, with applied migrations that no set contains marked
// Missing.
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	ctx = db.WithDB(ctx, r.d)
	s := &Schema{ctx: ctx, d: r.d, dialect: r.dialect}
	exists, err := s.HasTable(r.table)
	if err != nil {
		return nil, err
	}
	var recs []record
	if exists {
		if recs, err = r.records(ctx); err != nil {
			return nil, err
		}
	}
	var out []Status
	seen := map[[2]string]bool{}
	for _, rec := range recs {
		key := [2]string{rec.Source, rec.ID}
		seen[key] = true
		known := slices.ContainsFunc(r.entries, func(e entry) bool { return e.source == rec.Source && e.id == rec.ID })
		out = append(out, Status{Source: rec.Source, ID: rec.ID, Applied: true, Batch: rec.Batch, AppliedAt: rec.AppliedAt, Missing: !known})
	}
	for _, e := range r.entries {
		if !seen[[2]string{e.source, e.id}] {
			out = append(out, Status{Source: e.source, ID: e.id})
		}
	}
	return out, nil
}

// Seed runs the named seeders, or all of them in order, each in its own
// transaction.
func (r *Runner) Seed(ctx context.Context, names ...string) error {
	ctx = db.WithDB(ctx, r.d)
	run := r.seeders
	if len(names) > 0 {
		run = nil
		for _, n := range names {
			i := slices.IndexFunc(r.seeders, func(s Seeder) bool { return s.Name == n })
			if i < 0 {
				return fmt.Errorf("migrate: no seeder named %q", n)
			}
			run = append(run, r.seeders[i])
		}
	}
	for _, s := range run {
		start := time.Now()
		if err := db.Tx(ctx, s.Run); err != nil {
			return fmt.Errorf("migrate: seeder %s: %w", s.Name, err)
		}
		r.log.DebugContext(ctx, "seeded", "seeder", s.Name, "took", time.Since(start))
	}
	return nil
}
