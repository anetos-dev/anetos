---
title: "2. Accounts"
since: v0.3.0
weight: 2
---

# 2. Accounts

Issues have authors, so the app needs accounts. One command writes them.

## Add accounts

```sh
go tool anetos make:auth
go run . migrate
```

`make:auth` writes the code of accounts into your project, as files you
own and can change:

| File | Holds |
|---|---|
| `app/models/user.go` | `User`, and `models.Users`, which tells package `auth` how to find and update users |
| `app/handlers/auth.go`, `app/handlers/settings.go` | Registration, sign-in (with a password, Google or GitHub), email verification, password reset, two-factor sign-in, API tokens, the settings page |
| `views/auth.templ`, `views/settings.templ` | Their pages |
| `routes/auth.go` | Their routes. Pages for signed-in users go in its `members` group |
| `auth_test.go` | Their tests |

`migrate` creates the `users`, `api_tokens` and `social_accounts`
tables.

## Try it

Open http://localhost:8080/register and sign up. The app sends a link to
verify your address: in development, emails go to the log
(`MAIL_DRIVER=log`), so look in the terminal where `anetos dev` runs and
open the link. You land on `/dashboard`; `/settings` changes your name,
password, language and time zone.

`go test ./...` now runs the accounts' tests too.

The [accounts guide](../../guides/accounts.md) explains what's there and
how to change it.

Next: [3. Issues](03-issues.md).
