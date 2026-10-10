// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"errors"
	"io"
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

// TestStore runs the behavior tests with sessions in a store.
func TestStore(t *testing.T) {
	useStore = true
	t.Cleanup(func() { useStore = false })
	for name, fn := range map[string]func(*testing.T){
		"NoCookieUntilUsed":         TestNoCookieUntilUsed,
		"ValuesPersist":             TestValuesPersist,
		"UnchangedSessionNotSaved":  TestUnchangedSessionNotSaved,
		"Flash":                     TestFlash,
		"IdleExpiryAndRefresh":      TestIdleExpiryAndRefresh,
		"CSRFToken":                 TestCSRFToken,
		"RegenerateAndInvalidate":   TestRegenerateAndInvalidate,
		"ErrorsAndOldInput":         TestErrorsAndOldInput,
		"ImplicitResponseAndStream": TestImplicitResponseAndStreaming,
		"ExpireOnClose":             TestExpireOnClose,
		"MaxLifetime":               TestMaxLifetime,
		"HeadersAndCookieName":      TestHeadersAndCookieName,
		"NestedMiddleware":          TestNestedMiddleware,
		"WriterInterfaces":          TestWriterInterfaces,
		"InvalidCookieLogged":       TestInvalidCookieLogged,
	} {
		t.Run(name, fn)
	}
}

func newStoreClient(t *testing.T, store cache.Store) *client {
	t.Helper()
	c := newClient(t, DefaultConfig())
	c.m.store, c.m.prefix = store, "t:session:"
	return c
}

func TestStoreRevokes(t *testing.T) {
	store := cache.NewMemoryStore()
	c := newStoreClient(t, store)
	var id string
	c.get(func(s *Session) { s.Put("user_id", 7); id = s.ID() })
	if len(c.cookie) > 200 {
		t.Errorf("the cookie holds more than the ID: %d bytes", len(c.cookie))
	}
	// The store key is a hash of the ID, not the ID.
	if store.Len() != 1 {
		t.Fatalf("%d items stored", store.Len())
	}
	if _, ok, _ := store.Get(context.Background(), "t:session:"+id); ok {
		t.Error("the session ID is the store key")
	}
	copied := c.cookie

	// Regenerate replaces the stored session: the old cookie is dead.
	c.get(func(s *Session) { s.Regenerate() })
	if store.Len() != 1 || c.cookie == copied {
		t.Errorf("after Regenerate: %d items, cookie changed %v", store.Len(), c.cookie != copied)
	}
	stolen := &client{t: t, m: c.m, cookie: copied, clock: c.clock, logs: c.logs}
	stolen.get(func(s *Session) {
		if s.Has("user_id") {
			t.Error("a cookie from before Regenerate still works")
		}
	})

	// Invalidate (log out) removes it from the store, for every copy.
	copied = c.cookie
	c.get(func(s *Session) { s.Invalidate() })
	if store.Len() != 0 {
		t.Errorf("%d items after Invalidate", store.Len())
	}
	stolen.cookie = copied
	stolen.get(func(s *Session) {
		if s.Has("user_id") {
			t.Error("a cookie from before Invalidate still works")
		}
	})

	// No cookie size limit.
	c.get(func(s *Session) { s.Put("big", strings.Repeat("x", 10000)) })
	c.get(func(s *Session) {
		if len(s.GetString("big")) != 10000 {
			t.Error("a large session wasn't kept")
		}
	})
}

// failing is a store whose reads fail.
type failing struct{ cache.Store }

var errDown = errors.New("store down")

func (failing) Get(context.Context, string) ([]byte, bool, error) { return nil, false, errDown }

// failWrites wraps a store: writes fail while fail is set.
type failWrites struct {
	cache.Store
	fail bool
}

func (f *failWrites) Set(ctx context.Context, k string, v []byte, ttl time.Duration) error {
	if f.fail {
		return errDown
	}
	return f.Store.Set(ctx, k, v, ttl)
}

func (f *failWrites) Replace(ctx context.Context, k string, v []byte, ttl time.Duration) (bool, error) {
	if f.fail {
		return false, errDown
	}
	return f.Store.Replace(ctx, k, v, ttl)
}

func (f *failWrites) Delete(ctx context.Context, k string) error {
	if f.fail {
		return errDown
	}
	return f.Store.Delete(ctx, k)
}

