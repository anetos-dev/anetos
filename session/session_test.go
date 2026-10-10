// SPDX-License-Identifier: Apache-2.0

package session

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
)

// useStore makes newClient's managers keep sessions in a memory store;
// TestStore sets it to run the other tests that way.
var useStore bool

type client struct {
	t      *testing.T
	m      *Manager
	cookie string
	clock  time.Time
	logs   *bytes.Buffer
}

func newClient(t *testing.T, cfg Config) *client {
	t.Helper()
	k, _ := encryption.ParseKey(encryption.GenerateKey())
	enc, _ := encryption.NewEncrypter(k)
	logs := &bytes.Buffer{}
	opts := []Option{WithLogger(slog.New(slog.NewTextHandler(logs, nil)))}
	if useStore {
		opts = append(opts, WithStore(cache.NewMemoryStore(), "t:session:"))
	}
	m, err := NewManager(cfg, enc, opts...)
	if err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, m: m, clock: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), logs: logs}
	m.now = func() time.Time { return c.clock }
	return c
}

// do runs fn inside the middleware and returns the response. The session
// cookie is kept like a browser would.
// response is what the tests look at in a response.
type response struct {
	Header  http.Header
	cookies []*http.Cookie
}

func (r response) Cookies() []*http.Cookie { return r.cookies }

func (c *client) do(req *http.Request, fn func(w http.ResponseWriter, r *http.Request)) response {
	c.t.Helper()
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: c.m.name, Value: c.cookie})
	}
	rec := httptest.NewRecorder()
	c.m.Middleware(http.HandlerFunc(fn)).ServeHTTP(rec, req)
	res := response{Header: rec.Header(), cookies: (&http.Response{Header: rec.Header()}).Cookies()}
	for _, ck := range res.Cookies() {
		if ck.Name == c.m.name {
			if ck.MaxAge < 0 {
				c.cookie = ""
			} else {
				c.cookie = ck.Value
			}
		}
	}
	return res
}

func (c *client) get(fn func(s *Session)) response {
	req := httptest.NewRequest(http.MethodGet, "/page?x=1", nil)
	req.Header.Set("Accept", "text/html")
	return c.do(req, func(w http.ResponseWriter, r *http.Request) {
		fn(From(r.Context()))
		w.WriteHeader(http.StatusOK)
	})
}

func TestNoCookieUntilUsed(t *testing.T) {
	c := newClient(t, DefaultConfig())
	res := c.get(func(s *Session) {
		if s == nil || s.Has("x") {
			t.Fatal("no empty session")
		}
	})
	if len(res.Cookies()) != 0 {
		t.Errorf("cookie set for an untouched session: %v", res.Header["Set-Cookie"])
	}
}

func TestValuesPersist(t *testing.T) {
	c := newClient(t, DefaultConfig())
	var id string
	res := c.get(func(s *Session) {
		s.Put("user_id", int64(42))
		s.Put("name", "Ada")
		id = s.ID()
	})
	ck := res.Cookies()[0]
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != 7200 || ck.Path != "/" {
		t.Errorf("cookie attributes: %+v", ck)
	}
	if res.Header.Get("Vary") != "Cookie" {
		t.Errorf("Vary = %q", res.Header.Get("Vary"))
	}
	c.get(func(s *Session) {
		uid, ok := Value[int64](s, "user_id")
		if !ok || uid != 42 || s.String("name") != "Ada" || s.ID() != id {
			t.Errorf("got %v %v %q %q", uid, ok, s.String("name"), s.ID())
		}
		if _, ok := Value[int64](s, "name"); ok {
			t.Error("decoding a string as int64 succeeded")
		}
		var name string
		if !s.Pull("name", &name) || name != "Ada" || s.Has("name") {
			t.Error("Pull")
		}
	})
	c.get(func(s *Session) {
		if s.Has("name") || !s.Has("user_id") {
			t.Error("Pull didn't persist")
		}
		s.Delete("user_id")
	})
	res = c.get(func(s *Session) {
		if s.Has("user_id") {
			t.Error("Delete didn't persist")
		}
	})
	if c.cookie != "" {
		t.Errorf("empty session keeps its cookie: %v", res.Header["Set-Cookie"])
	}
}

