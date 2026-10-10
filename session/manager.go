// SPDX-License-Identifier: Apache-2.0

package session

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/internal/httperr"
)

// Config configures sessions. Environment keys use the SESSION_ prefix;
// see docs/site/reference/configuration.md.
type Config struct {
	// Cookie is the cookie's name. SESSION_COOKIE, default "anetos_session".
	// A Secure cookie with the default Path and no Domain gets the
	// "__Host-" prefix, so no other site (subdomains included) can set it.
	Cookie string `env:"COOKIE" default:"anetos_session"`

	// TTL is how long a session lasts without a request.
	// SESSION_TTL, default 2h.
	TTL time.Duration `env:"TTL" was:"LIFETIME" default:"2h"`

	// MaxTTL is how long a session lasts in all, however active: a
	// copied cookie can't be used forever. Regenerate (at login) restarts
	// it. SESSION_MAX_TTL, default 168h (7 days); 0 disables.
	MaxTTL time.Duration `env:"MAX_TTL" was:"MAX_LIFETIME" default:"168h"`

	// ExpireOnClose makes the cookie a browser-session cookie, dropped
	// when the browser closes (TTL still applies). SESSION_EXPIRE_ON_CLOSE.
	ExpireOnClose bool `env:"EXPIRE_ON_CLOSE"`

	// Domain and Path scope the cookie. SESSION_DOMAIN (default: the
	// request's host only) and SESSION_PATH (default "/").
	Domain string `env:"DOMAIN"`
	Path   string `env:"PATH" default:"/"` // see Domain

	// Secure sends the cookie over HTTPS only. SESSION_SECURE; the default
	// is true, except in the development and testing environments (with
	// New).
	Secure *bool `env:"SECURE"`

	// SameSite is lax, strict or none. SESSION_SAME_SITE, default lax.
	// "none" requires Secure.
	SameSite string `env:"SAME_SITE" default:"lax"`

	// Driver is where sessions are kept: cookie (the whole session in the
	// encrypted cookie), database, or a driver passed to New (redis).
	// SESSION_DRIVER, default cookie.
	Driver string `env:"DRIVER" default:"cookie"`

	// Table is the database driver's table. SESSION_TABLE, default
	// sessions. Pass the same name to [Migrations].
	Table string `env:"TABLE" default:"sessions"`

	// Prefix starts the store keys of server-side sessions.
	// SESSION_PREFIX, default APP_NAME followed by ":session:".
	Prefix string `env:"PREFIX"`
}

// Validate implements config.Validator.
func (c Config) Validate() error {
	var errs []error
	if err := (&http.Cookie{Name: c.Cookie, Value: "x"}).Valid(); err != nil || c.Cookie == "" {
		errs = append(errs, fmt.Errorf("SESSION_COOKIE %q is not a valid cookie name", c.Cookie))
	}
	if c.Domain != "" {
		if err := (&http.Cookie{Name: "n", Value: "x", Domain: c.Domain}).Valid(); err != nil {
			errs = append(errs, fmt.Errorf("SESSION_DOMAIN %q is not a valid cookie domain", c.Domain))
		}
	}
	if c.TTL < time.Minute {
		errs = append(errs, errors.New("SESSION_TTL must be at least 1m"))
	}
	if c.MaxTTL != 0 && c.MaxTTL < c.TTL {
		errs = append(errs, errors.New("SESSION_MAX_TTL must be 0 or at least SESSION_TTL"))
	}
	if !strings.HasPrefix(c.Path, "/") || strings.ContainsFunc(c.Path, func(r rune) bool { return r == ';' || unicode.IsControl(r) }) {
		errs = append(errs, fmt.Errorf("SESSION_PATH %q must start with / (and contain no ;)", c.Path))
	}
	if c.Driver == "" {
		errs = append(errs, errors.New("SESSION_DRIVER must not be empty"))
	}
	if len(c.Prefix) > 100 || !utf8.ValidString(c.Prefix) || strings.ContainsRune(c.Prefix, 0) {
		errs = append(errs, fmt.Errorf("SESSION_PREFIX %q must be text of at most 100 bytes", c.Prefix))
	}
	switch strings.ToLower(c.SameSite) {
	case "lax", "strict":
	case "none":
		if c.Secure != nil && !*c.Secure {
			errs = append(errs, errors.New("SESSION_SAME_SITE=none requires SESSION_SECURE=true"))
		}
	default:
		errs = append(errs, fmt.Errorf("SESSION_SAME_SITE %q is not one of lax, strict, none", c.SameSite))
	}
	return errors.Join(errs...)
}

