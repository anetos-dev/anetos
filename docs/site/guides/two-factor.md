---
title: Two-factor sign-in and password confirmation
since: v0.3.0
group: "Accounts and security"
weight: 304
---

# Two-factor sign-in and password confirmation

Ask for more than a password: a code from an authenticator app (Google
Authenticator, 1Password, Authy… : TOTP, RFC 6238) after it, with
recovery codes for a lost phone; and the password again before
sensitive pages. Package `auth` does the parts that must be right
(secrets encrypted, codes used once, throttling); your handlers and
pages call it. The complete app is
[`examples/auth`](../../../examples/auth).

An app made with [`make:auth`](accounts.md) has all of this already: a
"Two-factor sign-in" link on the dashboard, the code after the password,
and the password asked again before turning it on or off. This guide
explains that code, and how to add it to an app of your own.

## Before you start

Set up [authentication](authentication.md) first. Two-factor sign-in
keeps its state in the users table: add a column for it (text, up to
1,024 characters, empty by default):

```go
// illustrative: in the users table's migration
t.String("two_factor", 1024).Default("")
```

## Steps

### 1. Store the state

`auth.Users` gets two functions: `TwoFactor` returns the stored state
of a user, `SetTwoFactor` stores a new one. Package `auth` writes it:
the secret encrypted with `APP_KEY` (for that user only), the recovery
codes hashed.

```go
// users tells package auth how to find and update users.
var users = auth.Users[*User]{
	ByID: func(ctx context.Context, id string) (*User, error) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, auth.ErrNoUser
		}
		u, err := db.Find[User](ctx, n) // db.ErrNotFound: no such user
		return &u, err
	},
	ByLogin: func(ctx context.Context, email string) (*User, error) {
		u, err := db.Query[User](ctx).Where(colEmail.Eq(strings.ToLower(email))).First()
		return &u, err
	},
	RememberToken: func(u *User) string { return u.RememberToken },
	SetRememberToken: func(ctx context.Context, u *User, token string) error {
		_, err := db.Query[User](ctx).Where(colID.Eq(u.ID)).Update(colRemember.Set(token))
		return err
	},
	SetPassword: func(ctx context.Context, u *User, hash string) error {
		_, err := db.Query[User](ctx).Where(colID.Eq(u.ID)).Update(colPassword.Set(hash))
		return err
	},
	// Two-factor sign-in: package auth stores its state (the secret
	// encrypted with APP_KEY, the recovery codes hashed) in a column.
	TwoFactor: func(u *User) string { return u.TwoFactor },
	SetTwoFactor: func(ctx context.Context, u *User, state string) error {
		u.TwoFactor = state
		_, err := db.Query[User](ctx).Where(colID.Eq(u.ID)).Update(colTwoF.Set(state))
		return err
	},
}
```

(Copied from [`examples/auth/users.go`](../../../examples/auth/users.go), region `users`.)

### 2. Let users turn it on

