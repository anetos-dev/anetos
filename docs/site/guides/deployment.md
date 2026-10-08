---
title: Deploy
since: v0.3.0
group: "Production"
weight: 800
---

# Deploy

An Anetos app is one binary: the migrations, views, translations and
static files are inside it. Deploying it is building it, giving it its
settings, running its migrations, and keeping it running. This guide
does that on a server with systemd, in a container, and on two hosting
platforms (Fly.io, Render). The examples use an app called `blog`.

Projects made by `anetos new` (v0.3) have the files this guide uses: a
`Dockerfile`, `deploy/blog.service` and `deploy/production.env.example`.
For an older project, run `anetos new` in an empty directory and copy
them over.

## Before you start

- The app's tests pass (`go test ./...`).
- A production database (PostgreSQL or MySQL), unless the app uses
  SQLite on one machine.
- For accounts and password resets: an email service (SMTP or a driver
  like Postmark). See [Mail](mail.md).

## Steps

### 1. Build

```sh
$ go tool anetos build
built bin/blog (linux/amd64, 18.3 MB)
```

`anetos build` generates the templ views and typed columns, then builds
a static binary (`CGO_ENABLED=0`, `-trimpath`, without debug symbols).
`--target` builds for another system, a Linux server from a Mac say:

```sh
go tool anetos build --target=linux/arm64 -o bin/blog-arm64
```

Build in the git repository: Go records the commit, and the tag if the
commit has one (`git tag v1.2.0`), and `blog version` prints them.
Where there is no repository (in a container), pass the version:

```sh
$ go tool anetos build --version=v1.2.0
$ ./bin/blog version
blog v1.2.0 (commit 1a2b3c4d5e6f, 2026-10-06T10:00:00Z)
Anetos v0.3.0
go1.26.8 linux/amd64
```

### 2. Prepare the settings

The app reads its settings from the environment; in production there is
no `.env` file. `deploy/production.env.example` lists the ones to set:

| Setting | Why |
|---|---|
| `APP_ENV=production` | The default when unset; JSON logs, secure cookies, `APP_DEBUG` refused |
| `APP_KEY` | A new key for production (`go tool anetos key:generate`), never the one in `.env`. Keep it secret: it encrypts sessions and two-factor secrets. When you change it, put the old one in `APP_PREVIOUS_KEYS` |
| `APP_URL` | The public URL (`https://blog.example.com`), for links in emails and social sign-in |
| `DB_CONNECTION`, `DB_URL` | The database, with TLS that checks the server (`sslmode=verify-full`, `tls=true`). With `DB_HOST` and the others instead of `DB_URL`, TLS is on and verified for any host but `localhost` (`DB_TLS`; `DB_TLS_CA` for a provider's own CA) |
| `CACHE_STORE`, `SESSION_DRIVER`, `QUEUE_DRIVER` | `database` (or `redis`) so that every process shares them: with `memory`, a second process has its own cache and queue |
| `MAIL_DRIVER`, `MAIL_FROM_ADDRESS` | `log` sends nothing (the app warns at start); in production it logs who an email is for and its subject, never its body, which may hold sign-in links |
| `HTTP_TRUSTED_PROXIES` | The address of the proxy in front of the app, so client IPs (rate limits, logs, the audit log) are the visitors' (step 5) |
| `STORAGE_ROOT` or `STORAGE_DRIVER` | Uploaded files: a directory that survives deploys, or `s3`/`gcs` |

An API project (`anetos new --stack=api`, v0.4) has no sessions, so
`SESSION_*` don't apply; it adds:

| Setting | Why |
|---|---|
| `AUTH_CLIENT_URL` | After `make:auth`: the client app's address (`https://app.example.com`), which the verification and password reset emails link to. The app refuses to start without it in production |
| `HTTP_CORS_ORIGINS` | The origins of browser apps that call the API (`https://app.example.com`), comma-separated; empty, none may. Server-side clients and mobile apps don't need it |

The app serves its API's description at `/api/v1/openapi.json`, made
from its routes; commit `openapi.json` with the code, for reviewers and
client developers ([Describe an API with OpenAPI](openapi.md)).

The app checks them when it starts, for any command: a missing or
wrong one stops it with a message naming the setting. Only `version`
runs without them. Settings that work but are unsafe
(`SESSION_SECURE=false`, a database reached without verified TLS, the
`log` mail driver) don't stop it: `blog doctor` reports them (v0.3; see
[Secure your app](security.md)).

