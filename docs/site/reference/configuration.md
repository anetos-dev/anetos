---
title: Configuration reference
since: v0.1.0
---

# Configuration reference

## Framework settings

Read by `anetos.New` into `anetos.AppConfig`.

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `APP_NAME` | string | `anetos` | Application name, added to every log line as `app` | v0.1 |
| `APP_ENV` | `development` \| `testing` \| `staging` \| `production` | `production` | Deployment environment; also selects `.env.<APP_ENV>` | v0.1 |
| `APP_DEBUG` | bool | `false` | Enables debugging aids. **Rejected when `APP_ENV=production`** | v0.1 |
| `APP_SHUTDOWN_TIMEOUT` | duration | `30s` | Total graceful-shutdown budget: components first, then shutdown hooks. Hooks always keep the smaller of 5s and a fifth of it. Keep it at or below your platform's grace period (Kubernetes default: 30s) | v0.1 |
| `APP_KEY` | `base64:…` (32 bytes) | empty | Encrypts and authenticates session cookies. Required by sessions, which fail at startup without it. Generate one with `go tool anetos key:generate`. Keep it secret | v0.1 |
| `APP_PREVIOUS_KEYS` | list of keys | empty | Old keys that still decrypt, so `APP_KEY` can be rotated without logging everyone out | v0.1 |
| `LOG_LEVEL` | `debug` \| `info` \| `warn` \| `error` | `info` | Minimum log level | v0.1 |
| `LOG_FORMAT` | `text` \| `json` \| empty | empty | Log format; empty means JSON in production, text elsewhere | v0.1 |

`APP_ENV` defaults to `production` so that a missing setting fails safe
(debug off, JSON logs). The keys have type `anetos.Secret`, which prints,
logs and encodes as `[redacted]`; use `string(cfg.Key)` for the value.

## Struct tags

Used by `config.Bind` and `config.Get`.

| Tag | Example | Meaning |
|---|---|---|
| `env:"KEY"` | `env:"DB_PORT"` | Read the field from `KEY` |
| `env:"KEY,required"` | `env:"DB_URL,required"` | Error if `KEY` is missing or empty and there is no default |
| `env:"-"` | | Ignore the field |
| `default:"value"` | `default:"5432"` | Used when the key is missing or empty |
| `prefix:"P_"` | `prefix:"DB_"` | On a nested struct (or pointer to struct) without `env`: prepend `P_` to its keys |

Untagged nested structs are bound recursively, without a prefix unless one is
given. Unexported fields are ignored.

## Supported field types

| Go type | Accepted values |
|---|---|
| `string` | Anything |
| `bool` | `true`/`false`, `1`/`0`, `t`/`f`, `yes`/`no`, `on`/`off` (any case) |
| `int`, `int8` … `int64` | Base-10 integers within range |
| `uint`, `uint8` … `uint64` | Base-10 non-negative integers within range |
| `float32`, `float64` | Decimal numbers |
| `time.Duration` | Go durations: `300ms`, `30s`, `5m`, `1h30m` |
| `[]T` of the above | Comma-separated; blanks trimmed; empty items dropped |
| `*T` of the above | Allocated only when a value is present |
| Types whose pointer implements `encoding.TextUnmarshaler` | Whatever `UnmarshalText` accepts, e.g. `slog.Level` |

Any other type is a binding error.

## `.env` syntax

| Form | Result |
|---|---|
| `KEY=value` | `value` (trailing spaces trimmed) |
| `KEY=value # comment` | `value`; a `#` preceded by a space starts a comment |
| `KEY=a#b` | `a#b` |
| `export KEY=value` | `value`; the `export ` prefix is ignored |
| `KEY=` or `KEY= # comment` | empty (treated as unset when binding) |
| `KEY="a\nb ${OTHER}"` | Double quotes: escapes `\n \r \t \" \\ \$`, `${VAR}` expansion, may span lines |
| `KEY='raw ${OTHER}'` | Single quotes: literal, may span lines |
| `KEY=${OTHER}-x` | Unquoted values also expand `${VAR}` |
| `# …` | Comment line |

