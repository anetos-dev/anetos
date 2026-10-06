// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/web"
)

// twoFactorRoutes adds the two-factor and confirmation routes of the
// tests' app.
func twoFactorRoutes(r *web.Router, a *auth.Auth[*user], s *store) {
	r.Post("/two-factor-challenge", func(c *web.Ctx) error {
		if _, err := a.AttemptTwoFactor(c, c.Request().FormValue("code")); err != nil {
			return err
		}
		return c.Redirect(http.StatusSeeOther, auth.Intended(c, "/home"))
	})
	r.Get("/pending", func(c *web.Ctx) error {
		if a.TwoFactorPending(c) {
			return c.Text(http.StatusOK, "pending")
		}
		return c.Text(http.StatusOK, "none")
	})
	r.Post("/social/{id}", func(c *web.Ctx) error {
		u, err := s.users().ByID(c, c.Param("id"))
		if err != nil {
			return err
		}
		if err := a.SignIn(c, u, false); err != nil {
			return err
		}
		return c.NoContent()
	})
	private := r.Group("", a.Require)
	private.Post("/two-factor/start", func(c *web.Ctx) error {
		u, _ := auth.User[*user](c)
		setup, err := a.StartTwoFactor(c, u, u.Email)
		if err != nil {
			return err
		}
		return c.Text(http.StatusOK, setup.Secret+" "+setup.URI)
	})
	private.Post("/two-factor/confirm", func(c *web.Ctx) error {
		u, _ := auth.User[*user](c)
		codes, err := a.ConfirmTwoFactor(c, u, c.Request().FormValue("code"))
		if err != nil {
			return err
		}
		return c.Text(http.StatusOK, strings.Join(codes, " "))
	})
	private.Post("/confirm-password", func(c *web.Ctx) error {
		if err := a.ConfirmPassword(c, c.Request().FormValue("password")); err != nil {
			return err
		}
		return c.Redirect(http.StatusSeeOther, auth.Intended(c, "/home"))
	})
	private.Post("/password", func(c *web.Ctx) error {
		u, _ := auth.User[*user](c)
		if err := a.ChangePassword(c, u, c.Request().FormValue("current"), c.Request().FormValue("new")); err != nil {
			return err
		}
		return c.NoContent()
	})
	danger := private.Group("", a.RequireConfirmed)
	danger.Get("/danger", func(c *web.Ctx) error { return c.Text(http.StatusOK, "dangerous page") })
	danger.Post("/danger", func(c *web.Ctx) error { return c.Text(http.StatusOK, "done") })
}

func TestTOTP(t *testing.T) {
	// RFC 6238, appendix B (SHA-1).
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // "12345678901234567890"
	for unix, want := range map[int64]string{59: "94287082", 1111111109: "07081804", 1111111111: "14050471",
		1234567890: "89005924", 2000000000: "69279037", 20000000000: "65353130"} {
		if got := auth.TOTPDigits([]byte("12345678901234567890"), time.Unix(unix, 0), 8); got != want {
			t.Errorf("TOTP at %d = %s, want %s", unix, got, want)
		}
		if got, err := auth.TwoFactorCode(strings.ToLower(secret), time.Unix(unix, 0)); err != nil || got != want[2:] {
			t.Errorf("TwoFactorCode at %d = %s, %v", unix, got, err)
		}
	}
	if _, err := auth.TwoFactorCode("not base32!", time.Now()); err == nil {
		t.Error("a bad secret")
	}
}

// setUp turns on two-factor sign-in for the signed-in user, at now, and
// returns the secret and the recovery codes.
func setUp(t *testing.T, b *browser, now time.Time) (string, []string) {
	t.Helper()
	res := b.do(http.MethodPost, "/two-factor/start", url.Values{})
	secret, uri, _ := strings.Cut(res.Body, " ")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(uri, "otpauth://totp/anetos:ada%40example.com?") ||
		!strings.Contains(uri, "secret="+secret) || !strings.Contains(uri, "issuer=anetos") {
		t.Fatalf("start: %d %q", res.StatusCode, res.Body)
	}
	if res := b.do(http.MethodPost, "/two-factor/confirm", url.Values{"code": {"000000"}}); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a wrong code: %d", res.StatusCode)
	}
	res = b.do(http.MethodPost, "/two-factor/confirm", url.Values{"code": {auth.TOTP(secret, now)}})
	codes := strings.Fields(res.Body)
	if res.StatusCode != http.StatusOK || len(codes) != 8 || len(codes[0]) != 11 || codes[0][5] != '-' {
		t.Fatalf("confirm: %d %q", res.StatusCode, res.Body)
	}
	return secret, codes
}