// LoadConfig reads the SESSION_* settings from src.
func LoadConfig(src config.Source) (Config, error) {
	var wrapper struct {
		Session Config `prefix:"SESSION_"`
	}
	if err := config.Bind(src, &wrapper); err != nil {
		return Config{}, err
	}
	return wrapper.Session, nil
}

// DefaultConfig returns the configuration used when no SESSION_* variables
// are set.
func DefaultConfig() Config {
	cfg, err := LoadConfig(config.Map{})
	if err != nil {
		panic(err) // the defaults are valid
	}
	return cfg
}

// Manager loads and saves sessions. Its Middleware method is the session
// middleware.
type Manager struct {
	cfg      Config
	enc      *encryption.Encrypter
	log      *slog.Logger
	name     string // cookie name, with the __Host- prefix when possible
	context  string // encryption context
	secure   bool
	sameSite http.SameSite
	now      func() time.Time

	store  cache.Store // nil: the cookie holds the session
	prefix string      // of the store's keys

	mu    sync.Mutex                                        // Use's writes
	inner atomic.Pointer[[]func(http.Handler) http.Handler] // Use's middleware
}

// Option configures [NewManager].
type Option func(*Manager)

// WithLogger sets the logger for cookie problems. Default slog.Default().
func WithLogger(l *slog.Logger) Option { return func(m *Manager) { m.log = l } }

// WithStore keeps sessions in store, under keys starting with prefix
// ("blog:session:"), instead of in the cookie: the cookie then holds only
// the encrypted session ID. Any cache store works: the database store,
// Redis, or the memory store (one process only; sessions end when it
// stops).
func WithStore(store cache.Store, prefix string) Option {
	return func(m *Manager) { m.store, m.prefix = store, prefix }
}

// NewManager returns a Manager that stores sessions in cookies encrypted
// by enc, or with [WithStore], in a server-side store.
func NewManager(cfg Config, enc *encryption.Encrypter, opts ...Option) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("session: invalid config: %w", err)
	}
	if enc == nil {
		return nil, errors.New("session: nil Encrypter")
	}
	m := &Manager{cfg: cfg, enc: enc, log: slog.Default(), secure: cfg.Secure == nil || *cfg.Secure, now: time.Now}
	switch strings.ToLower(cfg.SameSite) {
	case "strict":
		m.sameSite = http.SameSiteStrictMode
	case "none":
		m.sameSite = http.SameSiteNoneMode
		m.secure = true
	default:
		m.sameSite = http.SameSiteLaxMode
	}
	switch {
	case strings.HasPrefix(cfg.Cookie, "__Host-") && (!m.secure || cfg.Domain != "" || cfg.Path != "/"):
		return nil, fmt.Errorf("session: a %q cookie must be Secure, without SESSION_DOMAIN, with SESSION_PATH=/ (browsers reject it otherwise)", cfg.Cookie)
	case strings.HasPrefix(cfg.Cookie, "__Secure-") && !m.secure:
		return nil, fmt.Errorf("session: a %q cookie must be Secure (browsers reject it otherwise)", cfg.Cookie)
	}
	m.name = cfg.Cookie
	if m.secure && cfg.Domain == "" && cfg.Path == "/" && !strings.HasPrefix(cfg.Cookie, "__") {
		m.name = "__Host-" + cfg.Cookie
	}
	m.context = "anetos/session\x00" + m.name
	for _, opt := range opts {
		opt(m)
	}
	return m, nil
}

// CookieName returns the name of the session cookie: SESSION_COOKIE, with
// the "__Host-" prefix when the cookie is Secure, has no Domain and has
// the Path "/".
func (m *Manager) CookieName() string { return m.name }

// Driver opens a server-side store for [New]. The database driver is
// built in; driver modules provide others (redis.SessionDriver()).
type Driver struct {
	// Name is the value of SESSION_DRIVER that selects the driver.
	Name string
	// Open returns the store for the app.
	Open func(app *anetos.App, cfg Config) (cache.Store, error)
}

