---
title: Add accounts
since: v0.3.0
group: "Your first app"
weight: 23
---

# Add accounts

`make:auth` writes user accounts into your app: registration, login
with "remember me", login with Google and GitHub, email verification,
password reset, two-factor authentication, a settings page and API tokens,
with their pages, emails, routes and tests.

```sh
go tool anetos make:auth
go run . migrate
```

Open http://localhost:8080/register. The header now has links to log
in and register, and, once you're logged in, your name, the settings
and a logout button (`AccountMenu`, in `views/auth.templ`). In
development, emails go to the `anetos dev` log: open the verification
link from there.

## Where users land

After logging in, users go to the page they asked for before logging
in, or else to `/dashboard`; after registering, to `/dashboard`. To send
them elsewhere, set `AUTH_HOME_URL` in `.env` and in production:

```sh
AUTH_HOME_URL=/posts
```

or change the default in `auth.go`, which `make:auth` wrote:

```go
// illustrative
a, err := auth.New(app, models.Users, auth.DefaultHomeURL("/posts"))
```

The setting wins over the default, so each deployment can choose.

## Pages for logged-in users only

`routes/auth.go` has a `loggedIn` group: guests who open its pages go to
the login page and come back after logging in. Put your own routes
there, such as `make:crud`'s (`Posts(loggedIn)`), and read the user in a
handler with `auth.User[*models.User](c)`. Their tests then log a user
in first, with the factory `make:auth` wrote:

```go
// illustrative
app := anetostest.New(t, setup)
ada := anetostest.Create(app, factories.Users)
anetostest.ActingAs(app, &ada).Get("/posts").AssertOK()
```

[Add accounts with make:auth](../guides/accounts.md) describes every
page, setting and file, and how to change them.

Next: [Test and build](test-and-build.md).