func TestTwoFactor(t *testing.T) {
	s := newStore(t)
	a, b, app := newAppWith(t, s)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	auth.SetNow(a, clock)
	app.SetClock(clock)
	login(b, "ada@example.com", "secret", false)
	secret, codes := setUp(t, b, now)
	u, _ := s.users().ByID(b.ctx, "1")
	if st, err := a.TwoFactor(u); err != nil || !st.On || st.RecoveryCodes != 8 {
		t.Errorf("status %+v, %v", st, err)
	}
	if strings.Contains(u.TwoF, secret) || strings.Contains(u.TwoF, codes[0]) {
		t.Error("the secret or a code is stored in the clear")
	}
	// Again: it's on.
	if res := b.do(http.MethodPost, "/two-factor/start", url.Values{}); res.StatusCode != http.StatusConflict {
		t.Errorf("start when on: %d", res.StatusCode)
	}
	b.do(http.MethodPost, "/logout", nil)
	now = now.Add(time.Minute)

	// The password isn't enough now.
	if res := login(b, "ada@example.com", "secret", false); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login: %d", res.StatusCode)
	}
	if b.do(http.MethodGet, "/pending", nil).Body != "pending" {
		t.Error("no pending sign-in")
	}
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("signed in before the code: %d", res.StatusCode)
	}
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {"123456"}}); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a wrong code: %d", res.StatusCode)
	}
	code := auth.TOTP(secret, now.Add(-30*time.Second)) // a clock a step behind
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {code[:3] + " " + code[3:]}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("the code: %d %s", res.StatusCode, res.Body)
	}
	if body := b.do(http.MethodGet, "/dashboard", nil).Body; body != "hello ada@example.com" {
		t.Errorf("dashboard: %q", body)
	}
	b.do(http.MethodPost, "/logout", nil)

	// A code works once, and an earlier one doesn't either.
	login(b, "ada@example.com", "secret", false)
	for _, c := range []string{code, auth.TOTP(secret, now.Add(-60*time.Second))} {
		if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {c}}); res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("a used code: %d", res.StatusCode)
		}
	}
	// A recovery code, as typed: once.
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {" " + strings.ToUpper(codes[2]) + " "}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("a recovery code: %d", res.StatusCode)
	}
	u, _ = s.users().ByID(b.ctx, "1")
	if st, _ := a.TwoFactor(u); st.RecoveryCodes != 7 {
		t.Errorf("%d recovery codes left", st.RecoveryCodes)
	}
	b.do(http.MethodPost, "/logout", nil)
	login(b, "ada@example.com", "secret", false)
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {codes[2]}}); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a used recovery code: %d", res.StatusCode)
	}

	// Throttled: AUTH_THROTTLE (5) attempts a minute, the rest refused.
	for range 4 {
		b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {"999999"}})
	}
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {auth.TOTP(secret, now)}}); res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("throttled: %d", res.StatusCode)
	}

	// The sign-in waits 10 minutes.
	now = now.Add(11 * time.Minute)
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {auth.TOTP(secret, now)}}); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("expired: %d", res.StatusCode)
	}
	// A password change meanwhile ends it too.
	login(b, "ada@example.com", "secret", false)
	s.setPassword(t, "1", "changed")
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {auth.TOTP(secret, now)}}); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("after a password change: %d", res.StatusCode)
	}
	if b.do(http.MethodGet, "/pending", nil).Body != "none" {
		t.Error("still pending")
	}

	// Other sign-in methods (SignIn) ask for the code too; Login doesn't.
	if res := b.do(http.MethodPost, "/social/1", nil); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("SignIn: %d", res.StatusCode)
	}
	now = now.Add(time.Minute)
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {auth.TOTP(secret, now)}}); res.StatusCode != http.StatusSeeOther {
		t.Errorf("SignIn's code: %d", res.StatusCode)
	}

	// New recovery codes replace the old; turning it off ends it.
	u, _ = s.users().ByID(b.ctx, "1")
	fresh, err := a.NewRecoveryCodes(b.ctx, u)
	if err != nil || len(fresh) != 8 || fresh[0] == codes[0] {
		t.Errorf("NewRecoveryCodes: %v, %v", fresh, err)
	}
	if err := a.DisableTwoFactor(b.ctx, u); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.TwoFactor(u); st.On || st.Started {
		t.Errorf("after DisableTwoFactor: %+v", st)
	}
	if _, err := a.NewRecoveryCodes(b.ctx, u); !errors.Is(err, auth.ErrTwoFactorOff) {
		t.Errorf("NewRecoveryCodes when off: %v", err)
	}
	b.do(http.MethodPost, "/logout", nil)
	if res := login(b, "ada@example.com", "changed", false); res.StatusCode != http.StatusSeeOther {
		t.Errorf("login without two-factor: %d", res.StatusCode)
	}
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {"123456"}}); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no pending sign-in: %d", res.StatusCode)
	}
}

