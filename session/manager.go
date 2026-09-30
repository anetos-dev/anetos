// SPDX-License-Identifier: Apache-2.0

package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
)

// Config configures sessions. Environment keys use the SESSION_ prefix;
// see docs/site/reference/configuration.md.
type Config struct {
	// Cookie is the cookie's name. SESSION_COOKIE, default "anetos_session".
	// A Secure cookie with the default Path and no Domain gets the
	// "__Host-" prefix, so no other site (subdomains included) can set it.
	Cookie string `env:"COOKIE" default:"anetos_session"`

	// Lifetime is how long a session lasts without a request.
	// SESSION_LIFETIME, default 2h.
	Lifetime time.Duration `env:"LIFETIME" default:"2h"`

	// MaxLifetime is how long a session lasts in all, however active: a
	// copied cookie can't be used forever. Regenerate (at login) restarts
	// it. SESSION_MAX_LIFETIME, default 168h (7 days); 0 disables.
	MaxLifetime time.Duration `env:"MAX_LIFETIME" default:"168h"`

	// ExpireOnClose makes the cookie a browser-session cookie, dropped
	// when the browser closes (Lifetime still applies). SESSION_EXPIRE_ON_CLOSE.
	ExpireOnClose bool `env:"EXPIRE_ON_CLOSE"`

	// Domain and Path scope the cookie. SESSION_DOMAIN (default: the
	// request's host only) and SESSION_PATH (default "/").
	Domain string `env:"DOMAIN"`
	Path   string `env:"PATH" default:"/"` // see Domain

	// Secure sends the cookie over HTTPS only. SESSION_SECURE; the default
	// is true, except in the development and testing environments (with
	// ForApp).
	Secure *bool `env:"SECURE"`

	// SameSite is lax, strict or none. SESSION_SAME_SITE, default lax.
	// "none" requires Secure.
	SameSite string `env:"SAME_SITE" default:"lax"`
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
	if c.Lifetime < time.Minute {
		errs = append(errs, errors.New("SESSION_LIFETIME must be at least 1m"))
	}
	if c.MaxLifetime != 0 && c.MaxLifetime < c.Lifetime {
		errs = append(errs, errors.New("SESSION_MAX_LIFETIME must be 0 or at least SESSION_LIFETIME"))
	}
	if !strings.HasPrefix(c.Path, "/") || strings.ContainsFunc(c.Path, func(r rune) bool { return r == ';' || unicode.IsControl(r) }) {
		errs = append(errs, fmt.Errorf("SESSION_PATH %q must start with / (and contain no ;)", c.Path))
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
}

// Option configures [NewManager].
type Option func(*Manager)

// WithLogger sets the logger for cookie problems. Default slog.Default().
func WithLogger(l *slog.Logger) Option { return func(m *Manager) { m.log = l } }

// NewManager returns a Manager that stores sessions in cookies encrypted
// by enc.
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

// ForApp returns a Manager configured from the application's SESSION_*
// settings and APP_KEY, and provides it as a *session.Manager service.
// Cookies are Secure by default, except in the development and testing
// environments.
func ForApp(app *anetos.App) (*Manager, error) {
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	if cfg.Secure == nil {
		env := app.Config().Env
		secure := !env.IsDevelopment() && !env.IsTesting()
		cfg.Secure = &secure
	}
	enc, err := encryption.ForApp(app)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	m, err := NewManager(cfg, enc, WithLogger(app.Logger()))
	if err != nil {
		return nil, err
	}
	anetos.Provide(app, m) // for anetostest, and code that needs it
	return m, nil
}

// Config returns the manager's configuration.
func (m *Manager) Config() Config { return m.cfg }

// Middleware loads the request's session from its cookie (or starts an
// empty one), makes it available through [From], and saves it in the
// response's Set-Cookie header when it changed. A new session sets no
// cookie until something is stored in it. Responses of requests that have
// a session get "Cache-Control: private" (unless the handler set
// Cache-Control) and "Vary: Cookie". If the same Manager's middleware
// already runs for the request, it does nothing.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if owner, _ := r.Context().Value(managerKey{}).(*Manager); owner == m {
			next.ServeHTTP(w, r) // this manager already runs for the request
			return
		}
		st := m.load(r)
		sw := &saver{ResponseWriter: w, m: m, st: st}
		ctx := context.WithValue(NewContext(r.Context(), st.s), managerKey{}, m)
		next.ServeHTTP(sw, r.WithContext(ctx))
		sw.save()
	})
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
	s       *Session
	had     bool   // a valid cookie came in
	loaded  []byte // its payload without Last, for change detection
	last    time.Time
	expired bool // a cookie came in but was invalid or expired
}

