---
title: Authentication reference
since: v0.2.0
---

# Authentication reference

Packages `auth`, `auth/password` and `auth/social`. How-to: [Authentication](../guides/authentication.md),
[Authorization](../guides/authorization.md). Settings: [configuration reference](configuration.md#authentication).

## Setup

| API | Does |
|---|---|
| `auth.Authenticatable` | `AuthID() string` and `AuthPassword() string`, implemented by the app's user type |
| `auth.Users[U]{ByID, ByLogin, RememberToken, SetRememberToken, SetPassword}` | How to find users (required: `ByID`, `ByLogin`; they return `db.ErrNotFound` or `auth.ErrNoUser`), store remember-me tokens and upgraded hashes |
| `auth.ForApp(app, users)` | `*auth.Auth[U]` from `AUTH_*` and `APP_KEY`; needs `cache.ForApp` first; provided to the app; once per app |
| `auth.New(cfg, users, enc, opts...)` | Without an app; `auth.WithLogger`, `auth.WithInsecureCookies` |
| `auth.Migrations()` | The `api_tokens` table, for `migrate.ForApp` |

## Middleware

| API | Does |
|---|---|
| `a.Middleware` | Makes the request's user available (from the session or a remember-me cookie); after the session middleware |
| `a.Require` | Signed-in users only: guests asking for a page are redirected to `AUTH_LOGIN_URL`; others, and every request on routes without sessions, get 401 |
| `a.Guest` | Guests only: signed-in users are redirected to `AUTH_HOME_URL` |
| `a.TokenMiddleware` | Signs in the user of a `Bearer` API token; invalid tokens get 401; no token (or another scheme) passes as a guest |

## Signing in and out

| API | Does |
|---|---|
| `a.Attempt(ctx, login, password, remember)` | Checks the password and signs in; `auth.ErrInvalidCredentials` (401), `*auth.ThrottledError` (429) past `AUTH_THROTTLE` attempts a minute per login or account and IP, or `AUTH_THROTTLE_IP` failures per IP |
| `a.Login(ctx, u, remember)` | Signs `u` in: new session ID; with `remember`, the remember-me cookie |
| `a.CanRemember()` | Whether "remember me" is available (`Users.RememberToken` set) |
| `a.Logout(ctx)` | Empties the session, removes the cookie, replaces the remember token |
| `auth.User[U](ctx)` | The signed-in user, and whether there is one |
| `auth.Current[U](ctx)` | The signed-in user, `auth.ErrUnauthenticated`, or the load error |
| `auth.Check(ctx)` | Whether a user is signed in |
| `auth.Intended(ctx, fallback)` | The page a guest asked for before logging in, or `fallback` |

## Tokens

| API | Does |
|---|---|
| `a.PasswordResetToken(u)`, `a.CheckPasswordResetToken(ctx, token)` | Reset tokens: `AUTH_RESET_TTL`, until the password changes; `auth.ErrInvalidToken` (400) |
| `a.VerificationToken(u, email)`, `a.CheckVerificationToken(ctx, token)` | Email-verification tokens: `AUTH_VERIFY_TTL`; returns the user and the address |
| `a.CreateToken(ctx, u, name, abilities, ttl)` | An API token (`<id>\|<secret>`, shown once) and its stored `auth.Token` |
| `a.Tokens(ctx, u)`, `a.RevokeToken(ctx, u, id)`, `a.RevokeAllTokens(ctx, u)` | A user's API tokens; delete one, or all |
| `auth.CurrentToken(ctx)`, `auth.TokenCan(ctx, ability)` | The request's API token; whether it (or a session user) may do `ability` (`"*"`: all) |

## Authorization

| API | Does |
|---|---|
| `auth.Authorize(ctx, policy, subject)` | nil, `auth.ErrUnauthenticated` (401) or `auth.ErrForbidden` (403); `policy` is `func(context.Context, U, T) bool` |
| `auth.AuthorizeUser(ctx, policy)` | The same for `func(context.Context, U) bool` |
| `auth.Allows`, `auth.AllowsUser` | The answer as a boolean (false for a guest) |
| `auth.IsDenied(err)` | Whether `err` is `ErrUnauthenticated` or `ErrForbidden` |

## Passwords

| API | Does |
|---|---|
| `password.Hash(pw)` | argon2id hash (19 MiB, 2 passes, 1 thread), as a PHC string; `password.ErrTooLong` over 1024 bytes |
| `password.Verify(pw, hash)` | Checks against argon2id or bcrypt (`$2a$`, `$2b$`, `$2y$`) hashes |
| `password.NeedsRehash(hash)` | bcrypt, or argon2id weaker than the defaults |
| `password.HashWith(pw, params)`, `password.Defaults()` | Other parameters (at most 256 MiB and 10 passes) |
| `password.IsBcrypt(hash)` | Whether the hash is bcrypt, which checks only a password's first 72 bytes |
| `password.Dummy(pw)` | Spends the time of a check, for unknown users |
| `password.VerifyContext(ctx, pw, hash)`, `password.DummyContext(ctx, pw)` | The same, giving up with ctx's error while waiting for a free slot (a few hashes are computed at once) |

## Social login

| API | Does |
|---|---|
| `social.Google()`, `social.GitHub()`, `social.GitHubAt(web, api)`, `social.OIDC(name, issuer)` | Providers; their `Scopes` and other fields can be changed |
| `social.Configured(app, providers...)` | The providers whose `SOCIAL_<NAME>_CLIENT_ID` and `_CLIENT_SECRET` are set |
| `social.ForApp(app, a, resolve, providers, opts...)` | `*social.Social[U]`; needs `APP_URL` (https in production); with no providers, its routes answer 404; `social.WithHTTPClient`, `social.WithLogger`, `social.WithCallbackPath` |
| `social.New(a, resolve, baseURL, creds, providers, opts...)` | Without an app |
| `s.Redirect`, `s.Callback` | Handlers for `/auth/{provider}/redirect` (`?remember=1`) and `/auth/{provider}/callback` |
| `s.Providers()`, `s.CallbackURL(name)` | Provider names, in the order given; a provider's callback URL to register |
| `social.Resolver[U]`, `social.Profile` | `func(ctx, Profile) (U, error)`: finds or creates the user; the profile has `Provider`, `Subject`, `Email`, `EmailVerified`, `Name`, `AvatarURL`, `Token` |
| `social.ErrNoAccount{Message}` | Refuses a sign-in with a message on the login page (`social` field) |
| `social.Migrations()` | The `social_accounts` table |
| `social.FindLink(ctx, p)`, `social.Link(ctx, p, userID)`, `social.Links(ctx, userID)`, `social.Unlink(ctx, userID, provider)` | Links between provider accounts and users |

