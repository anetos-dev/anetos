---
title: Social login
since: v0.2.0
---

# Social login

Let users sign in with Google, GitHub, or any OpenID Connect provider
(Okta, Auth0, Microsoft Entra ID, Keycloak, GitLab…). The complete app is
[`examples/auth`](../../../examples/auth).

An app made with [`make:auth`](accounts.md) has Google and GitHub set up
already: set their settings (below) and the buttons appear. This guide
explains what that code does, and how to add social login to an app of
your own.

## Before you start

Set up [authentication](authentication.md) first: social login finds or
creates your user and signs them in with `auth`. You also need:

- `APP_URL`, the app's public URL (`https://example.com`, or
  `http://localhost:8080` in development). Providers send users back to
  `APP_URL/auth/<provider>/callback`.
- An OAuth app at each provider, with that callback URL registered: the
  [Google Cloud console](https://console.cloud.google.com/apis/credentials)
  ("OAuth client ID", type "Web application") or
  [GitHub](https://github.com/settings/developers) ("New OAuth App").
  Put its client ID and secret in `SOCIAL_<NAME>_CLIENT_ID` and
  `SOCIAL_<NAME>_CLIENT_SECRET`:

```env
APP_URL=https://example.com
SOCIAL_GOOGLE_CLIENT_ID=1234-abc.apps.googleusercontent.com
SOCIAL_GOOGLE_CLIENT_SECRET=…
SOCIAL_GITHUB_CLIENT_ID=Iv1.abc
SOCIAL_GITHUB_CLIENT_SECRET=…
```

## Steps

### 1. Link accounts to users

Add `social.Migrations()` to your migrations, for the `social_accounts`
table that links provider accounts to users. Then write the function
that finds the user for a provider account:

```go
// findOrCreate returns the user of a provider account: the one it is
// linked to; else the user with its verified email address (who then
// signs in with either); else a new user. An address the provider hasn't
// verified can't be trusted to find anyone, and nor can one the user
// hasn't verified here: someone could have registered it first, with a
// password, to take over the account of whoever signs in with it later.
func findOrCreate(ctx context.Context, p social.Profile) (*User, error) {
	id, linked, err := social.FindLink(ctx, p)
	if err != nil {
		return nil, err
	}
	if linked {
		u, err := users.ByID(ctx, id)
		if !errors.Is(err, db.ErrNotFound) {
			return u, err
		}
		// The user was deleted: forget the link and start again.
		if err := social.Unlink(ctx, id, p.Provider); err != nil {
			return nil, err
		}
	}
	if p.Email == "" || !p.EmailVerified {
		return nil, &social.ErrNoAccount{Message: "Your " + p.Provider + " account has no verified email address."}
	}
	var u *User
	err = db.Tx(ctx, func(ctx context.Context) error { // the user and the link, or neither
		u, err = users.ByLogin(ctx, p.Email)
		if errors.Is(err, db.ErrNotFound) {
			now := anetos.Now(ctx).UTC()
			name := p.Name
			if name == "" {
				name = p.Email
			}
			u = &User{Name: name, Email: strings.ToLower(p.Email), EmailVerifiedAt: &now} // no password
			err = db.Create(ctx, u)
		}
		if err != nil {
			return err
		}
		if u.EmailVerifiedAt == nil {
			return &social.ErrNoAccount{Message: "An account with this email address exists. Log in with your password and verify the address, then sign in with " + p.Provider + "."}
		}
		// Linked already to another account at the provider: the address
		// was reused, not the same person.
		links, err := social.Links(ctx, u.AuthID())
		if err != nil {
			return err
		}
		if slices.ContainsFunc(links, func(l social.Account) bool { return l.Provider == p.Provider }) {
			return &social.ErrNoAccount{Message: "The account with this email address signs in with another " + p.Provider + " account."}
		}
		return social.Link(ctx, p, u.AuthID())
	})
	return u, err
}
```

(Copied from [`examples/auth/users.go`](../../../examples/auth/users.go), region `social`.)

Link users by the account (`FindLink`, `Link`), not by email: addresses
change, and can be reused. When you delete a user, delete their links
(`social.Unlink`); the example also forgets a link whose user is gone. Only use an email address to find an existing
user when both the provider and your app have verified it; otherwise
someone could register the address first, with a password, and take over
the account of whoever later signs in with it. Nor does the example link
a second account of the same provider to a user by email: an address
the provider gave to someone new (a closed account, a reused work
address) isn't the same person. Return `*social.ErrNoAccount` to refuse
with a message on the login page.

### 2. Set up the providers and routes

```go
// Providers with SOCIAL_<NAME>_CLIENT_ID and _CLIENT_SECRET set; their
// callbacks are APP_URL/auth/<name>/callback.
s, err := social.ForApp(app, a, findOrCreate, social.Configured(app, social.Google(), social.GitHub()))
if err != nil {
	return nil, err
}
```

(Copied from [`examples/auth`](../../../examples/auth/main.go), region `social-setup`.)

`social.Configured` keeps the providers whose credentials are set, so a
provider is enabled by its settings; with none, the routes answer 404.
For another OpenID Connect provider, add
`social.OIDC("okta", "https://example.okta.com")`: its endpoints are read
from the issuer's discovery document. Give the issuer exactly as the
provider writes it, trailing slash included (Auth0:
`https://TENANT.auth0.com/`), and a tenant's own issuer rather than a
multi-tenant one (Microsoft Entra ID's `/common`). Microsoft Entra ID
doesn't send `email_verified` by default, so its addresses count as
unverified. Every URL of a provider must be `https`.

Add the redirect and callback routes to the guest pages (see the routes in
[Authentication](authentication.md#2-set-up-auth-and-protect-routes)):

```go
// illustrative
guests.Get("/auth/{provider}/redirect", s.Redirect)
guests.Get("/auth/{provider}/callback", s.Callback)
```

and a link per provider on the login page, from `s.Providers()` (their
names, in URLs) and `s.Title(name)` (their names for people, "Google"):

```html
<a href="/auth/github/redirect">Sign in with GitHub</a>
<a href="/auth/google/redirect?remember=1">Sign in with Google (remember me)</a>
```

A failed or canceled sign-in returns to `AUTH_LOGIN_URL` with an error on
the `social` field (`view.Errors(ctx).Get("social")`); a successful one
goes to the page the user wanted, or `AUTH_HOME_URL`
(`social.WithHomeURL("/dashboard")` sets another).

### 3. Test

With the `anetostest.FakeSocial` option, every provider signs in through a
stand-in provider the test controls (nothing reaches Google or GitHub,
and no settings are needed). `app.SocialSignIn` follows the redirect
route, plays the provider's page for the account you give, and returns
the app's answer to the callback:

```go
func TestSocialSignIn(t *testing.T) {
	// FakeSocial: Google and GitHub sign in through a stand-in provider.
	app := anetostest.New(t, setup, anetostest.FakeSocial())
	app.Get("/login").AssertSee(`href="/auth/google/redirect"`, "Sign in with Google")

	// A new account: a user is created, with the address verified.
	grace := anetostest.SocialAccount{ID: "g-1", Email: "grace@example.com", EmailVerified: true, Name: "Grace"}
	app.SocialSignIn("/auth/google/redirect", grace).AssertRedirect("/") // AUTH_HOME_URL
	app.Get("/dashboard").AssertSee("Hello, Grace").AssertDontSee("Please verify")
	app.PostForm("/logout", nil)

	// The same account again, with a new address: the linked user.
	grace.Email = "grace@new.example"
	app.SocialSignIn("/auth/google/redirect", grace).AssertRedirect("/")
	n, err := db.RawFirst[int64](app.Context(), "SELECT COUNT(*) FROM users")
	if err != nil || n != 1 {
		t.Errorf("users: %d, %v", n, err)
	}
}
```

(Copied from [`examples/auth/social_test.go`](../../../examples/auth/social_test.go), region `test-social`.)

## How it works

`Redirect` sends the browser to the provider with a random `state`, a
PKCE code challenge and, for OpenID Connect, a `nonce`, and keeps them in
the session. `Callback` accepts the provider's answer only with that
state, once, within ten minutes; exchanges the code for tokens with the
PKCE verifier; and reads the profile. For OpenID Connect providers the
profile comes from the ID token, whose issuer, audience, expiry and nonce
are checked; its signature isn't, because it came straight from the
provider's token endpoint over TLS (which OpenID Connect allows, and
which is why endpoints must be `https`). For GitHub, it comes from the
API: the account's primary address, and whether GitHub verified it.

Requests to providers refuse redirects to anything but `https`, and a
provider's discovery document is fetched in the background (a failure
is retried after 30 seconds, and the document is refreshed daily, keeping
the old endpoints if that fails), so a slow provider doesn't hold up
requests past their deadline. A fetch in progress at shutdown may run for
up to 10 more seconds.

`anetostest.FakeSocial` runs a stand-in OpenID Connect provider on a
local TLS server and hands it to the app (through an internal hook,
honored only with `APP_ENV=testing`) before `setup` runs:
`social.ForApp` then points every provider at it (keeping their names,
titles and scopes), and `social.Configured` keeps every provider. The
flow is the real one, state, PKCE and ID token checks included, with
one difference: every provider signs in as an OpenID Connect provider,
so a provider that reads the profile from its API (GitHub's
`/user/emails`, a `Provider.Profile` of your own) doesn't run that code
in these tests.

`Profile.Token` holds the provider's tokens, for calling its API with the
scopes asked for; change `Provider.Scopes` to ask for more.

> **Coming from Laravel?** This is Socialite's `redirect()` and `user()`
> as two handlers, with the find-or-create step as your function.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `social: APP_URL "" must be the app's public URL` | `APP_URL` isn't set | Set it; it must match the callback URL registered with the provider |
| The provider says `redirect_uri_mismatch` | The registered callback URL differs from `APP_URL/auth/<name>/callback` | Register exactly that URL (scheme, host, port) |
| Every sign-in returns to the login page with "didn't complete" | The session cookie isn't sent back from the provider (a `SameSite=Strict` cookie, or another host) | Keep `SESSION_SAME_SITE=lax` and use the same host as `APP_URL` |
| `social: set SOCIAL_GOOGLE_CLIENT_ID and SOCIAL_GOOGLE_CLIENT_SECRET` | A provider was passed without credentials | Set them, or pass the providers through `social.Configured` |
| "has no verified email address" | The account's address isn't verified at the provider | Verify it there, or link accounts another way |

## Next steps

- [Authentication](authentication.md)
- [Authentication reference](../reference/auth.md#social-login)