func TestStartedTwoFactor(t *testing.T) {
	s := newStore(t)
	a, b := newApp(t, s)
	u, _ := s.users().ByID(b.ctx, "1")
	if _, err := a.StartedTwoFactor(u, "ada"); !errors.Is(err, auth.ErrTwoFactorOff) {
		t.Errorf("none started: %v", err)
	}
	started, err := a.StartTwoFactor(b.ctx, u, "ada")
	if err != nil {
		t.Fatal(err)
	}
	again, err := a.StartedTwoFactor(u, "ada")
	if err != nil || again != started {
		t.Errorf("StartedTwoFactor = %+v, %v; want %+v", again, err, started)
	}
	if st, _ := a.TwoFactor(u); st.On || !st.Started {
		t.Errorf("status %+v", st)
	}
	if _, err := a.ConfirmTwoFactor(b.ctx, u, auth.TOTP(started.Secret, time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartedTwoFactor(u, "ada"); !errors.Is(err, auth.ErrTwoFactorOn) {
		t.Errorf("once on: %v", err)
	}
	if _, err := a.ConfirmTwoFactor(b.ctx, u, "123456"); !errors.Is(err, auth.ErrTwoFactorOn) {
		t.Errorf("confirm twice: %v", err)
	}
	if err := a.DisableTwoFactor(b.ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmTwoFactor(b.ctx, u, "123456"); !errors.Is(err, auth.ErrTwoFactorOff) {
		t.Errorf("confirm without a start: %v", err)
	}
}

func TestTwoFactorPendingSignsOut(t *testing.T) {
	s := newStore(t)
	a, b := newApp(t, s)
	login(b, "ada@example.com", "secret", false)
	setUp(t, b, time.Now())
	// Bob, signed in, signs in as Ada: he is signed out while it waits.
	other := &browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}
	login(other, "bob@example.com", "secret", false)
	if res := login(other, "ada@example.com", "secret", false); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login: %d", res.StatusCode)
	}
	if res := other.do(http.MethodGet, "/id", nil); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("still signed in: %d %s", res.StatusCode, res.Body)
	}
	// A state that can't be read fails closed.
	u, _ := s.users().ByID(b.ctx, "1")
	u.TwoF = "garbage"
	if _, err := a.TwoFactor(u); err == nil {
		t.Error("a corrupt state")
	}
}

