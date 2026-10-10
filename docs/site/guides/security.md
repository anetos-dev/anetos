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
deployment: a key, HTTPS, the proxies to trust, the database's
connection. This guide is the checklist before you go live, with
`doctor` to check what a machine can check.

## Before you start

An app made with `anetos new` (v0.3 or later), ready to deploy: see
[Deploy](deployment.md) first.

## Steps

### 1. Run the doctor

In the project:

```bash
go tool anetos doctor
```

```text
Checking the project (Anetos v0.3.0, go1.26.8).
  ok  .env
  ok  git

Checking blog (APP_ENV=development).
The checks of deployment settings apply to production and staging: run doctor with those settings too (on the server: ./blog doctor).
  ok       app
  ok       db
  ok       session
  ok       http
  warning  migrations: 1 migration(s) haven't run (2026_10_07_090000_create_posts_table): run migrate
0 problems, 1 warning.
```

It checks the project (`.env` readable only by you, no file of secrets
in git), then builds the app and runs its own `doctor` command, which
checks the settings: each feature adds its checks as it is set up. A
**problem** is unsafe or broken, and the command exits 1; a **warning**
is often a mistake; a **note** is a choice worth knowing about.
`--strict` fails on warnings too, for CI; `--vuln` runs `govulncheck`.

Your machine has development settings, where most checks don't apply.
Run the binary's `doctor` where the production settings are, after
each change to them. With the systemd unit `anetos new` writes, run it
as the unit runs the app, with the same user, directory and settings:

```bash
sudo systemd-run --pipe --wait --collect --quiet \
  -p DynamicUser=yes -p StateDirectory=blog --working-directory=/var/lib/blog \
  -E APP_ENV=production -E STORAGE_ROOT=/var/lib/blog/storage \
  -p EnvironmentFile=/etc/blog/env \
  /opt/blog/blog doctor
```

(add `-E DB_NAME=/var/lib/blog/app.db` for SQLite, as the unit
does). In a container, `docker compose run --rm web doctor`. The
[reference](../reference/cli.md#the-doctor-command) lists every check.

### 2. Keep the secrets secret

- Make a new `APP_KEY` for production (`go tool anetos key:generate`):
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
  development: don't set `SESSION_SECURE=false`.
- `HSTS` is sent in production, for two years, subdomains included:
  serve every subdomain over HTTPS before you go live.
- Behind a proxy, set `HTTP_TRUSTED_PROXIES` to its address only. Then
  the app takes the client's address from the proxy's
  `X-Forwarded-For`; rate limits, throttling and logs depend on it.
  Never trust `0.0.0.0/0`: any client could pick its address.

### 4. Encrypt the database's connection

A remote `DB_HOST` is reached over TLS, with the server's certificate
checked (`DB_TLS=verify`, the default for any host but this machine).
A managed database usually signs with its own authority: download its
certificate and set `DB_TLS_CA=/etc/blog/db-ca.pem`. Use
`DB_TLS=skip-verify` or `none` only on a private network you trust.
With `DB_URL`, the URL's own options decide (`sslmode=verify-full`,
`tls=true`); `doctor` reads them.

### 5. Add a Content-Security-Policy

A Content-Security-Policy tells browsers which scripts may run, so an
injected `<script>` doesn't. Anetos doesn't set one for your pages (the
admin has its own): a policy has to fit your scripts. For the pages
`anetos new` and the generators write, which load their scripts from
`/assets/`, this one fits:

```go
// illustrative: in setup, after web.NewServer
if app.Config().Env.IsProduction() { // the dev server's reload script is inline
	srv.Router().UseGlobal(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; "+
				"base-uri 'none'; form-action 'self'; frame-ancestors 'self'")
			next.ServeHTTP(w, r)
		})
	})
}
```

`'unsafe-inline'` for styles lets htmx's indicator styles and the
admin's banner work; inline scripts stay blocked. Add the hosts of
anything else your pages load (`script-src 'self' https://cdn.example.com`),
and check the browser's console for blocked resources before you ship.

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