func TestFlash(t *testing.T) {
	c := newClient(t, DefaultConfig())
	c.get(func(s *Session) { s.Flash("status", "Saved.") })
	c.get(func(s *Session) {
		if s.String("status") != "Saved." {
			t.Error("flash not available on the next request")
		}
		s.Flash("other", 1)
	})
	c.get(func(s *Session) {
		if s.Has("status") || !s.Has("other") {
			t.Error("flash survived two requests, or a new one was lost")
		}
		s.Keep("other")
	})
	c.get(func(s *Session) {
		if !s.Has("other") {
			t.Error("Keep")
		}
		s.Reflash()
	})
	c.get(func(s *Session) {
		if !s.Has("other") {
			t.Error("Reflash")
		}
		s.Put("other", 2) // a stored value is no longer a flash
	})
	c.get(func(*Session) {})
	c.get(func(s *Session) {
		if v, _ := Value[int](s, "other"); v != 2 {
			t.Errorf("put over a flash: %v", v)
		}
	})
}

func TestIdleExpiryAndRefresh(t *testing.T) {
	c := newClient(t, DefaultConfig())
	c.get(func(s *Session) { s.Put("k", 1) })
	c.clock = c.clock.Add(5 * time.Minute)
	if res := c.get(func(*Session) {}); len(res.Cookies()) != 0 {
		t.Error("unchanged session rewritten after 5 minutes")
	}
	c.clock = c.clock.Add(20 * time.Minute)
	if res := c.get(func(*Session) {}); len(res.Cookies()) != 1 {
		t.Error("unchanged session not refreshed after 25 minutes")
	}
	c.clock = c.clock.Add(119 * time.Minute)
	c.get(func(s *Session) {
		if !s.Has("k") {
			t.Error("session expired before its idle lifetime")
		}
	})
	c.clock = c.clock.Add(121 * time.Minute)
	res := c.get(func(s *Session) {
		if s.Has("k") {
			t.Error("session outlived its idle lifetime")
		}
	})
	if ck := res.Cookies(); len(ck) != 1 || ck[0].MaxAge >= 0 {
		t.Errorf("expired cookie not removed: %v", res.Header["Set-Cookie"])
	}
}

func TestTamperedCookie(t *testing.T) {
	c := newClient(t, DefaultConfig())
	c.get(func(s *Session) { s.Put("admin", false) })
	c.cookie = c.cookie[:len(c.cookie)-2] + "AA"
	c.get(func(s *Session) {
		if s.Has("admin") {
			t.Error("tampered cookie accepted")
		}
	})
	// A cookie encrypted for another cookie name is rejected.
	other := newClient(t, DefaultConfig())
	other.m.enc = c.m.enc
	other.m.name, other.m.context = "other", "anetos/session\x00other"
	other.get(func(s *Session) { s.Put("admin", true) })
	c.cookie = other.cookie
	c.get(func(s *Session) {
		if s.Has("admin") {
			t.Error("cookie from another name accepted")
		}
	})
}

func TestCSRFToken(t *testing.T) {
	c := newClient(t, DefaultConfig())
	var tok string
	c.get(func(s *Session) {
		tok = s.Token()
		if tok == s.Token() {
			t.Error("tokens aren't masked")
		}
		if !s.VerifyToken(tok) || !s.VerifyToken(s.Token()) {
			t.Error("own token rejected")
		}
	})
	c.get(func(s *Session) {
		for _, bad := range []string{"", "x", tok[:len(tok)-1], strings.Repeat("A", len(tok))} {
			if s.VerifyToken(bad) {
				t.Errorf("accepted %q", bad)
			}
		}
		if !s.VerifyToken(tok) {
			t.Error("token not kept across requests")
		}
		s.RegenerateToken()
		if s.VerifyToken(tok) {
			t.Error("old token accepted after RegenerateToken")
		}
	})
	fresh := NewSession()
	if fresh.VerifyToken(tok) {
		t.Error("session without a token accepted one")
	}
}

func TestRegenerateAndInvalidate(t *testing.T) {
	c := newClient(t, DefaultConfig())
	var id, tok string
	c.get(func(s *Session) {
		s.Put("cart", 3)
		id, tok = s.ID(), s.Token()
		s.Regenerate()
		if s.ID() == id || s.VerifyToken(tok) || !s.Has("cart") {
			t.Error("Regenerate")
		}
		id = s.ID()
	})
	c.get(func(s *Session) {
		if s.ID() != id {
			t.Error("regenerated ID not saved")
		}
		s.Invalidate()
		if s.ID() == id || s.Has("cart") {
			t.Error("Invalidate")
		}
	})
	if c.cookie != "" {
		t.Error("invalidated empty session keeps its cookie")
	}
}

