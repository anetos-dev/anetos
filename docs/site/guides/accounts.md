---
title: Add accounts with make:auth
since: v0.2.0
---

# Add accounts with make:auth

Give your app user accounts in one command: registration, login with
"remember me" and throttling, logout, email verification, password
reset and API tokens, with their pages, emails, routes, migration and
tests. The code is written into your app, where you change it as you
like; the parts that must be right (password hashing, tokens, sessions,
throttling) stay in the [`auth`](authentication.md) package, so fixes
reach you with `go get -u`.

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
// Accounts (anetos make:auth): registration, login, email
// verification, password reset and API tokens.
if _, err := setupAuth(app, srv.Router(), sessions); err != nil {
	return nil, err
}
```

If your `setup` no longer has that statement, `make:auth` says so and
you add the call yourself.

### 2. Migrate and try it

```bash
go run . migrate     # the users and api_tokens tables
go tool anetos dev
```

Open `/register`. In development, emails go to the log
(`MAIL_DRIVER=log`), with their links; with `QUEUE_DRIVER=database`, the
app's workers send them, so they appear a moment later.

### 3. Run the tests

`auth_test.go` registers, verifies the address by following the
emailed link, logs in and out, resets the password, and uses an API
token:

```bash
go test ./...
```

Keep these tests as you change the code: they cover the flows that
matter.

### 4. Make it yours

| File | Holds |
|---|---|
| `app/models/user.go` | `User`, and `models.Users`: how `auth` finds users and stores their tokens. Add columns here and in a migration |
| `app/handlers/auth.go` | `handlers.Accounts`: each page and form. Validation messages, redirects and what happens after registration are here |
| `views/auth.templ` | The pages, inside your `Layout` |
| `app/mailers/auth.go`, `views/auth_mail.templ` | The verification and reset emails |
| `routes/auth.go` | The routes and their names (`login`, `register`, `dashboard`, …), and the rate limits of the forgotten-password and verification forms |
| `auth.go` | `setupAuth`: the `api_tokens` migration, `auth.ForApp` and the routes |
| `database/migrations/…_create_users_table.go` | The users table |

The routes:

| Route | For |
|---|---|
| `GET`, `POST /register` | Guests: create an account, email the verification link, sign in (10 posts a minute per client IP address) |
| `GET`, `POST /login` | Guests: sign in (`AUTH_THROTTLE` limits failures) |
| `GET`, `POST /forgot-password` | Guests: email a reset link (5 posts a minute per client IP address; 3 links an hour per address, the same answer after) |
| `GET`, `POST /reset-password` | Guests: choose a new password with the link |
| `GET /verify-email` | Anyone with the link: verify the address (400 if the link is bad, expired, or for an address the user has since changed) |
| `GET /dashboard` | Signed-in users: the account and its API tokens |
| `POST /logout` | Signed-in users |
| `POST /email/verification-notification` | Signed-in users: email the link again (3 a minute per client IP address, 6 an hour per account) |
| `POST /tokens`, `POST /tokens/{id}/delete` | Signed-in users: create (shown once) and revoke API tokens |
| `GET /api/me` | API clients, with `Authorization: Bearer <token>` |

Signed-in users who open a guest page go to `AUTH_HOME_URL` (default
`/`; set `AUTH_HOME_URL=/dashboard` to send them to the dashboard, where
logging in and registering lead); guests who open a member page go to
`AUTH_LOGIN_URL` and come back after logging in. To protect your own pages, put them in a group with
`a.Middleware` and `a.Require`, as `routes/auth.go` does: `setupAuth`
returns the `*auth.Auth`.

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

`make:auth` doesn't add social login or authorization policies: see
[Social login](social-login.md) and [Authorization](authorization.md).

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

## Next steps

- [Authentication](authentication.md): the `auth` package.
- [Send email](mail.md): send the emails with SMTP or Postmark.
- [Authorization](authorization.md): policies.

> **Coming from Laravel?** `anetos make:auth` is Breeze: the
> controllers, views and routes are published into your app. Hashing,
> remember-me, throttling and tokens (Fortify and Sanctum's parts) stay in
> the library.
