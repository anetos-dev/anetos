# Tracker: the reference app

An issue tracker built with Anetos, to show how the pieces fit in a real
app. It started as `anetos new`, `anetos make:auth` and `anetos
make:admin`; everything else is the app's own code, written the way the
guides describe. The [tutorial](../../docs/site/getting-started/tutorial/README.md)
builds a smaller version of it step by step.

| Feature | Where | Guide |
|---|---|---|
| Projects with members: owners, members, viewers | [`app/access`](app/access/access.go), [`app/handlers/members.go`](app/handlers/members.go) | [Roles and permissions](../../docs/site/guides/roles-and-permissions.md) |
| Issues numbered per project (WEB-12), with labels, priorities, assignees | [`app/models/issue.go`](app/models/issue.go), [`app/handlers/issues.go`](app/handlers/issues.go) | [Models](../../docs/site/guides/models.md), [Relations](../../docs/site/guides/relations.md) |
| Lists filtered, sorted, searched and paginated | [`app/handlers/projects.go`](app/handlers/projects.go) | [Queries](../../docs/site/guides/queries.md), [Search](../../docs/site/guides/search.md) |
| Comments added and issues closed without a page load | [`views/issues.templ`](views/issues.templ) | [Views](../../docs/site/guides/views.md) |
| Files attached to issues | [`app/handlers/attachments.go`](app/handlers/attachments.go) | [Storage](../../docs/site/guides/storage.md) |
| Each issue's history | [`app/handlers/issues.go`](app/handlers/issues.go) (`activity`) | [Audit log](../../docs/site/guides/audit-log.md) |
| Emails about assignments and comments, from queue jobs, in each user's language | [`app/jobs`](app/jobs/notify.go), [`app/mailers`](app/mailers/issues.go) | [Queues](../../docs/site/guides/queues.md), [Mail](../../docs/site/guides/mail.md) |
| A digest every weekday morning | [`app/tasks`](app/tasks/digest.go), `schedules` in [`main.go`](main.go) | [Scheduling](../../docs/site/guides/scheduling.md) |
| Accounts, settings (with an email preference), two-factor authentication | `make:auth`'s files | [Accounts](../../docs/site/guides/accounts.md) |
| An admin for users, roles, projects, issues (with a trash) and the activity | [`admin.go`](admin.go), [`app/admin`](app/admin) | [Admin](../../docs/site/guides/admin.md) |
| A JSON API with tokens, read-only ones included | [`app/handlers/api.go`](app/handlers/api.go), [`routes/tracker.go`](routes/tracker.go) | [Handlers](../../docs/site/guides/handlers.md) |
| Every page's text in a catalog | [`locales/en`](locales/en/app.yaml) | [Translations](../../docs/site/guides/translations.md) |
| A Dockerfile, a systemd unit | [`Dockerfile`](Dockerfile), [`deploy`](deploy) | [Deploy](../../docs/site/guides/deployment.md) |

## Run it

```sh
cp .env.example .env
go tool anetos key:generate           # APP_KEY
go run . migrate
go run . db:seed                      # Ada and Grace, two projects
go tool anetos dev                    # http://localhost:8080
```

Log in as `ada@example.com` (an administrator: `/admin`) or
`grace@example.com`, both with the password `correct horse`. Emails go
to the log (`MAIL_DRIVER=log`).

It runs on SQLite by default; set `DB_DRIVER` and `DB_URL` for
PostgreSQL or MySQL (`main.go` passes all three drivers).

## Test it

```sh
go test ./...
```

The tests (`*_test.go` here) cover each feature through HTTP, as a
browser or an API client would: [`tracker_test.go`](tracker_test.go)
builds a project with an owner, a member, a viewer and an outsider, and
logs them in with `anetostest.ActingAs`. They run on an in-memory
SQLite database; for PostgreSQL or MySQL, create a database and set
`DB_DRIVER` and `DB_URL`:

```sh
DB_DRIVER=postgres DB_URL=postgres://localhost/tracker_test go test ./...
```

On MySQL and MariaDB, the tests that search commit their rows and
delete them after (full-text indexes there only see committed rows).
