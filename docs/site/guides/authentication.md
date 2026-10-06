---
title: Authentication
since: v0.2.0
group: "Accounts and security"
weight: 301
---

# Authentication

Let people register, log in (with "remember me"), log out, verify their
email address and reset a forgotten password, and give API clients
tokens. The complete app is [`examples/auth`](../../../examples/auth).
In an `anetos new` project, `go tool anetos make:auth` writes all of this
into your app: see [Add accounts with make:auth](accounts.md). This
guide is the `auth` package underneath, step by step.

## Before you start

You need sessions ([Sessions and flash messages](sessions.md)), the
cache (login throttling counts failures in it, see
[Cache values](cache.md)) and a users table. Passwords are hashed with
`auth/password` (argon2id).

## Steps

### 1. Describe your users

Your user model implements two methods:

```go
// User is an account. The two Auth methods are what package auth needs.
type User struct {
	db.Model
	Name            string     `db:"name" json:"name"`
	Email           string     `db:"email" json:"email"` // stored in lower case
	Password        string     `db:"password" json:"-"`  // password.Hash
	RememberToken   string     `db:"remember_token" json:"-"`
	EmailVerifiedAt *time.Time `db:"email_verified_at" json:"email_verified_at"`
	Admin           bool       `db:"admin" json:"admin"`
	TwoFactor       string     `db:"two_factor" json:"-"` // two-factor sign-in, encrypted by package auth
}

// AuthID implements auth.Authenticatable.
func (u *User) AuthID() string { return strconv.FormatInt(u.ID, 10) }

// AuthPassword implements auth.Authenticatable.
func (u *User) AuthPassword() string { return u.Password }
```

(Copied from [`examples/auth/users.go`](../../../examples/auth/users.go), region `user`.)

`auth.Users` tells the package how to find users, and, optionally, how
to store remember-me tokens and upgraded password hashes:

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

`ByID` and `ByLogin` return `db.ErrNotFound` (or `auth.ErrNoUser`) when
there is no such user. Store email addresses in lower case, and look them
up the same way.

### 2. Set up auth and protect routes

```go
// AUTH_* settings. Signing in leads to /dashboard, unless
// AUTH_HOME_URL names another page.
a, err := auth.ForApp(app, users, auth.DefaultHomeURL("/dashboard"))
if err != nil {
	return nil, err
}
```

(Copied from [`examples/auth`](../../../examples/auth/main.go), region `setup`.)

Add `a.Middleware` after the session middleware, then `a.Require` on the
routes for signed-in users and `a.Guest` on the login and registration
pages:

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

(Copied from [`examples/auth`](../../../examples/auth/main.go), region `routes`.)

`Require` sends guests asking for a page to `AUTH_LOGIN_URL` (remembering
the page for `auth.Intended`), and answers other requests (JSON, htmx)
with 401. `Guest` sends signed-in users to `AUTH_HOME_URL`.

Read the current user in a handler with `auth.User` (or `auth.Current`,
which also returns the error if loading the user failed):

```go
// illustrative
u, ok := auth.User[*User](c)
```

The user is loaded from the database when first asked for, once per
request.

### 3. Register

Hash the password, create the user and sign them in:

```go
func (h Accounts) Register(c *web.Ctx, in RegisterInput) (web.Responder, error) {
	email := strings.ToLower(in.Email) // stored and looked up in lower case
	taken, err := db.Query[User](c).Where(colEmail.Eq(email)).Exists()
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, validate.Fail("email", "The email has already been taken.")
	}
	hash, err := password.Hash(in.Password)
	if err != nil {
		return nil, err
	}
	u := &User{Name: in.Name, Email: email, Password: hash}
	if err := db.Create(c, u); err != nil {
		return nil, err
	}
	// The account exists now: a mail server that fails doesn't undo it
	// (an app with a queue sends with mailer.Queue, which retries).
	if err := sendLink(c, u.Email, "Verify your email address", "/verify-email?token="+url.QueryEscape(h.auth.VerificationToken(u, u.Email))); err != nil {
		c.Logger().Error("send the verification link", "error", err)
	}
	if err := h.auth.Login(c, u, false); err != nil {
		return nil, err
	}
	return web.Redirect("/dashboard"), nil
}
```

