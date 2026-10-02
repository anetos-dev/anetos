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
| `APP_URL` | URL | empty | The app's public URL (`https://example.com`, no path), for links that leave the app: OAuth callbacks (social login), links in emails (`mailer.URL`) | v0.2 |
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
| `SESSION_DRIVER` | `cookie` \| `database` \| a driver's name (`redis`) | `cookie` | Where sessions are kept: the encrypted cookie, or a server-side store (the cookie then holds the encrypted session ID); `redis` needs `redis.SessionDriver()` passed to `session.ForApp` | v0.2 |
| `SESSION_TABLE` | string | `sessions` | The database driver's table; pass the same name to `session.Migrations` | v0.2 |
| `SESSION_PREFIX` | string | `APP_NAME` + `:session:` | Starts the store keys of server-side sessions. `anetostest` sets one per test app | v0.2 |

The cookie is always `HttpOnly`. Its content is encrypted with `APP_KEY`;
with the cookie driver it holds the whole session, limited to about 4 KB.
Server-side sessions are stored, encrypted, under keys starting with `SESSION_PREFIX`.

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
| `DB_REPEATED_QUERIES` | int | `5` when `APP_ENV` is `development` or `testing`, off elsewhere | Warn when a unit of work (a request, a job, a listener, a task) runs the same query this many times or more: an N+1. `0` disables; otherwise at least 2. See [Find N+1 queries](../guides/n-plus-one.md) | v0.2 |

### Search

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `SEARCH_LANGUAGE` | `simple` \| a language | `simple` | How search matches words: `simple` as written, in any language; `english` (PostgreSQL, SQLite) also matches their other forms; on PostgreSQL any text search configuration (`german`, `french`…). MySQL has `simple` only. Search indexes are built for it: change it, then run `search:reindex` | v0.3 |
| `SEARCH_RANKING` | `default` \| `bm25` | `default` | Order of search results: the database's own ranking, or BM25 (SQLite; PostgreSQL 17+ with the pg_textsearch extension; not MySQL). PostgreSQL needs `search:reindex` after a change | v0.3 |

The app refuses to start when the database can't serve these settings,
or when a search index was built for other ones (except for `migrate…`
and `search:reindex`); see [Search](../guides/search.md).

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

## Cache

Read by `cache.ForApp` (or `cache.LoadConfig`) into `cache.Config`.

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `CACHE_STORE` | `memory` \| `database` \| a driver's name (`redis`) | `memory` | Selects the store; `redis` needs `redis.CacheDriver()` passed to `cache.ForApp` | v0.2 |
| `CACHE_PREFIX` | string | `APP_NAME` + `:cache:` | Starts every key, so apps (and other features in Redis) can share a store; `cache:clear` removes only these keys. `anetostest` sets one per test app | v0.2 |
| `CACHE_TABLE` | string | `cache` | The database store's table; pass the same name to `cache.Migrations` | v0.2 |

## Queue

Read by `queue.ForApp` (or `queue.LoadConfig`) into `queue.Config`. See
[Queues](../guides/queues.md).

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `QUEUE_DRIVER` | `sync` \| `memory` \| `database` \| a driver's name (`redis`) | `sync` | Where jobs are kept; `sync` runs each job when it is dispatched. `redis` needs `redis.QueueDriver()` passed to `queue.ForApp` | v0.2 |
| `QUEUE_DEFAULT` | queue name | `default` | The queue of jobs dispatched without `queue.OnQueue`, and of workers without `queue.Queues`. Lower-case letters, digits and `. _ : -`, up to 100 | v0.2 |
| `QUEUE_TRIES` | int ≥ 1 | `3` | Attempts per job, unless its type sets `queue.Tries` | v0.2 |
| `QUEUE_TIMEOUT` | duration | `1m` | How long an attempt may run, unless its type sets `queue.Timeout` | v0.2 |
| `QUEUE_BACKOFF` | duration | `10s` | The wait before the first retry, doubling for each later one, unless the type sets `queue.Backoff` | v0.2 |
| `QUEUE_BACKOFF_MAX` | duration | `10m` | Caps the doubling | v0.2 |
| `QUEUE_POLL` | duration | `1s` | How long an idle worker waits before looking for jobs again | v0.2 |
| `QUEUE_TABLE`, `QUEUE_FAILED_TABLE` | string | `jobs`, `failed_jobs` | The database driver's tables; pass the same names to `queue.Migrations` | v0.2 |
| `QUEUE_PREFIX` | string | `APP_NAME` + `:queue:` | Starts the Redis driver's keys (Redis 5 or later). In a Redis Cluster, put a hash tag in it (`{blog}:queue:`). `anetostest` sets one per test app | v0.2 |