func TestErrorsAndOldInput(t *testing.T) {
	c := newClient(t, DefaultConfig())
	c.get(func(s *Session) {
		s.FlashErrors(FieldError{"title", "The title field is required."}, FieldError{"body", "Too short."})
		s.FlashInput(url.Values{"title": {""}, "body": {"hi"}, "_token": {"x"}, "password": {"p"}, "new_Password": {"p"}, "_method": {"PUT"}, "api_secret": {"s"}, "reset_token": {"t"}})
		if len(s.Errors()) != 0 {
			t.Error("errors visible in the request that flashed them")
		}
	})
	c.get(func(s *Session) {
		errs := s.Errors()
		if len(errs) != 2 || errs[0].Field != "title" || errs[1].Message != "Too short." {
			t.Errorf("errors = %v", errs)
		}
		old := s.OldInput()
		if len(old) != 2 || old.Get("body") != "hi" {
			t.Errorf("old = %v", old)
		}
		if v, ok := s.Old("title"); !ok || v != "" {
			t.Errorf("Old(title) = %q %v", v, ok)
		}
		if _, ok := s.Old("password"); ok {
			t.Error("password kept")
		}
	})
	c.get(func(s *Session) {
		if len(s.Errors()) != 0 || len(s.OldInput()) != 0 {
			t.Error("errors or input lasted two requests")
		}
	})
}

func TestCookieSize(t *testing.T) {
	c := newClient(t, DefaultConfig())
	c.get(func(s *Session) {
		s.Put("k", "v")
		s.FlashErrors(FieldError{"body", "Too long."})
		s.FlashInput(url.Values{"body": {strings.Repeat("x", 5000)}})
	})
	c.get(func(s *Session) {
		if len(s.Errors()) != 1 || len(s.OldInput()) != 0 {
			t.Error("oversized input should be dropped, errors kept")
		}
	})
	if !strings.Contains(c.logs.String(), "form input too large") {
		t.Errorf("no warning: %s", c.logs)
	}
	before := c.cookie
	c.get(func(s *Session) { s.Put("big", strings.Repeat("y", 5000)) })
	if c.cookie != before || !strings.Contains(c.logs.String(), "session too large") {
		t.Errorf("oversized session saved, or not logged: %s", c.logs)
	}
}

func TestImplicitResponseAndStreaming(t *testing.T) {
	c := newClient(t, DefaultConfig())
	// The handler writes nothing: the cookie still goes out.
	res := c.do(httptest.NewRequest(http.MethodGet, "/", nil), func(w http.ResponseWriter, r *http.Request) {
		From(r.Context()).Put("a", 1)
	})
	if len(res.Cookies()) != 1 {
		t.Error("no cookie for an empty response")
	}
	// Changes after the response started are not saved.
	c.do(httptest.NewRequest(http.MethodGet, "/", nil), func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("x"))
		http.NewResponseController(w).Flush() //nolint:errcheck // recorder supports it
		From(r.Context()).Put("late", 1)
	})
	c.get(func(s *Session) {
		if s.Has("late") || !s.Has("a") {
			t.Error("late change saved, or earlier value lost")
		}
	})
}