(Copied from [`examples/auth`](../../../examples/auth/main.go), region `register`.)

### 4. Log in and out

`Attempt` checks the password and signs the user in; turn its errors
into a form error:

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

(Copied from [`examples/auth`](../../../examples/auth/main.go), region `login`.)

A failed login gives the same error whether the address has no account
or the password is wrong, and takes about as long (users with an old
bcrypt hash, until they next log in, can take longer). After
`AUTH_THROTTLE` attempts (5 by default) in a minute for one login, or
one account, from one IP address, and after `AUTH_THROTTLE_IP` failures
(50) from one address whatever the login, `Attempt` returns a
`*auth.ThrottledError` without checking the password. Users whose hash is
stronger than the defaults (made with `password.HashWith`) also take
longer to check than unknown logins. `Login` (and so `Attempt`) gives the
session a new ID; `Logout` empties it and signs the user out of every
browser where they chose "remember me". With the default cookie
sessions, a copy of the session cookie taken before the logout keeps
working until it expires; use a server-side `SESSION_DRIVER` to revoke
sessions at logout.

### 5. Verify email addresses

Send a link with a verification token, and check it when it's followed:

```go
func (h Accounts) VerifyEmail(c *web.Ctx, in TokenQuery) (web.Responder, error) {
	u, email, err := h.auth.CheckVerificationToken(c, in.Token)
	if err != nil {
		return nil, err // 400 for a bad or expired link
	}
	if email == u.Email && u.EmailVerifiedAt == nil {
		now := anetos.Now(c).UTC()
		if _, err := db.Query[User](c).Where(colID.Eq(u.ID)).Update(colVerified.Set(&now)); err != nil {
			return nil, err
		}
	}
	c.Session().Flash("status", "Your email address is verified.")
	return web.Redirect("/dashboard"), nil
}
```

(Copied from [`examples/auth`](../../../examples/auth/main.go), region `verify`.)

`sendLink` emails the link with the [mailer](mail.md), as an absolute URL
on `APP_URL` (`mailer.URL`); in tests, `anetostest` records the email,
and the test follows its link.

### 6. Reset forgotten passwords

```go
func (h Accounts) SendReset(c *web.Ctx, in ForgotInput) (web.Responder, error) {
	u, err := users.ByLogin(c, in.Email)
	switch {
	case err == nil:
		if err := sendLink(c, u.Email, "Reset your password", "/reset-password?token="+url.QueryEscape(h.auth.PasswordResetToken(u))); err != nil {
			return nil, err
		}
	case !errors.Is(err, db.ErrNotFound):
		return nil, err
	}
	// The same answer either way, so the form doesn't reveal who has an
	// account.
	c.Session().Flash("status", "If that address has an account, we've emailed it a link.")
	return web.Redirect("/login"), nil
}

func (h Accounts) Reset(c *web.Ctx, in ResetInput) (web.Responder, error) {
	u, err := h.auth.CheckPasswordResetToken(c, in.Token)
	if errors.Is(err, auth.ErrInvalidToken) {
		return nil, validate.Fail("password", "This reset link is invalid or has expired. Ask for a new one.")
	}
	if err != nil {
		return nil, err
	}
	hash, err := password.Hash(in.Password)
	if err != nil {
		return nil, err
	}
	// Only if the password is still the one the link was made for: two
	// uses of the link at once change it once.
	n, err := db.Query[User](c).Where(colID.Eq(u.ID), colPassword.Eq(u.Password)).Update(colPassword.Set(hash))
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, validate.Fail("password", "This reset link is invalid or has expired. Ask for a new one.")
	}
	// The new password signs out every session and remembered browser
	// (their password fingerprint no longer matches); API tokens are
	// revoked too.
	if err := h.auth.RevokeAllTokens(c, u); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Your password has been reset. You can log in now.")
	return web.Redirect("/login"), nil
}
```

(Copied from [`examples/auth`](../../../examples/auth/main.go), region `reset`.)