Turning it on takes two steps. `a.StartTwoFactor(ctx, u, account)`
stores a new secret and returns it, with its `otpauth://` URI: the user
scans it as a QR code (package [`qr`](https://pkg.go.dev/anetos.dev/anetos/qr) draws
one as SVG) or types the key. `a.ConfirmTwoFactor(ctx, u, code)` turns
it on once the user types the code their app shows, and returns eight
recovery codes, to show once. Until then, `a.StartedTwoFactor` shows the
same setup again; `a.TwoFactor(u)` says where it stands.

```go
// TwoFactor shows two-factor sign-in: off, being set up (a QR code for
// the authenticator app), or on; and the recovery codes, once.
func (h Accounts) TwoFactor(c *web.Ctx) error {
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	st, err := h.auth.TwoFactor(u)
	if err != nil {
		return err
	}
	data := map[string]any{"On": st.On, "Left": st.RecoveryCodes, "Codes": strings.Fields(view.Flash(c, "codes"))}
	if st.Started {
		setup, err := h.auth.StartedTwoFactor(u, u.Email)
		if err != nil {
			return err
		}
		svg, err := qr.SVG(setup.URI, qr.M, 200) // otpauth://totp/…
		if err != nil {
			return err
		}
		data["Secret"], data["QR"] = setup.Secret, template.HTML(svg) //nolint:gosec // package qr's markup
	}
	return render(c, "two-factor", data)
}

// StartTwoFactor makes a new secret, shown by TwoFactor until confirmed.
func (h Accounts) StartTwoFactor(c *web.Ctx) error {
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	if _, err := h.auth.StartTwoFactor(c, u, u.Email); err != nil && !errors.Is(err, auth.ErrTwoFactorOn) {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/two-factor")
}

// ConfirmTwoFactor turns it on with a code of the app, and shows the
// recovery codes, once.
func (h Accounts) ConfirmTwoFactor(c *web.Ctx, in CodeInput) (web.Responder, error) {
	u, err := auth.Current[*User](c)
	if err != nil {
		return nil, err
	}
	codes, err := h.auth.ConfirmTwoFactor(c, u, in.Code)
	switch {
	case errors.Is(err, auth.ErrInvalidCode):
		return nil, validate.Fail("code", "That code isn't right.")
	case err != nil:
		return nil, err
	}
	c.Session().Flash("codes", strings.Join(codes, " "))
	return web.Redirect("/two-factor"), nil
}

// DisableTwoFactor turns it off.
func (h Accounts) DisableTwoFactor(c *web.Ctx) error {
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	if err := h.auth.DisableTwoFactor(c, u); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/two-factor")
}
```

(Copied from [`examples/auth/main.go`](../../../examples/auth/main.go), region `two-factor`.)

The issuer the app shows is `APP_NAME`. `a.NewRecoveryCodes(ctx, u)`
replaces the recovery codes; `a.DisableTwoFactor(ctx, u)` turns it off
(the admin's users pages can too, for someone who lost their phone).

### 3. Ask for the code at sign-in

For a user with it on, `a.Attempt` checks the password, then stops:
it returns `auth.ErrTwoFactorRequired`, and the sign-in waits, in the
session, for 10 minutes. Send the user to the code page
(`AUTH_CHALLENGE_URL`, default `/two-factor-challenge`):

```go
func (h Accounts) Login(c *web.Ctx, in LoginInput) (web.Responder, error) {
	_, err := h.auth.Attempt(c, in.Email, in.Password, in.Remember)
	var throttled *auth.ThrottledError
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return nil, validate.Fail("email", "These credentials don't match our records.")
	case errors.As(err, &throttled):
		return nil, validate.Fail("email", "Too many login attempts. Try again in a minute.")
	case errors.Is(err, auth.ErrTwoFactorRequired):
		return web.Redirect("/two-factor-challenge"), nil // the password was right: now the code
	case err != nil:
		return nil, err
	}
	return web.Redirect(auth.Intended(c, h.auth.Config().HomeURL)), nil // the page they wanted, or AUTH_HOME_URL
}

func (h Accounts) Logout(c *web.Ctx) error {
	if err := h.auth.Logout(c); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/login")
}
```

(Copied from [`examples/auth/main.go`](../../../examples/auth/main.go), region `login`.)

There, `a.AttemptTwoFactor(ctx, code)` finishes the sign-in with the
app's code or a recovery code, which is then used up:

```go
// Challenge finishes a sign-in waiting for a code (after Login).
func (h Accounts) Challenge(c *web.Ctx, in CodeInput) (web.Responder, error) {
	_, err := h.auth.AttemptTwoFactor(c, in.Code)
	var throttled *auth.ThrottledError
	switch {
	case errors.Is(err, auth.ErrInvalidCode):
		return nil, validate.Fail("code", "That code isn't right.")
	case errors.As(err, &throttled):
		return nil, validate.Fail("code", "Too many tries. Try again in a minute.")
	case errors.Is(err, auth.ErrNoPendingSignIn): // none, or it expired
		return web.Redirect("/login"), nil
	case err != nil:
		return nil, err
	}
	return web.Redirect(auth.Intended(c, h.auth.Config().HomeURL)), nil
}
```

(Copied from [`examples/auth/main.go`](../../../examples/auth/main.go), region `challenge`.)

Other ways of signing in ask for the code too: sign-in with Google or
GitHub ([social login](social-login.md)) sends users with it on to
`AUTH_CHALLENGE_URL`. In your own sign-in code, use `a.SignIn(ctx, u,
remember)`, which does the same; `a.Login` signs in without asking
(after registration, say). A remember-me cookie, given after the code,
keeps the user signed in without it.

An API without sessions (since v0.4) gets the code in a second request:
`a.AttemptCredentials` answers a `*auth.TwoFactorChallenge`, whose
`Token` the client sends back with the code to
`a.AttemptTwoFactorChallenge`, under the same limits
([Add accounts to an API](api-accounts.md#4-two-factor-sign-in)).

### 4. Ask for the password again

Before sensitive pages (turning two-factor sign-in on or off, changing
the email address, deleting the account), ask for the password again
with the `a.RequireConfirmed` middleware: users who haven't confirmed it
in the last `AUTH_CONFIRM_TTL` (15 minutes) go to `AUTH_CONFIRM_URL`
(`/confirm-password`), then back. API clients and htmx requests get 423.

```go
// ConfirmPassword checks the password again (it holds for
// AUTH_CONFIRM_TTL), then goes back to the page that asked for it.
func (h Accounts) ConfirmPassword(c *web.Ctx, in PasswordInput) (web.Responder, error) {
	switch err := h.auth.ConfirmPassword(c, in.Password); {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return nil, validate.Fail("password", "That isn't your password.")
	case err != nil:
		return nil, err
	}
	return web.Redirect(auth.Intended(c, h.auth.Config().HomeURL)), nil
}
```

(Copied from [`examples/auth/main.go`](../../../examples/auth/main.go), region `confirm`.)

The routes:

```go
pages := r.Group("", sessions.Middleware, web.CSRF(), a.Middleware)
pages.Get("/", func(c *web.Ctx) error { return c.Redirect(http.StatusSeeOther, "/dashboard") })
pages.Get("/verify-email", web.H(h.VerifyEmail))

guests := pages.Group("", a.Guest) // signed-in users go to AUTH_HOME_URL
guests.Get("/register", h.page("register"))
guests.Post("/register", web.H(h.Register))
guests.Get("/login", h.page("login"))
guests.Post("/login", web.H(h.Login))
guests.Get("/forgot-password", h.page("forgot"))
guests.With(ratelimit.Middleware("forgot-password", ratelimit.PerMinute(5))).Post("/forgot-password", web.H(h.SendReset))
guests.Get("/reset-password", h.page("reset"))
guests.Post("/reset-password", web.H(h.Reset))
guests.Get("/auth/{provider}/redirect", s.Redirect) // "Sign in with …" links here
guests.Get("/auth/{provider}/callback", s.Callback)
guests.Get("/two-factor-challenge", h.page("challenge")) // AUTH_CHALLENGE_URL
guests.Post("/two-factor-challenge", web.H(h.Challenge))

members := pages.Group("", a.Require) // guests go to AUTH_LOGIN_URL
members.Get("/dashboard", h.Dashboard)
members.Post("/logout", h.Logout)
members.Post("/tokens/{id}/delete", web.H(h.RevokeToken))
members.Get("/users/{id}", web.H(h.ShowUser))
members.Get("/admin", h.Admin)
members.Post("/password", web.H(h.ChangePassword))
members.Get("/confirm-password", h.page("confirm")) // AUTH_CONFIRM_URL
members.Post("/confirm-password", web.H(h.ConfirmPassword))

// Two-factor sign-in (AUTH_TWO_FACTOR_URL) and API tokens (they work
// without the browser): the password again first.
secure := members.Group("", a.RequireConfirmed)
secure.Post("/tokens", web.H(h.CreateToken))
secure.Get("/two-factor", h.TwoFactor)
secure.Post("/two-factor", h.StartTwoFactor)
secure.Post("/two-factor/confirm", web.H(h.ConfirmTwoFactor))
secure.Post("/two-factor/disable", h.DisableTwoFactor)

api := r.Group("/api", a.TokenMiddleware, a.Require) // Authorization: Bearer <token>
api.Get("/me", h.Me)
```

(Copied from [`examples/auth/main.go`](../../../examples/auth/main.go), region `routes`.)

The [admin](admin.md) asks for the password before its dangerous
actions too, and with `ADMIN_TWO_FACTOR=required` lets in only users
with two-factor sign-in on.

## How it works

Codes are TOTP (RFC 6238): HMAC-SHA-1 of a 160-bit secret and the time
in 30-second steps, six digits, as authenticator apps expect. A code is
accepted for its step and the one either side (a phone's clock a little
off), and only for a step later than the last one used, so a code seen
over someone's shoulder can't be used again. Recovery codes are ten
letters and digits (50 bits; a 0 or 1 typed for o or l is read as
them), stored as SHA-256 hashes, each used once.

The state is one encrypted value: `APP_KEY` encrypts it for that user
alone, so it can't be copied to another; rotate keys with
`APP_PREVIOUS_KEYS`, as for everything `APP_KEY` encrypts. If it can't
be read (the key was lost), signing in fails rather than skipping the
code; turn it off for the user (`DisableTwoFactor`, or the admin).

Codes, like passwords, are throttled: `AUTH_THROTTLE` tries a minute
for a user, from any address, while signing in, confirming a setup or a
password; and at sign-in, 50 wrong codes a day for a user, after which
codes are refused until the day is over (someone who has the password
gets very few guesses; the warnings in the log say so, and the user
should change their password). A sign-in waiting for its code ends
after 10 minutes, at a password change, and when another sign-in
starts in the session. Turning two-factor sign-in on signs the user out
of their other sessions (with `Users.SessionKey`) and remember-me
cookies, which were signed in without a code.

Confirming the password lasts `AUTH_CONFIRM_TTL`; signing in or out, and
acting as another user, forget it. Users without a password (they sign
in with Google or GitHub) can't type one: for them, a sign-in through
`a.SignIn` (social login's) counts as a confirmation for
`AUTH_CONFIRM_TTL`, so they confirm by signing out and in again.

| Setting | Default | |
|---|---|---|
| `AUTH_CHALLENGE_URL` | `/two-factor-challenge` | Where a sign-in asks for its code |
| `AUTH_TWO_FACTOR_URL` | `/two-factor` | Where users turn it on and off (the admin links there) |
| `AUTH_CONFIRM_URL` | `/confirm-password` | Where `RequireConfirmed` sends users |
| `AUTH_CONFIRM_TTL` | `15m` | How long a confirmed password holds |

## Testing it

`auth.TwoFactorCode(secret, t)` is the code an app shows at `t`:

```go
// Two-factor sign-in: turned on with a code of the authenticator app
// (auth.TwoFactorCode computes it), then asked for after the password.
func TestTwoFactor(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := createUser(t, app, "Ada", "ada@example.com", false)
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"password1"}})

	app.Get("/two-factor").AssertRedirect("/confirm-password") // the password again first
	app.PostForm("/confirm-password", url.Values{"password": {"password1"}}).AssertRedirect("/two-factor")
	app.PostForm("/two-factor", nil).AssertRedirect("/two-factor").Follow().AssertSee("<svg", "Or type this key")

	ada, _ = users.ByID(app.Context(), ada.AuthID()) // with the secret stored
	setup, err := anetos.MustResolve[*auth.Auth[*User]](app.App).StartedTwoFactor(ada, ada.Email)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := auth.TwoFactorCode(setup.Secret, app.Now())
	app.PostForm("/two-factor/confirm", url.Values{"code": {code}}).AssertRedirect("/two-factor").
		Follow().AssertSee("Your recovery codes", "On: 8 recovery codes left.")

	app.PostForm("/logout", nil)
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"password1"}}).AssertRedirect("/two-factor-challenge")
	app.Get("/dashboard").AssertRedirect("/login") // not signed in yet
	app.Get("/two-factor-challenge")
	app.PostForm("/two-factor-challenge", url.Values{"code": {code}}).AssertValidationErrors("code") // used already
	app.Travel(30 * time.Second)
	code, _ = auth.TwoFactorCode(setup.Secret, app.Now())
	app.PostForm("/two-factor-challenge", url.Values{"code": {code}}).AssertRedirect("/dashboard")
}
```

(Copied from [`examples/auth/main_test.go`](../../../examples/auth/main_test.go), region `test-two-factor`.)

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| "That code isn't right" with a fresh code | The phone's clock is off by more than 30 seconds | Set its time automatically |
| Every code refused with 429 for hours | 50 wrong codes in a day for that user | Wait, and change the password: someone may have it |
| A user who signs in with Google is sent to confirm a password | Users without a password confirm by signing in again | Sign out and in again, then go back within `AUTH_CONFIRM_TTL` |
| The code is refused right after turning it on | That code was used to turn it on: each is used once | Wait for the next one |
| `the user's two-factor state can't be read` | `APP_KEY` changed without the old key in `APP_PREVIOUS_KEYS` | Put the old key back in `APP_PREVIOUS_KEYS`, or turn it off for the user |
| `two-factor sign-in needs Users.TwoFactor and Users.SetTwoFactor` | `auth.Users` lacks them | Add them (step 1) |
| Social sign-in skips the code | Your callback signs in with `a.Login` | Use `a.SignIn` |

## Next steps

- [Add an admin panel](admin.md), which can require it.
- [Authentication](authentication.md): passwords, sessions, remember me.
- [Social login](social-login.md).
