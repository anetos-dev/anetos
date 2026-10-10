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
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/web"
)

// apiLoginRoutes are an API's sign-in, without sessions: the user's ID,
// or a two-factor challenge (202).
func apiLoginRoutes(r *web.Router, a *auth.Auth[*user]) {
	api := r.Group("/api/v1", a.Middleware)
	api.Post("/login", func(c *web.Ctx) error {
		u, err := a.AttemptCredentials(c, c.Request().FormValue("email"), c.Request().FormValue("password"))
		if challenge, ok := errors.AsType[*auth.TwoFactorChallenge](err); ok {
			return c.Text(http.StatusAccepted, challenge.Token)
		}
		if err != nil {
			return err
		}
		return c.Text(http.StatusOK, u.ID)
	})
	api.Post("/login/two-factor", func(c *web.Ctx) error {
		u, err := a.AttemptTwoFactorChallenge(c, c.Request().FormValue("challenge"), c.Request().FormValue("code"))
		if err != nil {
			return err
		}
		return c.Text(http.StatusOK, u.ID)
	})
	// Without the auth middleware: no client address.
	r.Post("/bare-login", func(c *web.Ctx) error {
		_, err := a.AttemptCredentials(c, "ada@example.com", "secret")
		return err
	})
}

func TestAttemptCredentials(t *testing.T) {
	s := newStore(t)
	a, b, app := newAppWith(t, s)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	auth.SetNow(a, clock)
	app.SetClock(clock)
	apiLogin := func(email, pw string) response {
		return b.do(http.MethodPost, "/api/v1/login", url.Values{"email": {email}, "password": {pw}})
	}
	if res := apiLogin("ada@example.com", "secret"); res.StatusCode != http.StatusOK || res.Body != "1" {
		t.Fatalf("login: %d %q", res.StatusCode, res.Body)
	}
	// Nothing was signed in to a session.
	if res := b.do(http.MethodGet, "/dashboard", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("a session after an API login: %d", res.StatusCode)
	}
	for _, c := range []struct{ email, pw string }{{"ada@example.com", "wrong"}, {"nobody@example.com", "secret"}, {"social@example.com", ""}} {
		if res := apiLogin(c.email, c.pw); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s/%s: %d", c.email, c.pw, res.StatusCode)
		}
	}
	// Attempt's throttling: AUTH_THROTTLE (5) a minute for a login.
	for range 3 {
		apiLogin("sam@example.com", "wrong")
	}
	if res := apiLogin("sam@example.com", "wrong"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("4th try: %d", res.StatusCode)
	}
	if res := apiLogin("sam@example.com", "secret"); res.StatusCode != http.StatusOK {
		t.Errorf("5th try, right: %d", res.StatusCode)
	}
	for range 5 {
		apiLogin("sam@example.com", "wrong")
	}
	if res := apiLogin("sam@example.com", "secret"); res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("after 5 wrong in a minute: %d", res.StatusCode)
	}
	s.set("2", func(u *user) { u.Disabled = true })
	if res := apiLogin("bob@example.com", "secret"); res.StatusCode != http.StatusForbidden {
		t.Errorf("disabled: %d", res.StatusCode)
	}
	if res := apiLogin("bob@example.com", "wrong"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("disabled, wrong password: %d", res.StatusCode)
	}
	if res := b.do(http.MethodPost, "/bare-login", nil); res.StatusCode != http.StatusInternalServerError {
		t.Errorf("without the middleware: %d", res.StatusCode)
	}
}

