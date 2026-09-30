// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

// Config is a database connection's configuration. [Connect] reads it from
// the DB_* keys; [LoadConfig] reads it with another prefix for extra
// connections.
type Config struct {
	// Connection selects the driver: sqlite, postgres or mysql.
	Connection string `env:"DB_CONNECTION" default:"sqlite"`
	// URL is a complete connection string in the driver's format. When set,
	// Host, Port, Database, Username and Password are ignored.
	URL      string `env:"DB_URL"`
	Host     string `env:"DB_HOST" default:"127.0.0.1"`
	Port     int    `env:"DB_PORT"` // 0: the driver's default port
	Database string `env:"DB_DATABASE"`
	Username string `env:"DB_USERNAME"`
	Password string `env:"DB_PASSWORD"`

	MaxOpenConns    int           `env:"DB_MAX_OPEN_CONNS" default:"25"`
	MaxIdleConns    int           `env:"DB_MAX_IDLE_CONNS" default:"25"`
	ConnMaxLifetime time.Duration `env:"DB_CONN_MAX_LIFETIME" default:"30m"`
	ConnMaxIdleTime time.Duration `env:"DB_CONN_MAX_IDLE_TIME" default:"5m"`

	// LogQueries logs every query with its duration at debug level. Unset
	// means on in development and off elsewhere.
	LogQueries *bool `env:"DB_LOG_QUERIES"`
	// SlowQuery logs queries that take at least this long as warnings.
	// Zero disables it.
	SlowQuery time.Duration `env:"DB_SLOW_QUERY" default:"500ms"`
}

// Validate implements config.Validator.
func (c Config) Validate() error {
	var errs []error
	if c.MaxOpenConns < 0 || c.MaxIdleConns < 0 {
		errs = append(errs, errors.New("DB_MAX_OPEN_CONNS and DB_MAX_IDLE_CONNS can't be negative"))
	}
	if c.Port < 0 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("DB_PORT %d is out of range", c.Port))
	}
	if c.SlowQuery < 0 {
		errs = append(errs, errors.New("DB_SLOW_QUERY can't be negative"))
	}
	return errors.Join(errs...)
}

// LoadConfig reads a Config from src with every key prefixed by prefix, so
// LoadConfig(src, "ANALYTICS_") reads ANALYTICS_DB_CONNECTION,
// ANALYTICS_DB_HOST and so on. An empty prefix reads the DB_* keys.
func LoadConfig(src config.Source, prefix string) (Config, error) {
	return config.Get[Config](prefixed{src, prefix})
}

type prefixed struct {
	src    config.Source
	prefix string
}

func (p prefixed) Lookup(key string) (string, bool) { return p.src.Lookup(p.prefix + key) }

// Driver pairs a [Dialect] with a way to open connections. Driver modules
// provide one each: sqlite.Driver(), postgres.Driver(), mysql.Driver().
type Driver struct {
	// Name is the value of DB_CONNECTION that selects this driver.
	Name    string
	Dialect Dialect
	// Tune, if set, adjusts cfg before the pool is opened and configured,
	// for settings a database needs (an in-memory SQLite database must use
	// a single connection, for example).
	Tune func(cfg *Config)
	// Open opens a connection pool for cfg. It should not connect yet.
	Open func(cfg Config) (*sql.DB, error)
}

// DB is a database connection pool with its dialect. It is safe for
// concurrent use; create one per database and share it.
//
// Queries find their DB in the context: [Connect] adds it to every context
// the app creates, and [WithDB] adds it to others.
type DB struct {
	sql     *sql.DB
	dialect Dialect
	log     *slog.Logger
	logAll  bool
	slow    time.Duration
}

// Option configures a [DB] created with [Open] or [New].
type Option func(*DB)

// WithLogger sets the logger for query logs. The default discards them.
func WithLogger(l *slog.Logger) Option { return func(d *DB) { d.log = l } }

// WithQueryLog turns logging of every query on or off.
func WithQueryLog(on bool) Option { return func(d *DB) { d.logAll = on } }

// WithSlowQuery sets the duration from which queries are logged as slow;
// zero disables it.
func WithSlowQuery(threshold time.Duration) Option { return func(d *DB) { d.slow = threshold } }

