# SaaS: one binary, every role

A small SaaS app made with `anetos new` and `anetos make:auth`, then
grown into everything v0.2 adds:

- **Accounts**: registration and login with a password, Google or
  GitHub; email verification; password reset; API tokens (all from
  `make:auth`).
- **A welcome email from a queue job**: registration and a first
  sign-in with a provider dispatch `jobs.SendWelcome` once the user is
  committed, and a worker sends the email
  ([`app/jobs/welcome.go`](app/jobs/welcome.go)).
- **A pub/sub topic**: the billing service publishes to
  `billing.subscription_changed`, and a listener sets the user's plan
  ([`app/listeners/billing.go`](app/listeners/billing.go)).
- **A scheduled task**: every minute, `end-trials` moves users whose
  14-day trial is over to the free plan
  ([`app/tasks/trials.go`](app/tasks/trials.go)).

One binary runs it all, or each role in its own processes, as you'd
deploy it.

## Run it

```sh
cp .env.example .env
go tool anetos key:generate >> .env   # APP_KEY
go run . migrate
go run .                              # everything; or go tool anetos dev
```

Open http://localhost:8080/register. Emails go to the log
(`MAIL_DRIVER=log`); the welcome email appears a moment after you
register, sent by a worker.

## Split it by role

Each process runs the components with its role, from the same binary.
They share the database (the jobs, and the scheduler's locks with
`CACHE_STORE=database`) and, for the topic, Redis:

```sh
# .env: PUBSUB_DRIVER=redis and REDIS_URL=redis://127.0.0.1:6379/0
go build -o bin/saas .
./bin/saas run --only=http         # the web server
./bin/saas run --only=workers      # the queue: welcome emails
./bin/saas run --only=listeners    # pub/sub: billing.subscription_changed
./bin/saas run --only=scheduler    # end-trials, every minute

# The billing service, from another terminal:
./bin/saas pubsub:publish billing.subscription_changed '{"email":"ada@example.com","plan":"pro"}'
```

Run as many `http`, `workers` and `listeners` processes as you need;
`OnOneServer` keeps the task to one scheduler at a time.

## Test it

```sh
go test ./...
```

- [`auth_test.go`](auth_test.go): the accounts (from `make:auth`).
- [`saas_test.go`](saas_test.go): the welcome job and email, plan
  changes and trial ends, in one process with `anetostest`.
- [`roles_test.go`](roles_test.go): builds the app and runs it as four
  processes, one per role, following a sign-up from the web process to
  the welcome email sent by a worker, a trial ended by the scheduler, and
  (with `ANETOS_TEST_REDIS_URL` set) a message consumed by the listeners;
  then runs it all in one process. It waits for the scheduler's next
  minute, so it takes up to a minute; `go test -short` skips it.

## Learn more

- [Add accounts with make:auth](../../docs/site/guides/accounts.md)
- [Queues](../../docs/site/guides/queues.md),
  [Pub/sub](../../docs/site/guides/pubsub.md),
  [Scheduling](../../docs/site/guides/scheduling.md)
- [The runtime supervisor](../../docs/site/concepts/runtime-supervisor.md): components, roles and `--only`