### 3. Run the migrations on each deploy

```sh
./blog migrate
```

Run it before starting the new version. In production, `migrate` runs
without `--force` (`migrate:rollback`, `migrate:reset` and `db:seed`
need it), and takes a lock in PostgreSQL and MySQL, so two servers
deploying at once run each migration once.

While the migrations run, the old version is still serving. Write them
so that it keeps working: add a column in one release and use it in the
next; stop using a column in one release and drop it in the next.

### 4. Run it

Pick one of the three ways below.

#### Run it on a server with systemd

`deploy/blog.service` runs the app as a user of its own, with
`/var/lib/blog` to write to, runs the migrations before each start, and
restarts it if it stops. Build for the server
(`--target=linux/amd64`), copy the binary, the unit and your settings
there, then:

```sh
sudo install -D bin/blog /opt/blog/blog
sudo install -D -m 600 production.env /etc/blog/env
sudo cp deploy/blog.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now blog
journalctl -u blog -f      # the logs
```

To deploy a new version, copy the binary over and restart:

```sh
sudo install bin/blog /opt/blog/blog && sudo systemctl restart blog
```

The restart stops the old process (it finishes its requests and jobs
within `APP_SHUTDOWN_TIMEOUT`; the unit kills it after 45 seconds),
migrates and starts the new one: requests wait a moment or fail during
it. For no gap, run two instances on different `HTTP_ADDR` behind the
proxy and restart them one at a time.

#### Run it in a container

```sh
docker build -t blog --build-arg VERSION=v1.2.0 .
docker run --rm --env-file production.env -v blog-data:/data blog migrate
docker run -d --name blog -p 8080:8080 --env-file production.env -v blog-data:/data blog
```

The `migrate` step needs the same volume as the server when the
database is SQLite (in `/data`). A SQLite project's image doesn't need
the step at all: its `Dockerfile` sets `MIGRATE_ON_RUN=true`, so the
server migrates when it starts. Run one container on a SQLite volume
that way; a second one on the same volume (the workers below) gets
`-e MIGRATE_ON_RUN=false`, since two starting at once could both
migrate. Until the migrations have run, the
server reports itself unavailable (`health:check`, `/health/ready`) and
logs which ones are pending, so a forgotten step shows as `unhealthy`.

The `Dockerfile` builds with `anetos build` in the Go image and copies
the binary alone into a distroless image (about 25 MB), run as user
65532. The image sets `APP_ENV=production`, `HTTP_ADDR=:8080` and
`STORAGE_ROOT=/data/storage` (and `DB_DATABASE=/data/app.db` for
SQLite): mount a volume on `/data`. Its `HEALTHCHECK` runs
`blog health:check`, which asks the server for `/health/ready`;
`docker ps` shows `healthy`.

`docker stop` sends SIGTERM and waits 10 seconds before killing; give
it the time the app may take: `docker stop -t 35 blog`, or
`stop_grace_period: 35s` in Compose.

