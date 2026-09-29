---
title: Configure your application
since: v0.1.0
---

# Configure your application

Read settings from `.env` files and the environment into typed Go structs,
with defaults, required keys and validation.

## Before you start

- The framework's own settings (`APP_*`, `LOG_*`) are listed in the
  [configuration reference](../reference/configuration.md).
- `anetos.New()` loads configuration for you. This guide shows how to add
  your own settings.

## Steps

### 1. Put settings in `.env`

```bash
# .env: local development only; never commit real secrets
APP_NAME=blog
APP_ENV=development

HEARTBEAT_INTERVAL=500ms
DB_URL=postgres://app:secret@localhost:5432/blog
REDIS_URL="redis://${REDIS_HOST}:6379"   # ${NAME} uses the environment, else earlier keys
```

Commit a `.env.example` with every key and safe placeholder values, and keep
`.env` itself out of git (the generated `.gitignore` already does).

### 2. Describe them with a struct

```go
// HeartbeatConfig is this app's own configuration, bound from the environment.
type HeartbeatConfig struct {
	Interval time.Duration `env:"HEARTBEAT_INTERVAL" default:"2s"`
	FailAt   int           `env:"HEARTBEAT_FAIL_AT"` // simulate a crash after N beats (0 = never)
}

func (c HeartbeatConfig) Validate() error {
	if c.Interval < 10*time.Millisecond {
		return errors.New("HEARTBEAT_INTERVAL must be at least 10ms")
	}
	return nil
}
```

(Copied from [`examples/lifecycle`](../../../examples/lifecycle/main.go),
region `config`.)

- `env:"KEY"` names the variable; `env:"KEY,required"` makes it mandatory.
- `default:"…"` applies when the key is missing **or empty**.
- A `Validate() error` method runs after binding, for rules that tags can't
  express.

### 3. Bind it at startup

```go
// illustrative
app, err := anetos.New()
if err != nil {
	return err
}
cfg, err := config.Get[HeartbeatConfig](app.Source())
if err != nil {
	return err // lists every missing or invalid key at once
}
```

Pass `cfg` (or the fields you need) to the code that uses it. Don't look up
keys by string elsewhere in the app.

### 4. Group related settings with prefixes

```go
// illustrative
type DatabaseConfig struct {
	URL     string `env:"URL,required"`
	MaxOpen int    `env:"MAX_OPEN" default:"20"`
}

type Config struct {
	Primary DatabaseConfig `prefix:"DB_"`         // DB_URL, DB_MAX_OPEN
	Replica DatabaseConfig `prefix:"DB_REPLICA_"` // DB_REPLICA_URL, …
}
```

## How it works

Values are looked up in three layers, highest priority first:

1. **Process environment**: what production deployments normally use.
2. **`.env.<APP_ENV>`**, e.g. `.env.testing`, if it exists.
3. **`.env`**, if it exists.

Missing files are fine. A file with a syntax error stops startup with the
file name and line number. Binding runs once at startup, and the rest of your
app only sees typed structs.

> **Coming from Laravel?** This replaces `env()` plus `config/*.php`. The
> struct is your config file, and there's no config cache to clear.

## Testing it

Pass a `config.Map` instead of reading real files or the environment:

```go
// illustrative
cfg, err := config.Get[HeartbeatConfig](config.Map{"HEARTBEAT_INTERVAL": "1s"})

app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing"}))
```

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `invalid duration "5"` | Durations need a unit | Use `5s`, `500ms`, `2m` |
| `required but not set` although the key is in `.env` | The value is empty | Empty counts as unset; give it a value |
| `APP_DEBUG=true is not allowed when APP_ENV=production` | Debug mode in production | Set `APP_ENV=development` locally |
| A value from `.env` is ignored | The same variable is set in your shell or container | The process environment wins; unset it there |
| Password with `$` is mangled | Written as `${...}` | Only `${NAME}` expands; use single quotes for literal values |

## Next steps

- [Configuration reference](../reference/configuration.md): every framework
  key and the full tag syntax.
- [Application lifecycle](../concepts/application-lifecycle.md): where
  configuration fits in startup.
