// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

type raceUser struct{ ID, Email, Password, Key, TwoF string }

func (u *raceUser) AuthID() string       { return u.ID }
func (u *raceUser) AuthPassword() string { return u.Password }

type raceStore struct {
	mu   sync.Mutex
	u    raceUser
	slow bool
}

func (s *raceStore) get() *raceUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.u
	return &c
}

func (s *raceStore) users() auth.Users[*raceUser] {
	return auth.Users[*raceUser]{
		ByID: func(ctx context.Context, id string) (*raceUser, error) {
			if s.slow {
				time.Sleep(50 * time.Millisecond) // database latency
			}
			if id != "1" {
				return nil, auth.ErrNoUser
			}
			return s.get(), nil
		},
		ByLogin: func(ctx context.Context, l string) (*raceUser, error) {
			if !strings.EqualFold(l, "ada@example.com") {
				return nil, auth.ErrNoUser
			}
			return s.get(), nil
		},
		SessionKey: func(u *raceUser) string { return u.Key },
		SetSessionKey: func(_ context.Context, u *raceUser, k string) error {
			s.mu.Lock()
			s.u.Key = k
			s.mu.Unlock()
			return nil
		},
		TwoFactor: func(u *raceUser) string { return u.TwoF },
		SetTwoFactor: func(_ context.Context, u *raceUser, st string) error {
			s.mu.Lock()
			s.u.TwoF = st
			s.mu.Unlock()
			u.TwoF = st
			return nil
		},
	}
}

type raceClient struct {
	h       http.Handler
	ctx     context.Context
	cookies map[string]*http.Cookie
}

func (c *raceClient) post(t *testing.T, path string, form url.Values) int {
	r := httptest.NewRequestWithContext(c.ctx, http.MethodPost, "http://app.test"+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range c.cookies {
		r.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	for _, ck := range (&http.Response{Header: w.Header()}).Cookies() {
		c.cookies[ck.Name] = ck
	}
	return w.Code
}

// Two concurrent challenges with the same code: only one signs in
// (the check and the update are under a lock).
func TestTOTPReuseRace(t *testing.T) {
	h, _ := password.Hash("secret")
	s := &raceStore{u: raceUser{ID: "1", Email: "ada@example.com", Password: h}}
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey()}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, err := cache.New(app); err != nil {
		t.Fatal(err)
	}
	sessions, err := session.New(app, session.Driver{Name: "mem", Open: func(*anetos.App, session.Config) (cache.Store, error) { return cache.NewMemoryStore(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(app, s.users())
	if err != nil {
		t.Fatal(err)
	}
	ctx := app.Context(context.Background())
	setup, err := a.StartTwoFactor(ctx, s.get(), "ada")
	if err != nil {
		t.Fatal(err)
	}
	code, _ := auth.TwoFactorCode(setup.Secret, time.Now())
	if _, err := a.ConfirmTwoFactor(ctx, s.get(), code); err != nil {
		t.Fatal(err)
	}
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	r := srv.Router().Group("", sessions.Middleware, a.Middleware)
	r.Post("/login", func(c *web.Ctx) error {
		_, err := a.Attempt(c, "ada@example.com", "secret", false)
		if err != nil && !errors.Is(err, auth.ErrTwoFactorRequired) {
			return err
		}
		return c.NoContent()
	})
	r.Post("/challenge", func(c *web.Ctx) error {
		if _, err := a.AttemptTwoFactor(c, c.Request().FormValue("code")); err != nil {
			return err
		}
		return c.NoContent()
	})
	victim := &raceClient{h: srv.Router(), ctx: ctx, cookies: map[string]*http.Cookie{}}
	attacker := &raceClient{h: srv.Router(), ctx: ctx, cookies: map[string]*http.Cookie{}}
	victim.post(t, "/login", nil)
	attacker.post(t, "/login", nil)
	// The step after the one ConfirmTwoFactor used.
	next, _ := auth.TwoFactorCode(setup.Secret, time.Now().Add(30*time.Second))
	s.slow = true
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, c := range []*raceClient{victim, attacker} {
		wg.Go(func() { codes[i] = c.post(t, "/challenge", url.Values{"code": {next}}) })
	}
	wg.Wait()
	if codes[0] == http.StatusNoContent && codes[1] == http.StatusNoContent {
		t.Errorf("one code signed in two sessions: %v", codes)
	}
}
