---
title: Secure your app
since: v0.3.0
group: "Production"
weight: 803
---

# Secure your app

Anetos starts safe: CSRF protection, encrypted session cookies,
parameterized queries, escaped views, security headers, throttled
logins. What it can't choose for you are the settings and the
deployment (a key, HTTPS, the proxies to trust, the database's
connection), and what only your code knows: who may read or change
whose records. This guide covers what Anetos protects in each stack,
the tools that check the rest, and the checklist before you go live.

## What Anetos protects

In every stack (the accounts' and sessions' rows once `make:auth` has added them to an API project):

| Defense | Where |
|---|---|
| Headers: `X-Content-Type-Options`, `X-Frame-Options: SAMEORIGIN`, `Referrer-Policy`, `Cross-Origin-Opener-Policy`; HSTS in production | `web.NewServer` |
| Limits: 10 MB bodies, 30 s requests, 10 s for headers | `HTTP_MAX_BODY`, `HTTP_REQUEST_TIMEOUT`, `HTTP_READ_HEADER_TIMEOUT` |
| Queries are parameterized; `Contains` and `StartsWith` escape `LIKE` wildcards | [Query builder](../reference/query-builder.md) |
| Passwords: argon2id; logins throttled per login and address, and per address; registration and emails rate-limited | `AUTH_THROTTLE`, `AUTH_THROTTLE_IP`; `make:auth`'s routes ([Rate limiting](rate-limiting.md) for your own) |
| Sessions: AES-256-GCM cookies, `HttpOnly`, `Secure` outside development and testing, `SameSite=Lax`, the `__Host-` prefix when `Secure` and without `SESSION_DOMAIN` | `session.New` ([Sessions](sessions.md)) |
| Logged-in pages aren't cached: `Cache-Control: no-store` | `auth.Require` |
| Uploaded files a browser could run (HTML, SVG, scripts) are served as sandboxed downloads | The storage handler ([Store files](storage.md)) |
| `APP_DEBUG=true` is refused in production; so is `VITE_DEV_URL` in production and staging | `APP_ENV` |
| The database's connection is verified for a remote host | `DB_TLS` |

And by stack, against other sites' requests (CSRF) and injected
scripts (XSS):

| Stack | Logins | Other sites' requests | What pages print |
|---|---|---|---|
| web (templ, htmx) | Session cookie | `web.CSRF()`: a token in forms and in htmx's header | templ escapes it |
| react, vue, svelte | Session cookie | `web.CSRF(web.WithXSRFCookie())`: Inertia's client sends the token back | React, Vue and Svelte escape it |
| api | API tokens, in the `Authorization` header | Not needed: browsers don't send tokens on their own | JSON |
| api, with your SPA (`HTTP_SPA_ORIGINS`) | Session cookie for the SPA, tokens for others | `web.FirstParty(…, web.CSRF(web.WithXSRFCookie()))` | Your SPA's framework |

[Protect your app in the browser](browser-security.md) covers these:
how each stack sends its token, CORS, a Content-Security-Policy, and
what to escape.

## The tools

Each tool checks a part a machine can see:

| Tool | What it checks | When |
|---|---|---|
| `go tool anetos check` | The build, vet, tests, `doctor`, and the security check's basic rules on the code, routes and settings | After each change: you, and coding agents before calling a change done |
| `go tool anetos doctor` | The settings: the key, HTTPS, cookies, proxies, the database's TLS, each feature's own | In the project; on the server (`./blog doctor`) after each change to the settings |
| `go tool anetos security:check` | The code (SQL, HTML, redirects, commands built from request data; secrets in files), the routes (no CSRF protection, no login), the settings, the dependencies' known vulnerabilities (govulncheck) | Before a release, and in CI, where its SARIF report puts findings on the pull request's lines |
| `security:check --url=https://…` | The running site, from outside: headers, cookies, TLS, files it mustn't serve, stack traces | After each deploy |

