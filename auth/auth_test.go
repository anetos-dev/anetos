// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
)

type user struct {
	ID       string
	Email    string
	Password string
	Remember string
	Admin    bool
	Disabled bool
	Key      string // session key
	TwoF     string // two-factor state
}

func (u *user) AuthID() string       { return u.ID }
func (u *user) AuthPassword() string { return u.Password }

// store is an in-memory user table.
type store struct {
	mu        sync.Mutex
	byID      map[string]user
	rehashed  int
	failLoads bool
	reenter   bool // ByID asks for the current user
}

func newStore(t *testing.T) *store {
	t.Helper()
	h, err := password.Hash("secret")
	if err != nil {
		t.Fatal(err)
	}
	return &store{byID: map[string]user{"1": {ID: "1", Email: "ada@example.com", Password: h}, "2": {ID: "2", Email: "bob@example.com", Password: h, Admin: true},
		"3": {ID: "3", Email: "sam@example.com", Password: h}, "4": {ID: "4", Email: "social@example.com"}}}
}

func (s *store) users() auth.Users[*user] {
	return auth.Users[*user]{
		ByID: func(ctx context.Context, id string) (*user, error) {
			if s.reenter {
				if _, err := auth.Current[*user](ctx); err != nil {
					return nil, err
				}
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.failLoads {
				return nil, errors.New("database down")
			}
			u, ok := s.byID[id]
			if !ok {
				return nil, auth.ErrUserNotFound
			}
			return &u, nil
		},
		ByLogin: func(_ context.Context, email string) (*user, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, u := range s.byID {
				if strings.EqualFold(u.Email, email) {
					return &u, nil
				}
			}
			return nil, auth.ErrUserNotFound
		},
		RememberToken: func(u *user) string { return u.Remember },
		SetRememberToken: func(_ context.Context, u *user, tok string) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			v := s.byID[u.ID]
			v.Remember = tok
			s.byID[u.ID] = v
			return nil
		},
		Disabled:   func(u *user) bool { return u.Disabled },
		SessionKey: func(u *user) string { return u.Key },
		SetSessionKey: func(_ context.Context, u *user, key string) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			v := s.byID[u.ID]
			v.Key = key
			s.byID[u.ID] = v
			return nil
		},
		TwoFactor: func(u *user) string { return u.TwoF },
		SetTwoFactor: func(_ context.Context, u *user, st string) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			v := s.byID[u.ID]
			v.TwoF = st
			s.byID[u.ID] = v
			u.TwoF = st
			return nil
		},
		SetPassword: func(_ context.Context, u *user, hash string) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			v := s.byID[u.ID]
			v.Password = hash
			s.byID[u.ID] = v
			s.rehashed++
			return nil
		},
	}
}

func (s *store) setPassword(t *testing.T, id, pw string) {
	h, err := password.Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.byID[id]
	v.Password = h
	s.byID[id] = v
}

// browser sends requests to the app's router, keeping cookies.
type browser struct {
	t   *testing.T
	h   http.Handler
	ctx context.Context // the app's
	jar *cookiejar.Jar
}

var base, _ = url.Parse("http://app.test/")

// response is what the tests look at.
type response struct {
	StatusCode int
	Header     http.Header
	Body       string
}