func TestConfig(t *testing.T) {
	no := false
	for _, cfg := range []Config{
		{Cookie: "a b", TTL: time.Hour, Path: "/", SameSite: "lax"},
		{Cookie: "s", TTL: time.Second, Path: "/", SameSite: "lax"},
		{Cookie: "s", TTL: time.Hour, Path: "x", SameSite: "lax"},
		{Cookie: "s", TTL: time.Hour, Path: "/", SameSite: "loose"},
		{Cookie: "s", TTL: time.Hour, Path: "/", SameSite: "none", Secure: &no},
		{Cookie: "app:session", TTL: time.Hour, Path: "/", SameSite: "lax"},
		{Cookie: "s", TTL: time.Hour, Path: "/", SameSite: "lax", Domain: "bad domain"},
		{Cookie: "s", TTL: time.Hour, MaxTTL: time.Minute, Path: "/", SameSite: "lax"},
		{Cookie: "s", TTL: time.Hour, Path: "/a\x00", SameSite: "lax"},
	} {
		if cfg.Validate() == nil {
			t.Errorf("%+v accepted", cfg)
		}
	}
	if _, err := LoadConfig(config.Map{"SESSION_TTL": "soon"}); err == nil {
		t.Error("bad lifetime accepted")
	}
	if _, err := NewManager(DefaultConfig(), nil); err == nil {
		t.Error("nil encrypter accepted")
	}
	k, _ := encryption.ParseKey(encryption.GenerateKey())
	enc, _ := encryption.NewEncrypter(k)
	for _, cfg := range []Config{
		func() Config { c := DefaultConfig(); c.Cookie = "__Host-s"; c.Domain = "example.com"; return c }(),
		func() Config { c := DefaultConfig(); c.Cookie = "__Host-s"; c.Secure = &no; return c }(),
		func() Config { c := DefaultConfig(); c.Cookie = "__Secure-s"; c.Secure = &no; return c }(),
	} {
		if _, err := NewManager(cfg, enc); err == nil {
			t.Errorf("%+v accepted", cfg)
		}
	}

	key := encryption.GenerateKey()
	for env, want := range map[string]bool{"development": false, "testing": false, "staging": true, "production": true} {
		app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": env, "APP_KEY": key}))
		if err != nil {
			t.Fatal(err)
		}
		m, err := New(app)
		if err != nil {
			t.Fatal(err)
		}
		if m.secure != want {
			t.Errorf("%s: secure = %v", env, m.secure)
		}
	}
	app, _ := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "development", "APP_KEY": key, "SESSION_SECURE": "true", "SESSION_SAME_SITE": "strict"}))
	m, err := New(app)
	if err != nil || !m.secure || m.sameSite != http.SameSiteStrictMode {
		t.Errorf("explicit settings: %v %v %v", m.secure, m.sameSite, err)
	}
	app, _ = anetos.New(anetos.WithSource(config.Map{}))
	if _, err := New(app); err == nil || !strings.Contains(err.Error(), "APP_KEY") {
		t.Errorf("missing key: %v", err)
	}
}

func TestExpireOnClose(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExpireOnClose = true
	c := newClient(t, cfg)
	res := c.get(func(s *Session) { s.Put("a", 1) })
	if ck := res.Cookies()[0]; ck.MaxAge != 0 || strings.Contains(res.Header.Get("Set-Cookie"), "Max-Age") {
		t.Errorf("cookie = %s", res.Header.Get("Set-Cookie"))
	}
}

func TestMaxLifetime(t *testing.T) {
	c := newClient(t, DefaultConfig())
	c.get(func(s *Session) { s.Put("user_id", 1) })
	for range 7 * 24 {
		c.clock = c.clock.Add(time.Hour)
		c.get(func(s *Session) { s.Put("seen", c.clock.Unix()) }) // active every hour
	}
	c.clock = c.clock.Add(time.Hour)
	c.get(func(s *Session) {
		if s.Has("user_id") {
			t.Error("session outlived SESSION_MAX_TTL")
		}
		s.Put("user_id", 2)
		s.Regenerate()
	})
	c.clock = c.clock.Add(time.Hour)
	c.get(func(s *Session) {
		if !s.Has("user_id") {
			t.Error("new session lost")
		}
	})
}

func TestHeadersAndCookieName(t *testing.T) {
	c := newClient(t, DefaultConfig())
	if c.m.CookieName() != "__Host-anetos_session" {
		t.Errorf("cookie name %q", c.m.CookieName())
	}
	res := c.get(func(s *Session) { s.Put("a", 1) })
	if res.Header.Get("Cache-Control") != "private" || res.Header.Get("Vary") != "Cookie" {
		t.Errorf("headers %v", res.Header)
	}
	res = c.do(httptest.NewRequest(http.MethodGet, "/", nil), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Add("Vary", "cookie")
	})
	if res.Header.Get("Cache-Control") != "no-store" || len(res.Header.Values("Vary")) != 1 {
		t.Errorf("handler headers overridden: %v", res.Header)
	}
	fresh := newClient(t, DefaultConfig())
	if res := fresh.get(func(*Session) {}); res.Header.Get("Cache-Control") != "" {
		t.Errorf("sessionless response marked private: %v", res.Header)
	}

	no := false
	for _, cfg := range []Config{
		func() Config { c := DefaultConfig(); c.Secure = &no; return c }(),
		func() Config { c := DefaultConfig(); c.Domain = "example.com"; return c }(),
		func() Config { c := DefaultConfig(); c.Path = "/app"; return c }(),
		func() Config { c := DefaultConfig(); c.Cookie = "__Secure-s"; return c }(),
	} {
		k, _ := encryption.ParseKey(encryption.GenerateKey())
		enc, _ := encryption.NewEncrypter(k)
		m, err := NewManager(cfg, enc)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(m.CookieName(), "__Host-") {
			t.Errorf("%+v: %q", cfg, m.CookieName())
		}
	}
}

