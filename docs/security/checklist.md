# Security review

The framework's own security review: what was checked, what was fixed,
and the risks we accept, with the reasons. Work package M7, for v0.3
(2026-10-07). Apps have their own checklist:
[Secure your app](../site/guides/security.md). To report a
vulnerability, see [SECURITY.md](../../SECURITY.md).

## Method

Four independent reviews of the code at v0.3's M10 commit, each
reading the code and, where a flaw looked real, proving it with a test
or a small app (scratch code, outside the repository):

1. **HTTP**: routing, middleware, CSRF, method override, redirects,
   client IP, headers, CORS, sessions, the dev server.
2. **Accounts**: passwords, throttling, two-factor, API tokens,
   verification and reset links, impersonation, social login, the admin,
   and the code `make:auth` writes.
3. **Data**: the query builder, raw SQL, search, the drivers'
   connection strings, storage, mail, queues, the AI module.
4. **Tools and deployment**: the `anetos` tool (`new`, `dev`, `add`,
   `build`, the generators), the deploy files `anetos new` writes, CI,
   and the dependencies (licenses and govulncheck).

No finding was critical or high. The medium and low ones were fixed in
v0.3, or are accepted below. The
[v0.3 upgrade guide](../site/upgrade/v0.3.md) lists the fixes that
change behaviour.

## Checklist

**Sound**: checked, nothing to change. **Fixed**: changed in v0.3.
**Accepted**: a known risk, kept for the reason given.

### HTTP

| Check | Result |
|---|---|
| CSRF on unsafe methods: `Origin`/`Sec-Fetch-Site` checks plus a masked session token | Sound |
| `_method` override: any POST could become a PUT or DELETE, whatever its body or origin | **Fixed**: only urlencoded and multipart forms are overridden, and never for `Sec-Fetch-Site: cross-site` |
| Open redirects through `back` and `intended` URLs | Sound; **fixed**: control characters in a local path are refused too |
| Client IP behind proxies | **Fixed**: `X-Real-IP` is no longer read (a client could set it when the proxy didn't); only `X-Forwarded-For` from `HTTP_TRUSTED_PROXIES` |
| Security headers: `nosniff`, `X-Frame-Options`, `Referrer-Policy`, `Cross-Origin-Opener-Policy`, HSTS in production | Sound |
| Content-Security-Policy | **Accepted**: none by default for app pages (the admin sets a strict one): a default policy would break apps' inline scripts and the dev server's reload script. The guide shows one to add |
| Request limits: body size, header and request timeouts | Sound (doctor warns when they are turned off) |
| Session cookies: AES-256-GCM, `HttpOnly`, `Secure` outside development, `SameSite=Lax`, `__Host-` prefix when possible, key rotation | Sound |
| Caching of signed-in pages | **Fixed**: `auth.Require` sets `Cache-Control: no-store` (sessions already set `private`) |
| Error pages and debug output | Sound: `APP_DEBUG=true` is refused in production |
| `anetos dev`: DNS rebinding (a web page reading the dev server through a name that resolves to 127.0.0.1) | **Fixed**: requests for host names other than localhost, IP addresses, `APP_URL`'s host and `--host` get 403; binding beyond loopback prints a warning |
| An anonymous page view that touches the session writes a row with server-side sessions | **Accepted**: the price of server-side sessions; the cookie driver (the default) writes nothing, and the database driver's rows expire |
| `X-Request-ID` from clients is reused | **Accepted**: only short values of safe characters; it is for tracing, not trust |

### Accounts

| Check | Result |
|---|---|
| Password hashing: argon2id, constant-time comparisons, rehashed at login when the parameters change | Sound |
| Login throttling: per login and IP address (`AUTH_THROTTLE`), and per IP address or IPv6 /64 (`AUTH_THROTTLE_IP`) | Sound |
| A TOTP code or recovery code used twice by two requests at the same moment | **Fixed**: the check and its record run under a cache lock per user |
| Password confirmation as a way to guess the password from a stolen session | **Fixed**: at most 50 wrong confirmations (and password changes) per account a day (a UTC day) |
| API tokens: SHA-256 of the secret stored, constant-time check, expiry | Sound |
| API tokens created by a stolen session, or while impersonating | **Fixed**: `make:auth`'s token route needs a recent password confirmation; `CreateToken` refuses while acting as another user |
| A reset link used twice at once; an account someone registered with another person's address | **Fixed** in `make:auth`'s code: the reset runs in one transaction that changes the password only if it is still the one the link was made for, revokes API tokens, and, for an address never verified, turns off the two-factor sign-in and social links the first registrant may have set up |
| Verification and reset links: tokens encrypted with `APP_KEY`, expiring, bound to their purpose; a reset token stops working once the password changes or the user is signed out everywhere | Sound |
| No limit per account across addresses: guessing one account's password from many addresses isn't slowed | **Accepted**: a limit per account would let anyone lock its owner out; slow argon2id hashes, the per-address limits and two-factor are the defence |
| "Forgot password" answers a little faster for unknown emails | **Accepted** and documented: the same answer is shown either way; mail is queued |
| The link that reverts an email change lasts 7 days | **Accepted**: the owner of the old address may only notice days later |
| Changing a user's email in the admin doesn't sign them out | **Accepted**: the admin is trusted; signing out is one action away |

### Data

| Check | Result |
|---|---|
| SQL injection: every query of the builder, relations and migrations is parameterized; identifiers are quoted | Sound |
| `LIKE` with user input: `%` and `_` act as wildcards | **Fixed**: `Contains`, `StartsWith` and `db.EscapeLike` escape them |
| Search as a denial of service: many terms, one-letter prefixes | **Fixed**: at most 10 terms; prefix matching for terms of 3 letters or more |
| SQLite accepting `"text"` as a string where a column was meant | **Fixed**: `_dqs=0` |
| Database connections in plain text to a remote server | **Fixed**: `DB_TLS` verifies the server's certificate by default for a remote `DB_HOST` |
| Files on local storage readable by other users | **Fixed**: files 0640, directories 0750 |
| Path traversal in storage paths (`..`, absolute paths, backslashes, control characters) | Sound: `storage.CheckPath` refuses them |
| The log mail driver writing reset links into production logs | **Fixed**: in production it logs who and what, not the body |
| Queued emails keep their links in the jobs table until sent | **Accepted**: the database is already trusted with the accounts; failed jobs can be flushed |
| The query log has the queries' values | **Accepted**: off in production unless `DB_LOG_QUERIES=true`, which doctor warns about |
| AI budgets can be overshot by requests running at the same moment | **Accepted** and documented: a budget is checked before a call and charged after it |
| Prompt injection through content a model reads | **Accepted** and documented: tools must check permissions as handlers do; the model is not a security boundary |

### Tools and deployment

| Check | Result |
|---|---|
| `anetos add` runs the plugin's code (to read its settings) | **Fixed**: it says so and asks first in a terminal (`--yes` doesn't) |
| The systemd unit `anetos new` writes | **Fixed**: sandboxed further (`systemd-analyze security` exposure 1.2, "OK") |
| `.env` files in container images | **Fixed**: `.dockerignore` leaves out `.env`, `.env.*` and `*.env` files at any depth, keeping the examples |
| `.env` written by `anetos new` | Sound: mode 0600, and in `.gitignore` |
| CI: actions by tag, credentials kept in the checkout | **Fixed**: actions pinned by commit, `persist-credentials: false`, timeouts; govulncheck on the minimum and latest Go releases, each at its latest patch; Dependabot weekly |
| The app reads a `.env` in its working directory, which the service may write (`/var/lib/<name>`): code running as the app could change its next start's settings | **Accepted**: such code can already do whatever the app can; the unit's settings are in `/etc/<name>/env`, which `ProtectSystem=strict` keeps read-only |
| Base images by tag, not digest | **Accepted**: tags get security updates; pin by digest if you rebuild rarely |
| Staging allows `APP_DEBUG=true` | **Accepted**: doctor warns about it |
| Flashed form input keeps a `code` field (a two-factor code) for one request | **Accepted**: the session is encrypted, and the code is single-use |
| `SESSION_DOMAIN` shares the cookie with every subdomain | **Accepted**: off by default; doctor notes it |