func (b *browser) do(method, path string, form url.Values, header ...string) response {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	u := base.ResolveReference(&url.URL{Path: path})
	if p, q, ok := strings.Cut(path, "?"); ok {
		u = base.ResolveReference(&url.URL{Path: p, RawQuery: q})
	}
	r := httptest.NewRequestWithContext(b.ctx, method, u.String(), body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Header.Set("Accept", "text/html")
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	for _, c := range b.jar.Cookies(base) {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	b.jar.SetCookies(base, (&http.Response{Header: w.Header()}).Cookies())
	return response{StatusCode: w.Code, Header: w.Header(), Body: w.Body.String()}
}

// drop removes a cookie from the browser, as if it expired.
func (b *browser) drop(name string) {
	b.jar.SetCookies(base, []*http.Cookie{{Name: name, Value: "", MaxAge: -1, Path: "/"}})
}

func newApp(t *testing.T, s *store) (*auth.Auth[*user], *browser) {
	a, b, _ := newAppWith(t, s)
	return a, b
}

func newAppWith(t *testing.T, s *store, env ...string) (*auth.Auth[*user], *browser, *anetos.App) {
	t.Helper()
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey()}
	for i := 0; i+1 < len(env); i += 2 {
		src[env[i]] = env[i+1]
	}
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	if _, err := cache.New(app); err != nil {
		t.Fatal(err)
	}
	// SESSION_DRIVER=mem keeps sessions on the server, in memory.
	sessions, err := session.New(app, session.Driver{Name: "mem", Open: func(*anetos.App, session.Config) (cache.Store, error) {
		return cache.NewMemoryStore(), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(app, s.users())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	r := srv.Router().Group("", sessions.Middleware, a.Middleware)
	r.Group("", a.Guest).Get("/login", func(c *web.Ctx) error { return c.Text(http.StatusOK, "login page") })
	r.Post("/login", func(c *web.Ctx) error {
		_, err := a.Attempt(c, c.Request().FormValue("email"), c.Request().FormValue("password"), c.Request().FormValue("remember") == "1")
		if err != nil {
			return err
		}
		return c.Redirect(http.StatusSeeOther, auth.Intended(c, "/home"))
	})
	r.Post("/logout", func(c *web.Ctx) error {
		if err := a.Logout(c); err != nil {
			return err
		}
		return c.Redirect(http.StatusSeeOther, "/")
	})
	r.Get("/id", func(c *web.Ctx) error {
		id, err := auth.CurrentID(c)
		if err != nil {
			return err
		}
		return c.Text(http.StatusOK, id)
	})
	r.Post("/impersonate/{id}", func(c *web.Ctx) error {
		u, err := s.users().ByID(c, c.Param("id"))
		if err != nil {
			return err
		}
		if err := a.Impersonate(c, u); err != nil {
			return err
		}
		return c.NoContent()
	})
	r.Post("/stop", func(c *web.Ctx) error {
		u, err := a.StopImpersonating(c)
		if err != nil {
			return err
		}
		return c.Text(http.StatusOK, u.ID)
	})
	r.Post("/tokens", func(c *web.Ctx) error {
		u, err := auth.Current[*user](c)
		if err != nil {
			return err
		}
		if _, _, err := a.CreateToken(c, u, "cli", []string{"*"}, 0); err != nil {
			return err
		}
		return c.NoContent()
	})
	r.Get("/whoami", func(c *web.Ctx) error {
		id, _ := auth.CurrentID(c)
		by, _ := auth.Impersonator(c)
		return c.Text(http.StatusOK, id+" by "+by)
	})
	private := r.Group("", a.Require)
	private.Get("/dashboard", func(c *web.Ctx) error {
		u, _ := auth.User[*user](c)
		return c.Text(http.StatusOK, "hello "+u.Email)
	})
	private.Get("/posts/{owner}", func(c *web.Ctx) error {
		canEdit := func(_ context.Context, u *user, owner string) bool { return u.ID == owner }
		if err := auth.Authorize(c, canEdit, c.Request().PathValue("owner")); err != nil {
			return err
		}
		return c.Text(http.StatusOK, "editable")
	})
	srv.Router().Group("/api", a.Require).Get("/me", func(c *web.Ctx) error { return c.NoContent() })
	twoFactorRoutes(r, a, s)
	apiLoginRoutes(srv.Router(), a)
	// An API on routes with sessions (an app's own front end's), behind
	// TokenMiddleware.
	r.Group("/session-api", a.TokenMiddleware, a.Require).Get("/me", func(c *web.Ctx) error { return c.NoContent() })
	private.Get("/admin", func(c *web.Ctx) error {
		if err := auth.AuthorizeUser(c, func(_ context.Context, u *user) bool { return u.Admin }); err != nil {
			return err
		}
		return c.Text(http.StatusOK, "admin")
	})
	ctx := app.Context(context.Background())
	return a, &browser{t: t, h: srv.Router(), ctx: ctx, jar: mustJar()}, app
}

func mustJar() *cookiejar.Jar {
	j, _ := cookiejar.New(nil)
	return j
}

func login(b *browser, email, pw string, remember bool) response {
	form := url.Values{"email": {email}, "password": {pw}}
	if remember {
		form.Set("remember", "1")
	}
	return b.do(http.MethodPost, "/login", form)
}

func TestLoginFlow(t *testing.T) {
	s := newStore(t)
	_, b := newApp(t, s)

	res := b.do(http.MethodGet, "/dashboard?tab=2", nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
		t.Fatalf("guest: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res := b.do(http.MethodGet, "/dashboard", nil, "Accept", "application/json"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("JSON guest: %d", res.StatusCode)
	}
	if res := login(b, "ada@example.com", "wrong", false); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", res.StatusCode)
	}
	if res := login(b, "nobody@example.com", "secret", false); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("unknown user: %d", res.StatusCode)
	}
	before := sessionCookie(b)
	res = login(b, "ADA@example.com", "secret", false)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/dashboard?tab=2" {
		t.Fatalf("login: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if sessionCookie(b) == before {
		t.Error("the session wasn't regenerated at login")
	}
	if body := b.do(http.MethodGet, "/dashboard", nil).Body; body != "hello ada@example.com" {
		t.Errorf("dashboard: %q", body)
	}
	if res := b.do(http.MethodGet, "/login", nil); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Errorf("Guest with a user: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res := b.do(http.MethodGet, "/admin", nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("policy: %d", res.StatusCode)
	}

	// A password change elsewhere logs this session out.
	s.setPassword(t, "1", "new secret")
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("after a password change: %d", res.StatusCode)
	}
	login(b, "ada@example.com", "new secret", false)
	b.do(http.MethodPost, "/logout", nil)
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("after logout: %d", res.StatusCode)
	}
}

func sessionCookie(b *browser) string {
	for _, c := range b.jar.Cookies(base) {
		if c.Name == "anetos_session" {
			return c.Value
		}
	}
	return ""
}

func TestThrottle(t *testing.T) {
	s := newStore(t)
	_, b := newApp(t, s)
	// Five failures a minute; up to eleven in case a minute starts
	// meanwhile.
	throttled := 0
	for i := 0; i < 11 && throttled == 0; i++ {
		switch res := login(b, "ada@example.com", "wrong", false); res.StatusCode {
		case http.StatusTooManyRequests:
			throttled = i
		case http.StatusUnauthorized:
		default:
			t.Fatalf("attempt %d: %d", i+1, res.StatusCode)
		}
	}
	if throttled < 5 {
		t.Fatalf("throttled after %d failures", throttled)
	}
	// Even the right password is refused now, for this login.
	if res := login(b, "ada@example.com", "secret", false); res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("throttled: %d", res.StatusCode)
	}
	if res := login(b, "bob@example.com", "secret", false); res.StatusCode != http.StatusSeeOther {
		t.Errorf("another login throttled: %d", res.StatusCode)
	}
}