func TestNestedMiddleware(t *testing.T) {
	c := newClient(t, DefaultConfig())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h := c.m.Middleware(c.m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		From(r.Context()).Put("a", 1)
	})))
	h.ServeHTTP(rec, req)
	if n := len(rec.Header().Values("Set-Cookie")); n != 1 {
		t.Errorf("%d Set-Cookie headers", n)
	}
	// Another manager's middleware inside gives its routes their own session.
	cfg := DefaultConfig()
	cfg.Cookie = "admin"
	other := newClient(t, cfg)
	rec = httptest.NewRecorder()
	c.m.Middleware(other.m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		From(r.Context()).Put("admin", true)
	}))).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if cookies := (&http.Response{Header: rec.Header()}).Cookies(); len(cookies) != 1 || cookies[0].Name != "__Host-admin" {
		t.Errorf("cookies %v", rec.Header().Values("Set-Cookie"))
	}
}

type hijackRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, nil
}

func TestWriterInterfaces(t *testing.T) {
	c := newClient(t, DefaultConfig())
	rec := &hijackRecorder{ResponseRecorder: httptest.NewRecorder()}
	c.m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		From(r.Context()).Put("a", 1)
		if _, _, err := http.NewResponseController(w).Hijack(); err != nil {
			t.Error(err)
		}
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !rec.hijacked || len(rec.Header().Values("Set-Cookie")) != 1 {
		t.Errorf("hijack: %v %v", rec.hijacked, rec.Header())
	}
	rec2 := httptest.NewRecorder()
	c.m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Hijacker); !ok {
			t.Error("not a Hijacker")
		}
		n, err := io.Copy(w, strings.NewReader("hello"))
		if err != nil || n != 5 {
			t.Error(n, err)
		}
	})).ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec2.Body.String() != "hello" {
		t.Errorf("body %q", rec2.Body)
	}
}

func TestInvalidCookieLogged(t *testing.T) {
	c := newClient(t, DefaultConfig())
	c.m.log = slog.New(slog.NewTextHandler(c.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c.cookie = "garbage"
	c.get(func(*Session) {})
	if !strings.Contains(c.logs.String(), "can't be decrypted") {
		t.Errorf("logs: %s", c.logs)
	}
}

func TestLoadAndEdit(t *testing.T) {
	c := newClient(t, DefaultConfig())
	// A request flashes a message and errors.
	c.get(func(s *Session) {
		s.Flash("status", "Saved.")
		s.FlashErrors(FieldError{"title", "Required."})
	})
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: c.m.name, Value: c.cookie})
		return r
	}
	// Editing (a test getting a CSRF token) doesn't use up the flash.
	var tok string
	ck, err := c.m.Edit(req(), func(s *Session) { tok = s.Token(); s.Put("seeded", true) })
	if err != nil || ck.Name != c.m.name || !ck.HttpOnly {
		t.Fatalf("Edit: %v %+v", err, ck)
	}
	c.cookie = ck.Value
	s := c.m.Load(req())
	if s.String("status") != "Saved." || len(s.Errors()) != 1 || !s.Has("seeded") || !s.VerifyToken(tok) {
		t.Errorf("after Edit: status=%q errors=%v seeded=%v token=%v", s.String("status"), s.Errors(), s.Has("seeded"), s.VerifyToken(tok))
	}
	// fn sees the session as a handler would, and Put on a flashed key
	// keeps it, as in a request.
	var seen []FieldError
	ck, err = c.m.Edit(req(), func(s *Session) { seen = s.Errors(); s.Put("status", "Kept.") })
	if err != nil || len(seen) != 1 {
		t.Fatalf("Edit saw errors %v: %v", seen, err)
	}
	c.cookie = ck.Value
	c.get(func(*Session) {}) // a request uses up the flash
	if s := c.m.Load(req()); s.String("status") != "Kept." || len(s.Errors()) != 0 {
		t.Errorf("after a request: status=%q errors=%v", s.String("status"), s.Errors())
	}
	// Load doesn't save.
	s.Put("unsaved", 1)
	if c.m.Load(req()).Has("unsaved") {
		t.Error("Load saved")
	}
	// Edit starts a session when there is none.
	if ck, err := c.m.Edit(httptest.NewRequest(http.MethodGet, "/", nil), func(s *Session) { s.Put("a", 1) }); err != nil || ck.Value == "" {
		t.Errorf("new session: %v", err)
	}
	if _, err := c.m.Edit(req(), func(s *Session) { s.Put("big", strings.Repeat("x", 5000)) }); err == nil {
		t.Error("oversized Edit accepted")
	}
}