Coding agents have the same checks: the MCP server's `security_check`
tool, and the `anetos-security` skill, which says how to fix each
finding ([Build with a coding agent](../getting-started/coding-agents.md)).
[Check your app's security](security-check.md) covers the security
check's rules, the ignore file, CI and the live checks.

## Before you start

An app made with `anetos new`, ready to deploy: see
[Deploy](deployment.md) first.

## Steps

The checklist before you go live.

### 1. Run the checks with production's settings

Your machine has development settings, where most of the settings'
checks don't apply. Run the binary's `doctor` and `security:check`
where the production settings are, after each change to them. With
the systemd unit `anetos new` writes, run them as the unit runs the
app, with the same user, directory and settings:

```bash
sudo systemd-run --pipe --wait --collect --quiet \
  -p DynamicUser=yes -p User=blog -p StateDirectory=blog --working-directory=/var/lib/blog \
  -E APP_ENV=production -E STORAGE_ROOT=/var/lib/blog/storage \
  -p EnvironmentFile=/etc/blog/env \
  /opt/blog/blog doctor
```

(add `-E DB_NAME=/var/lib/blog/app.db` for SQLite, as the unit
does, and run `security:check` the same way). `User=blog` gives the
command the service's own user: `blog.service`'s dynamic user is named
after it, and without it the command would get another one, and take
the state directory from the running service. In a container, with the
stack up: `docker compose --env-file compose.env run --rm --no-deps web
doctor`.

A **problem** is unsafe or broken, and the command exits 1; a
**warning** is often a mistake; a **note** is a choice worth knowing
about. `--strict` fails on warnings too. The
[reference](../reference/cli.md#the-doctor-command) lists every check.

In the project, run the whole security check, which reads the code and
the dependencies too, and fix what it finds or record why it's fine
([Check your app's security](security-check.md)):

```bash
go tool anetos security:check
```

### 2. Keep the secrets secret

- Make a new `APP_KEY` for production (`go tool anetos key:generate --show`):
  it encrypts sessions, two-factor secrets and the links in emails.
  Never reuse the one in `.env`. To change it, move the old one to
  `APP_PREVIOUS_KEYS`, so sessions survive
  ([Rotate the key](sessions.md#rotate-the-key)).
- Keep settings in files only the app's user reads: `.env` is 0600, and
  the deploy files read `/etc/blog/env`. Never commit them;
  `anetos doctor` fails when git tracks one. If one was committed,
  change every secret in it: the history keeps them.
- Give the database user the rights the app needs, not a superuser.

### 3. Serve it over HTTPS only

- Put HTTPS in front ([Deploy](deployment.md#5-put-https-in-front)) and
  set `APP_URL=https://…`. Session cookies are `Secure` outside
  development and testing: don't set `SESSION_SECURE=false`.
- `HSTS` is sent in production, for two years, subdomains included:
  serve every subdomain over HTTPS before you go live.
- Behind a proxy, set `HTTP_TRUSTED_PROXIES` to its address only. Then
  the app takes the client's address from the proxy's
  `X-Forwarded-For`; rate limits, throttling and logs depend on it.
  Never trust `0.0.0.0/0`: any client could pick its address. A
  platform whose proxy sets a header of its own (`Fly-Client-IP`)
  takes `HTTP_CLIENT_IP_HEADER`, when the app can be reached only
  through that proxy ([Deploy to a cloud platform](cloud-platforms.md)).

### 4. Encrypt the database's connection

A remote `DB_HOST` is reached over TLS, with the server's certificate
checked (`DB_TLS=verify`, the default for any host but this machine).
A managed database usually signs with its own authority: download its
certificate and set `DB_TLS_CA=/etc/blog/db-ca.pem`. Use
`DB_TLS=skip-verify` or `none` only on a private network you trust.
With `DB_URL`, the URL's own options decide (`sslmode=verify-full`,
`tls=true`); `doctor` reads them.

The other connections too: mail needs TLS (STARTTLS, or `smtps://`)
unless the server is this machine or `MAIL_SMTP_URL` says `tls=none`,
which `doctor` warns of for another machine; a remote Redis takes
`rediss://` in `REDIS_URL`.

### 5. Set the browser's rules

- Add a Content-Security-Policy, so an injected script doesn't run:
  one policy fits the pages of every stack
  ([Add a Content-Security-Policy](browser-security.md#4-add-a-content-security-policy)).
- List only production's origins in `HTTP_SPA_ORIGINS` (your SPA's
  address, not `localhost`) and `HTTP_CORS_ORIGINS` (sites whose
  scripts call your API); never `*` with credentials
  ([CORS](browser-security.md#3-allow-other-sites-scripts-only-where-you-mean-to-cors)).
- Leave `SESSION_DOMAIN` empty unless your SPA is on another subdomain
  than the API ([its cost](spa-login.md#1-list-the-spas-origins)).

### 6. Protect the accounts

With [`make:auth`](accounts.md):

- Offer [two-factor authentication](two-factor.md), and require it for
  admins.
- Put sensitive pages behind `a.RequireConfirmed` (the password typed
  again): the generated code does for two-factor settings, API tokens
  and account deletion.
- Give [API tokens](authentication.md#8-give-api-clients-tokens) the abilities they need, not
  `*`, and an expiry.
- Check permissions in every handler and AI tool that changes data
  ([Roles and permissions](roles-and-permissions.md)): hiding a button
  isn't a check, and a model's tool call is a request like any other.
  The security check's `source.owner` (advanced) finds a handler that
  loads a record by its ID without one.
- Keep a record of who changed what with the
  [audit log](audit-log.md).

### 7. Keep up to date

- Run the security check (or `govulncheck ./...`) in CI: it reports
  the known vulnerabilities in the code your app calls, Go's standard
  library included. Run it with the Go you build with: CI that
  installs go.mod's `go` line exactly (`go 1.26.0`, as `setup-go`'s
  `go-version-file` does) reports standard library advisories fixed
  since; ask for the release's latest patch (`1.26.x`) instead.
- Update Anetos when a release fixes a vulnerability; read the
  [upgrade guide](../upgrade/README.md) of each release. Security fixes go
  into the latest minor release ([SECURITY.md](../../../SECURITY.md)).
- Rebuild the binary or image for each Go security release, which fixes
  the standard library you compiled in. Update the frontend's packages
  too (`npm audit` in `frontend/`): the security check reads Go
  modules only.

## How it works

The protections come in layers, and each tool sees one:

1. **Defaults**, in the framework: the tables above, with nothing to
   set.
2. **Settings**, which `doctor` checks: each feature adds its checks as
   it is set up, and the deployed settings decide most of them.
3. **Code and routes**, which the security check reads: request data
   reaching SQL, HTML, redirects and commands within a function; each
   route's middleware, as `route:list` shows it; the dependencies.
4. **The running site**, which `--url` checks: what a proxy or another
   server in front changes.

None of them sees who may read or change whose records, your business
logic, or your proxy's and firewall's rules: those need tests and
review. The framework's own security review, with the risks it
accepts, is in the repository's
[docs/security/checklist.md](../../security/checklist.md).

To add a check of your own settings to `doctor`, for a feature of
yours or a plugin:

```go
// illustrative
app.AddCheck(anetos.Check{Name: "billing", Security: true, Run: func(ctx context.Context) []anetos.Finding {
	if cfg.TestMode && app.Config().Env.IsProduction() {
		return []anetos.Finding{{Severity: anetos.Problem, Message: "BILLING_TEST_MODE=true in production: nobody pays; remove it"}}
	}
	return nil
}})
```

`Security: true` puts its findings in the security check's report too.
Rules about routes or the code are security rules of their own:
[Add your own rules](security-check.md#add-your-own-rules).

## Testing it

Run `go tool anetos check` on each change, and the security check in
CI ([Run it in CI](security-check.md#5-run-it-in-ci)); run the binary's
`doctor` in your deploy script before the new version starts, so a
problem stops the deploy. Test your own checks by running the command:

```go
// illustrative
var out bytes.Buffer
code := app.ExecuteArgs(ctx, []string{"doctor"}, &out, &out)
```

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `doctor` on the server says `APP_ENV=development`, or reports settings the service doesn't have | It didn't get the service's settings | Run it as the service runs: `systemd-run` with the unit's user and settings (step 1), or `docker compose --env-file compose.env run --rm --no-deps web doctor` |
| `the app doesn't boot, so 1 check(s) didn't run` | The database is unreachable, or a feature refuses its settings | Read the error after it; `doctor` reports the other checks anyway |
| `x509: certificate signed by unknown authority` connecting to the database | The server's certificate is signed by its provider's own authority | `DB_TLS_CA` with the provider's certificate |
| The security check finds nothing about the settings | `APP_ENV` isn't production or staging, where those rules apply | Run the binary's `security:check` with the production settings (step 1) |

## Next steps

- [Protect your app in the browser](browser-security.md): CSRF, CORS, a
  Content-Security-Policy, escaping.
- [Check your app's security](security-check.md): the security check
  and its rules.
- [Deploy](deployment.md)
- [Add two-factor authentication](two-factor.md)
- [CLI reference: doctor](../reference/cli.md#anetos-doctor---strict---vuln)