// DatabaseDriver keeps sessions in the app's database
// (SESSION_DRIVER=database), in the table SESSION_TABLE created by
// [Migrations]. Call db.Connect before session.New.
func DatabaseDriver() Driver {
	return Driver{Name: "database", Open: func(app *anetos.App, cfg Config) (cache.Store, error) {
		d, err := anetos.Resolve[*db.DB](app)
		if err != nil {
			return nil, errors.New("the database driver needs the app's database: call db.Connect before session.New")
		}
		return cache.NewDatabaseStore(d, cfg.Table), nil
	}}
}

// Migrations returns the migration creating the database driver's table
// (default "sessions"), for migrate.New:
//
//	migrate.New(app, []*migrate.Set{migrations.All, session.Migrations("")})
func Migrations(table string) *migrate.Set {
	if table == "" {
		table = "sessions"
	}
	s := migrate.NewSet("session")
	s.AddFunc("2026_10_01_000100_create_"+table+"_table",
		func(s *migrate.Schema) error { return cache.CreateTable(s, table) },
		func(s *migrate.Schema) error { return s.Drop(table) })
	return s
}

// New returns a Manager configured from the application's SESSION_*
// settings and APP_KEY, and provides it as a *session.Manager service.
// Cookies are Secure by default, except in the development and testing
// environments. SESSION_DRIVER picks where sessions are kept: cookie and
// database are built in; pass others, such as redis.SessionDriver().
// Server-side sessions use keys starting with SESSION_PREFIX (default
// APP_NAME and ":session:").
func New(app *anetos.App, drivers ...Driver) (*Manager, error) {
	if _, ok := anetos.Lookup[*Manager](app); ok {
		return nil, errors.New("session: New called twice for one app")
	}
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	if cfg.Secure == nil {
		env := app.Config().Env
		secure := !env.IsDevelopment() && !env.IsTesting()
		cfg.Secure = &secure
	}
	enc, err := encryption.New(app)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	opts := []Option{WithLogger(app.Logger().With("component", "session"))}
	if cfg.Driver != "cookie" {
		all := append([]Driver{DatabaseDriver()}, drivers...)
		i := slices.IndexFunc(all, func(d Driver) bool { return d.Name == cfg.Driver })
		if i < 0 {
			names := []string{"cookie"}
			for _, d := range all {
				names = append(names, d.Name)
			}
			return nil, fmt.Errorf("session: SESSION_DRIVER is %q, but the drivers are [%s]; pass its driver to session.New (redis.SessionDriver() from drivers/redis)", cfg.Driver, strings.Join(names, ", "))
		}
		store, err := all[i].Open(app, cfg)
		if err != nil {
			return nil, fmt.Errorf("session: open the %s store: %w", cfg.Driver, err)
		}
		prefix := cfg.Prefix
		if prefix == "" {
			prefix = app.Config().Name + ":session:"
		}
		opts = append(opts, WithStore(store, prefix))
	}
	m, err := NewManager(cfg, enc, opts...)
	if err != nil {
		return nil, err
	}
	m.now = app.Now // sessions expire on the app's clock, which tests can move
	env := app.Config().Env
	app.AddCheck(anetos.Check{Name: "session", Run: func(context.Context) []anetos.Finding { return checks(cfg, env) }})
	anetos.Provide(app, m) // for anetostest, and code that needs it
	return m, nil
}

// ForApp is [New].
//
// Deprecated: Use New; ForApp is removed in v0.6.
//
//go:fix inline
func ForApp(app *anetos.App, drivers ...Driver) (*Manager, error) {
	return New(app, drivers...)
}

// checks are the doctor's checks of the SESSION_* settings.
func checks(cfg Config, env anetos.Environment) []anetos.Finding {
	var out []anetos.Finding
	if env.Deployed() && cfg.Secure != nil && !*cfg.Secure {
		out = append(out, anetos.Finding{Severity: anetos.Problem, Message: fmt.Sprintf("SESSION_SECURE=false in %s: browsers send the session cookie over plain HTTP too, where it can be stolen; remove the setting and serve the app over HTTPS", env)})
	}
	if strings.EqualFold(cfg.SameSite, "none") {
		out = append(out, anetos.Finding{Severity: anetos.Warning, Message: "SESSION_SAME_SITE=none: other sites' pages send the session cookie too, and only the CSRF token stops their forms; use lax unless another site must embed the app"})
	}
	if cfg.Domain != "" {
		out = append(out, anetos.Finding{Severity: anetos.Note, Message: fmt.Sprintf("SESSION_DOMAIN=%s: every subdomain gets the session cookie, so any of them can read or set it; leave it empty unless the app spans subdomains", cfg.Domain)})
	}
	return out
}