func TestTwoFactorChallenge(t *testing.T) {
	s := newStore(t)
	a, b, app := newAppWith(t, s)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	auth.SetNow(a, clock)
	app.SetClock(clock)
	login(b, "ada@example.com", "secret", false)
	secret, codes := setUp(t, b, now)
	b.do(http.MethodPost, "/logout", nil)
	now = now.Add(time.Minute)

	res := b.do(http.MethodPost, "/api/v1/login", url.Values{"email": {"ada@example.com"}, "password": {"secret"}})
	challenge := res.Body
	if res.StatusCode != http.StatusAccepted || challenge == "" || strings.Contains(challenge, "ada") {
		t.Fatalf("login: %d %q", res.StatusCode, res.Body)
	}
	if errors.Is(&auth.TwoFactorChallenge{}, auth.ErrTwoFactorRequired) != true {
		t.Error("a challenge isn't ErrTwoFactorRequired")
	}
	try := func(challenge, code string) response {
		return b.do(http.MethodPost, "/api/v1/login/two-factor", url.Values{"challenge": {challenge}, "code": {code}})
	}
	if res := try(challenge, "000000"); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a wrong code: %d", res.StatusCode)
	}
	code := auth.TOTP(secret, now)
	if res := try(challenge, code); res.StatusCode != http.StatusOK || res.Body != "1" {
		t.Fatalf("the code: %d %q", res.StatusCode, res.Body)
	}
	if res := try(challenge, code); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("the code again: %d", res.StatusCode)
	}
	// The challenge works again with a new code, or a recovery code
	// (once), within its 10 minutes.
	now = now.Add(time.Minute)
	if res := try(challenge, auth.TOTP(secret, now)); res.StatusCode != http.StatusOK {
		t.Errorf("a new code: %d", res.StatusCode)
	}
	if res := try(challenge, codes[0]); res.StatusCode != http.StatusOK {
		t.Errorf("a recovery code: %d", res.StatusCode)
	}
	if res := try(challenge, codes[0]); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a used recovery code: %d", res.StatusCode)
	}
	for _, bad := range []string{"", "nonsense", challenge[:len(challenge)-2] + "xx", a.PasswordResetToken(&user{ID: "1"})} {
		if res := try(bad, auth.TOTP(secret, now.Add(30*time.Second))); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("challenge %q: %d", bad, res.StatusCode)
		}
	}
	now = now.Add(9*time.Minute + time.Second)
	if res := try(challenge, auth.TOTP(secret, now)); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("an expired challenge: %d", res.StatusCode)
	}

	// A new password (or signing out everywhere) ends the challenges
	// made before it.
	challenge = b.do(http.MethodPost, "/api/v1/login", url.Values{"email": {"ada@example.com"}, "password": {"secret"}}).Body
	s.setPassword(t, "1", "secret")
	now = now.Add(time.Minute)
	if res := try(challenge, auth.TOTP(secret, now)); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a challenge from before the password changed: %d", res.StatusCode)
	}
	// So does signing out everywhere (a new session key).
	challenge = b.do(http.MethodPost, "/api/v1/login", url.Values{"email": {"ada@example.com"}, "password": {"secret"}}).Body
	ada, _ := s.users().ByID(b.ctx, "1")
	if err := a.SignOutEverywhere(b.ctx, ada); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if res := try(challenge, auth.TOTP(secret, now)); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a challenge from before signing out everywhere: %d", res.StatusCode)
	}
	// A disabled account: the code is right, the sign-in refused.
	challenge = b.do(http.MethodPost, "/api/v1/login", url.Values{"email": {"ada@example.com"}, "password": {"secret"}}).Body
	s.set("1", func(u *user) { u.Disabled = true })
	now = now.Add(time.Minute)
	if res := try(challenge, auth.TOTP(secret, now)); res.StatusCode != http.StatusForbidden {
		t.Errorf("disabled meanwhile: %d", res.StatusCode)
	}
}

