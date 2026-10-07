---
title: Add accounts with make:auth
since: v0.2.0
group: "Accounts and security"
weight: 300
---

# Add accounts with make:auth

Give your app user accounts in one command: registration, login with
"remember me" and throttling, sign-in with Google and GitHub,
two-factor sign-in, account settings, logout, email verification,
password reset and API tokens, with their pages,
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
created app/handlers/settings.go
created app/mailers/auth.go
created views/auth.templ
created views/settings.templ
created views/auth_mail.templ
created routes/auth.go
created auth.go
created auth_test.go
created locales/en/auth.yaml
created database/factories/users.go
created database/migrations/2026_10_02_090000_create_users_table.go
updated .env: SOCIAL_* settings
updated .env.example: SOCIAL_* settings
updated deploy/production.env.example: SOCIAL_* settings
updated main.go: setup calls setupAuth
updated views/layout.templ: the header shows AccountMenu
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
// Google or GitHub, two-factor sign-in, account settings, email
// verification, password reset and API tokens.
if _, err := setupAuth(app, srv.Router(), sessions); err != nil {
	return nil, err
}
```

If your `setup` no longer has that statement, `make:auth` says so and
you add the call yourself.

It also adds `@AccountMenu()` to the layout's header (after the nav's
`</nav>`): links to log in and register for guests; the user's name
(to the dashboard), the settings and a logout button for signed-in
users. `setupAuth` calls `sessions.Use(a.Middleware)`, so every page
with a session knows who is signed in, the home page of `routes/web.go`
included. A layout without that nav gets nothing, and `make:auth` prints
the line to add where you like.

The pages use the starter theme's classes ([Style your
app](styling.md)): a centered card for the forms, cards on the
dashboard and the settings page.

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
emailed link, logs in and out, turns on two-factor sign-in and signs in
with a recovery code, changes the name, password, language, time zone
and email address in the settings, resets the password, uses an API token,
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
| `app/models/user.go` | `User`, and `models.Users`: how `auth` finds users, which are disabled (`disabled_at`), and stores their tokens, session keys (`session_key`, replaced to sign them out everywhere) and two-factor state (`two_factor`, encrypted); the language and time zone they chose (`locale`, `time_zone`: `PreferredLocale`, `PreferredTimeZone`) and a new address waiting for its link (`pending_email`). Add columns here and in a migration |
| `app/handlers/auth.go` | `handlers.Accounts`: each page and form. Validation messages, redirects and what happens after registration are here; `SocialUser` finds or creates the user of a Google or GitHub account; `SendVerification` and `SendPasswordReset` email the links (the admin's buttons use them too) |
| `app/handlers/settings.go`, `views/settings.templ` | The settings page and its forms: add your own fields here |
| `views/auth.templ` | The pages, inside your `Layout` |
| `app/mailers/auth.go`, `views/auth_mail.templ` | The verification, reset and change-of-address emails |
| `routes/auth.go` | The routes and their names (`login`, `register`, `dashboard`, `settings`, …), the rate limits of the forgotten-password and verification forms, and what the settings allow (`AllowEmailChange`, `AllowAccountDeletion`) |
| `auth.go` | `setupAuth`: the `api_tokens` and `social_accounts` migrations, `auth.ForApp`, `social.ForApp` with the providers, and the routes |
| `database/migrations/…_create_users_table.go` | The users table |

The routes:

| Route | For |
|---|---|
| `GET`, `POST /register` | Guests: create an account, email the verification link, sign in (10 posts a minute per client IP address) |
| `GET`, `POST /login` | Guests: sign in (`AUTH_THROTTLE` limits failures) |
| `GET`, `POST /two-factor-challenge` | Guests whose sign-in waits for a two-factor code: the authenticator app's, or a recovery code |
| `GET`, `POST /forgot-password` | Guests: email a reset link (5 posts a minute per client IP address; 3 links an hour per address, the same answer after) |
| `GET`, `POST /reset-password` | Guests: choose a new password with the link. A reset signs the user out everywhere and revokes their API tokens (v0.3); for an address never verified, the link verifies it and turns off the two-factor sign-in and Google and GitHub links that whoever registered it first may have set up. Two uses of the link at once change the password once |
| `GET /auth/{provider}/redirect`, `GET /auth/{provider}/callback` | Guests: sign in with Google or GitHub (404 for a provider whose settings aren't set) |
| `GET /verify-email` | Anyone with the link: verify the address (400 if the link is bad, expired, or for an address the user has since changed) |
| `GET /dashboard` | Signed-in users: the account and its API tokens |
| `POST /logout` | Signed-in users |
| `POST /email/verification-notification` | Signed-in users: email the link again (3 a minute per client IP address, 6 an hour per account) |
| `POST /tokens` | Signed-in users who confirmed their password lately (v0.3): create an API token (shown once); refused while an admin acts as the user |
| `POST /tokens/{id}/delete` | Signed-in users: revoke an API token |
| `GET /settings`; `POST /settings/profile`, `/settings/password`, `/settings/preferences` | Signed-in users: their settings (below) |
| `POST /settings/email` | Signed-in users who confirmed their password lately: a new email address (with `AllowEmailChange`; 5 tries an hour per account) |
| `POST /settings/email/cancel` | Signed-in users: drop the change |
| `GET /settings/email/verify` | Anyone with the link emailed to the new address: make it the account's |
| `GET`, `POST /settings/email/revert` | Anyone with the link emailed to the old address: undo the change and secure the account (10 posts a minute per client IP address) |
| `POST /settings/delete` | Signed-in users who confirmed their password lately: delete the account (with `AllowAccountDeletion`) |
| `GET`, `POST /confirm-password` | Signed-in users: type the password again before a sensitive page (`a.RequireConfirmed`), then go back |
| `GET`, `POST /two-factor`, `POST /two-factor/confirm`, `/recovery-codes`, `/disable` | Signed-in users who confirmed their password lately: turn two-factor sign-in on (a QR code, then a code), get new recovery codes, turn it off. See [Two-factor sign-in](two-factor.md) |
| `GET /api/me` | API clients, with `Authorization: Bearer <token>` |

Logging in, registering and signing in with a provider lead to the page
the user asked for before logging in, or else to `AUTH_HOME_URL`:
`/dashboard`, the default `setupAuth` gives in `auth.go`
(`auth.DefaultHomeURL("/dashboard")`). To send users elsewhere, set
`AUTH_HOME_URL=/projects` in the environment, or change the default in
`auth.go`; the setting wins, so each deployment can choose. Signed-in
users who open a guest page go there too; guests who open a member page
go to `AUTH_LOGIN_URL` and come back after logging in. To protect your own pages, put them in a group with
`a.Middleware` and `a.Require`, as `routes/auth.go` does: `setupAuth`
returns the `*auth.Auth`.

Disabled users (`disabled_at` set, as the [admin](admin.md) does) are
signed out at their next request and see "This account is disabled."
when they sign in with the right password; their API tokens stop
working.

#### The settings page

`/settings` (`AUTH_SETTINGS_URL`; the dashboard and the
[admin](admin.md) link to it) lets a signed-in user change:

- **Their name.**
- **Their password**, with the current one; their other browsers and
  devices are signed out, and this one stays signed in (and remembered,
  if it was) (`auth.ChangePassword`). A user who signs in with Google or
  GitHub has no password: they set one within a few minutes of signing
  in.
- **Their language and time zone**, from the app's languages and
  `i18n.TimeZones()`; pages and emails use them
  ([translations](translations.md)). "Your browser's" language forgets
  the one chosen on this browser (`c.ForgetLocale`).
- **Their email address**, after typing their password again: that is
  what keeps someone with a stolen session from taking the account's
  sign-in and reset channel. The new address gets a link, and becomes
  theirs (verified) once it is followed, so a typo can't lock them out;
  until then they sign in with the old one. Once it is made, their other
  sessions and the reset links sent to the old address stop working
  (`auth.SignOutOthers`). The old address, if it was verified, is told
  of the change, with a link that undoes it, for `AUTH_REVERT_TTL` (7
  days), even once it is made: if the change wasn't theirs, someone has
  their password, so undoing it also secures the account. The address
  goes back (verified), the password is cleared, two-factor sign-in
  turned off, API tokens and links to Google and GitHub removed,
  everyone signed out, and the old address gets a link to choose a new
  password. The link opens a page with a button, as mail scanners
  follow links. Turn it off with
  `AllowEmailChange: false` in `routes/auth.go`, for apps whose
  addresses come from an organisation.
- **Delete their account**, after typing their password again: the user,
  their API tokens and Google or GitHub links go, and they're signed
  out. Off by default: `AllowAccountDeletion: true` turns it on. If the
  app gives users roles, remove them in `DeleteAccount` too
  (`rbac.RemoveUser`); other data of theirs is the app's to delete or
  keep.

It links to two-factor sign-in. To add a field (a phone number, a
newsletter choice), add a column and a migration, the field to
`User`, a form to `views/settings.templ` and a handler to
`app/handlers/settings.go`, as `UpdateProfile` does.

Apps that ran `make:auth` before have no settings page. To add it, take
from a new project's `make:auth`: the `pending_email`, `locale` and
`time_zone` columns (a migration) and `User` fields, with
`PreferredLocale` and `PreferredTimeZone`; `app/handlers/settings.go`,
`views/settings.templ`, the `ChangeEmail` and `EmailChanging` mailables
and their views; the `AllowEmailChange` and `AllowAccountDeletion` fields
of `Accounts` and the settings routes; the `auth.settings`, `auth.mail`
and other new keys of `locales/en/auth.yaml`. Then run `go tool anetos
gen` and `go tool templ generate`.

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
| `no app/models directory` | Not an `anetos new` project | Start from [`examples/auth`](../../../examples/auth) |
| `main.go doesn't have the routes.Register call` | `setup` was changed | Add the `setupAuth` call it prints |
| `no such table: users` | The migrations haven't run | `go run . migrate` |
| `URL needs the app's public URL` when registering | `APP_URL` isn't set | Set it in `.env` |
| No email in the log | `QUEUE_DRIVER=database` and the workers aren't running | Run the app with `go tool anetos dev` or `go run .`, or use `QUEUE_DRIVER=sync` |
| No "Sign in with Google" button | Its `SOCIAL_GOOGLE_*` settings are empty | Set both in `.env` |
| `redirect_uri_mismatch` at Google | The callback URL registered there isn't `APP_URL/auth/google/callback` | Fix `APP_URL` or the URL at Google |

## Next steps

- [Authentication](authentication.md): the `auth` package.
- [Social login](social-login.md): providers, linking and testing.
- [Two-factor sign-in and password confirmation](two-factor.md).
- [Send email](mail.md): send the emails with SMTP or Postmark.
- [Authorization](authorization.md): policies.

> **Coming from Laravel?** `anetos make:auth` is Breeze: the
> controllers, views and routes are published into your app. Hashing,
> remember-me, throttling and tokens (Fortify and Sanctum's parts) stay in
> the library, and so does the OAuth flow (Socialite's part).
