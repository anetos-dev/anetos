---
title: Add accounts with make:auth
since: v0.2.0
---

# Add accounts with make:auth

Give your app user accounts in one command: registration, login with
"remember me" and throttling, sign-in with Google and GitHub, logout,
email verification, password reset and API tokens, with their pages,
emails, routes, migration and tests. The code is written into your app, where you change it as you
like; the parts that must be right (password hashing, tokens, sessions,
throttling, the OAuth flow) stay in the [`auth`](authentication.md) and
[`social`](social-login.md) packages, so fixes reach you with `go get -u`.

## Before you start

You have a project made with `anetos new` (it has sessions, the cache,
the queue and the mailer that accounts use), and no `User` model yet.

## Steps

### 1. Generate the accounts

From the project's directory:

```bash
go tool anetos make:auth
```

```text
created app/models/user.go
created app/handlers/auth.go
created app/mailers/auth.go
created views/auth.templ
created views/auth_mail.templ
created routes/auth.go
created auth.go
created auth_test.go
created database/migrations/2026_10_02_090000_create_users_table.go
updated .env: SOCIAL_* settings
updated .env.example: SOCIAL_* settings
updated main.go: setup calls setupAuth
wrote app/models/models_gen.go
```

`make:auth` writes nothing if one of these files exists, or if a name
they declare (`User`, `Accounts`, `Login`, …) is taken in its package;
if writing fails midway, it removes what it wrote. It runs `go mod
tidy`, `anetos gen` (the `User` model's typed columns) and `templ
generate` (the pages), checks that the project builds, and adds a call
to `setupAuth` to `setup` in `main.go`, after `routes.Register`:

```go
// illustrative: main.go after make:auth
routes.Register(srv.Router(), sessions)
// Accounts (anetos make:auth): registration, login with a password,
// Google or GitHub, email verification, password reset and API tokens.
if _, err := setupAuth(app, srv.Router(), sessions); err != nil {
	return nil, err
}
```

If your `setup` no longer has that statement, `make:auth` says so and
you add the call yourself.

### 2. Migrate and try it

```bash
go run . migrate     # the users, api_tokens and social_accounts tables
go tool anetos dev
```

Open `/register`. In development, emails go to the log
(`MAIL_DRIVER=log`), with their links; with `QUEUE_DRIVER=database`, the
app's workers send them, so they appear a moment later.

### 3. Turn on sign-in with Google or GitHub

Each provider is on once its client ID and secret are set; until then
its button doesn't show and its routes answer 404. Create an OAuth app
at the provider (the [Google Cloud console](https://console.cloud.google.com/apis/credentials):
"OAuth client ID", type "Web application"; [GitHub](https://github.com/settings/developers):
"New OAuth App"), register `APP_URL/auth/google/callback` (or
`/auth/github/callback`) as its callback URL, and fill in the settings
`make:auth` added to `.env`:

```env
APP_URL=http://localhost:8080
SOCIAL_GOOGLE_CLIENT_ID=1234-abc.apps.googleusercontent.com
SOCIAL_GOOGLE_CLIENT_SECRET=…
```

The login and registration pages then show "Sign in with Google". A
first sign-in creates a user, with the address verified and no password
(they can set one with "Forgot your password?"); an account whose
address both the provider and your app have verified is linked to the
existing user. [Social login](social-login.md) explains the rules and
how to add another provider (Okta, Auth0, any OpenID Connect provider).

### 4. Run the tests

`auth_test.go` registers, verifies the address by following the
emailed link, logs in and out, resets the password, uses an API token,
and signs in with Google and GitHub through a stand-in provider
(`anetostest.FakeSocial`):

```bash
go test ./...
```

Keep these tests as you change the code: they cover the flows that
matter.

### 5. Make it yours

| File | Holds |
|---|---|
| `app/models/user.go` | `User`, and `models.Users`: how `auth` finds users and stores their tokens. Add columns here and in a migration |
| `app/handlers/auth.go` | `handlers.Accounts`: each page and form. Validation messages, redirects and what happens after registration are here; `SocialUser` finds or creates the user of a Google or GitHub account |
| `views/auth.templ` | The pages, inside your `Layout` |
| `app/mailers/auth.go`, `views/auth_mail.templ` | The verification and reset emails |
| `routes/auth.go` | The routes and their names (`login`, `register`, `dashboard`, …), and the rate limits of the forgotten-password and verification forms |
| `auth.go` | `setupAuth`: the `api_tokens` and `social_accounts` migrations, `auth.ForApp`, `social.ForApp` with the providers, and the routes |
| `database/migrations/…_create_users_table.go` | The users table |

The routes:

| Route | For |
|---|---|
| `GET`, `POST /register` | Guests: create an account, email the verification link, sign in (10 posts a minute per client IP address) |
| `GET`, `POST /login` | Guests: sign in (`AUTH_THROTTLE` limits failures) |
| `GET`, `POST /forgot-password` | Guests: email a reset link (5 posts a minute per client IP address; 3 links an hour per address, the same answer after) |
| `GET`, `POST /reset-password` | Guests: choose a new password with the link |
| `GET /auth/{provider}/redirect`, `GET /auth/{provider}/callback` | Guests: sign in with Google or GitHub (404 for a provider whose settings aren't set) |
| `GET /verify-email` | Anyone with the link: verify the address (400 if the link is bad, expired, or for an address the user has since changed) |
| `GET /dashboard` | Signed-in users: the account and its API tokens |
| `POST /logout` | Signed-in users |
| `POST /email/verification-notification` | Signed-in users: email the link again (3 a minute per client IP address, 6 an hour per account) |
| `POST /tokens`, `POST /tokens/{id}/delete` | Signed-in users: create (shown once) and revoke API tokens |
| `GET /api/me` | API clients, with `Authorization: Bearer <token>` |

Signed-in users who open a guest page go to `AUTH_HOME_URL` (default
`/`; set `AUTH_HOME_URL=/dashboard` to send them to the dashboard, where
logging in, registering and signing in with a provider lead); guests who open a member page go to
`AUTH_LOGIN_URL` and come back after logging in. To protect your own pages, put them in a group with
`a.Middleware` and `a.Require`, as `routes/auth.go` does: `setupAuth`
returns the `*auth.Auth`.

To do more when someone signs up (a welcome email, a trial), change
`Register` and `SocialUser` in `app/handlers/auth.go`, which create
users. [`examples/saas`](../../../examples/saas), a `make:auth` app,
queues a welcome job in the transaction that creates the user, so the
job runs only if the user is committed:

```go
// welcome queues the welcome email of a new user, once the transaction
// that creates them commits: a worker sends it (jobs.SendWelcome).
func welcome(ctx context.Context, u *models.User) error {
	return queue.Dispatch(ctx, jobs.SendWelcome{UserID: u.ID}, queue.AfterCommit())
}
```

(Copied from [`examples/saas/app/handlers/auth.go`](../../../examples/saas/app/handlers/auth.go), region `dispatch`.)

```go
// SendWelcome emails a new user the welcome email. Registration and the
// first sign-in with Google or GitHub dispatch it once the user is
// committed; a worker runs it, with the queue's retries.
type SendWelcome struct {
	UserID int64 `json:"user_id"`
}

// Handle sends the email.
func (j SendWelcome) Handle(ctx context.Context) error {
	u, err := db.Find[models.User](ctx, j.UserID)
	if errors.Is(err, db.ErrNotFound) {
		return queue.Permanent(err) // deleted since: retrying won't help
	}
	if err != nil {
		return err
	}
	url, err := mailer.URL(ctx, "/dashboard")
	if err != nil {
		return err
	}
	m := mailers.Welcome{Name: u.Name, Email: u.Email, URL: url}
	if u.TrialEndsAt != nil {
		m.TrialEnds = *u.TrialEndsAt
	}
	return mailer.Send(ctx, m)
}
```

(Copied from [`examples/saas/app/jobs/welcome.go`](../../../examples/saas/app/jobs/welcome.go), region `job`.)

`setup` registers the job type, `queue.Register[jobs.SendWelcome](q,
queue.Tries(5))`, where `anetos new` left a comment for it; see
[Queues](queues.md).

## How it works

The generated code is the same kind of code as
[`examples/auth`](../../../examples/auth), which the
[Authentication](authentication.md) guide walks through, adapted to a
`anetos new` project: templ pages in `views`, typed columns, the mailer
for the links (`mailer.Queue`, so a slow mail server doesn't slow the
request), and route names. Links in emails are absolute, on `APP_URL`.
Reset and verification tokens are signed, not stored: a reset link
works for `AUTH_RESET_TTL` and once, a verification link for
`AUTH_VERIFY_TTL`. Registration creates the user and renders the email
in one transaction, so an email that can't be rendered leaves no
account behind; the queue sends it after the commit. Names are kept on
one line (control characters dropped), and emails go to the address
alone, the name only in their body.

The forgotten-password form answers the same whether or not the address
has an account, but takes a little longer when it does (it renders and
queues the email): someone measuring response times could tell. The
registration form says when an address is taken, as most sites do.

Signing in with a provider follows the rules of
[Social login](social-login.md): accounts are linked to users by the
provider's account ID, not by email, and an address finds an existing
user only when both the provider and your app have verified it, and the
user has no other account of that provider linked.

`make:auth` doesn't add authorization policies: see
[Authorization](authorization.md).

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `…_create_users_table.go exists` or `app/models/user.go exists` | The app has users already, or `make:auth` ran before | Rename yours, or add accounts by hand from [Authentication](authentication.md) |
| `app/models already declares User` (or another name) | A name the generated files declare is taken in that package | Rename yours |
| `the project doesn't build` after the files are written | Your code and the generated code clash, or a tool failed | Fix it, then run the commands it prints |
| `no app/models directory` | Not a `anetos new` project | Start from [`examples/auth`](../../../examples/auth) |
| `main.go doesn't have the routes.Register call` | `setup` was changed | Add the `setupAuth` call it prints |
| `no such table: users` | The migrations haven't run | `go run . migrate` |
| `URL needs the app's public URL` when registering | `APP_URL` isn't set | Set it in `.env` |
| No email in the log | `QUEUE_DRIVER=database` and the workers aren't running | Run the app with `go tool anetos dev` or `go run .`, or use `QUEUE_DRIVER=sync` |
| No "Sign in with Google" button | Its `SOCIAL_GOOGLE_*` settings are empty | Set both in `.env` |
| `redirect_uri_mismatch` at Google | The callback URL registered there isn't `APP_URL/auth/google/callback` | Fix `APP_URL` or the URL at Google |

## Next steps

- [Authentication](authentication.md): the `auth` package.
- [Social login](social-login.md): providers, linking and testing.
- [Send email](mail.md): send the emails with SMTP or Postmark.
- [Authorization](authorization.md): policies.

> **Coming from Laravel?** `anetos make:auth` is Breeze: the
> controllers, views and routes are published into your app. Hashing,
> remember-me, throttling and tokens (Fortify and Sanctum's parts) stay in
> the library, and so does the OAuth flow (Socialite's part).