## Dependencies

Direct dependencies of the published modules, reviewed for licenses
and known vulnerabilities (govulncheck, 2026-10-07). None is GPL, AGPL,
LGPL or SSPL.

| Module | Dependency | License |
|---|---|---|
| core | `go.yaml.in/yaml/v3` | MIT and Apache-2.0 |
| core | `golang.org/x/crypto`, `x/oauth2`, `x/text` | BSD-3-Clause |
| cli | `golang.org/x/mod`, `x/tools`, `go.yaml.in/yaml/v3` | BSD-3-Clause; MIT and Apache-2.0 |
| drivers/postgres | `github.com/jackc/pgx/v5` | MIT |
| drivers/mysql | `github.com/go-sql-driver/mysql` | MPL-2.0 (file-level copyleft: using it unchanged imposes nothing on apps) |
| drivers/sqlite | `modernc.org/sqlite` | BSD-3-Clause |
| drivers/redis | `github.com/redis/go-redis/v9` | BSD-2-Clause |
| drivers/s3 | `github.com/minio/minio-go/v7`; `github.com/johannesboyne/gofakes3` (tests) | Apache-2.0; MIT |
| drivers/gcs | `cloud.google.com/go/storage`, `google.golang.org/api`; `github.com/fsouza/fake-gcs-server` (tests) | Apache-2.0; BSD-2-Clause |
| drivers/gcppubsub | `cloud.google.com/go/pubsub/v2`, `google.golang.org/grpc`, `google.golang.org/protobuf` | Apache-2.0; BSD-3-Clause |
| drivers/anthropic | `github.com/anthropics/anthropic-sdk-go` | MIT |
| drivers/openai | `github.com/openai/openai-go/v3` | Apache-2.0 |
| drivers/gemini | `google.golang.org/genai` | Apache-2.0 |

govulncheck found no vulnerability that the modules' code reaches. Two
are in the module graph without being called: GO-2026-5932 (reported at
module level only) and GO-2026-6443 (grpc, in the Gemini driver's
graph). Notes for later: the Anthropic SDK pulls a release candidate of
a YAML library; OpenCensus (in the Google modules' graph) is archived;
`goskiplist` (in a test dependency's graph) is no longer maintained.

## Next review

Before the public release (v0.5), for what v0.4 and v0.5 add (the API
stack, the design kits), and again before v1.0.
