---
title: "Tutorial: build an issue tracker"
since: v0.3.0
weight: 30
---

# Tutorial: build an issue tracker

In this tutorial you build a small issue tracker: people sign up, open
issues, comment on them, search them, and get an email when someone
comments on theirs. Then you build it for production. Along the way you
use most of what an Anetos app is made of: models and migrations,
handlers and routes, templ views and forms, accounts, htmx, full-text
search, events, queue jobs, email, and tests.

The whole tutorial takes about an hour. Short on time? Parts 1 to 3,
then 7, take about 30 minutes and give you an app with accounts and
issues, ready to deploy.

| Part | You add |
|---|---|
| [1. Create the app](01-create-the-app.md) | The project, running with live reload |
| [2. Accounts](02-accounts.md) | Sign-up, sign-in, email verification, settings |
| [3. Issues](03-issues.md) | A model, a migration, pages to list, open and edit issues, a test |
| [4. The issue page](04-the-issue-page.md) | Comments posted with htmx, closing issues, an event |
| [5. Search](05-search.md) | A full-text index and a search page |
| [6. Email the author](06-email-the-author.md) | A mail, and a queued listener that sends it |
| [7. Deploy](07-deploy.md) | The production build, as a binary and an image |

## Before you start

- Go 1.26 or later (`go version`).
- The `anetos` tool:

  ```sh
  go install anetos.dev/anetos/cli/cmd/anetos@latest
  ```

- Some Go: structs, methods, errors. You don't need to know Anetos.

No database server is needed: the app uses SQLite, in a file.

## Reading the code

Each block shows code to put in a file, and the text says where. A new
file starts with its package line, named after its folder (`package
handlers` in `app/handlers`), then the imports the tutorial shows with
it. When a block replaces generated code, the text says which.

## The finished code

[`examples/tutorial`](../../../../examples/tutorial) is the app at the
end of part 6, compiled and tested with every change to Anetos. If
something doesn't work, compare your files with it.

When you're done, [`examples/tracker`](../../../../examples/tracker) is
a bigger version of the same app: projects with members and roles,
labels, files, an admin, a JSON API. It's there to read when you build
your own.