To run the background work in its own container, override the command,
and turn the health check off (it doesn't serve HTTP):

```sh
docker run -d --name blog-worker --env-file production.env --no-healthcheck blog run --only=workers,scheduler
```

The [complete example](#complete-example) runs the web server, the
workers and PostgreSQL with Docker Compose.

#### Run it on a hosting platform

Platforms build the image from the `Dockerfile`, run it, and route
HTTPS to it. Two examples:

**Fly.io**: `fly launch` finds the `Dockerfile`; edit the `fly.toml` it
writes:

```toml
app = "blog"
primary_region = "ams"
# Fly sends SIGINT and waits 5 seconds by default.
kill_signal = "SIGTERM"
kill_timeout = 35

# Runs in a temporary machine before each deploy (no volumes there).
[deploy]
  release_command = "migrate"

# Each value replaces the image's CMD; the binary stays the entrypoint.
[processes]
  web = "run --only=http"
  worker = "run --only=workers,scheduler"

[env]
  APP_URL = "https://blog.fly.dev"
  CACHE_STORE = "database"
  SESSION_DRIVER = "database"
  QUEUE_DRIVER = "database"

[http_service]
  internal_port = 8080
  force_https = true
  processes = ["web"]

  [[http_service.checks]]
    grace_period = "15s"
    interval = "30s"
    method = "GET"
    timeout = "5s"
    path = "/health/ready"
```

Set the secrets with `fly secrets set APP_KEY=base64:… DB_URL=…`, then
`fly deploy`. The release command has no volume, so this suits
PostgreSQL or MySQL. For SQLite, run one machine of a single process
(no `[processes]`), with a volume (`[[mounts]] source = "blog_data"`,
`destination = "/data"`) and without `release_command`: the image
migrates when it starts (`MIGRATE_ON_RUN=true`, set by the
`Dockerfile`), and the health check passes once it has.

**Render**: a `render.yaml` blueprint with a PostgreSQL database; the
web service and the worker share their settings through an environment
group (except `APP_KEY` and `DB_URL`, which a group can't hold):

```yaml
services:
  - type: web
    name: blog
    runtime: docker
    plan: starter
    healthCheckPath: /health/ready
    # Render's commands replace the image's entrypoint: give the binary's path.
    preDeployCommand: /app/blog migrate
    dockerCommand: /app/blog run --only=http
    envVars:
      - fromGroup: blog
      - key: APP_KEY
        sync: false            # asked for on the first deploy: the same key for both
      - key: DB_URL
        fromDatabase:
          name: blog-db
          property: connectionString
  - type: worker
    name: blog-worker
    runtime: docker
    plan: starter
    dockerCommand: /app/blog run --only=workers,scheduler
    envVars:
      - fromGroup: blog
      - key: APP_KEY
        sync: false            # asked for on the first deploy: the same key for both
      - key: DB_URL
        fromDatabase:
          name: blog-db
          property: connectionString

envVarGroups:
  - name: blog
    envVars:
      - key: APP_URL
        value: https://blog.onrender.com
      - key: DB_CONNECTION
        value: postgres
      - key: CACHE_STORE
        value: database
      - key: SESSION_DRIVER
        value: database
      - key: QUEUE_DRIVER
        value: database

databases:
  - name: blog-db
```

`APP_KEY` must be `base64:` and 32 bytes, so don't let Render generate
it (`generateValue`). The pre-deploy command needs a paid instance
type.

### 5. Put HTTPS in front

The app serves plain HTTP. On a server, put a proxy in front for HTTPS.
With [Caddy](https://caddyserver.com), which gets the certificate
itself:

```text
blog.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

Then, in the settings:

```sh
HTTP_ADDR=127.0.0.1:8080        # reachable only through the proxy
HTTP_TRUSTED_PROXIES=127.0.0.1
```

`HTTP_TRUSTED_PROXIES` lets the app take the client's IP from
`X-Forwarded-For`, but only on connections from those addresses. Caddy
sets it; with nginx, add `proxy_set_header X-Forwarded-For
$proxy_add_x_forwarded_for;` (the app doesn't read `X-Real-IP`, which a
proxy that doesn't set it passes through from the client). With
`docker run -p`, connections come from the Docker network's gateway
(`172.17.0.1` by default); on a platform, from its proxy: trust its
network. The request log's `ip` field shows the address while it isn't
trusted.

> **Warning:** Trust only addresses that nothing but your proxy uses. If
> the app can be reached directly from a trusted address, anyone can
> claim any IP, and rate limits, the audit log and `ADMIN_ALLOW_IPS`
> believe them.

### 6. Split the roles as you grow

`run` starts every component: the web server, queue workers, event
listeners, the scheduler. With more traffic, run them in separate
processes or machines (`run --only=http`, `run --only=workers`;
[the runtime supervisor](../concepts/runtime-supervisor.md) explains
roles):

| Role | How many |
|---|---|
| `http` | As many as you need, behind the load balancer |
| `workers`, `listeners` | As many as the jobs need; they share the queue (`QUEUE_DRIVER=database` or `redis`) and the broker |
| `scheduler` | One; or several, with tasks marked `schedule.OnOneServer()` and a shared cache (`CACHE_STORE=database` or `redis`) |

Make sure some process runs each role the app has: a scheduler that no
process runs never runs its tasks. `run --only=` with a role the app
doesn't have fails and lists those it has. `scheduler` is known as soon
as `schedule.ForApp` is called, so `--only=workers,scheduler` works
before the app has a task; add `listeners` once it uses
[pub/sub](pubsub.md).

Every process stops gracefully on SIGTERM: the server stops accepting
requests and finishes those in flight, workers finish their jobs, then
the app closes the database. Give it `APP_SHUTDOWN_TIMEOUT` (30 seconds)
before your platform kills it, or lower the setting to fit.

## Complete example

The web server, the background work and PostgreSQL on one Docker host
with Docker Compose. The migrations run first, in a container that
stops when they are done; the others start after it. Put the
`compose.yaml` next to the `Dockerfile`, with `production.env` (without
`DB_*`: they are set here):

```yaml
# compose.yaml. The database password goes in compose.env (chmod 600,
# out of git), not on the command line:
#   echo "POSTGRES_PASSWORD=$(openssl rand -hex 24)" > compose.env
#   docker compose --env-file compose.env up -d --build
x-app: &app
  build: .
  image: blog
  env_file: production.env
  environment:
    DB_CONNECTION: postgres
    # Plain text on the hosts' own network, with nothing else on it.
    DB_URL: postgres://blog:${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD}@db:5432/blog?sslmode=disable
  volumes:
    - app-data:/data
  stop_grace_period: 35s # APP_SHUTDOWN_TIMEOUT and a margin

services:
  db:
    image: postgres:17
    environment:
      POSTGRES_USER: blog
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD}
      POSTGRES_DB: blog
    volumes:
      - db-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "blog"]
      interval: 5s

  # Runs the migrations, then stops; the others start after it.
  migrate:
    <<: *app
    command: migrate
    healthcheck:
      disable: true
    depends_on:
      db:
        condition: service_healthy

  web:
    <<: *app
    command: run --only=http
    ports:
      - "127.0.0.1:8080:8080" # for the HTTPS proxy on the host
    restart: unless-stopped
    depends_on:
      migrate:
        condition: service_completed_successfully

  worker:
    <<: *app
    command: run --only=workers,scheduler
    healthcheck:
      disable: true # no HTTP server here
    restart: unless-stopped
    depends_on:
      migrate:
        condition: service_completed_successfully

volumes:
  db-data:
  app-data:
```

The database is reached over Compose's private network, hence
`sslmode=disable`. Set `HTTP_TRUSTED_PROXIES` to the Compose network's
gateway (`docker network inspect blog_default`) for the proxy on the
host. To deploy a new version, run the same command: Compose rebuilds,
migrates, and replaces the containers that changed.

## How it works

- `anetos build` runs `go build` with `-trimpath -ldflags="-s -w"`, and
  `-X anetos.dev/anetos.buildVersion=…` for `--version`. Without cgo,
  the binary needs nothing on the machine, not even its C library, so
  it runs on a distroless or `scratch` image. SQLite works without cgo.
- The binary embeds `public/`, `locales/` and the migrations with
  `go:embed`; templ views are Go code. Nothing is read from the
  project's directory at run time, except `.env` files if present
  (`.dockerignore` keeps them out of the image).
- `/health/live` answers 200 while the process runs; `/health/ready`
  answers 200 while it runs, isn't shutting down, and its components
  are ready, so a load balancer stops sending requests as soon as
  shutdown starts. With `migrate.ForApp`, it also answers 503 while the
  database has migrations the app hasn't run (checked every 5 seconds;
  `MIGRATE_READINESS=false` turns that off, for a platform that waits
  for readiness before a later migration step). `health:check` asks the server on `HTTP_ADDR` (on
  `127.0.0.1` when the address has no host, `0.0.0.0` or `[::]`) and
  exits 1 if it isn't ready.

## Testing it

Before the first deploy, run the production build locally:

```sh
go tool anetos build
export APP_ENV=production APP_DEBUG=false APP_KEY=$(go tool anetos key:generate | cut -d= -f2-)
export DB_DATABASE=/tmp/blog.db STORAGE_ROOT=/tmp/blog-storage   # SQLite; DB_URL for the others
./bin/blog migrate
./bin/blog run
```

`APP_DEBUG=false` overrides the `.env` of development, which the app
still reads in the project's directory. In another terminal, with the
same `HTTP_ADDR`:

```sh
$ ./bin/blog health:check
ok
```

`./bin/blog doctor` with the same settings checks them, and says
which migrations haven't run. After a deploy, `blog version` (on the
server, or `docker run --rm blog version`) tells which version runs.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `APP_DEBUG=true is not allowed when APP_ENV=production` | `APP_DEBUG=true` copied from `.env` | Remove it |
| `session: encryption: APP_KEY is not set` | The key isn't in the environment (the image has no `.env`) | Set it from your secrets |
| Signing in doesn't stick over plain HTTP | Production cookies are HTTPS-only (`SESSION_SECURE`) | Serve over HTTPS (step 5) |
| Every visitor has the same IP, rate limits hit everyone | The proxy's address is the client IP | Set `HTTP_TRUSTED_PROXIES` (step 5) |
| Jobs dispatched by the web process never run | `QUEUE_DRIVER=sync` or `memory` with workers in another process | `QUEUE_DRIVER=database` or `redis` |
| `permission denied` writing files in the container | The volume isn't writable by user 65532 | Mount on `/data` (the image prepares it), or `chown 65532` the host directory |
| The container stays `unhealthy` | It doesn't serve HTTP (`run --only=workers`), it isn't ready (`/health/ready` answers 503 while a component restarts or migrations haven't run: the log says which), or `HTTP_HEALTH_ROUTES=false` | Turn the check off for workers (`--no-healthcheck`, `healthcheck: disable: true`); read the logs; keep the health routes on |
| `unknown role "scheduler"` (or `listeners`) | The app has none: no `schedule.ForApp` (or `pubsub.ForApp`) | Leave it out of `--only`; the error lists the app's roles |
| Jobs cut off at each deploy | The platform kills the process before it finishes | Raise its grace period (`docker stop -t`, `kill_timeout`, `TimeoutStopSec`) or lower `APP_SHUTDOWN_TIMEOUT` |
| `blog version` prints `(devel)` | Built outside a git repository without `--version` | `anetos build --version=v1.2.0` (the `Dockerfile` takes `--build-arg VERSION`) |

## Next steps

- [Secure your app](security.md): the checklist before going live.
- [Configuration](configuration.md) and the [settings reference](../reference/configuration.md).
- [Migrations](migrations.md).
- [The runtime supervisor](../concepts/runtime-supervisor.md): roles and graceful shutdown.
- [`anetos build`](../reference/cli.md#anetos-build).
