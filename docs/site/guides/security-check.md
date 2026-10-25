---
title: Check your app's security
since: v0.5.0
group: "Production"
weight: 804
---

# Check your app's security

`security:check` checks your app against security rules: its code
(SQL built from request data, a redirect to a URL from the request, a
secret written in a file), its dependencies (known vulnerabilities, with
govulncheck), its routes (a form's route without CSRF protection, an
admin page anyone can open) and its settings (session cookies sent over
plain HTTP, mail in plain text). Each finding says where it is (a line
of your code, or a setting), why it matters and how to fix it. The
report comes as text, JSON, SARIF (for GitHub's code scanning) or
Markdown.

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

problem  source.sql  app/handlers.Posts.Search  app/handlers/posts.go:48
         Raw gets SQL built from request data: a client can change the query (SQL injection); pass values as arguments (?), and choose column names from a list
         A05:2025 Injection · https://docs.anetos.dev/guides/security-check/#sourcesql

2 problems, 0 warnings, 0 notes.
None of these rules finding anything doesn't make the app secure: who may read or change whose records, business logic and the code itself need tests and review.
```

The tool builds the app, checks the code and runs govulncheck (built
in; it needs the network for vuln.go.dev, and `--no-vuln` skips it),
then runs the app's `security:check` command, as `doctor` does, which
adds its own findings: one report. `--out` and `--ignore` name files relative to
the folder you're in. A **problem** is almost always a hole, and the command
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

`where` is the place as the report shows it: a route (`POST /import`),
a setting, a function with its package's path in the module
(`app/handlers.Posts.Search`; a function literal counts as the function
it's in), a file (for
`source.secret`) or a vulnerability (`GO-2026-6612
golang.org/x/net/http2`). An entry without `where` ignores every place
of its rule. It expires at
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
login, a handler that loads a record by ID without an access check,
SQL built from strings that aren't constants, debug logs in production.

```bash
go tool anetos security:check --level=advanced
```

### 4. Check the production settings

Your machine has development settings, where most of the settings'
rules don't apply. Run the binary's command where the production
settings are, as you run [doctor](security.md#1-run-the-doctor). It
checks the settings and the routes; the code and the dependencies are
the tool's, in the project:

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
        go-version: stable # the Go you build with: govulncheck checks its standard library
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

### `source.sql`

SQL built from request data (with `+`, `fmt.Sprintf`, a
`strings.Builder`) given to `db.Raw`, `db.Exec`, `db.SQL`,
`db.OrderRaw`, `WhereRaw`, `Join`, `GroupBy`, `db.Select`, `SetRaw`, a
migration's `Exec` or the methods of `database/sql`'s `DB`, `Tx` and
`Conn`: a client can change the query. **Fix:** pass values as
arguments (`?`), and choose a column or a sort order from a list: a
value checked in the same function against constants (a `switch` whose
`default` replaces it or returns, `if !slices.Contains(list, v)`, `if
!allowed[v]`, `if _, ok := allowed[v]; !ok`, `v != "a" && v != "b"`)
isn't the client's anymore. Problem; A05:2025 Injection. At the advanced
level, SQL built from strings that aren't constants, wherever they come
from, is a warning: look at where they come from.

The rules follow request data (`c.Param`, `c.Query`, `c.Header`,
`http.Request`'s form, URL, headers and body, and a typed handler's
input) through one function, not into the functions it calls: a helper
that builds SQL from its parameters shows at the advanced level only.

### `source.xss`

Request data given to `templ.Raw`, `c.HTML`, or made
`template.HTML` (or `JS`, `URL`, `CSS`…), which aren't escaped:
anyone can put a script in the page. **Fix:** let templ or
html/template escape it; sanitize HTML you must keep. Problem;
A05:2025. In `.templ` files, the finding is at the `.templ` line; the
rule sees request data a view reads itself, not what a handler passes
to a component's parameters, nor text written to an
`http.ResponseWriter`.

### `source.redirect`

A redirect (`c.Redirect`, `web.Redirect`, `http.Redirect`) to a URL
from the request: a link to your site can send visitors anywhere. Not
when the URL starts with a constant that keeps it on a known site
(`"/posts/"+id`, `"https://example.com/"+path`), or when the function
checks it first: `strings.HasPrefix(next, "//")` (with the leading `/`),
or `url.Parse` and a look at the result's `Host`, `Scheme` or
`IsAbs`. **Fix:** allow your own paths only (a leading `/`, not
`//`), or redirect to a route by name. Problem; A01:2025.

### `source.exec`

`exec.Command` with request data: as the program, or in a shell's
command line (`sh -c`), a problem (a client can run any command); as
another argument, a warning (a value starting with `-` becomes an
option). **Fix:** choose the program from a list, pass the value as one
argument, after `--`. A05:2025.

### `source.tls`

`tls.Config{InsecureSkipVerify: true}`: TLS connections accept any
certificate, so anyone on the network can read and change them.
**Fix:** give the server's authority (`RootCAs`). Problem; A04:2025.

### `source.rand`

`math/rand` (or `math/rand/v2`) for a value whose name says it's a
secret (`token`, `password`, `salt`, `otp`, `sessionID`, `resetCode`…;
not `sessionCount`), returned by a function named so (`NewToken`), or
`math/rand.Read` anywhere: its numbers can be predicted. **Fix:** `crypto/rand` (`rand.Text()` for a token). Warning;
A04:2025.

### `source.secret`

A secret written in the project's files (Go, templ, YAML, JSON, TOML,
scripts; not `.env` and the other files of settings, nor tests): a key
recognized by its form (AWS, Stripe, GitHub, Slack, Google, Anthropic,
OpenAI, SendGrid, a private key) is a problem; a value that looks
random given to a name like `password`, `secret`, `token` or `api_key`
(not `passwordHash`), a warning: in code, a string literal; in
settings files, any value. Files that say they're generated aren't
searched. The report shows the secret's start only. **Fix:** revoke
it, and read it from a setting. A04:2025.