func TestConfirmPassword(t *testing.T) {
	s := newStore(t)
	a, b, app := newAppWith(t, s)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	auth.SetNow(a, clock)
	app.SetClock(clock)
	login(b, "ada@example.com", "secret", false)

	// A page asks first, and comes back after.
	res := b.do(http.MethodGet, "/danger?x=1", nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/confirm-password" {
		t.Fatalf("unconfirmed: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res := b.do(http.MethodPost, "/danger", url.Values{}, "Accept", "application/json"); res.StatusCode != http.StatusLocked {
		t.Errorf("JSON: %d", res.StatusCode)
	}
	if res := b.do(http.MethodPost, "/confirm-password", url.Values{"password": {"wrong"}}); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a wrong password: %d", res.StatusCode)
	}
	res = b.do(http.MethodPost, "/confirm-password", url.Values{"password": {"secret"}})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/danger?x=1" {
		t.Fatalf("confirmed: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if body := b.do(http.MethodPost, "/danger", url.Values{}).Body; body != "done" {
		t.Errorf("confirmed: %q", body)
	}
	// It holds for AUTH_CONFIRM_TTL (15m).
	now = now.Add(16 * time.Minute)
	res = b.do(http.MethodPost, "/danger", url.Values{}, "Referer", "http://app.test/settings?tab=2")
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("expired: %d", res.StatusCode)
	}
	// A form goes back to its page.
	res = b.do(http.MethodPost, "/confirm-password", url.Values{"password": {"secret"}})
	if res.Header.Get("Location") != "/settings?tab=2" {
		t.Errorf("back to %s", res.Header.Get("Location"))
	}
	// Signing in again forgets it, as does acting as another user.
	b.do(http.MethodPost, "/logout", nil)
	login(b, "ada@example.com", "secret", false)
	if res := b.do(http.MethodGet, "/danger", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("after signing in again: %d", res.StatusCode)
	}
	b.do(http.MethodPost, "/confirm-password", url.Values{"password": {"secret"}})
	b.do(http.MethodPost, "/impersonate/3", nil)
	if res := b.do(http.MethodGet, "/danger", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("acting as another user: %d", res.StatusCode)
	}
	if res := b.do(http.MethodPost, "/confirm-password", url.Values{"password": {"secret"}}); res.StatusCode != http.StatusForbidden {
		t.Errorf("confirming while acting: %d", res.StatusCode)
	}
	// A user without a password can't confirm one.
	b.do(http.MethodPost, "/stop", nil)
	b.do(http.MethodPost, "/logout", nil)
	other := &browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}
	other.do(http.MethodPost, "/social/4", nil)
	if res := other.do(http.MethodPost, "/confirm-password", url.Values{"password": {""}}); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no password: %d", res.StatusCode)
	}
	// Signing in again confirms it, for AUTH_CONFIRM_TTL.
	if body := other.do(http.MethodGet, "/danger", nil).Body; body != "dangerous page" {
		t.Errorf("signed in afresh without a password: %q", body)
	}
	now = now.Add(16 * time.Minute)
	if res := other.do(http.MethodGet, "/danger", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("later: %d", res.StatusCode)
	}
	if a.PasswordConfirmed(b.ctx) {
		t.Error("confirmed outside a request")
	}
}

func rememberCookie(b *browser) *http.Cookie {
	for _, c := range b.jar.Cookies(base) {
		if c.Name == "anetos_remember" {
			return c
		}
	}
	return nil
}

// Turning it on ends the remember-me cookies and other sessions signed in
// without a code, keeping this one; remember me then waits for the code.
func TestTwoFactorAndRememberMe(t *testing.T) {
	s := newStore(t)
	a, b, app := newAppWith(t, s)
	now := time.Now()
	app.SetClock(func() time.Time { return now })
	login(b, "ada@example.com", "secret", true)
	old := rememberCookie(b)
	other := &browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}
	login(other, "ada@example.com", "secret", false)
	if old == nil {
		t.Fatal("no remember-me cookie")
	}
	secret, _ := setUp(t, b, now)
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Errorf("this session after turning it on: %d", res.StatusCode)
	}
	if res := other.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("another session after turning it on: %d", res.StatusCode)
	}
	stolen := &browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}
	stolen.jar.SetCookies(base, []*http.Cookie{old})
	if res := stolen.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("an old remember-me cookie: %d", res.StatusCode)
	}

	// Remember me with a code: no cookie until the code.
	b.do(http.MethodPost, "/logout", nil)
	now = now.Add(time.Minute)
	login(b, "ada@example.com", "secret", true)
	if rememberCookie(b) != nil {
		t.Error("a remember-me cookie before the code")
	}
	b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {auth.TOTP(secret, now)}})
	if rememberCookie(b) == nil {
		t.Fatal("no remember-me cookie after the code")
	}
	b.drop("anetos_session")
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Errorf("remembered: %d", res.StatusCode)
	}

	// Disabled while the sign-in waits: refused.
	b.do(http.MethodPost, "/logout", nil)
	now = now.Add(time.Minute)
	login(b, "ada@example.com", "secret", false)
	s.mu.Lock()
	v := s.byID["1"]
	v.Disabled = true
	s.byID["1"] = v
	s.mu.Unlock()
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {auth.TOTP(secret, now)}}); res.StatusCode != http.StatusForbidden {
		t.Errorf("disabled: %d", res.StatusCode)
	}
	_ = a
}