// Config returns the manager's configuration.
func (m *Manager) Config() Config { return m.cfg }

// Store returns the store of server-side sessions (nil for cookie
// sessions) and the prefix of their keys.
func (m *Manager) Store() (cache.Store, string) { return m.store, m.prefix }

// Middleware loads the request's session from its cookie (or starts an
// empty one), makes it available through [From], and saves it in the
// response's Set-Cookie header when it changed. A new session sets no
// cookie until something is stored in it. Responses of requests that have
// a session get "Cache-Control: private" (unless the handler set
// Cache-Control) and "Vary: Cookie". If the same Manager's middleware
// already runs for the request, it does nothing.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	var cached atomic.Pointer[chained] // next, inside Use's middleware
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if owner, _ := r.Context().Value(managerKey{}).(*Manager); owner == m {
			next.ServeHTTP(w, r) // this manager already runs for the request
			return
		}
		st, err := m.load(r)
		if err != nil {
			// Serving the request with an empty session would log the
			// visitor out, and saving it would overwrite theirs.
			m.log.Error("session: the session store failed; answering 503", "error", err)
			w.Header().Set("Retry-After", "5")
			httperr.Write(w, r, &httperr.StatusError{Status: http.StatusServiceUnavailable, Err: fmt.Errorf("session: %w", err)})
			return
		}
		sw := &saver{ResponseWriter: w, m: m, st: st, ctx: context.WithoutCancel(r.Context())}
		ctx := context.WithValue(WithSession(r.Context(), st.s), managerKey{}, m)
		h := next
		if mws := m.inner.Load(); mws != nil {
			c := cached.Load()
			if c == nil || c.mws != mws {
				c = &chained{mws: mws, h: next}
				for _, mw := range slices.Backward(*mws) {
					c.h = mw(c.h)
				}
				cached.Store(c)
			}
			h = c.h
		}
		h.ServeHTTP(sw, r.WithContext(ctx))
		sw.save()
	})
}

// chained is a route's handler inside the middleware of [Manager.Use].
type chained struct {
	mws *[]func(http.Handler) http.Handler // the list it was built with
	h   http.Handler
}

// Use adds middleware that run inside [Manager.Middleware] wherever it
// runs, in order: after the session is loaded, before the route's other
// middleware. It is for what every page with a session needs, whichever
// group it is in, such as the logged-in user (make:auth's setupAuth calls
// sessions.Use(a.Middleware), so the home page's header knows who is
// logged in). It applies to the routes registered before it and after;
// call it while setting up the app, before serving. The middleware may
// run again in a route's group: they must allow that, as auth's and the
// session's do.
func (m *Manager) Use(mw ...func(http.Handler) http.Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []func(http.Handler) http.Handler
	if cur := m.inner.Load(); cur != nil {
		list = append(list, *cur...)
	}
	list = append(list, mw...)
	m.inner.Store(&list)
}

type managerKey struct{}

// payload is what the cookie holds, encrypted.
type payload struct {
	V       int                        `json:"v"`
	ID      string                     `json:"id"`
	Token   string                     `json:"t,omitempty"`
	Created int64                      `json:"c"`
	Last    int64                      `json:"l"`
	Data    map[string]json.RawMessage `json:"d,omitempty"`
	Flash   []string                   `json:"f,omitempty"`
	Errors  []FieldError               `json:"e,omitempty"`
	Old     map[string][]string        `json:"o,omitempty"`
}

// state is a loaded session and what came in, to detect changes.
type state struct {
	s   *Session
	id  string // the ID that came in (server-side sessions)
	had bool   // a valid cookie came in
	// consumes says the payload that came in had flashed values, errors
	// or input, which this request uses up: saving changes it.
	consumes bool
	last     time.Time
	expired  bool // a cookie came in but was invalid or expired
}