// Use's middleware run inside the session middleware, in order, for
// handlers wrapped before and after; nested session middleware run them
// once.
func TestUse(t *testing.T) {
	c := newClient(t, DefaultConfig())
	var trail []string
	mark := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if From(r.Context()) == nil {
					t.Errorf("%s: no session", name)
				}
				trail = append(trail, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { trail = append(trail, "handler") })
	before := c.m.Middleware(handler)
	serve := func(h http.Handler) string {
		trail = nil
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		return strings.Join(trail, ",")
	}
	if got := serve(before); got != "handler" {
		t.Errorf("without Use: %s", got)
	}
	c.m.Use(mark("a"), mark("b"))
	after := c.m.Middleware(c.m.Middleware(handler))
	for _, h := range []http.Handler{before, after, before} {
		if got := serve(h); got != "a,b,handler" {
			t.Errorf("with Use: %s", got)
		}
	}
	c.m.Use(mark("c"))
	if got := serve(before); got != "a,b,c,handler" {
		t.Errorf("after a second Use: %s", got)
	}
}

// A session is written back only when something changed it: reading,
// storing the same value again or deleting a missing key don't; a flash
// used up, a new value or a new token do.
func TestUnchangedSessionNotSaved(t *testing.T) {
	c := newClient(t, DefaultConfig())
	c.get(func(s *Session) { s.Put("name", "Ada"); s.Token() })
	for name, fn := range map[string]func(s *Session){
		"read":           func(s *Session) { _ = s.String("name"); _ = s.Token(); _ = s.Has("x") },
		"same value":     func(s *Session) { s.Put("name", "Ada") },
		"missing delete": func(s *Session) { s.Delete("nothing") },
		"keep nothing":   func(s *Session) { s.Keep("nothing") },
		"no errors":      func(s *Session) { s.FlashErrors() },
	} {
		if res := c.get(fn); len(res.Cookies()) != 0 {
			t.Errorf("%s: cookie written: %v", name, res.Header["Set-Cookie"])
		}
	}
	// In order: each changes the session.
	steps := []struct {
		name string
		fn   func(s *Session)
	}{
		{"new value", func(s *Session) { s.Put("name", "Grace") }},
		{"delete", func(s *Session) { s.Delete("name") }},
		{"new token", func(s *Session) { s.RegenerateToken() }},
		{"flash", func(s *Session) { s.Flash("status", "Saved.") }},
		{"flash used up", func(s *Session) {}},
		{"errors", func(s *Session) { s.FlashErrors(FieldError{Field: "a", Message: "b"}) }},
		{"errors used up", func(s *Session) {}},
		{"regenerate", func(s *Session) { s.Regenerate() }},
		{"flash again", func(s *Session) { s.Flash("status", "Saved.") }},
		{"the flashed value stored to keep", func(s *Session) { s.Put("status", "Saved.") }},
		{"flash to keep", func(s *Session) { s.Flash("kept", "Yes.") }},
		{"kept", func(s *Session) { s.Keep("kept") }},
		{"reflashed", func(s *Session) { s.Reflash() }},
		{"used up at last", func(s *Session) {}},
	}
	for _, step := range steps {
		if res := c.get(step.fn); len(res.Cookies()) == 0 {
			t.Errorf("%s: no cookie written", step.name)
		}
	}
	c.get(func(s *Session) {
		if s.String("status") != "Saved." {
			t.Error("a flashed value stored again with Put was dropped")
		}
	})
}
