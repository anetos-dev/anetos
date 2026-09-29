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
| `LOG_LEVEL` | `debug` \| `info` \| `warn` \| `error` | `info` | Minimum log level | v0.1 |
| `LOG_FORMAT` | `text` \| `json` \| empty | empty | Log format; empty means JSON in production, text elsewhere | v0.1 |

`APP_ENV` defaults to `production` so that a missing setting fails safe
(debug off, JSON logs).

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
| `bool` | `true`/`false` (also `TRUE`, `True`, …), `1`/`0`, `t`/`f` |
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
