---
title: Authentication and authorization
since: v0.2.0
group: "Accounts and security"
weight: 400
---

# Authentication and authorization

How Anetos knows who is making a request, and what they may do.

## Who is signed in

A signed-in browser carries the session cookie; the session holds the
user's ID and a fingerprint of their password hash. `a.Middleware` puts
a small record in the request's context, and the user is loaded from the
database the first time a handler (or `a.Require`) asks for it. If the
fingerprint no longer matches, the password has changed since this
session signed in, and the session is signed out: changing a password
ends every other session.

Signing in gives the session a new ID, so an ID that was known before
(planted by an attacker, or seen on a shared computer) is useless
afterwards. Signing out empties the session and, with a server-side
session driver, removes it from the store. With cookie sessions there is
nothing on the server to remove: a copy of the cookie taken before the
logout works until it expires.

"Remember me" adds a second, long-lived cookie, encrypted with
`APP_KEY`, holding the user's ID, a random remember token stored with the
user, the password fingerprint and an expiry checked on the server. When the session has ended, the cookie signs the user in to a
new one. Logging out replaces the token, which signs the user out of every
remembered browser at once.

API clients don't use cookies: they send `Authorization: Bearer
<id>|<secret>`. The database holds only a SHA-256 hash of the secret,
with the token's abilities and expiry.

Social login signs users in with an account elsewhere: the app sends
the browser to the provider with a one-time state and a PKCE challenge,
checks what comes back, and asks your code which user the account
belongs to (links are kept in `social_accounts`), then signs that user in
like a password login.

Two-factor sign-in adds a second step after either: the session holds a
sign-in waiting for a code (not a signed-in user) for ten minutes, until
the code of the user's authenticator app, or a recovery code, finishes
it. The app's secret for that user is stored encrypted with `APP_KEY`;
recovery codes, like API tokens, only as hashes. Before sensitive pages,
the app can ask for the password again: the session remembers when it
was last confirmed.

## What is stored, and what isn't

Password-reset and email-verification links carry tokens that are
encrypted with `APP_KEY` rather than stored: the user's ID, an expiry
and, for resets, the password fingerprint. A reset token therefore stops
working as soon as the password changes, so it can be used once, and
there is no table of pending resets to clean up.

Passwords are hashed with argon2id, which makes guessing slow and
memory-hungry for an attacker; only a few hashes are computed at once, so
a flood of logins waits instead of exhausting memory. Failed logins are
counted in the cache per login and per account (from one IP address), and
per IP address, and refused past their limits.

## What a user may do

Authorization uses typed policies: functions that take the user and the
thing acted on and return a boolean. `auth.Authorize` finds the signed-in
user and calls the policy; its errors carry the HTTP status (401 for no
user, 403 for a refusal), so handlers return them as they are. Because
policies are Go functions, the compiler checks that the user and subject
types match, and there are no ability names to misspell.

Roles say what a user may do from who they are rather than from the
thing acted on: package `auth/rbac` gives users roles, globally or in a
scope such as a team, made of permissions declared in code. See [Roles
and permissions](roles-and-permissions.md).

## Related

- [Authentication](../guides/authentication.md), [Authorization](../guides/authorization.md), [Roles and permissions](../guides/roles-and-permissions.md), [Social login](../guides/social-login.md), [Two-factor sign-in](../guides/two-factor.md)
- [Authentication reference](../reference/auth.md)
- [Sessions and flash messages](../guides/sessions.md)