// Wrong codes are capped at 50 a day, beyond AUTH_THROTTLE a minute.
func TestTwoFactorDailyCap(t *testing.T) {
	s := newStore(t)
	_, b, app := newAppWith(t, s)
	now := time.Now()
	app.SetClock(func() time.Time { return now })
	login(b, "ada@example.com", "secret", false)
	secret, _ := setUp(t, b, now)
	b.do(http.MethodPost, "/logout", nil)
	for i := range 50 {
		if i%5 == 0 {
			now = now.Add(61 * time.Second)
			login(b, "ada@example.com", "secret", false)
		}
		if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {"999999"}}); res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("try %d: %d", i, res.StatusCode)
		}
	}
	now = now.Add(61 * time.Second)
	login(b, "ada@example.com", "secret", false)
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {auth.TOTP(secret, now)}}); res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("after 50 wrong codes: %d", res.StatusCode)
	}
	now = now.Add(25 * time.Hour)
	login(b, "ada@example.com", "secret", false)
	if res := b.do(http.MethodPost, "/two-factor-challenge", url.Values{"code": {auth.TOTP(secret, now)}}); res.StatusCode != http.StatusSeeOther {
		t.Errorf("a day later: %d", res.StatusCode)
	}
}

func TestChangePassword(t *testing.T) {
	s := newStore(t)
	_, b, app := newAppWith(t, s)
	now := time.Now()
	app.SetClock(func() time.Time { return now })
	login(b, "ada@example.com", "secret", true)
	other := &browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}
	login(other, "ada@example.com", "secret", false)

	if res := b.do(http.MethodPost, "/password", url.Values{"current": {"wrong"}, "new": {"new secret"}}); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a wrong current password: %d", res.StatusCode)
	}
	if res := b.do(http.MethodPost, "/password", url.Values{"current": {"secret"}, "new": {"new secret"}}); res.StatusCode != http.StatusNoContent {
		t.Fatalf("changed: %d %s", res.StatusCode, res.Body)
	}
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Errorf("this session after the change: %d", res.StatusCode)
	}
	if res := other.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("another session after the change: %d", res.StatusCode)
	}
	// This browser stays remembered, with a new cookie.
	if c := rememberCookie(b); c == nil {
		t.Error("the remember-me cookie is gone")
	} else {
		remembered := &browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}
		remembered.jar.SetCookies(base, []*http.Cookie{c})
		if res := remembered.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
			t.Errorf("the new remember-me cookie: %d", res.StatusCode)
		}
	}
	b.do(http.MethodPost, "/logout", nil)
	if res := login(b, "ada@example.com", "secret", false); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("the old password: %d", res.StatusCode)
	}
	if res := login(b, "ada@example.com", "new secret", false); res.StatusCode != http.StatusSeeOther {
		t.Errorf("the new password: %d", res.StatusCode)
	}

	// Acting as someone: not theirs to change.
	b.do(http.MethodPost, "/impersonate/3", nil)
	if res := b.do(http.MethodPost, "/password", url.Values{"current": {"secret"}, "new": {"x"}}); res.StatusCode != http.StatusForbidden {
		t.Errorf("acting as someone: %d", res.StatusCode)
	}

	// Without a password: one is set after a fresh sign-in.
	social := &browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}
	social.do(http.MethodPost, "/social/4", nil)
	if res := social.do(http.MethodPost, "/password", url.Values{"new": {"first password"}}); res.StatusCode != http.StatusNoContent {
		t.Fatalf("a first password: %d", res.StatusCode)
	}
	if res := social.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Errorf("still signed in: %d", res.StatusCode)
	}
	if res := login(&browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}, "social@example.com", "first password", false); res.StatusCode != http.StatusSeeOther {
		t.Errorf("signing in with it: %d", res.StatusCode)
	}
	late := &browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}
	s.mu.Lock()
	v := s.byID["4"]
	v.Password = ""
	s.byID["4"] = v
	s.mu.Unlock()
	late.do(http.MethodPost, "/social/4", nil)
	now = now.Add(16 * time.Minute)
	if res := late.do(http.MethodPost, "/password", url.Values{"new": {"x"}}); res.StatusCode != http.StatusLocked {
		t.Errorf("long after signing in: %d", res.StatusCode)
	}
}