### `source.path`

Advanced. A file path from the request given to `os.Open`,
`os.ReadFile`, `os.WriteFile`, `os.Remove`… or `http.ServeFile`: `../`
reaches any file the app can read. **Fix:** keep the name only
(`filepath.Base`) in a known folder, or open it with `os.Root`.
Warning; A01:2025.

### `source.ssrf`

Advanced. An HTTP request (`http.Get`, `http.NewRequest`, a client's
`Get`…) to a URL from the request: a client can make the app call
internal services. **Fix:** allow only the hosts you expect. Warning;
A01:2025.

### `source.mass-assign`

Advanced. `web.H` with a handler whose input is a model: a client can
set any of its fields (ID, owner, role). **Fix:** bind into a struct
with the fields a form may change, and copy them. Warning; A08:2025
Software or Data Integrity Failures.

### `source.owner`

Advanced. A handler that loads a record by an ID from the request
(`db.Find`, a query's `Find` without a `Where`) and checks nobody's
access: no `rbac` call, no `auth.Authorize…` or `Allows…`, nothing
named like a check (`CanEdit`, `authorizeOwner`, `postPolicy`), and no
comparison of an owner field (`note.UserID != user.ID`). Getting the
current user isn't a check. Any logged-in user can open any record by
changing the ID. Fine when every user may see every record, as `make:crud`'s
handlers assume. **Fix:** check access, or scope the query to
the user. Warning; A01:2025.

### `dependency.vuln`

A known vulnerability (govulncheck, from vuln.go.dev) in a module or
in Go's standard library: in a function the app calls, a problem, at
the line of your code nearest the vulnerable call; in a package it
imports without calling the vulnerable code, a note, at the module's
line in `go.mod` (the standard library's: the `go` or `toolchain`
line). **Fix:** update the module (`go get module@version`, the report
says which), or build with the Go release that fixed it. When
govulncheck can't reach the database, or doesn't finish in 5 minutes,
a note says so; when it fails otherwise, a warning. A03:2025 Software Supply Chain Failures.

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
`source.`, `dependency.`, `check.` and `ignore.` are the framework's.
See the [CLI reference](../reference/cli.md#app-binary-commands).

## How it works

The tool builds the app, loads the project's packages (not their
tests) with their types, and runs its rules about the code, as an
analyzer on each function; it searches the project's files for
secrets, and runs govulncheck. It writes what it found to a file, which
the app's `security:check` reads (`--findings`). The app's command boots the app, as other commands do, then runs the
doctor's checks and keeps their findings about security (those with a
rule), and runs the security rules: web's about routes read
`route:list`'s information, each middleware by name
([Routing](routing.md)), and each route's file and line, which the
router records when the route is registered: the line of your code
that registered it, or that called the module that did (the admin's
`Mount`). The report sorts the
findings by severity, applies `security-ignore.yaml`, and gives each
its OWASP Top 10:2025 category.

A clean report means none of these rules found anything, not that the
app is secure: the rules follow request data within a function, not
across functions or through the database, and can't know who may read
or change whose records; business logic and the code itself need tests
and review.

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| Nothing is found about the settings | `APP_ENV` isn't production or staging, where those rules apply | Run `./<app> security:check` with the production settings |
| `route.csrf` on a webhook | The route is in a group with the session middleware | Move it to a group of its own, without sessions (and check the sender's signature) |
| A finding at a line in the module cache | A plugin registered the route from code your app doesn't call (its own `init`, say) | Look at the plugin's settings for the fix; report it to its authors, or ignore it with a reason |
| `the project doesn't build` | The code rules need the project's packages to type-check | Fix the build (`go build ./...`) |
| `the known vulnerabilities weren't checked` | govulncheck couldn't reach vuln.go.dev | Run it where it has the network; `--no-vuln` to skip it |
| A finding on code that is safe | The rule can't see the check: it's in another function, or not one of the forms it knows | Check in the same function (a `switch` or `slices.Contains` against constants, a map of allowed values), or ignore it in `security-ignore.yaml` with the reason |
| `there's no .env` | The tool needs the app's settings to boot it | `cp .env.example .env && go tool anetos key:generate`, or set `APP_ENV` and `APP_KEY` |
| `field wher not found` | A key of `security-ignore.yaml` misspelled | Use `rule`, `where`, `reason` and `expires` |
| `has no reason` | An entry of `security-ignore.yaml` without `reason:` | Say why the finding is safe to leave |

## Next steps

- [Secure your app](security.md): the checklist before you go live.
- [Rate limiting](rate-limiting.md), [Roles and permissions](roles-and-permissions.md).
