// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	Host     string `env:"DB_HOST" default:"127.0.0.1"` // server host (PostgreSQL, MySQL)
	Port     int    `env:"DB_PORT"`                     // 0: the driver's default port
	Database string `env:"DB_DATABASE"`                 // database name; for SQLite, the file (default database/app.db)
	Username string `env:"DB_USERNAME"`                 // user to connect as
	Password string `env:"DB_PASSWORD"`                 // that user's password

	// Pool settings, as for database/sql's DB.SetMaxOpenConns and friends.
	MaxOpenConns    int           `env:"DB_MAX_OPEN_CONNS" default:"25"`     // connections open at most
	MaxIdleConns    int           `env:"DB_MAX_IDLE_CONNS" default:"25"`     // idle connections kept
	ConnMaxLifetime time.Duration `env:"DB_CONN_MAX_LIFETIME" default:"30m"` // a connection is closed after this long
	ConnMaxIdleTime time.Duration `env:"DB_CONN_MAX_IDLE_TIME" default:"5m"` // an idle connection is closed after this long

	// LogQueries logs every query with its duration at debug level. Unset
	// means on in development and off elsewhere.
	LogQueries *bool `env:"DB_LOG_QUERIES"`
	// SlowQuery logs queries that take at least this long as warnings.
	// Zero disables it.
	SlowQuery time.Duration `env:"DB_SLOW_QUERY" default:"500ms"`
	// RepeatedQueries logs a warning when a unit of work (a request, a
	// job…) runs the same query this many times or more: an N+1. Unset
	// means 5 in development and testing, off elsewhere; 0 disables it.
	RepeatedQueries *int `env:"DB_REPEATED_QUERIES"`
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
	if c.RepeatedQueries != nil && (*c.RepeatedQueries < 0 || *c.RepeatedQueries == 1) {
		errs = append(errs, errors.New("DB_REPEATED_QUERIES must be 0 (off) or at least 2"))
	}
	return errors.Join(errs...)
}

// String describes the connection without secrets: the password, and
// passwords inside URL, are masked, so a printed or logged Config leaks
// nothing (fmt's %v, %+v and %#v, and slog through [Config.LogValue]).
func (c Config) String() string {
	logQueries := "unset"
	if c.LogQueries != nil {
		logQueries = strconv.FormatBool(*c.LogQueries)
	}
	repeated := "unset"
	if c.RepeatedQueries != nil {
		repeated = strconv.Itoa(*c.RepeatedQueries)
	}
	return fmt.Sprintf("{Connection:%s URL:%s Host:%s Port:%d Database:%s Username:%s Password:%s "+
		"MaxOpenConns:%d MaxIdleConns:%d ConnMaxLifetime:%s ConnMaxIdleTime:%s LogQueries:%s SlowQuery:%s RepeatedQueries:%s}",
		c.Connection, maskDSN(c.URL), c.Host, c.Port, c.Database, c.Username, mask(c.Password),
		c.MaxOpenConns, c.MaxIdleConns, c.ConnMaxLifetime, c.ConnMaxIdleTime, logQueries, c.SlowQuery, repeated)
}

// GoString masks secrets for %#v too.
func (c Config) GoString() string { return "db.Config" + c.String() }

// LogValue logs the Config as [Config.String] does, for every slog
// handler (JSON included).
func (c Config) LogValue() slog.Value { return slog.StringValue(c.String()) }

const masked = "xxxxx"

func mask(s string) string {
	if s == "" {
		return ""
	}
	return masked
}

// secretKey reports whether a DSN parameter holds a secret.
func secretKey(k string) bool {
	k = strings.ToLower(k)
	return strings.Contains(k, "pass") || strings.Contains(k, "secret") || strings.Contains(k, "token") || strings.Contains(k, "key")
}

// maskDSN masks the secrets of a connection string: the password of a URL
// ("postgres://u:p@h/db") or of MySQL's form ("u:p@tcp(h)/db"), and
// secret-looking parameters in a query string or in libpq's key=value form
// ("host=h password='p'"). If anything that looks like a password is left,
// it masks the whole string.
func maskDSN(s string) string {
	if s == "" {
		return ""
	}
	var out string
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return masked
		}
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), masked)
		}
		u.RawQuery = maskQuery(u.RawQuery)
		out = u.String()
	case keyValueDSN.MatchString(s):
		out = maskKeyValues(s)
	default: // MySQL's user:pass@proto(addr)/db?params, or a SQLite path?params
		base, query, hasQuery := strings.Cut(s, "?")
		if at := strings.LastIndex(base, "@"); at >= 0 {
			if user, _, ok := strings.Cut(base[:at], ":"); ok {
				base = user + ":" + masked + base[at:]
			}
		}
		out = base
		if hasQuery {
			out += "?" + maskQuery(query)
		}
	}
	// Belt and braces: if "pass" is still there other than in a masked
	// parameter's name, keep nothing.
	if strings.Contains(strings.ToLower(maskedParam.ReplaceAllString(out, "")), "pass") {
		return masked
	}
	return out
}

var (
	keyValueDSN = regexp.MustCompile(`^\s*[A-Za-z_]+\s*=`)                 // libpq's host=h user=u …
	maskedParam = regexp.MustCompile(`(?i)[a-z0-9_]*\s*=\s*xxxxx|:xxxxx@`) // what maskDSN wrote
)

