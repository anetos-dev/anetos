// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/encryption"
)

func (s *store) set(id string, fn func(u *user)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.byID[id]
	fn(&v)
	s.byID[id] = v
}

func TestDisabledAccounts(t *testing.T) {
	s := newStore(t)
	a, b := newApp(t, s)
	login(b, "ada@example.com", "secret", true)
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("dashboard: %d", res.StatusCode)
	}

	// Disabled: signed out at the next request, the remember-me cookie
	// stops working, and signing in says why only once the password is
	// right. (API tokens: in the admin's tests, which have a database.)
	s.set("1", func(u *user) { u.Disabled = true })
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("a disabled user's session: %d", res.StatusCode)
	}
	if res := login(b, "ada@example.com", "wrong", false); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a wrong password: %d", res.StatusCode)
	}
	if res := login(b, "ada@example.com", "secret", false); res.StatusCode != http.StatusForbidden {
		t.Errorf("signing in disabled: %d", res.StatusCode)
	}
	if u, _ := s.users().ByID(context.Background(), "1"); !a.Disabled(u) {
		t.Error("Disabled")
	}
	b.drop("anetos_session")
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("remembered while disabled: %d", res.StatusCode)
	}
	ctx := a.ActAs(b.ctx, "1")
	if _, err := auth.CurrentID(ctx); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("ActAs a disabled user: %v", err)
	}

	// Enabled again: in again.
	s.set("1", func(u *user) { u.Disabled = false })
	if res := login(b, "ada@example.com", "secret", false); res.StatusCode != http.StatusSeeOther {
		t.Errorf("signing in enabled: %d", res.StatusCode)
	}
}

func TestSignOutEverywhere(t *testing.T) {
	s := newStore(t)
	a, phone := newApp(t, s)
	laptop := &browser{t: t, h: phone.h, ctx: phone.ctx, jar: mustJar()}
	login(phone, "ada@example.com", "secret", true)
	login(laptop, "ada@example.com", "secret", false)
	if !a.CanSignOutEverywhere() {
		t.Fatal("CanSignOutEverywhere")
	}
	u, _ := s.users().ByID(context.Background(), "1")
	if err := a.SignOutEverywhere(phone.ctx, u); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string]*browser{"phone": phone, "laptop": laptop} {
		if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
			t.Errorf("%s still signed in: %d", name, res.StatusCode)
		}
	}
	// The phone's remember-me cookie stopped working too: dropping the
	// session doesn't bring it back.
	phone.drop("anetos_session")
	if res := phone.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("remembered after signing out everywhere: %d", res.StatusCode)
	}
	if res := login(laptop, "ada@example.com", "secret", false); res.StatusCode != http.StatusSeeOther {
		t.Errorf("signing in again: %d", res.StatusCode)
	}
	if res := laptop.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Errorf("signed in again: %d", res.StatusCode)
	}

	// Without the hooks, it isn't available.
	users := s.users()
	users.SessionKey, users.SetSessionKey = nil, nil
	key, err := encryption.ParseKey(encryption.GenerateKey())
	if err != nil {
		t.Fatal(err)
	}
	enc, err := encryption.NewEncrypter(key)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := auth.NewWithConfig(a.Config(), users, enc, auth.WithInsecureCookies())
	if err != nil {
		t.Fatal(err)
	}
	if plain.CanSignOutEverywhere() || plain.SignOutEverywhere(context.Background(), u) == nil {
		t.Error("SignOutEverywhere without session keys")
	}
	users.SessionKey = s.users().SessionKey
	if _, err := auth.NewWithConfig(a.Config(), users, enc); err == nil {
		t.Error("SessionKey without SetSessionKey accepted")
	}
}

func TestImpersonate(t *testing.T) {
	s := newStore(t)
	_, b := newApp(t, s)
	if res := b.do(http.MethodPost, "/impersonate/1", nil); res.StatusCode != http.StatusUnauthorized && res.StatusCode != http.StatusSeeOther {
		t.Errorf("a guest impersonating: %d", res.StatusCode)
	}
	login(b, "bob@example.com", "secret", false)
	if res := b.do(http.MethodPost, "/impersonate/2", nil); res.StatusCode < 400 {
		t.Errorf("acting as oneself: %d", res.StatusCode)
	}
	if res := b.do(http.MethodPost, "/stop", nil); res.StatusCode != http.StatusConflict {
		t.Errorf("stop without impersonating: %d", res.StatusCode)
	}
	if res := b.do(http.MethodPost, "/impersonate/1", nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("impersonate: %d %s", res.StatusCode, res.Body)
	}
	if got := b.do(http.MethodGet, "/whoami", nil).Body; got != "1 by 2" {
		t.Errorf("whoami: %q", got)
	}
	if res := b.do(http.MethodPost, "/impersonate/3", nil); res.StatusCode < 400 {
		t.Errorf("nested: %d", res.StatusCode)
	}
	// No API token for the user acted as: it would outlive the act.
	if res := b.do(http.MethodPost, "/tokens", nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("a token while acting as another user: %d", res.StatusCode)
	}
	if res := b.do(http.MethodPost, "/stop", nil); res.StatusCode != http.StatusOK || res.Body != "2" {
		t.Errorf("stop: %d %q", res.StatusCode, res.Body)
	}
	if got := b.do(http.MethodGet, "/whoami", nil).Body; got != "2 by " {
		t.Errorf("whoami after: %q", got)
	}

	// A disabled user can't be acted as.
	s.set("3", func(u *user) { u.Disabled = true })
	if res := b.do(http.MethodPost, "/impersonate/3", nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("acting as a disabled user: %d", res.StatusCode)
	}
	// An impersonator who is signed out everywhere meanwhile loses both.
	b.do(http.MethodPost, "/impersonate/1", nil)
	s.set("2", func(u *user) { u.Key = "new" })
	if got := b.do(http.MethodGet, "/whoami", nil).Body; got != " by " {
		t.Errorf("whoami after the impersonator was signed out: %q", got)
	}
	// Logout ends both, and doesn't sign the user acted as out of their
	// remembered browsers.
	s.set("2", func(u *user) { u.Key = "" })
	phone := &browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}
	login(phone, "ada@example.com", "secret", true)
	login(b, "bob@example.com", "secret", false)
	b.do(http.MethodPost, "/impersonate/1", nil)
	b.do(http.MethodPost, "/logout", nil)
	if got := b.do(http.MethodGet, "/whoami", nil).Body; got != " by " {
		t.Errorf("whoami after logout: %q", got)
	}
	phone.drop("anetos_session")
	if res := phone.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Errorf("the user acted as was signed out of a remembered browser: %d", res.StatusCode)
	}
	// Stopping when the impersonator can't sign in any more signs out.
	login(b, "bob@example.com", "secret", false)
	b.do(http.MethodPost, "/impersonate/1", nil)
	s.set("2", func(u *user) { u.Disabled = true })
	if res := b.do(http.MethodPost, "/stop", nil); res.StatusCode < 400 {
		t.Errorf("stop with a disabled impersonator: %d", res.StatusCode)
	}
	if got := b.do(http.MethodGet, "/whoami", nil).Body; got != " by " {
		t.Errorf("whoami: %q", got)
	}
}
