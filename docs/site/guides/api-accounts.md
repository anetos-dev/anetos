---
title: Add accounts to an API
since: v0.4.0
group: "Accounts and security"
weight: 301
---

# Add accounts to an API

In a project made with `anetos new --stack=api`, `make:auth` writes
accounts that sign in with API tokens: registration, login (with
two-factor codes for users who turn them on), logout, email
verification, password reset and change, and token management, as JSON
endpoints under `/api/v1`. There are no sessions or cookies: a client
keeps its token and sends it in every request.

## Before you start

- An API project: [Create a project](../getting-started/create-a-project.md#an-api-instead).
  In a project with pages, `make:auth` writes the pages instead
  ([Add accounts with make:auth](accounts.md)).
- A client app with pages for two emailed links: `/verify-email` and
  `/reset-password`, at the address in `AUTH_CLIENT_URL`.

## Steps

### 1. Generate the accounts

```sh
go tool anetos make:auth
go run . migrate
```

`make:auth` writes the `User` model and its migration,
`app/handlers/auth.go` (the handlers), `routes/auth.go` (the routes),
`app/mailers/` (the emails), `auth.go` (`setupAuth`, which `main.go`
now calls), `locales/en/auth.yaml` (the messages) and `auth_test.go`.
It adds `AUTH_CLIENT_URL` to `.env`, `.env.example` and
`deploy/production.env.example`. The code is yours: change the
handlers, the responses and the emails as your app needs.

### 2. Set the client app's address

The verification and password reset emails link to the client app,
not to the API:

```sh
AUTH_CLIENT_URL=http://localhost:5173
```

A link reads `http://localhost:5173/reset-password?token=…`. The
client app's page shows a form, and posts the token to the API. In
production and staging, the app refuses to start without
`AUTH_CLIENT_URL`, since no one could register.

### 3. Register and log in

```sh
curl -X POST http://localhost:8080/api/v1/register \
  -H 'Content-Type: application/json' \
  -d '{"name":"Ada","email":"ada@example.com","password":"correct horse","password_confirmation":"correct horse","device_name":"Ada'"'"'s phone"}'
```

The answer, `201 Created`, has a token and the user:

```json
{"token":"1|q7Xw…","user":{"id":1,"name":"Ada","email":"ada@example.com","email_verified_at":null,"created_at":"2026-10-08T09:00:00Z"}}
```

`POST /api/v1/login` with `email`, `password` and an optional
`device_name` answers the same way, with `200 OK`. Send the token in
every request:

```sh
curl http://localhost:8080/api/v1/me -H 'Authorization: Bearer 1|q7Xw…'
```

A login's token has every ability (`*`) and lasts 30 days; it's named
after `device_name` (`API` without one). `POST /api/v1/logout` revokes
it.

### 4. Two-factor sign-in

A user turns it on in three requests, with their token:

1. `POST /api/v1/two-factor` with their `password`: the key for the
   authenticator app, as `secret`, `uri` (`otpauth://`) and `qr_code`
   (an SVG image to show).
2. `POST /api/v1/two-factor/confirm` with a `code` of the app: it's on,
   and the answer has eight `recovery_codes` to show once. The user's
   other tokens are revoked: they were made with the password alone.
3. `GET /api/v1/two-factor` reports it: `enabled`, `started`,
   `recovery_codes` left.

From then on, the login answers a challenge instead of a token:

```json
{"two_factor":true,"challenge":"AQx…"}
```

The client asks for a code, and sends both to
`POST /api/v1/login/two-factor` (`challenge`, `code`, optional
`device_name`), which answers with the token. A recovery code works in
place of the app's code, once. The challenge works for 10 minutes; a
422 on the `challenge` field means it expired, and the user logs in
again.

`POST /api/v1/two-factor/recovery-codes` makes new recovery codes, and
`POST /api/v1/two-factor/disable` turns it off. Both take the
`password`.

### 5. Tokens for programs

`POST /api/v1/tokens` makes a token for a script or another service:
a `name`, the `abilities` it may use (every one, `*`, if none; up to
20 names of 1 to 100 characters, without spaces) and the user's
`password`. The answer, `201`, shows the token once; it lasts 90 days.
A token with narrower abilities than `*` uses your API but not the
account: `/password`, `/tokens` and `/two-factor` answer it 403, so a
leaked integration token can't make more tokens or revoke the user's
others. `GET /api/v1/tokens` lists the user's tokens (`current` marks
the request's), and `DELETE /api/v1/tokens/{id}` revokes one. Check an
ability in your handlers with `auth.TokenCan`:

```go
// illustrative
if !auth.TokenCan(c, "orders.write") {
	return nil, web.Error(http.StatusForbidden, "")
}
```

## The endpoints

| Method and path | With a token | Body | Answer |
|---|---|---|---|
| `POST /register` | no | `name`, `email`, `password`, `password_confirmation`, `device_name` | 201: `token`, `user` |
| `POST /login` | no | `email`, `password`, `device_name` | `token`, `user`; or `two_factor`, `challenge` |
| `POST /login/two-factor` | no | `challenge`, `code`, `device_name` | `token`, `user` |
| `POST /forgot-password` | no | `email` | 204, whether or not the address has an account |
| `POST /reset-password` | no | `token` (from the link), `password`, `password_confirmation` | 204; every token of the user is revoked |
| `POST /verify-email` | no | `token` (from the link) | 204 |
| `GET /me` | yes | | the user |
| `POST /logout` | yes | | 204; the token is revoked |
| `POST /email/verification-notification` | yes | | 204; the link again, unless verified |
| `PUT /password` | yes | `current_password`, `password`, `password_confirmation` | 204; the user's other tokens are revoked |
| `GET /tokens` | yes | | the tokens, newest first |
| `POST /tokens` | yes | `name`, `abilities`, `password` | 201: `token` and the token's fields |
| `DELETE /tokens/{id}` | yes | | 204 |
| `GET /two-factor` | yes | | `enabled`, `started`, `recovery_codes` |
| `POST /two-factor` | yes | `password` | `secret`, `uri`, `qr_code` |
| `POST /two-factor/confirm` | yes | `code` | `recovery_codes` |
| `POST /two-factor/recovery-codes` | yes | `password` | `recovery_codes` |
| `POST /two-factor/disable` | yes | `password` | 204 |

All paths are under `/api/v1`. The routes from `PUT /password` on need
a token with every ability (`*`). Errors are [problem details](handlers.md#errors):

- 422 with `errors` for the fields: a wrong password or a disabled
  account is an error of the `email` field at login (of `challenge` at
  `/login/two-factor`), of `password` or `current_password` elsewhere;
  a used or expired reset link, of `token`.
- 400 for a verification link that is malformed, expired, or for an
  address the user no longer has: the client app says so and offers a
  new one.
- 401 without a valid token, with `WWW-Authenticate: Bearer`; 403 for a
  token without the `*` ability on the account's routes.
- 409 when two-factor sign-in is on already (`POST /two-factor`) or off
  (`/two-factor/confirm` without a started setup,
  `/two-factor/recovery-codes`).
- 429 with `Retry-After` after too many tries.

## How it works

The handlers call package `auth` without a session:
`AttemptCredentials` checks the password with the same throttling as
the pages' login, and for a user with two-factor sign-in on returns a
`*auth.TwoFactorChallenge`, an encrypted note of the user and a
fingerprint of their password; `AttemptTwoFactorChallenge` checks it
and the code, each code working once. `CheckPassword` asks for the
password again where the pages use `RequireConfirmed`: a stolen token
alone can't make more tokens or change two-factor sign-in.
[Authentication](authentication.md) explains tokens and the rest.

The responses are structs of `app/handlers/auth.go`
(`UserResponse`, `SignInResponse`, `TokenResponse`…), never the
`User` model, so a new column never shows by accident. The emails are
`app/mailers/auth.html`, an `html/template` file, with their text in
`locales/en/auth.yaml`: `anetos lang:add` brings the same keys'
translations as the pages' ([Translations](translations.md)).

Throttling: registration 10 a minute per client address, reset
requests 5 a minute per client address and 3 links an hour per email
address, verification links 3 a minute per client address and 6 an
hour per user, logins and codes as `AUTH_THROTTLE` and
`AUTH_THROTTLE_IP` say.

> **Note:** Signing in with Google or GitHub, changing the email
> address and deleting the account are the pages' features: an API
> project's `make:auth` leaves them out. Add the endpoints you need to
> the generated handlers.

## Testing it

`auth_test.go` tests every endpoint through HTTP: registering,
verifying, logging in and out, two-factor sign-in, resets, password
changes and tokens. It sets `AUTH_CLIENT_URL` with `anetostest.Env`,
reads the emails' links with `anetostest.Mailables`, and signs requests
with `app.WithHeader("Authorization", "Bearer "+token)`. Run it with
`go test ./...`.

## Common problems

- **Registration answers 500 "AUTH_CLIENT_URL isn't set".** The emails
  need the client app's address: set `AUTH_CLIENT_URL` (in tests, with
  `anetostest.Env`).
- **Every request answers 401 after a password reset.** A reset
  revokes every token of the user, and a new password every other
  token: log in again.
- **A browser app can't call the API.** List its address in
  `HTTP_CORS_ORIGINS` ([Configuration](../reference/configuration.md)).

## Next steps

- [Authentication](authentication.md): package `auth`, tokens and
  abilities.
- [Roles and permissions](roles-and-permissions.md): what users may do,
  with tokens' abilities as a ceiling.
- [Rate limiting](rate-limiting.md).
