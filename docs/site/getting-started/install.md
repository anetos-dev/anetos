---
title: Install Anetos
since: v0.3.0
group: "Installation"
weight: 10
---

# Install Anetos

Anetos needs Go and its command-line tool, `anetos`, which creates
projects and writes code.

## Go

Install Go 1.26 or later from [go.dev/dl](https://go.dev/dl/), then
check:

```sh
go version   # go version go1.26.x …
```

Nothing else is required: the default database, SQLite, is built into
the app (in pure Go, so no C compiler either), and the views' compiler,
templ, comes with each project.

## The `anetos` tool

```sh
go install anetos.dev/anetos/cli/cmd/anetos@latest
anetos version
```

`go install` puts it in `$(go env GOPATH)/bin` (usually `~/go/bin`):
add that folder to your `PATH` if `anetos` isn't found.

You use this copy once per project, for `anetos new`. Each project then
pins its own version of the tool in `go.mod`, and you run it there as
`go tool anetos`, so everyone working on the project uses the same one.

## Upgrading

`go install anetos.dev/anetos/cli/cmd/anetos@latest` again updates the
tool for new projects. A project upgrades its own copy with Anetos:
see [Upgrading](../upgrade/README.md).

Next: [Set up your editor](editor.md).