A reset token works for `AUTH_RESET_TTL` (an hour) and only until the
password changes, so it works once; the update is conditional on the old
hash, so two uses at the same moment change it once. Changing the
password signs the user out of every other session (cookie sessions:
when they're next used, as the password fingerprint no longer matches)
and remembered browser; `a.RevokeAllTokens` removes their API tokens.
Throttle the form that sends links, as the example does, so it can't be
used to flood someone's inbox.

### 7. Let users change their password

`a.ChangePassword(ctx, u, current, new)` checks the current password
(throttled, `AUTH_THROTTLE` tries a minute), stores the new one hashed,
and signs the user out of their other sessions and remembered browsers
(`a.SignOutOthers`), keeping the one they're using: it gets a new
session ID, and stays remembered if it was. A user without a password (who signs in
with Google, say) sets one without `current`, if they signed in in the
last `AUTH_CONFIRM_TTL`.

```go
// ChangePassword changes the signed-in user's password. Their other
// browsers and devices are signed out; this one stays signed in.
func (h Accounts) ChangePassword(c *web.Ctx, in NewPasswordInput) (web.Responder, error) {
	u, err := auth.Current[*User](c)
	if err != nil {
		return nil, err
	}
	switch err := h.auth.ChangePassword(c, u, in.CurrentPassword, in.Password); {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return nil, validate.Fail("current_password", "That isn't your password.")
	case err != nil:
		return nil, err
	}
	c.Session().Flash("status", "Password changed.")
	return web.Redirect("/dashboard"), nil
}
```

(Copied from [`examples/auth`](../../../examples/auth/main.go), region `change-password`.)

[`make:auth`](accounts.md)'s settings page has it, with the user's name,
email address, language and time zone.

### 8. Give API clients tokens

Add `auth.Migrations()` to your migrations for the `api_tokens` table.
Tokens are created for a user with abilities, and shown once:

```go
func (h Accounts) CreateToken(c *web.Ctx, in NewTokenInput) (web.Responder, error) {
	u, err := auth.Current[*User](c)
	if err != nil {
		return nil, err
	}
	plain, _, err := h.auth.CreateToken(c, u, in.Name, []string{"profile:read"}, 90*24*time.Hour)
	if err != nil {
		return nil, err
	}
	c.Session().Flash("token", plain) // shown once
	return web.Redirect("/dashboard"), nil
}

// Me is GET /api/me, for API clients with a token.
func (Accounts) Me(c *web.Ctx) error {
	if !auth.TokenCan(c, "profile:read") {
		return auth.ErrForbidden
	}
	u, err := auth.Current[*User](c)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, u)
}
```

(Copied from [`examples/auth`](../../../examples/auth/main.go), region `api-tokens`.)

Clients send `Authorization: Bearer <token>` to routes with
`a.TokenMiddleware` (see the routes above). Keep those routes in their
own group, without the session middleware and `web.CSRF`: a CSRF check
would refuse API clients' posts, and without sessions `a.Require`
answers 401 instead of redirecting. Other `Authorization` schemes are
ignored (the request is a guest). `auth.TokenCan` checks a
token's abilities; a user signed in with a session (your own pages and
front end) may do anything. `a.Tokens` lists a user's tokens and
`a.RevokeToken` deletes one.

Put the route that creates tokens behind `a.RequireConfirmed`, as the
example does: a token outlives the session, so someone holding a stolen
session shouldn't be able to make one without the password. `CreateToken`
refuses while an admin acts as the user (403), for the same reason (v0.3).

### 9. Test

```go
func TestRegisterAndLogin(t *testing.T) {
	app := anetostest.New(t, setup)

	app.Get("/register").AssertOK()
	app.PostForm("/register", url.Values{"name": {"Ada"}, "email": {"Ada@Example.com"},
		"password": {"password1"}, "password_confirmation": {"password1"}}).
		AssertRedirect("/dashboard").
		Follow().
		AssertSee("Hello, Ada", "Please verify your email address")

	// The emailed link verifies the address.
	app.Get(emailedLink(t, app, "Verify your email address")).AssertRedirect("/dashboard").Follow().
		AssertSee("Your email address is verified.").
		AssertDontSee("Please verify")

	app.PostForm("/logout", nil).AssertRedirect("/login")
	app.Get("/dashboard").AssertRedirect("/login") // guests go to the login page

	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"wrong"}}).
		AssertValidationErrors("email")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"password1"}}).
		AssertRedirect("/dashboard") // the page asked for before logging in
}
```