### 7. Keep up to date

- Run `govulncheck ./...` (or `go tool anetos doctor --vuln`) in CI: it
  reports the known vulnerabilities in the code your app calls, Go's
  standard library included. Run it with the Go you build with: CI that
  installs go.mod's `go` line exactly (`go 1.26.0`, as `setup-go`'s
  `go-version-file` does) reports standard library advisories fixed
  since; ask for the release's latest patch (`1.26.x`) instead.
- Update Anetos when a release fixes a vulnerability; read the
  [upgrade guide](../upgrade/v0.3.md) of each release. Security fixes go
  into the latest minor release ([SECURITY.md](../../../SECURITY.md)).
- Rebuild the binary or image for each Go security release, which fixes
  the standard library you compiled in.

## How it works

What Anetos does without settings, and where to change it:

| Defence | Where |
|---|---|
| CSRF: unsafe requests need a same-origin `Origin` (or `Sec-Fetch-Site`) and a token from the session | `web.CSRF`, on the `pages` group ([Forms](forms.md)) |
| Sessions: AES-256-GCM cookies, `HttpOnly`, `Secure` outside development, `SameSite=Lax` | `session.New` ([Sessions](sessions.md)) |
| Logged-in pages aren't cached: `Cache-Control: no-store` | `auth.Require` |
| Headers: `X-Content-Type-Options`, `X-Frame-Options: SAMEORIGIN`, `Referrer-Policy`, `Cross-Origin-Opener-Policy`; HSTS in production | `web.NewServer` |
| Limits: 10 MB bodies, 30 s requests, 10 s for headers | `HTTP_MAX_BODY`, `HTTP_REQUEST_TIMEOUT`, `HTTP_READ_HEADER_TIMEOUT` |
| Queries are parameterized; `Contains` and `StartsWith` escape `LIKE` wildcards | [Query builder](../reference/query-builder.md) |
| Views escape what they print | templ |
| Passwords: argon2id; logins throttled per login and address, and per address | `AUTH_THROTTLE`, `AUTH_THROTTLE_IP` |
| `APP_DEBUG=true` is refused in production | `APP_ENV` |
| The database's connection is verified for a remote host | `DB_TLS` |

`doctor` checks the settings these depend on; it can't see your code,
your proxy or your firewall. The framework's own security review, with
the risks it accepts, is in the repository's
[docs/security/checklist.md](../../security/checklist.md).

To add a check of your own settings to `doctor`, for a feature of
yours or a plugin:

```go
// illustrative
app.AddCheck(anetos.Check{Name: "billing", Run: func(ctx context.Context) []anetos.Finding {
	if cfg.TestMode && app.Config().Env.IsProduction() {
		return []anetos.Finding{{Severity: anetos.Problem, Message: "BILLING_TEST_MODE=true in production: nobody pays; remove it"}}
	}
	return nil
}})
```

## Testing it

Run `go tool anetos doctor --strict` in CI, and the binary's `doctor`
in your deploy script before the new version starts: a problem stops the
deploy. Test your own checks by running the command:

```go
// illustrative
var out bytes.Buffer
code := app.ExecuteArgs(ctx, []string{"doctor"}, &out, &out)
```

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `doctor` on the server says `APP_ENV=development`, or reports settings the service doesn't have | It didn't get the service's settings | Run it as the service runs: `systemd-run` with the unit's settings (step 1), or `docker compose run --rm web doctor` |
| `the app doesn't boot, so 1 check(s) didn't run` | The database is unreachable, or a feature refuses its settings | Read the error after it; `doctor` reports the other checks anyway |
| `x509: certificate signed by unknown authority` connecting to the database | The server's certificate is signed by its provider's own authority | `DB_TLS_CA` with the provider's certificate |
| Pages break after adding the policy | It blocks something they load | The browser's console names the resource; add its host to the policy |

## Next steps

- [Deploy](deployment.md)
- [Add two-factor authentication](two-factor.md)
- [CLI reference: doctor](../reference/cli.md#anetos-doctor---strict---vuln)