func TestRememberMe(t *testing.T) {
	s := newStore(t)
	_, b := newApp(t, s)
	login(b, "ada@example.com", "secret", true)
	var remember *http.Cookie
	for _, c := range b.jar.Cookies(base) {
		if c.Name == "anetos_remember" {
			remember = c
		}
	}
	if remember == nil {
		t.Fatal("no remember-me cookie")
	}
	// The session ends (the browser closed): the cookie logs back in.
	b.drop("anetos_session")
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("remembered: %d", res.StatusCode)
	}
	if sessionCookie(b) == "" {
		t.Error("no new session after a remembered login")
	}
	// Logout logs out every remembered browser.
	other := &browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}
	other.jar.SetCookies(base, []*http.Cookie{remember})
	b.do(http.MethodPost, "/logout", nil)
	if res := other.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("remember cookie after logout: %d", res.StatusCode)
	}
	// A tampered cookie is removed.
	other.jar.SetCookies(base, []*http.Cookie{{Name: "anetos_remember", Value: "garbage", Path: "/"}})
	other.do(http.MethodGet, "/dashboard", nil)
	for _, c := range other.jar.Cookies(base) {
		if c.Name == "anetos_remember" {
			t.Error("a tampered remember cookie was kept")
		}
	}
}

func TestRehashAndLoadErrors(t *testing.T) {
	s := newStore(t)
	weak, _ := password.HashWith("secret", password.Params{Memory: 1024, Time: 1, Threads: 1})
	s.byID["1"] = user{ID: "1", Email: "ada@example.com", Password: weak}
	_, b := newApp(t, s)
	if res := login(b, "ada@example.com", "secret", false); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", res.StatusCode)
	}
	if s.rehashed != 1 || password.NeedsRehash(s.byID["1"].Password) {
		t.Errorf("hash not upgraded (%d)", s.rehashed)
	}
	// The upgraded hash doesn't log the session out.
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Errorf("after the upgrade: %d", res.StatusCode)
	}
	// A failure to load the user is an error, not a logout.
	s.failLoads = true
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusInternalServerError {
		t.Errorf("load failure: %d", res.StatusCode)
	}
	s.failLoads = false
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Errorf("after the failure: %d", res.StatusCode)
	}
}