(Copied from [`examples/auth/main_test.go`](../../../examples/auth/main_test.go), region `test-auth`.)

Tests that need a signed-in user can log in through the form, as above,
or create the user and post to your login route. API tokens work in tests
like in production:

```go
func TestAPIToken(t *testing.T) {
	app := anetostest.New(t, setup)
	createUser(t, app, "Ada", "ada@example.com", false)
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {"ada@example.com"}, "password": {"password1"}})
	app.PostForm("/confirm-password", url.Values{"password": {"password1"}}) // tokens need it
	app.PostForm("/tokens", url.Values{"name": {"cli"}}).AssertRedirect("/dashboard")
	token := app.Session().String("token") // flashed to show once

	// The API routes don't use the session: only the token counts.
	app.GetJSON("/api/me").AssertStatus(http.StatusUnauthorized)
	app.WithHeader("Authorization", "Bearer "+token).GetJSON("/api/me").
		AssertOK().
		AssertJSONPath("email", "ada@example.com")
}
```

(Copied from [`examples/auth/main_test.go`](../../../examples/auth/main_test.go), region `test-api-token`.)

## How it works

The session holds the user's ID and a fingerprint of their password hash:
when the password changes, other sessions no longer match and are signed
out. "Remember me" sets a second cookie, encrypted with `APP_KEY`, holding
the user's ID and remember token (a column of your users table): when the
session has ended, the cookie signs the user back in to a new session.
`Logout` replaces the token, which invalidates every remembered browser.

Verification and reset tokens aren't stored: they are encrypted with
`APP_KEY` and hold the user's ID, an expiry and (for resets) the password
fingerprint. API tokens are `<id>|<secret>`; only a SHA-256 hash of the
secret is stored, and the use is recorded at most once a minute.

Password hashes are argon2id with OWASP's recommended parameters
(19 MiB, 2 passes); at most a few run at once (as many as there are
CPUs), so a burst of logins waits rather than exhausting memory. Existing
bcrypt hashes (from Laravel, say) still work (bcrypt only checks a
password's first 72 bytes), and `Attempt` replaces them, and argon2id
hashes weaker than the defaults, after the next login when you set
`Users.SetPassword`. The new hash changes the password fingerprint, so
the user's other sessions are signed out once.

> **Coming from Laravel?** `Auth::attempt`, `Auth::login`, `Auth::logout`,
> `auth()->user()` and the `auth`/`guest` middleware map to `a.Attempt`,
> `a.Login`, `a.Logout`, `auth.User` and `a.Require`/`a.Guest`. Sanctum's
> personal access tokens and `tokenCan` map to `a.CreateToken` and
> `auth.TokenCan`. Breeze is `anetos make:auth`
> ([Add accounts with make:auth](accounts.md)). Socialite is
> [Social login](social-login.md).

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `auth: login throttling needs the app's cache` at startup | `cache.ForApp` isn't called, or is called after `auth.ForApp` | Set up the cache first |
| `auth: no auth state in the context` | The route lacks `a.Middleware` | Add it after the session middleware |
| `auth: ForApp was already called for this app` | Two `auth.ForApp` calls | An app has one Auth, for one user type |
| `the current user was asked for while it was being loaded` | `Users.ByID` calls `auth.User` or `auth.Check` (through a query scope, say) | Don't ask for the current user while finding it |
| Every page redirects to the login page after logging in | `a.Middleware` runs before the session middleware | Put the session middleware first |
| Users are signed out when they change their password | By design: other sessions end | Call `a.Login` again after changing the password in the current request |
| "Remember me" returns an error | `Users.RememberToken`/`SetRememberToken` are unset | Add a `remember_token` column and set both |
| API requests get 401 with a valid token | The route uses the session middleware and `a.Middleware` but not `a.TokenMiddleware` | Put API routes in a group with `a.TokenMiddleware` |

## Next steps

- [Authorization](authorization.md)
- [Roles and permissions](roles-and-permissions.md)
- [Social login](social-login.md)
- [Two-factor sign-in and password confirmation](two-factor.md)
- [Rate limiting](rate-limiting.md)
- [Sessions and flash messages](sessions.md)
