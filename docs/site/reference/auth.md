---
title: Authentication reference
since: v0.2.0
---

# Authentication reference

Packages `auth`, `auth/password`, `auth/social` and `auth/rbac`. How-to: [Authentication](../guides/authentication.md),
[Authorization](../guides/authorization.md), [Roles and permissions](../guides/roles-and-permissions.md). Settings: [configuration reference](configuration.md#authentication).

## Setup

| API | Does |
|---|---|
| `auth.Authenticatable` | `AuthID() string` and `AuthPassword() string`, implemented by the app's user type |
| `auth.Users[U]{ByID, ByLogin, RememberToken, SetRememberToken, SetPassword, Disabled, SessionKey, SetSessionKey}` | How to find users (required: `ByID`, `ByLogin`; they return `db.ErrNotFound` or `auth.ErrNoUser`), store remember-me tokens and upgraded hashes; which accounts are disabled (`Disabled`, v0.3); the session key sessions are bound to (`SessionKey` and `SetSessionKey`, both or neither, v0.3) |
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
| `auth.CurrentID(ctx)` | The signed-in user's `AuthID`, `auth.ErrUnauthenticated`, or the load error, for code that works with any user type (v0.3) |
| `a.ActAs(ctx, userID, opts...)`, `auth.ActAs(ctx, userID, opts...)` | A context whose signed-in user is that user (loaded with `Users.ByID` when asked for; none if it doesn't exist), for queue jobs and commands working for a user; no session (`Attempt`, `Login` and `Logout` refuse) and no token, unless `auth.WithAbilities(abilities)` gives it a token's limits. The function finds the app's Auth in ctx (v0.3) |
| `auth.Intended(ctx, fallback)` | The page a guest asked for before logging in, or `fallback` |

## Accounts

| API | Does |
|---|---|
| `Users.Disabled`, `a.Disabled(u)` | A disabled user is signed out on their next request; `Attempt` (once the password checks out) and `Login` return `auth.ErrDisabled` (403); their remember-me cookies and API tokens stop working; `ActAs` treats them as a guest (v0.3) |
| `a.SignOutEverywhere(ctx, u)`, `a.CanSignOutEverywhere()` | Ends every session and remember-me cookie of `u` by giving them a new session key (`Users.SetSessionKey`) and remember token; API tokens stay (v0.3) |
| `a.Impersonate(ctx, u)` | Signs the current user in as `u`, keeping who they are in the session; not through API tokens, not nested, not for a disabled `u` (`ErrDisabled`) or oneself. Each request checks that the impersonator may still sign in; `Logout` ends both (leaving `u`'s remember-me token alone). The impersonator's remember-me cookie is removed. Check who may first (`rbac.AuthorizeOver`) (v0.3) |
| `a.StopImpersonating(ctx)` | Signs the impersonator back in and returns them; `auth.ErrNotImpersonating` (409) without impersonation; if they can't sign in any more, signs out and returns why (v0.3) |
| `auth.Impersonator(ctx)` | The ID of the user acting as the signed-in one, and whether there is one (v0.3) |

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
| `social.Google()`, `social.GitHub()`, `social.GitHubAt(web, api)`, `social.OIDC(name, issuer)` | Providers; their `Scopes`, `Title` ("Google", for buttons; `OIDC`'s defaults to its name) and other fields can be changed |
| `social.Configured(app, providers...)` | The providers whose `SOCIAL_<NAME>_CLIENT_ID` and `_CLIENT_SECRET` are set |
| `social.ForApp(app, a, resolve, providers, opts...)` | `*social.Social[U]`; needs `APP_URL` (https in production); with no providers, its routes answer 404; `social.WithHTTPClient`, `social.WithLogger`, `social.WithCallbackPath`, `social.WithHomeURL(path)` (where users go without an intended page; default `AUTH_HOME_URL`) |
| `social.New(a, resolve, baseURL, creds, providers, opts...)` | Without an app |
| `s.Redirect`, `s.Callback` | Handlers for `/auth/{provider}/redirect` (`?remember=1`) and `/auth/{provider}/callback` |
| `s.Providers()`, `s.CallbackURL(name)` | Provider names, in the order given; a provider's callback URL to register |
| `social.Resolver[U]`, `social.Profile` | `func(ctx, Profile) (U, error)`: finds or creates the user; the profile has `Provider`, `Subject`, `Email`, `EmailVerified`, `Name`, `AvatarURL`, `Token` |
| `social.ErrNoAccount{Message}` | Refuses a sign-in with a message on the login page (`social` field) |
| `s.Providers()`, `s.Title(name)` | The providers' names, in the order given, and their titles, for sign-in buttons |
| `social.Migrations()` | The `social_accounts` table |
| `social.FindLink(ctx, p)`, `social.Link(ctx, p, userID)`, `social.Links(ctx, userID)`, `social.Unlink(ctx, userID, provider)` | Links between provider accounts and users |

## Roles and permissions

Package `auth/rbac` (v0.3). Concepts: [Roles and permissions](../concepts/roles-and-permissions.md).

### Declaring

| API | Does |
|---|---|
| `rbac.Permission` | A string type: `const EditPosts rbac.Permission = "posts.edit"`; lowercase letters, digits and `. _ : -`, up to 100 characters; also the API token ability that allows it |
| `rbac.Role{Name, Title, Permissions, Super, Custom}` | A role; `Super` has every permission (code only); `Custom` is set for roles of the database. `r.Allows(p)`, `r.DisplayName()` (the title, or the name) |
| `rbac.ForApp(app, permissions, roles...)` | `*rbac.Registry`, checked (unique names, roles of declared permissions); provided to the app and its contexts; caches grants per unit of work; adds the commands below; once per app |
| `rbac.New(permissions, roles...)`, `rbac.WithRegistry(ctx, reg)`, `rbac.From(ctx)` | Without an app; `rbac.ErrNoRegistry` when the context has none |
| `reg.Declare(permissions...)` | Adds permissions to the registry, for packages that bring their own (the admin); idempotent; call it at setup, before the app serves (v0.3) |
| `rbac.AuthorizeOver(ctx, userID)` | nil if the signed-in user has every permission `userID` has in every scope (and a super role wherever they have one), else `auth.ErrForbidden`: for managing their account (v0.3) |
| `rbac.GivenTo(ctx, userID)` | The roles and permissions given to a user, as stored (`rbac.Given{Scope, Role, Permission}`), by scope and name (v0.3) |
| `rbac.RoleCounts(ctx)`, `rbac.Holders(ctx, role, limit)` | How many users have each role; who has a role, and where (`rbac.Holder{UserID, Scope}`) (v0.3) |
| `reg.Permissions()`, `reg.Declared(p)`, `reg.Roles()`, `reg.Role(name)` | What was declared |
| `rbac.Migrations()` | The `rbac_grants` and `rbac_roles` tables |

### Scopes

| API | Does |
|---|---|
| `rbac.Scope`, `rbac.Global` | Where a grant applies: `rbac.Global` (`""`) everywhere, or one scope |
| `rbac.ScopeOf(kind, id)` | `"team:42"`; `kind` is lowercase letters, digits, `_` and `-` (panics otherwise); IDs compare exactly (`ABC` isn't `abc`, on every database); stored scopes are up to 100 bytes (`rbac.ErrInvalidScope`, 422) |
| `s.Kind()`, `s.ID()`, `s.String()` | `"team"`, `"42"`; `"global"` for `rbac.Global` |

### Checking the signed-in user

A global grant applies in every scope. For a request signed in with an
API token, a permission must also be among the token's abilities (or
the token has `*`), and role checks are false unless it has `*`.

| API | Does |
|---|---|
| `rbac.Authorize(ctx, p)`, `rbac.AuthorizeIn(ctx, scope, p)` | nil, `auth.ErrUnauthenticated` (401), `auth.ErrForbidden` (403), or another error (the database; an undeclared permission, 500) |
| `rbac.Can(ctx, p)`, `rbac.CanIn(ctx, scope, p)` | The answer as a boolean: false for a guest; other errors are logged |
| `rbac.HasRole(ctx, role)`, `rbac.HasRoleIn(ctx, scope, role)` | Whether the user has the role in scope or globally |
| `rbac.AuthorizeRole(ctx, scope, role)` | nil if the user may give the role in scope: they have every permission it allows there (super there, with a `*` token, for a super role); else 401, 403 or `rbac.ErrUnknownRole`. Compares permissions, not names |
| `rbac.AuthorizeRolesOf(ctx, scope, userID)` | nil if the user may give every role `userID` has in exactly `scope`, so may change or remove them; else 401 or 403 |
| `rbac.Require(perms...)`, `rbac.RequireIn(scopeFn, perms...)` | Middleware: every permission, globally or in `scopeFn(r)`'s scope; the error of `Authorize` otherwise. After the auth middleware |
| `rbac.PathScope(kind, param)` | A `scopeFn` from a path parameter: `PathScope("team", "team")` on `/teams/{team}`; 404 without it |
| `rbac.Current(ctx)` | The signed-in user's `*rbac.Grants` (token limits applied), or `auth.ErrUnauthenticated` |

### Any user's grants

| API | Does |
|---|---|
| `rbac.Of(ctx, userID)` | The user's `*rbac.Grants`, read in one query (two with roles of the database) once per unit of work (request, job, listener, task, tool call) |
| `g.Can(p)`, `g.CanIn(scope, p)` | Whether a grant in scope, or a global one, allows `p` |
| `g.HasRole(role)`, `g.HasRoleIn(scope, role)` | Whether the user has the role in scope or globally (declared or stored roles only; none for a token without `*`) |
| `g.Roles(scope)` | The roles assigned in exactly `scope`, by name (the same rules) |
| `g.Permissions(scope)` | The permissions allowed in `scope` (with global grants), in declaration order |
| `g.Scopes(kind)` | The scopes of `kind` where the user has a role or a declared permission, sorted: their teams |

### Changing grants

| API | Does |
|---|---|
| `rbac.Assign(ctx, userID, scope, roles...)` | Adds roles (declared, or of the database: `rbac.ErrUnknownRole`, 422); user IDs are up to 100 bytes |
| `rbac.Unassign(ctx, userID, scope, roles...)` | Takes roles away in exactly `scope` |
| `rbac.Sync(ctx, userID, scope, roles...)` | Makes `roles` the user's only roles in `scope` (none: removes them all) |
| `rbac.Grant(ctx, userID, scope, perms...)`, `rbac.Revoke(…)` | Single permissions without a role (`rbac.ErrUnknownPermission`, 422) |
| `rbac.RemoveUser(ctx, userID)` | Every grant of the user, in every scope |
| `rbac.RemoveScope(ctx, scope)` | Every grant in the scope (not `rbac.Global`) |
| `rbac.Assignments(ctx, scope)` | `[]rbac.Assignment{UserID, Role}` in exactly `scope`, by user and role: its members |
| `rbac.UsersWith(ctx, scope, p)` | IDs of the users allowed `p` in `scope` (with global grants), sorted |

### Roles of the database

| API | Does |
|---|---|
| `rbac.CreateRole(ctx, role)` | Stores a role of declared permissions; `rbac.ErrRoleExists` (409), `rbac.ErrInvalidRole` (422: invalid name or title, a code role's name, super); users still holding a removed code role of the name lose it |
| `rbac.UpdateRole(ctx, role)` | Replaces its title and permissions; `rbac.ErrUnknownRole`, `rbac.ErrInvalidRole` for a code role |
| `rbac.DeleteRole(ctx, name)` | Deletes it and its assignments |
| `rbac.Roles(ctx)`, `rbac.FindRole(ctx, name)` | Every role, code's first; one role, code's or the database's |

### Commands

| Command | Does |
|---|---|
| `rbac:roles` | The roles, where they're defined, their users and permissions; warns of assigned roles that are neither declared nor stored, and of stored roles a code role replaces |
| `rbac:user <user-id>` | A user's roles and permissions by scope |
| `rbac:assign [--scope=team:42] <user-id> <role>` | Gives a role, globally without `--scope` |
| `rbac:unassign [--scope=team:42] <user-id> <role>` | Takes it away |