func TestCurrentID(t *testing.T) {
	s := newStore(t)
	_, b := newApp(t, s)
	if res := b.do(http.MethodGet, "/id", nil, "Accept", "application/json"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("guest: %d", res.StatusCode)
	}
	login(b, "bob@example.com", "secret", false)
	if res := b.do(http.MethodGet, "/id", nil); res.StatusCode != http.StatusOK || res.Body != "2" {
		t.Errorf("logged in: %d %q", res.StatusCode, res.Body)
	}
	s.failLoads = true
	if res := b.do(http.MethodGet, "/id", nil); res.StatusCode != http.StatusInternalServerError {
		t.Errorf("load failure: %d", res.StatusCode)
	}
	if _, err := auth.CurrentID(context.Background()); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("no middleware: %v", err)
	}
}

func TestWithUser(t *testing.T) {
	s := newStore(t)
	_, _, app := newAppWith(t, s)
	ctx := app.Context(context.Background())
	asBob, err := auth.WithUser(ctx, "2")
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := auth.User[*user](asBob); !ok || u.Email != "bob@example.com" {
		t.Errorf("WithUser bob: %+v, %v", u, ok)
	}
	if id, err := auth.CurrentID(asBob); err != nil || id != "2" {
		t.Errorf("CurrentID: %q, %v", id, err)
	}
	if err := auth.AuthorizeUser(asBob, func(_ context.Context, u *user) bool { return u.Admin }); err != nil {
		t.Errorf("bob is an admin: %v", err)
	}
	if _, ok := auth.CurrentToken(asBob); ok {
		t.Error("a token")
	}
	// The limits of the token a request had.
	limited, _ := auth.WithUser(ctx, "2", auth.WithAbilities([]string{"posts:read"}))
	if tok, ok := auth.CurrentToken(limited); !ok || tok.UserID != "2" || !auth.TokenCan(limited, "posts:read") || auth.TokenCan(limited, "posts:write") {
		t.Errorf("abilities: %+v", tok)
	}
	gone, _ := auth.WithUser(ctx, "99")
	if auth.Check(gone) {
		t.Error("a user that doesn't exist is logged in")
	}
	s.failLoads = true
	failing, _ := auth.WithUser(ctx, "1")
	if _, err := auth.Current[*user](failing); err == nil || errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("a load failure: %v", err)
	}
	if _, err := auth.WithUser(context.Background(), "1"); err == nil {
		t.Error("WithUser without an Auth in the context")
	}
}

func TestTokens(t *testing.T) {
	s := newStore(t)
	a, _ := newApp(t, s)
	ctx := context.Background()
	ada, _ := s.users().ByID(ctx, "1")

	tok := a.PasswordResetToken(ada)
	if u, err := a.CheckPasswordResetToken(ctx, tok); err != nil || u.ID != "1" {
		t.Fatalf("reset token: %v, %v", u, err)
	}
	if _, _, err := a.CheckVerificationToken(ctx, tok); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("a reset token verified an email: %v", err)
	}
	s.setPassword(t, "1", "changed")
	if _, err := a.CheckPasswordResetToken(ctx, tok); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("reset token after the password changed: %v", err)
	}
	if _, err := a.CheckPasswordResetToken(ctx, tok[:len(tok)-2]+"xx"); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("tampered token: %v", err)
	}

	vt := a.VerificationToken(ada, "ada@new.example")
	u, email, err := a.CheckVerificationToken(ctx, vt)
	if err != nil || u.ID != "1" || email != "ada@new.example" {
		t.Errorf("verification: %v %q %v", u, email, err)
	}
	delete(s.byID, "1")
	if _, _, err := a.CheckVerificationToken(ctx, vt); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("token of a deleted user: %v", err)
	}
}