func TestStoreFailures(t *testing.T) {
	c := newStoreClient(t, cache.NewMemoryStore())
	c.get(func(s *Session) { s.Put("k", 1) })

	// A store that can't be read: 503, and nothing saved or removed.
	c.m.store = failing{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: c.m.name, Value: c.cookie})
	rec := httptest.NewRecorder()
	ran := false
	c.m.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true })).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || ran || rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("status %d, handler ran %v, Set-Cookie %q", rec.Code, ran, rec.Header().Get("Set-Cookie"))
	}
	if !strings.Contains(c.logs.String(), "store down") {
		t.Errorf("logs: %s", c.logs)
	}
	if _, err := c.m.Edit(req, func(*Session) {}); !errors.Is(err, errDown) {
		t.Errorf("Edit: %v", err)
	}

	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}

	// A store that can't be written: the response goes out, logged.
	c.m.store = &failWrites{Store: cache.NewMemoryStore(), fail: true}
	c.cookie = ""
	res := c.get(func(s *Session) { s.Put("k", 2) })
	if len(res.Cookies()) != 0 {
		t.Error("cookie set for an unsaved session")
	}
	if !strings.Contains(c.logs.String(), "changes not saved") {
		t.Errorf("logs: %s", c.logs)
	}
}

func TestStoreLoadAndEdit(t *testing.T) {
	c := newStoreClient(t, cache.NewMemoryStore())
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.cookie != "" {
			r.AddCookie(&http.Cookie{Name: c.m.name, Value: c.cookie})
		}
		return r
	}
	ck, err := c.m.Edit(req(), func(s *Session) { s.Put("seeded", true) })
	if err != nil {
		t.Fatal(err)
	}
	c.cookie = ck.Value
	if !c.m.Load(req()).Has("seeded") {
		t.Error("Load doesn't see what Edit stored")
	}
	old := c.cookie
	ck, err = c.m.Edit(req(), func(s *Session) { s.Regenerate() })
	if err != nil {
		t.Fatal(err)
	}
	c.cookie = old
	if c.m.Load(req()).Has("seeded") {
		t.Error("Edit's Regenerate left the old session")
	}
	c.cookie = ck.Value
	if !c.m.Load(req()).Has("seeded") {
		t.Error("the regenerated session lost its values")
	}
}