func (m *Manager) load(r *http.Request) (*state, error) {
	now := m.now()
	st := &state{}
	if c, err := r.Cookie(m.name); err == nil && c.Value != "" {
		var p *payload
		var why string
		if m.store == nil {
			p, why = m.decode(c.Value, now)
		} else if p, why, err = m.fetch(r.Context(), c.Value, now); err != nil {
			return nil, err
		}
		if p != nil {
			st.s = fromPayload(p)
			st.id = p.ID
			st.had = true
			st.last = time.Unix(p.Last, 0)
			st.consumes = len(p.Flash) > 0 || len(p.Errors) > 0 || len(p.Old) > 0
		} else {
			st.expired = true
			m.log.Debug("session: starting a new session", "reason", why)
		}
	}
	if st.s == nil {
		st.s = NewSession()
		st.s.created = now
	}
	st.s.last = now
	return st, nil
}

// decode opens a cookie value, or says why it can't be used.
func (m *Manager) decode(value string, now time.Time) (*payload, string) {
	plain, err := m.enc.DecryptString(value, m.context)
	if err != nil {
		return nil, "cookie can't be decrypted (tampered, or encrypted with a key that isn't configured)"
	}
	var p payload
	if json.Unmarshal([]byte(plain), &p) != nil || p.V != 1 || p.ID == "" {
		return nil, "cookie holds no session"
	}
	if why := m.expired(&p, now); why != "" {
		return nil, why
	}
	return &p, ""
}

// fetch reads the session whose encrypted ID the cookie holds from the
// store, or says why there is none.
func (m *Manager) fetch(ctx context.Context, value string, now time.Time) (*payload, string, error) {
	id, derr := m.enc.DecryptString(value, m.context)
	if derr != nil {
		// A bad cookie starts a new session; it isn't a failure.
		return nil, "cookie can't be decrypted (tampered, or encrypted with a key that isn't configured)", nil //nolint:nilerr // see above
	}
	b, ok, err := m.store.Get(ctx, m.key(id))
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "session not in the store (expired or ended)", nil
	}
	plain, derr := m.enc.DecryptString(string(b), m.storeContext(m.key(id)))
	var p payload
	if derr != nil || json.Unmarshal([]byte(plain), &p) != nil || p.V != 1 {
		return nil, "stored session is invalid (or encrypted with a key that isn't configured)", nil //nolint:nilerr // a bad entry starts a new session
	}
	p.ID = id
	if why := m.expired(&p, now); why != "" {
		return nil, why, nil
	}
	return &p, "", nil
}

// expired says why the session p can't be used at now, or "".
func (m *Manager) expired(p *payload, now time.Time) string {
	last, created := time.Unix(p.Last, 0), time.Unix(p.Created, 0)
	switch {
	case last.After(now.Add(time.Minute)) || created.After(now.Add(time.Minute)):
		return "session times are in the future"
	case now.Sub(last) > m.cfg.TTL:
		return "session idle for longer than SESSION_TTL"
	case m.cfg.MaxTTL > 0 && now.Sub(created) > m.cfg.MaxTTL:
		return "session older than SESSION_MAX_TTL"
	}
	return ""
}

// key is the store key of the session id: a hash, so that reading the
// store doesn't give anyone a usable session.
func (m *Manager) key(id string) string {
	sum := sha256.Sum256([]byte(id))
	return m.prefix + base64.RawURLEncoding.EncodeToString(sum[:])
}

func fromPayload(p *payload) *Session {
	s := &Session{
		id:      p.ID,
		token:   p.Token,
		created: time.Unix(p.Created, 0),
		data:    p.Data,
		errsNow: p.Errors,
		oldNow:  p.Old,
	}
	if s.data == nil {
		s.data = map[string]json.RawMessage{}
	}
	for _, k := range p.Flash {
		if _, ok := s.data[k]; ok {
			s.flashNow = append(s.flashNow, k)
		}
	}
	return s
}

// toPayload returns what to save: values flashed by the previous request
// are dropped unless kept.
func (s *Session) toPayload() *payload {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := &payload{V: 1, ID: s.id, Token: s.token, Created: s.created.Unix(), Last: s.last.Unix(),
		Flash: s.flashNew, Errors: s.errsNew, Old: s.oldNew}
	if len(s.data) > 0 {
		p.Data = maps.Clone(s.data)
		for _, k := range s.flashNow {
			delete(p.Data, k)
		}
	}
	return p
}

// empty reports whether saving p is pointless: nothing but identifiers.
func (p *payload) empty() bool {
	return len(p.Data) == 0 && p.Token == "" && len(p.Flash) == 0 && len(p.Errors) == 0 && len(p.Old) == 0
}