func TestAuthorize(t *testing.T) {
	type post struct{ Owner string }
	canEdit := func(_ context.Context, u *user, p post) bool { return p.Owner == u.ID }
	ctx := context.Background()
	if err := auth.Authorize(ctx, canEdit, post{"1"}); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("guest: %v", err)
	}
	if auth.Allows(ctx, canEdit, post{"1"}) || !auth.IsDenied(auth.ErrForbidden) {
		t.Error("Allows for a guest")
	}
}

func TestConfig(t *testing.T) {
	cfg, err := auth.LoadConfig(config.Map{})
	if err != nil || cfg.LoginURL != "/login" || cfg.Throttle != 5 {
		t.Errorf("defaults %+v %v", cfg, err)
	}
	for _, env := range []config.Map{{"AUTH_LOGIN_URL": "//evil.example"}, {"AUTH_HOME_URL": "https://x"}, {"AUTH_HOME_URL": "/\t/evil.example"}, {"AUTH_THROTTLE": "0"}, {"AUTH_RESET_TTL": "1s"}} {
		if _, err := auth.LoadConfig(env); err == nil {
			t.Errorf("%v accepted", env)
		}
	}
	// New needs the cache.
	app, _ := anetos.New(anetos.WithSource(config.Map{"APP_KEY": encryption.GenerateKey()}), anetos.WithLogOutput(io.Discard))
	defer app.Close()
	if _, err := auth.New(app, newStore(t).users()); err == nil || !strings.Contains(err.Error(), "cache.New") {
		t.Errorf("without a cache: %v", err)
	}
	if _, err := auth.NewWithConfig(cfg, auth.Users[*user]{}, nil); err == nil {
		t.Error("empty Users accepted")
	}
}

func TestThrottleVariants(t *testing.T) {
	s := newStore(t)
	_, b := newApp(t, s)
	for i := 0; i < 11 && login(b, "sam@example.com", "wrong", false).StatusCode != http.StatusTooManyRequests; i++ {
	}
	// "ſ" (long s) matches "s" in ByLogin's EqualFold, but not in the
	// login's key: the account's own count still stops it.
	if res := login(b, "ſam@example.com", "secret", false); res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("a variant of the login: %d", res.StatusCode)
	}
	// Many logins from one address: limited after AUTH_THROTTLE_IP (50)
	// failures, within twice that in case a minute starts meanwhile.
	limited := false
	for i := 0; i < 110 && !limited; i++ {
		res := login(b, "nobody"+strconv.Itoa(i)+"@example.com", "x", false)
		if res.StatusCode == http.StatusTooManyRequests {
			limited = i >= 39 // at most 11 failures above
			if !limited {
				t.Fatalf("limited after %d failures", i+5)
			}
		}
	}
	if !limited {
		t.Error("never limited per address")
	}
}

func TestNoPasswordUser(t *testing.T) {
	s := newStore(t)
	_, b := newApp(t, s)
	if res := login(b, "social@example.com", "", false); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a user without a password: %d", res.StatusCode)
	}
}

func TestRememberExpiryAndPasswordChange(t *testing.T) {
	s := newStore(t)
	a, b := newApp(t, s)
	now := time.Now()
	auth.SetNow(a, func() time.Time { return now })
	login(b, "ada@example.com", "secret", true)
	b.drop("anetos_session")
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("remembered: %d", res.StatusCode)
	}
	// Past AUTH_REMEMBER_TTL the cookie is refused, whatever the
	// browser does with it.
	now = now.Add(721 * time.Hour)
	b.drop("anetos_session")
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("expired remember cookie: %d", res.StatusCode)
	}
	now = time.Now()
	login(b, "ada@example.com", "secret", true)
	b.drop("anetos_session")
	s.setPassword(t, "1", "changed")
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("remember cookie after a password change: %d", res.StatusCode)
	}
	// Logging in without remember removes an earlier remember cookie.
	login(b, "ada@example.com", "changed", true)
	login(b, "ada@example.com", "changed", false)
	for _, c := range b.jar.Cookies(base) {
		if c.Name == "anetos_remember" {
			t.Error("the remember cookie outlived a login without it")
		}
	}
}