`${NAME}` resolves with the same priority as the final configuration: the
process environment first, then keys defined earlier in the same file, then
lower layers (for `.env.<APP_ENV>`, that's `.env`). So a reference always sees
the value the application sees. Unknown names become empty; a malformed
reference such as `${` or `${A:-default}` is an error. `$NAME` without
braces is **not** expanded. If a key repeats, the last value wins. A UTF-8
byte order mark and CRLF line endings are handled.

## Load order

`config.Load` (called by `anetos.New`) layers sources, highest priority first:

1. Process environment
2. `.env.<APP_ENV>`: `APP_ENV` is taken from the process environment, else
   from `.env`
3. `.env`

`anetos.WithConfigDir(dir)` changes where the files are read from, and
`anetos.WithSource(src)` replaces the whole mechanism (useful in tests).

## HTTP server

Read by `web.NewServer` (or `web.LoadConfig`) into `web.Config`.

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `HTTP_ADDR` | string | `:8080` | Listen address. `127.0.0.1:0` picks a free port (tests) | v0.1 |
| `HTTP_READ_HEADER_TIMEOUT` | duration | `10s` | Time to read request headers (slowloris protection) | v0.1 |
| `HTTP_READ_TIMEOUT` | duration | `30s` | Time to read the whole request | v0.1 |
| `HTTP_WRITE_TIMEOUT` | duration | `30s` | Time to write the response | v0.1 |
| `HTTP_IDLE_TIMEOUT` | duration | `2m` | Keep-alive idle time | v0.1 |
| `HTTP_SHUTDOWN_GRACE` | duration | `15s` | On shutdown, how long in-flight requests may finish before their contexts are canceled and connections closed. Capped at half of `APP_SHUTDOWN_TIMEOUT`, so later stages keep time to drain | v0.1 |
| `HTTP_REQUEST_TIMEOUT` | duration | `30s` | Deadline on each request's context; expiry gives 503. `0` disables | v0.1 |
| `HTTP_MAX_BODY` | size | `10MB` | Maximum request body (`512KB`, `10MB`, `1GB`; units are powers of 1024); larger gives 413. `0` disables | v0.1 |
| `HTTP_TRUSTED_PROXIES` | list of IPs/CIDRs | empty | Peers whose `X-Forwarded-For`/`X-Real-IP` are trusted for the client IP | v0.1 |
| `HTTP_ACCESS_LOG` | bool | `true` | One log line per request | v0.1 |
| `HTTP_HEALTH_ROUTES` | bool | `true` | Serve `GET /health/live` and `GET /health/ready` | v0.1 |
| `HTTP_CORS_ORIGINS` | list | empty (CORS off) | Allowed origins; `*` for any; `https://*.example.com` for subdomains | v0.1 |
| `HTTP_CORS_METHODS` | list | `GET,HEAD,POST,PUT,PATCH,DELETE` | Allowed methods for preflights | v0.1 |
| `HTTP_CORS_HEADERS` | list | `Accept,Authorization,Content-Type,X-Requested-With,X-Request-ID` | Allowed request headers | v0.1 |
| `HTTP_CORS_EXPOSE` | list | `X-Request-ID` | Response headers readable by browsers | v0.1 |
| `HTTP_CORS_CREDENTIALS` | bool | `false` | Allow cookies; can't be combined with `*` | v0.1 |
| `HTTP_CORS_MAX_AGE` | duration | `10m` | How long browsers cache preflights | v0.1 |

Streaming responses (server-sent events, long polling) should set
`HTTP_WRITE_TIMEOUT=0` and `HTTP_REQUEST_TIMEOUT=0` (or override them per
route with `http.ResponseController`), and end the stream when
`srv.Stopping()` is closed so shutdown doesn't wait for the grace period.

HSTS (`Strict-Transport-Security`) is sent automatically when
`APP_ENV=production`; serve production over HTTPS (usually at your proxy or
load balancer).

## Sessions

Read by `session.ForApp` (or `session.LoadConfig`) into `session.Config`.

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `SESSION_COOKIE` | string | `anetos_session` | Cookie name. A Secure cookie without `SESSION_DOMAIN` and with path `/` gets the `__Host-` prefix. A name you give with `__Host-` or `__Secure-` must meet the browser's rules for it | v0.1 |
| `SESSION_LIFETIME` | duration | `2h` | The session ends after this long without a request (at least `1m`) | v0.1 |
| `SESSION_MAX_LIFETIME` | duration | `168h` | The session ends this long after it started or was regenerated (login), however active. `0` disables | v0.1 |
| `SESSION_EXPIRE_ON_CLOSE` | bool | `false` | Browser-session cookie: dropped when the browser closes | v0.1 |
| `SESSION_DOMAIN` | string | empty (this host only) | Cookie domain; set it to share the session with subdomains | v0.1 |
| `SESSION_PATH` | string | `/` | Cookie path | v0.1 |
| `SESSION_SECURE` | bool | `true`, except `development` and `testing` | Send the cookie over HTTPS only | v0.1 |
| `SESSION_SAME_SITE` | `lax` \| `strict` \| `none` | `lax` | Cookie SameSite mode; `none` requires `SESSION_SECURE=true` | v0.1 |

The cookie is always `HttpOnly`. Its content is encrypted with `APP_KEY`
and limited to about 4 KB.

## Database

Read by `db.Connect` (or `db.LoadConfig(src, prefix)`, which reads the same
keys with a prefix, e.g. `ANALYTICS_DB_HOST`) into `db.Config`.

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `DB_CONNECTION` | `sqlite` \| `postgres` \| `mysql` | `sqlite` | Selects one of the drivers passed to `db.Connect` | v0.1 |
| `DB_URL` | string | empty | Complete connection string in the driver's format; when set, the five keys below are ignored. Use it for TLS and driver options | v0.1 |
| `DB_HOST` | string | `127.0.0.1` | Server host (PostgreSQL, MySQL) | v0.1 |
| `DB_PORT` | int | driver default (5432, 3306) | Server port | v0.1 |
| `DB_DATABASE` | string | empty; SQLite: `database/app.db` | Database name, or the SQLite file (`:memory:` for an in-memory database) | v0.1 |
| `DB_USERNAME` | string | empty | User | v0.1 |
| `DB_PASSWORD` | string | empty | Password | v0.1 |
| `DB_MAX_OPEN_CONNS` | int | `25` | Maximum open connections (in-memory SQLite always uses 1) | v0.1 |
| `DB_MAX_IDLE_CONNS` | int | `25` | Maximum idle connections kept for reuse | v0.1 |
| `DB_CONN_MAX_LIFETIME` | duration | `30m` | Connections are replaced after this long | v0.1 |
| `DB_CONN_MAX_IDLE_TIME` | duration | `5m` | Idle connections are closed after this long | v0.1 |
| `DB_LOG_QUERIES` | bool | on when `APP_ENV=development` | Log every query, with its arguments and duration, at debug level | v0.1 |
| `DB_SLOW_QUERY` | duration | `500ms` | Log queries taking at least this long as warnings (without arguments). `0` disables | v0.1 |

Driver specifics:

- **SQLite** connections use WAL journaling, a 5s busy timeout, foreign
  keys and immediate transactions. The file's directory is created if
  missing.
- **SQLite** stores times as UTC text in the format of its own
  `CURRENT_TIMESTAMP` (`2026-09-30 12:00:00.5`), so column defaults and
  values written by the app compare correctly.
- **PostgreSQL** sessions use the UTC time zone unless `DB_URL` sets
  `timezone`.
- **MySQL** connections always read times as UTC `time.Time`
  (`parseTime=true`, `loc=UTC`) and report matched rows for updates
  (`clientFoundRows=true`), also when `DB_URL` says otherwise; sessions use
  `time_zone='+00:00'` unless `DB_URL` sets it.