## Pub/sub

Read by `pubsub.ForApp` (or `pubsub.LoadConfig`) into `pubsub.Config`,
and by the drivers. See [Pub/sub listeners](../guides/pubsub.md).

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `PUBSUB_DRIVER` | `memory` \| a driver's name (`redis`, `gcp`) | `memory` | The broker; `redis` needs `redis.PubSubDriver()` and `gcp` needs `gcppubsub.Driver()` passed to `pubsub.ForApp` | v0.2 |
| `PUBSUB_PREFIX` | string | none | Starts topic names in the broker (Redis keys, Google IDs). Topics are shared with the other services using the broker, so leave it empty unless you need to separate them (or, in a Redis Cluster, need a hash tag: `{events}:`). `anetostest` sets one per test app | v0.2 |
| `PUBSUB_REDIS_MAXLEN` | int ≥ 0 | `1000000` | About how many messages each Redis stream keeps; 0 for no limit | v0.2 |
| `PUBSUB_GCP_PROJECT` | string | none (required with `gcp`) | The Google Cloud project's ID | v0.2 |
| `PUBSUB_GCP_CREATE` | bool | `false` | Create missing Google topics and subscriptions (development, the emulator) | v0.2 |

## Scheduler

Read by `schedule.ForApp` (or `schedule.LoadConfig`) into
`schedule.Config`. See [Scheduling](../guides/scheduling.md).

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `SCHEDULE_TIMEZONE` | IANA time zone (`Asia/Dhaka`) | `UTC` | The time zone of schedules without `.In(tz)`. Times that clock changes skip don't run that day; times they repeat run twice (see [Scheduling](../guides/scheduling.md#2-add-it-to-the-scheduler)) | v0.2 |

## Mail