func TestAppNewDrivers(t *testing.T) {
	key := encryption.GenerateKey()
	newApp := func(env config.Map) *anetos.App {
		env["APP_KEY"] = key
		env["APP_NAME"] = "blog"
		app, err := anetos.New(anetos.WithSource(env), anetos.WithLogOutput(io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = app.Close() })
		return app
	}
	if _, err := New(newApp(config.Map{"SESSION_DRIVER": "redis"})); err == nil ||
		!strings.Contains(err.Error(), "[cookie, database]") || !strings.Contains(err.Error(), "redis.SessionDriver()") {
		t.Errorf("unknown driver: %v", err)
	}
	if _, err := New(newApp(config.Map{"SESSION_DRIVER": "database"})); err == nil || !strings.Contains(err.Error(), "db.Connect") {
		t.Errorf("database driver without a database: %v", err)
	}
	var got Config
	store := cache.NewMemoryStore()
	m, err := New(newApp(config.Map{"SESSION_DRIVER": "mem"}), Driver{Name: "mem", Open: func(_ *anetos.App, cfg Config) (cache.Store, error) {
		got = cfg
		return store, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if m.store != store || m.prefix != "blog:session:" || got.Table != "sessions" {
		t.Errorf("store %v, prefix %q, config %+v", m.store, m.prefix, got)
	}
	if m, err := New(newApp(config.Map{})); err != nil || m.store != nil {
		t.Errorf("default driver: %v, %v", m, err)
	}
	cfg := DefaultConfig()
	cfg.Driver = ""
	if err := cfg.Validate(); err == nil {
		t.Error("empty SESSION_DRIVER accepted")
	}
	for _, p := range []string{strings.Repeat("p", 101), "a\x00b", "bad\xff"} {
		cfg := DefaultConfig()
		cfg.Prefix = p
		if err := cfg.Validate(); err == nil {
			t.Errorf("SESSION_PREFIX %.20q accepted", p)
		}
	}
}

func TestStoreRaces(t *testing.T) {
	store := cache.NewMemoryStore()
	c := newStoreClient(t, store)
	c.get(func(s *Session) { s.Put("user_id", 7) })

	// A request that started before a logout (a stolen cookie, a slow
	// form post) doesn't bring the session back when it ends.
	thief := &client{t: t, m: c.m, cookie: c.cookie, clock: c.clock, logs: c.logs}
	res := thief.get(func(s *Session) {
		c.get(func(s *Session) { s.Invalidate() }) // the owner logs out meanwhile
		s.Flash("status", "x")
	})
	if store.Len() != 0 {
		t.Errorf("%d sessions stored after the logout", store.Len())
	}
	if len(res.Cookies()) != 0 {
		t.Errorf("the late request set a cookie: %v", res.Header["Set-Cookie"])
	}
	thief.get(func(s *Session) {
		if s.Has("user_id") {
			t.Error("the logged-out session came back")
		}
	})
}

func TestStoreRegenerateRace(t *testing.T) {
	store := cache.NewMemoryStore()
	c := newStoreClient(t, store)
	c.get(func(s *Session) { s.Put("cart", 1) })
	// A request that started before a login (which regenerates the
	// session) mustn't remove the login's cookie when it ends.
	var loginCookie string
	late := &client{t: t, m: c.m, cookie: c.cookie, clock: c.clock, logs: c.logs}
	res := late.get(func(s *Session) {
		c.get(func(s *Session) { s.Put("user_id", 7); s.Regenerate() })
		loginCookie = c.cookie
		_ = s.Token() // a change to save
	})
	if len(res.Cookies()) != 0 {
		t.Errorf("the late request set a cookie: %v", res.Header["Set-Cookie"])
	}
	c.cookie = loginCookie
	c.get(func(s *Session) {
		if !s.Has("user_id") {
			t.Error("the login was lost")
		}
	})
}

// TestStoreSwap checks that an entry copied to another session's key
// isn't accepted.
func TestStoreSwap(t *testing.T) {
	store := cache.NewMemoryStore()
	admin := newStoreClient(t, store)
	var adminID string
	admin.get(func(s *Session) { s.Put("user_id", 1); adminID = s.ID() })
	other := &client{t: t, m: admin.m, clock: admin.clock, logs: admin.logs}
	var otherID string
	other.get(func(s *Session) { s.Put("user_id", 2); otherID = s.ID() })
	ctx := context.Background()
	v, _, _ := store.Get(ctx, admin.m.key(adminID))
	if err := store.Set(ctx, admin.m.key(otherID), v, time.Hour); err != nil {
		t.Fatal(err)
	}
	other.get(func(s *Session) {
		if v, _ := Value[int](s, "user_id"); v == 1 {
			t.Error("a copied entry gave another session the admin's values")
		}
	})
}

func TestStoreFailedRegenerate(t *testing.T) {
	store := &failWrites{Store: cache.NewMemoryStore()}
	c := newStoreClient(t, store)
	c.get(func(s *Session) { s.Put("cart", 3) })
	before := c.cookie
	store.fail = true
	c.get(func(s *Session) { s.Regenerate() })
	store.fail = false
	if c.cookie != before {
		t.Error("a cookie was sent for a session that wasn't saved")
	}
	c.get(func(s *Session) {
		if !s.Has("cart") {
			t.Error("a failed Regenerate lost the session")
		}
	})
	// A failed removal at logout is logged.
	store.fail = true
	c.get(func(s *Session) { s.Invalidate() })
	store.fail = false
	if !strings.Contains(c.logs.String(), "copies of its cookie work until it expires") {
		t.Errorf("logs: %s", c.logs)
	}
}

func TestStoreContents(t *testing.T) {
	store := cache.NewMemoryStore()
	c := newStoreClient(t, store)
	var id string
	c.get(func(s *Session) { s.Put("secret", "hunter2"); id = s.ID(); _ = s.Token() })
	b, ok, err := store.Get(context.Background(), c.m.key(id))
	if err != nil || !ok {
		t.Fatalf("stored session: %v %v", ok, err)
	}
	if strings.Contains(string(b), id) || strings.Contains(string(b), "hunter2") {
		t.Error("the store holds the session ID or its values in the clear")
	}

	// Large form input is dropped; a session past the limit isn't saved.
	c.get(func(s *Session) {
		s.FlashErrors(FieldError{"body", "Too long."})
		s.FlashInput(url.Values{"body": {strings.Repeat("x", maxStoredInput+1)}})
	})
	c.get(func(s *Session) {
		if len(s.Errors()) != 1 {
			t.Error("the errors weren't kept")
		}
		if _, ok := s.Old("body"); ok {
			t.Error("oversized input kept")
		}
	})
	c.get(func(s *Session) { s.Put("big", strings.Repeat("x", maxStored)) })
	c.get(func(s *Session) {
		if s.Has("big") || !s.Has("secret") {
			t.Error("an oversized session was saved")
		}
	})
	if !strings.Contains(c.logs.String(), "session too large") {
		t.Errorf("logs: %s", c.logs)
	}
}