func (m *Manager) load(r *http.Request) *state {
	now := m.now()
	st := &state{}
	if c, err := r.Cookie(m.name); err == nil && c.Value != "" {
		p, why := m.decode(c.Value, now)
		if p != nil {
			st.s = fromPayload(p)
			st.had = true
			st.last = time.Unix(p.Last, 0)
			st.loaded = comparable(p)
		} else {
			st.expired = true
			m.log.Debug("session: starting a new session", "reason", why)
		}
	}
	if st.s == nil {
		st.s = New()
		st.s.created = now
	}
	st.s.last = now
	return st
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
	last, created := time.Unix(p.Last, 0), time.Unix(p.Created, 0)
	switch {
	case last.After(now.Add(time.Minute)) || created.After(now.Add(time.Minute)):
		return nil, "session times are in the future"
	case now.Sub(last) > m.cfg.Lifetime:
		return nil, "session idle for longer than SESSION_LIFETIME"
	case m.cfg.MaxLifetime > 0 && now.Sub(created) > m.cfg.MaxLifetime:
		return nil, "session older than SESSION_MAX_LIFETIME"
	}
	return &p, ""
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

// comparable encodes p without its activity time.
func comparable(p *payload) []byte {
	q := *p
	q.Last = 0
	b, _ := json.Marshal(q)
	return b
}

// empty reports whether saving p is pointless: nothing but identifiers.
func (p *payload) empty() bool {
	return len(p.Data) == 0 && p.Token == "" && len(p.Flash) == 0 && len(p.Errors) == 0 && len(p.Old) == 0
}

// maxCookie is the most a Set-Cookie value may hold; browsers allow about
// 4096 bytes for name, value and attributes.
const maxCookie = 3900

// saver saves the session when the response starts.
type saver struct {
	http.ResponseWriter
	m     *Manager
	st    *state
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
	w.m.save(w.ResponseWriter, w.st)
}

func (m *Manager) save(w http.ResponseWriter, st *state) {
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
		if st.had || st.expired {
			m.setCookie(w, "", -1) // nothing left: remove the cookie
		}
		return
	}
	changed := !st.had || !bytes.Equal(comparable(p), st.loaded)
	if !changed && s.last.Sub(st.last) < m.refreshAfter() {
		return
	}
	value, ok := m.encode(p)
	if !ok && len(p.Old) > 0 {
		m.log.Warn("session: form input too large for the session cookie; not kept", "cookie", m.name)
		p.Old = nil
		value, ok = m.encode(p)
	}
	if !ok {
		m.log.Error("session: session too large for its cookie (about 4 KB); changes not saved. Store less, or store an ID and keep the data in the database",
			"cookie", m.name, "bytes", len(value))
		return
	}
	maxAge := int(m.cfg.Lifetime / time.Second)
	if m.cfg.ExpireOnClose {
		maxAge = 0
	}
	m.setCookie(w, value, maxAge)
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
	return max(m.cfg.Lifetime/10, time.Minute)
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
func (m *Manager) Load(r *http.Request) *Session { return m.load(r).s }

// Edit changes the session r carries (starting one if there is none)
// without counting as a request: fn sees the session as a handler would
// (flashed values, [Session.Errors], [Session.Old]), and what was flashed
// for the next request stays for it, as with [Session.Reflash]. It returns
// the session cookie to send with later requests. Tests use it to prepare
// a session, or to get a CSRF token (s.Token()).
func (m *Manager) Edit(r *http.Request, fn func(s *Session)) (*http.Cookie, error) {
	s := m.load(r).s
	fn(s)
	s.Reflash()
	value, ok := m.encode(s.toPayload())
	if !ok {
		return nil, errors.New("session: session too large for its cookie (about 4 KB)")
	}
	maxAge := int(m.cfg.Lifetime / time.Second)
	if m.cfg.ExpireOnClose {
		maxAge = 0
	}
	return m.cookie(value, maxAge), nil
}