Read by `mailer.ForApp` (or `mailer.LoadConfig`) into `mailer.Config`,
and by the drivers. See [Send email](../guides/mail.md).

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `MAIL_DRIVER` | `log` \| `smtp` \| `memory` \| a driver's name (`postmark`) | `log` | How emails are sent: `log` writes them to the app's log (a warning in production), `memory` keeps them (tests; `anetostest` sets it); `postmark` needs `postmark.Driver()` passed to `mailer.ForApp` | v0.2 |
| `MAIL_FROM_ADDRESS` | email address | none | The sender of messages without one; sending fails without either | v0.2 |
| `MAIL_FROM_NAME` | string | `APP_NAME` | The sender's name | v0.2 |
| `MAIL_SMTP_URL` | URL | `smtp://127.0.0.1:1025` | The SMTP server: `smtp://user:pass@host:587` (STARTTLS, required unless the host is local) or `smtps://…:465` (TLS); parameters `tls=none`, `timeout` (default `30s`), `local_name`. See [SMTP settings](../guides/mail.md#smtp-settings) | v0.2 |
| `MAIL_POSTMARK_TOKEN` | string | none (required with `postmark`) | The Postmark server's API token (`POSTMARK_API_TEST` checks requests without sending) | v0.2 |
| `MAIL_POSTMARK_STREAM` | string | `outbound` | The Postmark message stream | v0.2 |

## AI

Read by `ai.ForApp` (or `ai.LoadConfig`) into `ai.Config`, and by the
drivers. See [Add AI to your app](../guides/ai.md).

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `AI_PROVIDER` | `anthropic` \| `openai` \| `openai-compatible` \| `gemini` \| `fake` \| another driver's name | none (required) | The provider that runs the models; its driver must be passed to `ai.ForApp` (`fake` is built in: it answers with scripted replies and never calls a model, a warning in production; `anetostest` sets it) | v0.3 |
| `AI_MODEL` | string | none (required by the drivers) | The model calls use unless they set `ai.Model`, by the provider's name for it | v0.3 |
| `AI_MAX_TOKENS` | int ≥ 1 | `4096` | The longest answer, in tokens, unless a call sets `ai.MaxTokens`; a longer one is cut off | v0.3 |
| `AI_TIMEOUT` | duration > 0 | `10m` | How long each request to the model may take, unless a call sets `ai.Timeout`; for a stream, including the time the reader's loop takes | v0.3 |
| `ANTHROPIC_API_KEY` | secret | none (required with `anthropic`) | The Anthropic API key | v0.3 |
| `ANTHROPIC_BASE_URL` | URL | Anthropic's | Another URL for the API (a proxy, a gateway) | v0.3 |
| `OPENAI_API_KEY` | secret | none (required with `openai`) | The OpenAI API key | v0.3 |
| `OPENAI_BASE_URL` | URL | OpenAI's | Another URL for OpenAI's API (a proxy, a gateway) | v0.3 |
| `OPENAI_COMPATIBLE_URL` | URL | none (required with `openai-compatible`) | The API URL of an OpenAI-compatible server, up to `/v1`: `http://localhost:11434/v1` for Ollama | v0.3 |
| `OPENAI_COMPATIBLE_KEY` | secret | none | Its API key, if it needs one (then the URL must be https, or on this machine) | v0.3 |
| `GEMINI_API_KEY` | secret | none (required with `gemini`) | The Gemini API key, from Google AI Studio | v0.3 |
| `GEMINI_BASE_URL` | URL | Google's | Another URL for the Gemini API | v0.3 |

The OpenAI and Anthropic SDKs also read their own variables from the
process environment (`OPENAI_ORG_ID`, `OPENAI_PROJECT_ID`,
`ANTHROPIC_AUTH_TOKEN`…): the OpenAI driver uses them, the Anthropic
and compatible drivers don't.

## Storage

Read by `storage.ForApp` (or `storage.LoadConfig`) into `storage.Config`
for each disk, and by the drivers. The default disk reads `STORAGE_*`; a
disk named in `STORAGE_DISKS`, say `avatars`, reads `STORAGE_AVATARS_*`
(`STORAGE_AVATARS_DRIVER`, `STORAGE_AVATARS_S3_BUCKET`, …). See
[Store files](../guides/storage.md).

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `STORAGE_DISKS` | comma-separated names | none | More disks: lower-case letters, digits and `_`, starting with a letter | v0.2 |
| `STORAGE_DRIVER` | `local` \| `memory` \| a driver's name (`s3`) | `local` | Where the disk's files are; `s3` needs `s3.Driver()` passed to `storage.ForApp`. Named disks default to the default disk's | v0.2 |
| `STORAGE_ROOT` | directory | `storage/app` (`storage/<name>` for a named disk) | The local driver's directory | v0.2 |
| `STORAGE_URL` | URL | none | Where the disk's files are served: a CDN, a public bucket, or the route of the disk's handler. Needed for `URL`, and for `TemporaryURL` on local disks | v0.2 |
| `STORAGE_PUBLIC` | bool | `false` | Anyone may read the files at `STORAGE_URL`, so `URL` works; otherwise only signed temporary URLs do | v0.2 |
| `STORAGE_S3_BUCKET` | string | none (required with `s3`) | The bucket | v0.2 |
| `STORAGE_S3_REGION` | string | `us-east-1` | The bucket's region (`auto` for R2). Named disks take the default disk's region, endpoint, keys and path style if they set none of them | v0.2 |
| `STORAGE_S3_ENDPOINT` | URL | AWS | The store's URL, for S3-compatible stores (R2, MinIO, …). Named disks take the default disk's region, endpoint, keys and path style if they set none of them | v0.2 |
| `STORAGE_S3_ACCESS_KEY`, `STORAGE_S3_SECRET_KEY` | string | none | The credentials; without them, the AWS environment variables, shared credentials file or instance role. Named disks take the default disk's region, endpoint, keys and path style if they set none of them | v0.2 |
| `STORAGE_S3_PATH_STYLE` | bool | `false` | The bucket in the URL's path (MinIO). Named disks take the default disk's region, endpoint, keys and path style if they set none of them | v0.2 |
| `STORAGE_S3_PREFIX` | string ending in `/` | none | A prefix for the disk's keys in the bucket (`uploads/`) | v0.2 |

## Redis

Read by `redis.Connect` (module `drivers/redis`, also used by
`redis.CacheDriver()`, `redis.SessionDriver()`, `redis.QueueDriver()` and
`redis.PubSubDriver()`) into `redis.Config`.

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `REDIS_URL` | URL | `redis://127.0.0.1:6379/0` | The server, with its user, password and database: `redis://:secret@cache.internal:6379/2`; `rediss://` for TLS. Options go in the query string (`?dial_timeout=3s`) | v0.2 |

The server is pinged when the app boots, so the app and commands that
boot it fail fast when Redis is unreachable.

## Authentication

Read by `auth.ForApp` (or `auth.LoadConfig`) into `auth.Config`.

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `AUTH_LOGIN_URL` | path | `/login` | Where `Require` sends guests asking for a page | v0.2 |
| `AUTH_HOME_URL` | path | `/` | Where `Guest` sends signed-in users | v0.2 |
| `AUTH_REMEMBER_LIFETIME` | duration | `720h` | How long "remember me" lasts | v0.2 |
| `AUTH_THROTTLE` | int | `5` | Login attempts allowed per minute for one login, or one account, from one IP address (cleared by a success) | v0.2 |
| `AUTH_THROTTLE_IP` | int | `50` | Failed logins allowed per minute from one IP address (IPv6: its /64), whatever the login | v0.2 |
| `AUTH_RESET_TTL` | duration | `60m` | How long a password-reset token works | v0.2 |
| `AUTH_VERIFY_TTL` | duration | `24h` | How long an email-verification token works | v0.2 |

The remember-me cookie is `HttpOnly`, `SameSite=Lax` and Secure like the
session cookie; a Secure one is named `__Host-anetos_remember`.

## Social login

Read by `social.ForApp` and `social.Configured`, for each provider name
(`GOOGLE`, `GITHUB`, or the upper-cased name given to `social.OIDC`, with
`-` as `_`).

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `SOCIAL_<NAME>_CLIENT_ID` | string | empty | The client ID of the app registered with the provider | v0.2 |
| `SOCIAL_<NAME>_CLIENT_SECRET` | string | empty | Its client secret | v0.2 |

Callback URLs are `APP_URL` followed by `/auth/<name>/callback`
(`social.WithCallbackPath` changes the path).


## Plugins

Each plugin's settings start with its name in capitals (`STRIPE_…`),
and are read by `ext.Load` into the plugin's own settings struct. `go
run . plugins:env` prints them with their defaults; `anetos add` adds
them to `.env.example`. Missing or invalid ones stop the app from
booting, with an error naming them. See [Use plugins](../guides/plugins.md).

### `postmark`

Read by the plugin of `plugins/postmark` (its mail transport reads
`MAIL_POSTMARK_*`: [Mail](#mail)).

| Key | Type | Default | Description | Since |
|---|---|---|---|---|
| `POSTMARK_WEBHOOK_USER` | string | empty | The user name of the webhook's basic auth, set in the webhook's URL in Postmark (`https://USER:PASSWORD@example.com/postmark/webhook`). Without it or the password, the webhook refuses every request (401) | v0.2 |
| `POSTMARK_WEBHOOK_PASSWORD` | secret | empty | The webhook's password | v0.2 |