// Open opens a connection pool with drv, configured by cfg. Zero pool
// settings keep database/sql's defaults. It doesn't connect; call
// [DB.Ping] to check the connection.
func Open(drv Driver, cfg Config, opts ...Option) (*DB, error) {
	if drv.Tune != nil {
		drv.Tune(&cfg)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}
	sqlDB, err := drv.Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", drv.Name, err)
	}
	// Zero keeps database/sql's default, so a Config literal with only a
	// few fields set still pools connections.
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	}
	opts = append([]Option{WithSlowQuery(cfg.SlowQuery)}, opts...)
	if cfg.LogQueries != nil {
		opts = append(opts, WithQueryLog(*cfg.LogQueries))
	}
	return New(sqlDB, drv.Dialect, opts...), nil
}

// New wraps an existing *sql.DB, for example one opened by other code or a
// test helper.
func New(sqlDB *sql.DB, d Dialect, opts ...Option) *DB {
	db := &DB{sql: sqlDB, dialect: d, log: slog.New(slog.DiscardHandler)}
	for _, o := range opts {
		o(db)
	}
	return db
}

// SQL returns the underlying *sql.DB.
func (d *DB) SQL() *sql.DB { return d.sql }

// Dialect returns the database's dialect.
func (d *DB) Dialect() Dialect { return d.dialect }

// Ping checks that the database is reachable.
func (d *DB) Ping(ctx context.Context) error { return d.sql.PingContext(ctx) }

// Close closes the connection pool.
func (d *DB) Close() error { return d.sql.Close() }

// Connect opens the app's default database from the DB_* configuration and
// makes it available to the app:
//
//	database, err := db.Connect(ctx, app, sqlite.Driver(), postgres.Driver())
//
// DB_CONNECTION picks one of the given drivers, so an app can use SQLite in
// development and PostgreSQL in production with both compiled in. Connect
// opens the connection pool, adds the DB to every context the app creates
// (see anetos.App.AddContextValue), provides it as a *db.DB service, and
// closes it in a shutdown hook. It pings the database when the app boots
// (right away if it already has), so the app and every command that boots
// it fail fast when the database is unreachable, while `help` doesn't need
// one.
//
// Queries are logged at debug level in development unless DB_LOG_QUERIES
// says otherwise.
func Connect(ctx context.Context, app *anetos.App, drivers ...Driver) (*DB, error) {
	cfg, err := config.Get[Config](app.Source())
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(drivers, func(d Driver) bool { return d.Name == cfg.Connection })
	if i < 0 {
		names := make([]string, len(drivers))
		for j, d := range drivers {
			names[j] = d.Name
		}
		return nil, fmt.Errorf("db: DB_CONNECTION is %q, but the drivers passed to Connect are [%s]; import the driver module and pass its Driver()", cfg.Connection, strings.Join(names, ", "))
	}
	opts := []Option{
		WithLogger(app.Logger().With("component", "db")),
		WithQueryLog(app.Config().Env.IsDevelopment()),
	}
	d, err := Open(drivers[i], cfg, opts...)
	if err != nil {
		return nil, err
	}
	check := &connCheck{d: d, name: cfg.Connection}
	if app.Booted() {
		if err := check.Boot(ctx, app); err != nil {
			return nil, errors.Join(err, d.Close())
		}
	} else {
		app.Use(check) // checked when the app boots, so help works without a database
	}
	app.AddContextValue(dbKey{}, d)
	anetos.Provide(app, d)
	app.OnShutdown("db", func(context.Context) error { return d.Close() })
	return d, nil
}

type dbKey struct{}

// WithDB returns ctx with d as the database for queries made with it. Use
// it in tests and for additional connections:
//
//	ctx = db.WithDB(ctx, analytics)
//	rows, err := db.Query[Event](ctx).Get()
//
// A transaction started on another DB in ctx is not used for d.
func WithDB(ctx context.Context, d *DB) context.Context {
	return context.WithValue(ctx, dbKey{}, d)
}

// From returns the database in ctx, or [ErrNoDB].
func From(ctx context.Context) (*DB, error) {
	if d, ok := ctx.Value(dbKey{}).(*DB); ok && d != nil {
		return d, nil
	}
	return nil, ErrNoDB
}

// connCheck pings the database when the app boots, so a missing or
// misconfigured database stops the app (or command) at startup.
type connCheck struct {
	d    *DB
	name string
}

func (c *connCheck) Name() string               { return fmt.Sprintf("db.Connect(%s, %p)", c.name, c.d) }
func (c *connCheck) Register(*anetos.App) error { return nil }
func (c *connCheck) Boot(ctx context.Context, _ *anetos.App) error {
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.d.Ping(pingCtx); err != nil {
		return fmt.Errorf("db: connect to %s: %w", c.name, err)
	}
	return nil
}