func TestTokenExpiry(t *testing.T) {
	s := newStore(t)
	a, _ := newApp(t, s)
	now := time.Now()
	auth.SetNow(a, func() time.Time { return now })
	ctx := context.Background()
	ada, _ := s.users().ByID(ctx, "1")
	reset, verify := a.PasswordResetToken(ada), a.VerificationToken(ada, ada.Email)
	now = now.Add(61 * time.Minute)
	if _, err := a.CheckPasswordResetToken(ctx, reset); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("expired reset token: %v", err)
	}
	if _, _, err := a.CheckVerificationToken(ctx, verify); err != nil {
		t.Errorf("verification token within a day: %v", err)
	}
	now = now.Add(24 * time.Hour)
	if _, _, err := a.CheckVerificationToken(ctx, verify); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("expired verification token: %v", err)
	}
}

func TestRequireAndPolicies(t *testing.T) {
	s := newStore(t)
	_, b, app := newAppWith(t, s)
	// A route without sessions answers 401, not a redirect.
	res := b.do(http.MethodGet, "/api/me", nil, "Accept", "*/*")
	if res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("API route: %d %v", res.StatusCode, res.Header)
	}
	if res := b.do(http.MethodGet, "/dashboard", nil, "HX-Request", "true"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("htmx guest: %d", res.StatusCode)
	}
	login(b, "ada@example.com", "secret", false)
	if res := b.do(http.MethodGet, "/posts/1", nil); res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("own post: %d, Cache-Control %q", res.StatusCode, res.Header.Get("Cache-Control"))
	}
	if res := b.do(http.MethodGet, "/posts/2", nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("another's post: %d", res.StatusCode)
	}
	// ByID asking for the current user is an error, not a deadlock.
	s.reenter = true
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusInternalServerError {
		t.Errorf("re-entrant load: %d", res.StatusCode)
	}
	s.reenter = false
	// One Auth per app.
	if _, err := auth.New(app, s.users()); err == nil {
		t.Error("a second Auth accepted")
	}
}

func TestConcurrentCurrent(t *testing.T) {
	s := newStore(t)
	a, b, app := newAppWith(t, s)
	login(b, "ada@example.com", "secret", false)
	// A handler asking for the user from several goroutines at once.
	var errs []error
	var mu sync.Mutex
	h := a.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if _, err := auth.Current[*user](r.Context()); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			})
		}
		wg.Wait()
	}))
	r := httptest.NewRequestWithContext(b.ctx, http.MethodGet, "http://app.test/", nil)
	for _, c := range b.jar.Cookies(base) {
		r.AddCookie(c)
	}
	m, _ := anetos.Resolve[*session.Manager](app)
	m.Middleware(h).ServeHTTP(httptest.NewRecorder(), r)
	if len(errs) != 0 {
		t.Errorf("concurrent Current: %v", errs)
	}
}

func TestIPLimitCountsFailures(t *testing.T) {
	s := newStore(t)
	_, b := newApp(t, s)
	// More successful logins from one address than AUTH_THROTTLE_IP.
	for i := range 55 {
		if res := login(b, "ada@example.com", "secret", false); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("login %d: %d", i+1, res.StatusCode)
		}
	}
}

// LoginSession refuses disabled users before touching the session (the
// logged-in case is anetostest.ActingAs's test).
func TestLoginSessionDisabled(t *testing.T) {
	a, _ := newApp(t, newStore(t))
	if err := a.LoginSession(nil, &user{ID: "9", Disabled: true}); !errors.Is(err, auth.ErrDisabled) {
		t.Errorf("LoginSession of a disabled user = %v", err)
	}
}