// maskQuery masks the values of secret parameters, keeping the order and
// the rest of the query as written.
func maskQuery(q string) string {
	if q == "" {
		return ""
	}
	parts := strings.Split(q, "&")
	for i, p := range parts {
		k, _, ok := strings.Cut(p, "=")
		if uk, err := url.QueryUnescape(k); err == nil {
			k = uk
		}
		if ok && secretKey(k) {
			parts[i] = strings.SplitN(p, "=", 2)[0] + "=" + masked
		}
	}
	return strings.Join(parts, "&")
}

// maskKeyValues masks secret values in libpq's key=value form, where
// values may be quoted ('my secret') and "=" may have spaces around it.
func maskKeyValues(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		// Copy whitespace.
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
			b.WriteByte(s[i])
			i++
		}
		// Key.
		start := i
		for i < len(s) && s[i] != '=' && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		key := s[start:i]
		b.WriteString(key)
		// Spaces and "=".
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			b.WriteByte(s[i])
			i++
		}
		if i >= len(s) || s[i] != '=' {
			continue
		}
		b.WriteByte('=')
		i++
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			b.WriteByte(s[i])
			i++
		}
		// Value: quoted with backslash escapes, or up to whitespace.
		vstart := i
		if i < len(s) && s[i] == '\'' {
			i++
			for i < len(s) && s[i] != '\'' {
				if s[i] == '\\' {
					i++
				}
				i++
			}
			i++ // closing quote
		} else {
			for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != '\n' {
				i++
			}
		}
		i = min(i, len(s))
		if secretKey(key) {
			b.WriteString(masked)
		} else {
			b.WriteString(s[vstart:i])
		}
	}
	return b.String()
}

// LoadConfig reads a Config from src with every key prefixed by prefix, so
// LoadConfig(src, "ANALYTICS_") reads ANALYTICS_DB_CONNECTION,
// ANALYTICS_DB_HOST and so on. An empty prefix reads the DB_* keys.
func LoadConfig(src config.Source, prefix string) (Config, error) {
	cfg, err := config.Get[Config](prefixed{src, prefix})
	if err != nil && prefix != "" {
		err = withPrefix(err, prefix)
	}
	return cfg, err
}

// withPrefix makes errors name the prefixed keys (ANALYTICS_DB_PORT, not
// DB_PORT).
func withPrefix(err error, prefix string) error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		parts := j.Unwrap()
		out := make([]error, len(parts))
		for i, e := range parts {
			out[i] = withPrefix(e, prefix)
		}
		return errors.Join(out...)
	}
	if fe, ok := err.(*config.FieldError); ok { //nolint:errorlint // Bind joins FieldErrors directly; only those are renamed
		c := *fe
		c.Key = prefix + c.Key
		return &c
	}
	return prefixedError{err, prefix}
}

type prefixedError struct {
	err    error
	prefix string
}

func (e prefixedError) Error() string {
	return strings.ReplaceAll(e.err.Error(), "DB_", e.prefix+"DB_")
}
func (e prefixedError) Unwrap() error { return e.err }

type prefixed struct {
	src    config.Source
	prefix string
}

func (p prefixed) Lookup(key string) (string, bool) { return p.src.Lookup(p.prefix + key) }

// Driver pairs a [Dialect] with a way to open connections. Driver modules
// provide one each: sqlite.Driver(), postgres.Driver(), mysql.Driver().
type Driver struct {
	// Name is the value of DB_CONNECTION that selects this driver.
	Name string
	// Dialect writes the database's SQL.
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

	repeated     int // WithRepeatedQueries
	repMu        sync.RWMutex
	repObservers []func(context.Context, RepeatedQuery)

	search  SearchConfig // WithSearch
	reqMu   sync.Mutex
	reqs    []requirement // Require
	checked bool          // Check passed
	ftMu    sync.Mutex
	ftWords *mysqlWords // what MySQL's full-text indexes skip, read once
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
	if cfg.RepeatedQueries != nil {
		opts = append(opts, WithRepeatedQueries(*cfg.RepeatedQueries))
	}
	return New(sqlDB, drv.Dialect, opts...), nil
}

// New wraps an existing *sql.DB, for example one opened by other code or a
// test helper.
func New(sqlDB *sql.DB, d Dialect, opts ...Option) *DB {
	db := &DB{sql: sqlDB, dialect: d, log: slog.New(slog.DiscardHandler), search: defaultSearch()}
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
// one; then it runs [DB.Check], so the app doesn't start with settings
// or features the database can't serve (SEARCH_*, [DB.Require]).
//
// Queries are logged at debug level in development unless DB_LOG_QUERIES
// says otherwise.
func Connect(ctx context.Context, app *anetos.App, drivers ...Driver) (*DB, error) {
	cfg, err := config.Get[Config](app.Source())
	if err != nil {
		return nil, err
	}
	search, err := config.Get[SearchConfig](app.Source())
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
	repeated := 0
	if env := app.Config().Env; env.IsDevelopment() || env.IsTesting() {
		repeated = 5
	}
	if cfg.RepeatedQueries != nil {
		repeated = *cfg.RepeatedQueries
	}
	opts := []Option{
		WithLogger(app.Logger().With("component", "db")),
		WithQueryLog(app.Config().Env.IsDevelopment()),
		WithRepeatedQueries(repeated),
		WithSearch(search),
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
	if repeated >= 2 {
		app.AroundUnits(d.Track) // requests, jobs, listeners, tasks
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
	return c.d.Check(pingCtx) // search settings, requirements, search indexes
}