// maxCookie is the most a Set-Cookie value may hold; browsers allow about
// 4096 bytes for name, value and attributes.
const maxCookie = 3900

// saveTimeout bounds a save to the store, which the response waits for.
const saveTimeout = 10 * time.Second

// saver saves the session when the response starts.
type saver struct {
	http.ResponseWriter
	m     *Manager
	st    *state
	ctx   context.Context // for the store
	saved bool
}

func (w *saver) WriteHeader(code int) {
	if code >= 200 || code == http.StatusSwitchingProtocols {
		w.save()
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *saver) Write(b []byte) (int, error) {
	w.save()
	return w.ResponseWriter.Write(b)
}

// ReadFrom keeps the underlying writer's fast path (sendfile) for io.Copy.
func (w *saver) ReadFrom(r io.Reader) (int64, error) {
	w.save()
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(r)
	}
	return io.Copy(writerOnly{w.ResponseWriter}, r)
}

type writerOnly struct{ io.Writer }

// Flush implements http.Flusher.
func (w *saver) Flush() {
	w.save()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// FlushError is what http.ResponseController calls, so a failed flush is
// reported.
func (w *saver) FlushError() error {
	w.save()
	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Hijack implements http.Hijacker, saving the session first.
func (w *saver) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.save()
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *saver) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *saver) save() {
	if w.saved {
		return
	}
	w.saved = true
	ctx, cancel := context.WithTimeout(w.ctx, saveTimeout)
	defer cancel()
	w.m.save(ctx, w.ResponseWriter, w.st)
}

func (m *Manager) save(ctx context.Context, w http.ResponseWriter, st *state) {
	s := st.s
	p := s.toPayload()
	h := w.Header()
	if st.had || st.expired || !p.empty() {
		// The response depends on the session: keep it out of shared caches.
		if h.Get("Cache-Control") == "" {
			h.Set("Cache-Control", "private")
		}
		addVary(h, "Cookie")
	}
	if p.empty() {
		if m.store != nil && st.had {
			m.revoke(ctx, st.id) // emptied or invalidated: ends every copy of the cookie
		}
		if st.had || st.expired {
			m.setCookie(w, "", -1) // nothing left: remove the cookie
		}
		return
	}
	changed := !st.had || st.consumes || s.changed()
	if !changed && s.last.Sub(st.last) < m.refreshAfter() {
		return
	}
	if m.store != nil {
		value, ok, err := m.persist(ctx, st, p)
		switch {
		case err != nil:
			m.log.Error("session: saving the session in the store failed; changes not saved", "error", err)
		case !ok:
			// Removed meanwhile by another request (a logout, or a login
			// that regenerated it): don't bring it back, and leave the
			// cookie that request set alone.
			m.log.Debug("session: the session ended during the request; not saved")
		default:
			m.setCookie(w, value, m.maxAge())
		}
		return
	}
	value, ok := m.encode(p)
	if !ok && len(p.Old) > 0 {
		m.log.Warn("session: form input too large for the session cookie; not kept", "cookie", m.name)
		p.Old = nil
		value, ok = m.encode(p)
	}
	if !ok {
		m.log.Error("session: session too large for its cookie (about 4 KB); changes not saved. Store less, keep the data in the database, or use a server-side SESSION_DRIVER",
			"cookie", m.name, "bytes", len(value))
		return
	}
	m.setCookie(w, value, m.maxAge())
}

// Limits on what a server-side session holds: failed forms' input is
// dropped past maxStoredInput, and larger sessions aren't saved, so no one
// can fill the store through a form.
const (
	maxStoredInput = 64 << 10
	maxStored      = 1 << 20
)

