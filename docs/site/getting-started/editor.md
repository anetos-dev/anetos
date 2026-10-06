---
title: Set up your editor
since: v0.3.0
group: "Installation"
weight: 11
---

# Set up your editor

An Anetos app's code is Go, and its pages are [templ](https://templ.guide)
files (`.templ`): HTML with Go expressions, compiled to Go. Your editor
needs to know both.

## Go

Install your editor's Go support, which runs `gopls`, Go's language
server: the Go extension in VS Code, the Go plugin in other JetBrains
IDEs (built into GoLand), `nvim-lspconfig`'s `gopls` in Neovim.

## templ

Install templ's extension: "templ-vscode" in VS Code, the "Templ"
plugin in GoLand, the `templ` language server in Neovim. They highlight
`.templ` files, complete Go in them, and report errors as you type.

The extensions start templ's language server, `templ lsp`, from your
`PATH`. Projects use templ as a tool of their module (`go tool templ`),
so install the same version for the editor too: the version is on the
`github.com/a-h/templ` line of the project's `go.mod`.

```sh
X
```

## Formatting

`gofmt` (your editor runs it on save) formats Go; `go tool templ fmt .`
formats `.templ` files. `go tool anetos dev` regenerates the templ
code and the models' typed columns as you save, so you don't run the
generators yourself.

Next: [Choose a database](databases.md).
