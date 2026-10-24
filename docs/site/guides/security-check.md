---
title: Check your app's security
since: v0.5.0
group: "Production"
weight: 804
---

# Check your app's security

`security:check` checks your app's settings and routes against
security rules: a form's route without CSRF protection, an admin page
anyone can open, session cookies sent over plain HTTP, mail in plain
text. Each finding says where it is (the line that registered the
route, or the setting), why it matters and how to fix it. The report
comes as text, JSON, SARIF (for GitHub's code scanning) or Markdown.

## Before you start

An app made with `anetos new`, with its settings (`.env`): the check
boots the app, as other commands do. The settings' rules apply where
the app is deployed, so run the check with the production settings too
(step 4).

## Steps

### 1. Run the check

In the project:

```bash
go tool anetos security:check
```

```text
Security check of blog (APP_ENV=development, level basic).
The settings' rules apply to production and staging: run it with those settings too (on the server, or APP_ENV=production with them).

problem  route.csrf  POST /posts/import  routes/web.go:31
         it changes data with the session's user and has no CSRF protection: another site's page can send it as the logged-in user; add web.CSRF() to its group, after the session middleware
         A01:2025 Broken Access Control · https://docs.anetos.dev/guides/security-check/#routecsrf

1 problem, 0 warnings, 0 notes.
None of these rules finding anything doesn't make the app secure: who may read or change whose records, business logic and the code itself need tests and review.
```

The tool builds the app and runs its `security:check` command, as
`doctor` does; `--out` and `--ignore` name files relative to the folder
you're in. A **problem** is almost always a hole, and the command
exits 1; a **warning** is often a mistake; a **note** is worth knowing.
`--strict` fails on warnings too.

### 2. Fix, or say why it's fine

Each finding names its rule and its fix ([the rules](#the-rules)
below). A route that is open to everyone on purpose says so in the
code, with a reason, and the rules about logins (`route.login`,
`route.api-auth`) leave it out:

```go
// illustrative
r.Post("/webhooks/stripe", h.Stripe).Public("Stripe signs its webhooks: the handler checks the signature")
```

Any other finding you decide to keep goes in `security-ignore.yaml`, at
the project's root, with the reason (required) and, if you like, a date
after which it's reported again:

```yaml
- rule: route.csrf
  where: POST /import
  reason: the importer's form posts from our own extension, with a token of its own
  expires: 2027-03-31
- rule: setting.log-level   # every place of the rule
  reason: we're looking into a bug this week
  expires: 2026-11-07
```

An entry without `where` ignores every place of its rule. It expires at
the end of its day in the app's time zone (`APP_TIMEZONE`); a key the
check doesn't know (`wher:`) is an error, since a typo would ignore
more, for longer. The report lists what it ignored. An entry that
expired while its finding is still there is a warning, and one that
matches nothing (fixed, or expired and fixed) a note. Commit the file:
it is where a reviewer sees what was let through, and why.

### 3. Look further

The basic level (the default) has the rules that find mistakes with
few false alarms. `--level=advanced` adds those that find what needs a
look and may be on purpose: an API route that changes data without a
login, debug logs in production.

```bash
go tool anetos security:check --level=advanced
```

### 4. Check the production settings

Your machine has development settings, where most of the settings'
rules don't apply. Run the binary's command where the production
settings are, as you run [doctor](security.md#1-run-the-doctor):

```bash
./blog security:check
```

### 5. Run it in CI

The SARIF report puts each finding on its line in a pull request, with
GitHub's code scanning:

```yaml
# illustrative: a GitHub Actions job
security:
  runs-on: ubuntu-latest
  permissions:
    contents: read
    security-events: write # to upload the report
  steps:
    - uses: actions/checkout@v7
    - uses: actions/setup-go@v7
      with:
        go-version-file: go.mod
    # The app boots with development settings: the route rules apply.
    - run: cp .env.example .env && go tool anetos key:generate
    - run: go tool anetos security:check --format=sarif --out=security.sarif
    - uses: github/codeql-action/upload-sarif@v4
      if: always() # the report too when the check fails
      with:
        sarif_file: security.sarif
```

The job fails on a problem, and the findings show in the pull request.
Files in the report are relative to the repository's root, also when
`go.mod` is in a folder of it. `--format=json` gives one object to read
in a script, and `--format=markdown` a table for an issue or a review.
`--out=FILE` writes the report to a file (`-`: the standard output).

## The rules

### `route.csrf`

A route that changes data (POST, PUT, PATCH, DELETE, or any method)
behind the session middleware, without `web.CSRF`: another site's page
can send it with the visitor's session. **Fix:** add `web.CSRF()` to
the route's group, after `sessions.Middleware`, as `anetos new`'s
`pages` group has it. Problem; A01:2025 Broken Access Control.

### `route.login`

A route under `/admin` (or named `admin.…`, or on an `admin.` host)
without a login: auth's `Require`, `rbac.Require`, `rbac.RequireIn` or
`auth.RequireAbilities`. **Fix:** put it in a group with them; or
`.Public("why")` if it's open on purpose. Problem; A01:2025.

### `route.debug`

A route that serves Go's debug endpoints (`/debug/pprof/`,
`/debug/vars`) without a login, for GET or any method: `/debug/pprof/…`
itself, or a mount such as `Handle("", "/debug/{path...}",
web.WrapHandler(http.DefaultServeMux))`. They show the app's memory and settings, and a
profile slows it down. `Public` doesn't excuse them. **Fix:** leave
them out of production builds, or behind a login and `rbac.Require`.
Problem; A02:2025 Security Misconfiguration.

### `route.rate-limit`

A POST route that creates accounts or sends email (`/register`,
`/forgot-password`, `/email/verification-notification`) without
`ratelimit.Middleware`: a script can fill the database or send mail in
your name. **Fix:** add a limit, as `make:auth`'s routes have. Logging
in isn't one of them: auth limits login attempts itself
(`AUTH_THROTTLE`). Warning; A07:2025 Authentication Failures.

### `route.api-auth`

Advanced. An API route (under `/api/`, or behind `web.JSONErrors`)
that changes data without a login, other than `make:auth`'s routes for
guests (register, log in, reset a password). **Fix:** put it behind
auth's `Require`, or `.Public("why")`. Warning; A01:2025.

### `setting.debug`

`APP_DEBUG=true` in staging (production refuses it): error pages show
stack traces and settings. Warning; A02:2025.

### `setting.app-key`

No `APP_KEY` where the app is deployed. Warning; A04:2025
Cryptographic Failures.

### `setting.app-url`

An `http://` `APP_URL` where the app is deployed: links in emails and
OAuth callbacks go over plain HTTP. Problem in production, warning in
staging; A04:2025.

### `setting.session-secure`

`SESSION_SECURE=false` where the app is deployed: the session cookie
goes over plain HTTP too. Problem; A07:2025.

### `setting.session-same-site`

`SESSION_SAME_SITE=none`: other sites' requests carry the session
cookie. Warning; A01:2025.

### `setting.session-domain`

`SESSION_DOMAIN` set: every subdomain gets the session cookie. Note;
A07:2025.

### `setting.trusted-proxies`

`HTTP_TRUSTED_PROXIES` trusting everyone, or a very wide range: clients
choose the IP address the app sees. Problem or warning; A02:2025.

### `setting.cors`

`HTTP_CORS_ORIGINS="*"`: scripts on any site may call the app. Warning;
A02:2025.

### `setting.body-limit`

`HTTP_MAX_BODY=0` where the app is deployed: one client can fill the
memory with a request's body. Warning; A02:2025.

### `setting.request-timeout`

`HTTP_REQUEST_TIMEOUT=0` where the app is deployed: slow requests hold
connections and memory for as long as they like. Warning; A02:2025.

### `setting.header-timeout`

`HTTP_READ_HEADER_TIMEOUT=0` where the app is deployed: a client that
sends its headers slowly holds a connection (Slowloris). Warning;
A02:2025.

### `setting.mail-tls`

`MAIL_SMTP_URL` with `tls=none` for a server off the machine: emails,
with their reset and login links, cross the network in plain text.
Warning; A04:2025.

### `setting.db-tls`

A database off the machine without TLS, or without checking the
server's certificate. Warning; A04:2025.

### `setting.db-log-queries`

`DB_LOG_QUERIES=true` where the app is deployed: the log gets every
query with its values. Warning; A09:2025 Security Logging and Alerting
Failures.

### `setting.log-level`

Advanced. `LOG_LEVEL=debug` where the app is deployed. Warning;
A09:2025.

### Other findings

- `check.<name>`: a finding of a doctor check marked `Security: true`
  (an app's or a plugin's), with its severity; A02:2025 unless it says.
- `check.panic`: a check or a rule panicked, so it didn't check
  anything. Problem.
- `ignore.expired`, `ignore.unused`: an entry of the ignore file that
  expired while its finding is there (warning), or that matches nothing
  (note).

## Add your own rules

An app or a plugin adds rules of its own. A rule about routes sees each
route with its middleware, the global ones first:

```go
// illustrative
app.AddSecurityRule(web.RouteRule(anetos.SecurityRule{ID: "billing.webhook-signature",
	Summary: "Payment webhooks without the signature check", OWASP: anetos.OWASPAccessControl},
	func(rt web.RouteInfo) []anetos.SecurityFinding {
		if strings.HasPrefix(rt.Pattern, "/webhooks/") && !rt.Uses("billing.VerifySignature") {
			return []anetos.SecurityFinding{{Severity: anetos.Problem,
				Message: "a webhook without the signature check: anyone can post it; add billing.VerifySignature"}}
		}
		return nil
	}))
```

Other rules implement `Run(ctx, app)` themselves (`anetos.SecurityRule`),
and a doctor check marked `Security: true` has its findings in the
report too (as `check.<name>`). A rule's `Level` is
`anetos.SecurityBasic` (the default) or `anetos.SecurityAdvanced`. Name
your rules after your app or plugin (`billing.…`): `route.`, `setting.`,
`check.` and `ignore.` are the framework's.
See the [CLI reference](../reference/cli.md#app-binary-commands).

## How it works

The command boots the app, as other commands do, then runs the
doctor's checks and keeps their findings about security (those with a
rule), and runs the security rules: web's about routes read
`route:list`'s information, each middleware by name
([Routing](routing.md)), and each route's file and line, which the
router records when the route is registered: the line of your code
that registered it, or that called the module that did (the admin's
`Mount`). The report sorts the
findings by severity, applies `security-ignore.yaml`, and gives each
its OWASP Top 10:2025 category.

It checks what a booted app can tell about itself. It doesn't read
your code (whether a query is built from user input, whether a handler
checks that the record is the user's): a clean report means none of
these rules found anything, not that the app is secure.

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| Nothing is found about the settings | `APP_ENV` isn't production or staging, where those rules apply | Run `./<app> security:check` with the production settings |
| `route.csrf` on a webhook | The route is in a group with the session middleware | Move it to a group of its own, without sessions (and check the sender's signature) |
| A finding at a line in the module cache | A plugin registered the route from code your app doesn't call (its own `init`, say) | Look at the plugin's settings for the fix; report it to its authors, or ignore it with a reason |
| `there's no .env` | The tool needs the app's settings to boot it | `cp .env.example .env && go tool anetos key:generate`, or set `APP_ENV` and `APP_KEY` |
| `field wher not found` | A key of `security-ignore.yaml` misspelled | Use `rule`, `where`, `reason` and `expires` |
| `has no reason` | An entry of `security-ignore.yaml` without `reason:` | Say why the finding is safe to leave |

## Next steps

- [Secure your app](security.md): the checklist before you go live.
- [Rate limiting](rate-limiting.md), [Roles and permissions](roles-and-permissions.md).