// persist writes p to the store and returns the cookie value for it. An
// existing session (same ID as loaded) is only replaced, so one removed
// meanwhile isn't written back (ok false); a new or regenerated one is
// written, and then the session it replaces is removed.
func (m *Manager) persist(ctx context.Context, st *state, p *payload) (value string, ok bool, err error) {
	q := *p
	q.ID = "" // the key identifies the session; the store doesn't hold the ID
	b, err := json.Marshal(q)
	if err != nil {
		return "", false, err
	}
	if len(b) > maxStoredInput && len(q.Old) > 0 {
		if old, _ := json.Marshal(q.Old); len(old) > maxStoredInput {
			m.log.Warn("session: form input too large to keep in the session; not kept")
			q.Old = nil
			if b, err = json.Marshal(q); err != nil {
				return "", false, err
			}
		}
	}
	if len(b) > maxStored {
		return "", false, fmt.Errorf("session too large (%d bytes, at most %d): store IDs, not records", len(b), maxStored)
	}
	key := m.key(p.ID)
	enc := m.enc.EncryptString(string(b), m.storeContext(key))
	regenerated := st.had && p.ID != st.id
	if st.had && !regenerated {
		if ok, err = m.store.Replace(ctx, key, []byte(enc), m.cfg.TTL); err != nil || !ok {
			return "", false, err
		}
	} else if err = m.store.Set(ctx, key, []byte(enc), m.cfg.TTL); err != nil {
		return "", false, err
	}
	if regenerated {
		m.revoke(ctx, st.id)
	}
	return m.enc.EncryptString(p.ID, m.context), true, nil
}

// revoke removes the session id from the store, trying twice; a failure
// is logged: copies of its cookie keep working until it expires.
func (m *Manager) revoke(ctx context.Context, id string) {
	err := m.store.Delete(ctx, m.key(id))
	if err != nil {
		err = m.store.Delete(ctx, m.key(id))
	}
	if err != nil {
		m.log.Error("session: removing an ended session from the store failed; copies of its cookie work until it expires", "error", err)
	}
}

// storeContext is the encryption context of the session stored under key:
// an entry copied to another key doesn't decrypt.
func (m *Manager) storeContext(key string) string { return m.context + "\x00store\x00" + key }

// maxAge is the cookie's Max-Age.
func (m *Manager) maxAge() int {
	if m.cfg.ExpireOnClose {
		return 0
	}
	return int(m.cfg.TTL / time.Second)
}

// addVary adds value to the Vary header unless it is there.
func addVary(h http.Header, value string) {
	for _, v := range h.Values("Vary") {
		for f := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(f), value) {
				return
			}
		}
	}
	h.Add("Vary", value)
}

// refreshAfter is how stale the cookie's activity time may get before an
// unchanged session is written again to extend it.
func (m *Manager) refreshAfter() time.Duration {
	return max(m.cfg.TTL/10, time.Minute)
}

func (m *Manager) encode(p *payload) (string, bool) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", false // can't happen: values are stored as JSON
	}
	v := m.enc.EncryptString(string(b), m.context)
	return v, len(m.name)+len(v) <= maxCookie
}

func (m *Manager) setCookie(w http.ResponseWriter, value string, maxAge int) {
	w.Header().Add("Set-Cookie", m.cookie(value, maxAge).String())
}

func (m *Manager) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     m.name,
		Value:    value,
		Path:     m.cfg.Path,
		Domain:   m.cfg.Domain,
		MaxAge:   maxAge,
		Secure:   m.secure,
		HttpOnly: true,
		SameSite: m.sameSite,
	}
}

// Load returns the session r carries (an empty one if its cookie is
// missing, invalid or expired), as the session middleware would give it
// to the request's handler. Changes to it are not saved. Tests use it to
// look at the session a response left.
func (m *Manager) Load(r *http.Request) *Session {
	st, err := m.load(r)
	if err != nil {
		m.log.Error("session: the session store failed", "error", err)
		s := NewSession()
		return s
	}
	return st.s
}

// Edit changes the session r carries (starting one if there is none)
// without counting as a request: fn sees the session as a handler would
// (flashed values, [Session.Errors], [Session.Old]), and what was flashed
// for the next request stays for it, as with [Session.Reflash]. It returns
// the session cookie to send with later requests. Tests use it to prepare
// a session, or to get a CSRF token (s.Token()).
func (m *Manager) Edit(r *http.Request, fn func(s *Session)) (*http.Cookie, error) {
	st, err := m.load(r)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	s := st.s
	fn(s)
	s.Reflash()
	p := s.toPayload()
	if m.store != nil {
		value, ok, err := m.persist(r.Context(), st, p)
		if err != nil {
			return nil, fmt.Errorf("session: %w", err)
		}
		if !ok {
			return nil, errors.New("session: the session ended while it was edited")
		}
		return m.cookie(value, m.maxAge()), nil
	}
	value, ok := m.encode(p)
	if !ok {
		return nil, errors.New("session: session too large for its cookie (about 4 KB)")
	}
	return m.cookie(value, m.maxAge()), nil
}
