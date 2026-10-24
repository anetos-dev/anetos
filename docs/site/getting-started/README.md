---
title: Getting started
since: v0.1.0
---

# Getting started

From nothing to an app with pages, accounts and tests, in the order
you'd do it. About fifteen minutes; the tutorial then builds a whole
app in an hour.

**Installation**

1. [Install Anetos](install.md): Go and the `anetos` tool.
2. [Set up your editor](editor.md) for Go and templ.
3. [Choose a database](databases.md): SQLite to start, PostgreSQL or
   MySQL when you like.
4. [Build with a coding agent](coding-agents.md) (optional): Claude
   Code, Codex, Cursor, Gemini CLI or GitHub Copilot.

**Your first app**

5. [Create a project](create-a-project.md) and run it with live reload.
6. [Project structure](project-structure.md): what each folder holds.
7. [Add pages for a model](crud.md) with `make:crud`.
8. [Add accounts](add-accounts.md) with `make:auth`.
9. [Test and build](test-and-build.md) for production.

**[Tutorial: build an issue tracker](tutorial/README.md)**: accounts,
pages by hand, htmx, search, events, a queue and email, step by step.

**An API instead**: a JSON API for a mobile app or a front end
built separately.

1. [Create a project](create-a-project.md#an-api-instead) with
   `--stack=api`.
2. [Add accounts to an API](../guides/api-accounts.md): log in with
   tokens.
3. [Add endpoints for a model](crud.md#in-an-api-project) with
   `make:crud`.
4. **[Tutorial: build an API](build-an-api.md)**: bookmarks that are
   each user's, tokens for scripts, tests and the API's OpenAPI
   description, in half an hour.

Then the [guides](../guides/) take one feature at a time, and the
[concepts](../concepts/) explain how Anetos works.