// Wrong codes through a challenge count toward the day's 50, as the
// session's do.
func TestTwoFactorChallengeDailyCap(t *testing.T) {
	s := newStore(t)
	a, b, app := newAppWith(t, s)
	now := awayFromMidnight(time.Now())
	clock := func() time.Time { return now }
	auth.SetNow(a, clock)
	app.SetClock(clock)
	login(b, "ada@example.com", "secret", false)
	secret, _ := setUp(t, b, now)
	b.do(http.MethodPost, "/logout", nil)
	var challenge string
	for i := range 50 {
		if i%5 == 0 {
			now = now.Add(61 * time.Second)
			challenge = b.do(http.MethodPost, "/api/v1/login", url.Values{"email": {"ada@example.com"}, "password": {"secret"}}).Body
		}
		if res := b.do(http.MethodPost, "/api/v1/login/two-factor", url.Values{"challenge": {challenge}, "code": {"999999"}}); res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("try %d: %d", i, res.StatusCode)
		}
	}
	now = now.Add(61 * time.Second)
	challenge = b.do(http.MethodPost, "/api/v1/login", url.Values{"email": {"ada@example.com"}, "password": {"secret"}}).Body
	if res := b.do(http.MethodPost, "/api/v1/login/two-factor", url.Values{"challenge": {challenge}, "code": {auth.TOTP(secret, now)}}); res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("after 50 wrong codes: %d", res.StatusCode)
	}
}

func TestCheckPassword(t *testing.T) {
	s := newStore(t)
	a, b, app := newAppWith(t, s)
	// A fixed clock: the limits count in windows aligned to the minute,
	// and the real clock may cross into the next one between the tries.
	now := time.Date(2026, 10, 8, 12, 0, 30, 0, time.UTC)
	clock := func() time.Time { return now }
	auth.SetNow(a, clock)
	app.SetClock(clock)
	ctx := b.ctx // with the app's cache, for the limits
	ada, _ := s.users().ByID(ctx, "1")
	if err := a.CheckPassword(ctx, ada, "secret"); err != nil {
		t.Errorf("right: %v", err)
	}
	if err := a.CheckPassword(ctx, ada, "wrong"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("wrong: %v", err)
	}
	social, _ := s.users().ByID(ctx, "4")
	if err := a.CheckPassword(ctx, social, ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("a user without a password: %v", err)
	}
	for range 4 {
		_ = a.CheckPassword(ctx, ada, "wrong")
	}
	var throttled *auth.ThrottledError
	if err := a.CheckPassword(ctx, ada, "secret"); !errors.As(err, &throttled) {
		t.Errorf("after AUTH_THROTTLE tries in a minute: %v", err)
	}
}

func TestClientLink(t *testing.T) {
	s := newStore(t)
	a, _, _ := newAppWith(t, s, "AUTH_CLIENT_URL", "https://app.example.com/")
	link, err := a.ClientLink("/reset-password", url.Values{"token": {"a b&c"}})
	if err != nil || link != "https://app.example.com/reset-password?token=a+b%26c" {
		t.Errorf("link %q, %v", link, err)
	}
	if link, _ := a.ClientLink("verify", nil); link != "https://app.example.com/verify" {
		t.Errorf("without a query: %q", link)
	}
	b, _ := newApp(t, s)
	if _, err := b.ClientLink("/x", nil); err == nil || !strings.Contains(err.Error(), "AUTH_CLIENT_URL") {
		t.Errorf("without AUTH_CLIENT_URL: %v", err)
	}
	for _, bad := range []string{"app.example.com", "ftp://app.example.com", "https://app.example.com/?a=1", "https:///x", "https://u:p@app.example.com"} {
		if _, err := auth.LoadConfig(config.Map{"AUTH_CLIENT_URL": bad}); err == nil {
			t.Errorf("AUTH_CLIENT_URL=%s accepted", bad)
		}
	}
	if cfg, err := auth.LoadConfig(config.Map{"AUTH_CLIENT_URL": "http://localhost:5173"}); err != nil || cfg.ClientURL != "http://localhost:5173" {
		t.Errorf("localhost: %+v %v", cfg, err)
	}
}

// Require's 401 asks for a Bearer token behind TokenMiddleware, with or
// without a session on the route.
func TestRequireBearerChallenge(t *testing.T) {
	s := newStore(t)
	_, b := newApp(t, s)
	for path, want := range map[string]string{"/session-api/me": "Bearer", "/api/me": "Bearer", "/dashboard": ""} {
		res := b.do(http.MethodGet, path, nil, "Accept", "application/json")
		if res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") != want {
			t.Errorf("%s: %d %q", path, res.StatusCode, res.Header.Get("WWW-Authenticate"))
		}
	}
}