// With sessions kept on the server, a copy of the session's cookie taken
// before a change of password doesn't work after it.
func TestChangePasswordEndsCopiesOfTheSession(t *testing.T) {
	s := newStore(t)
	_, b, _ := newAppWith(t, s, "SESSION_DRIVER", "mem")
	login(b, "ada@example.com", "secret", false)
	thief := &browser{t: t, h: b.h, ctx: b.ctx, jar: mustJar()}
	thief.jar.SetCookies(base, b.jar.Cookies(base))
	if res := thief.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("the copy before: %d", res.StatusCode)
	}
	if res := b.do(http.MethodPost, "/password", url.Values{"current": {"secret"}, "new": {"new secret"}}); res.StatusCode != http.StatusNoContent {
		t.Fatalf("changed: %d", res.StatusCode)
	}
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusOK {
		t.Errorf("this browser: %d", res.StatusCode)
	}
	if res := thief.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("the copy after: %d", res.StatusCode)
	}
}

// Reset links end when the user is signed out elsewhere (a new session
// key), as after a change of email address.
func TestSignOutOthersEndsResetLinks(t *testing.T) {
	s := newStore(t)
	a, b, _ := newAppWith(t, s)
	u, _ := s.users().ByID(b.ctx, "1")
	tok := a.PasswordResetToken(u)
	if _, err := a.CheckPasswordResetToken(b.ctx, tok); err != nil {
		t.Fatal(err)
	}
	if err := a.SignOutOthers(b.ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CheckPasswordResetToken(b.ctx, tok); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("after SignOutOthers: %v", err)
	}
}

func TestEmailRevertToken(t *testing.T) {
	s := newStore(t)
	a, b, app := newAppWith(t, s)
	now := time.Now()
	app.SetClock(func() time.Time { return now })
	u, _ := s.users().ByID(b.ctx, "1")
	tok := a.EmailRevertToken(u, "ada@example.com", "new@example.com")
	got, old, nu, err := a.CheckEmailRevertToken(b.ctx, tok)
	if err != nil || got.ID != "1" || old != "ada@example.com" || nu != "new@example.com" {
		t.Fatalf("CheckEmailRevertToken = %v %q %q %v", got, old, nu, err)
	}
	// Not a verification token, nor the other way round.
	if _, _, err := a.CheckVerificationToken(b.ctx, tok); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("as a verification token: %v", err)
	}
	if _, _, _, err := a.CheckEmailRevertToken(b.ctx, a.VerificationToken(u, "new@example.com")); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("a verification token: %v", err)
	}
	// AUTH_REVERT_TTL: 7 days.
	now = now.Add(6 * 24 * time.Hour)
	if _, _, _, err := a.CheckEmailRevertToken(b.ctx, tok); err != nil {
		t.Errorf("after 6 days: %v", err)
	}
	now = now.Add(2 * 24 * time.Hour)
	if _, _, _, err := a.CheckEmailRevertToken(b.ctx, tok); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("after 8 days: %v", err)
	}
}