// WithDefaultHomeURL is HomeURL's default; AUTH_HOME_URL, when set, wins.
func TestDefaultHomeURL(t *testing.T) {
	for _, tc := range []struct {
		env  config.Map
		opt  string
		want string
	}{
		{config.Map{}, "", "/"},
		{config.Map{}, "/dashboard", "/dashboard"},
		{config.Map{"AUTH_HOME_URL": ""}, "/dashboard", "/dashboard"},
		{config.Map{"AUTH_HOME_URL": "/issues"}, "/dashboard", "/issues"},
	} {
		src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey()}
		maps.Copy(src, tc.env)
		app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = app.Close() })
		if _, err := cache.New(app); err != nil {
			t.Fatal(err)
		}
		var opts []auth.Option
		if tc.opt != "" {
			opts = append(opts, auth.WithDefaultHomeURL(tc.opt))
		}
		a, err := auth.New(app, newStore(t).users(), opts...)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.Config().HomeURL; got != tc.want {
			t.Errorf("%v, WithDefaultHomeURL(%q): HomeURL %q, want %q", tc.env, tc.opt, got, tc.want)
		}
	}
	// A default that isn't a path of the app is refused.
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey()}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	if _, err := cache.New(app); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.New(app, newStore(t).users(), auth.WithDefaultHomeURL("https://example.com/")); err == nil || !strings.Contains(err.Error(), "AUTH_HOME_URL") {
		t.Errorf("an absolute URL: %v", err)
	}
}

// RequireAbilities lets through a token with the abilities, a session,
// and refuses a token without them (403) and a guest (401).
func TestRequireAbilities(t *testing.T) {
	s := newStore(t)
	_, _, app := newAppWith(t, s)
	ctx := app.Context(context.Background())
	mw := auth.RequireAbilities("posts:read", "posts:write")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	reader, _ := auth.WithUser(ctx, "2", auth.WithAbilities([]string{"posts:read"}))
	writer, _ := auth.WithUser(ctx, "2", auth.WithAbilities([]string{"posts:read", "posts:write"}))
	all, _ := auth.WithUser(ctx, "2", auth.WithAbilities([]string{"*"}))
	session, _ := auth.WithUser(ctx, "2")
	for _, c := range []struct {
		name   string
		ctx    context.Context
		status int
	}{
		{"guest", ctx, http.StatusUnauthorized},
		{"reader", reader, http.StatusForbidden},
		{"writer", writer, http.StatusNoContent},
		{"every ability", all, http.StatusNoContent},
		{"session", session, http.StatusNoContent},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(c.ctx, http.MethodGet, "/", nil))
		if rec.Code != c.status || c.status == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: %d %v", c.name, rec.Code, rec.Header())
		}
	}
	// A user that can't be loaded is that error, not a guest.
	s.failLoads = true
	failing, _ := auth.WithUser(ctx, "2", auth.WithAbilities([]string{"*"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(failing, http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("load failure: %d", rec.Code)
	}
	s.failLoads = false

	r := web.NewRouter()
	r.With(mw).Get("/x", func(*web.Ctx) error { return nil })
	if docs := r.Routes()[0].Middleware; len(docs) != 1 || docs[0].Security != "bearer" || !slices.Equal(docs[0].Scopes, []string{"posts:read", "posts:write"}) {
		t.Errorf("doc: %+v", docs)
	}
	defer func() {
		if recover() == nil {
			t.Error("no abilities accepted")
		}
	}()
	auth.RequireAbilities()
}

// Several bad paths are reported in the settings' order, every time.
func TestConfigErrorsInOrder(t *testing.T) {
	var first string
	for i := range 20 {
		_, err := auth.LoadConfig(config.Map{"AUTH_LOGIN_URL": "login", "AUTH_HOME_URL": "home", "AUTH_SETTINGS_URL": "settings"})
		if err == nil {
			t.Fatal("bad paths accepted")
		}
		if i == 0 {
			first = err.Error()
			login, home, settings := strings.Index(first, "AUTH_LOGIN_URL"), strings.Index(first, "AUTH_HOME_URL"), strings.Index(first, "AUTH_SETTINGS_URL")
			if login > home || home > settings {
				t.Errorf("order: %s", first)
			}
		} else if err.Error() != first {
			t.Fatalf("the messages changed order:\n%s\n%s", first, err)
		}
	}
}
