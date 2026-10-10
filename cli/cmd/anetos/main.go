// SPDX-License-Identifier: Apache-2.0

// Command anetos is Anetos's developer tool. Install it as a tool of your
// module and run it with go tool:
//
//	go get -tool anetos.dev/anetos/cli/cmd/anetos@latest
//	go tool anetos generate
//
// Commands:
//
//	new <directory>           create a project
//	dev                       run the app with live reload
//	build [-o file]           build the app for production: one binary
//	make:handler <Name>       add a handler
//	make:model <Name>         add a model (--migration: and its migration)
//	make:migration <name>     add a migration
//	make:middleware <Name>    add a middleware
//	make:agent <Name>         add an AI agent
//	make:crud <Model> <fields>  add a model with pages to list, show, create, edit and delete it
//	make:auth                 add accounts: registration, login, verification, reset, API tokens
//	make:admin                add the admin interface (after make:auth)
//	make:admin-resource <Model>  add a model to the admin
//	generate [--check] [packages]  generate typed columns for models (default ./...)
//	css:build [--check]       compile the Tailwind CSS stylesheet
//	css:use [<framework>] [--force]  switch the project's CSS framework
//	add <module>[@version]    install a plugin
//	remove <module>           uninstall a plugin
//	locale:add <locale>...    add translations of the framework's messages
//	doctor                    check the project and the app's settings
//	key:generate              set APP_KEY in .env
//	version                   print the version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"

	"anetos.dev/anetos/cli/internal/modelgen"
	"anetos.dev/anetos/internal/cmdname"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `Usage: anetos <command> [arguments]

Commands:
  new <directory>           create a project
  dev                       run the app with live reload
  build [-o file]           build the app for production: one static binary (bin/<name>)
  make:handler <Name>       add a handler to app/handlers
  make:model <Name>         add a model to app/models (--migration: and its migration)
  make:migration <name>     add a migration to database/migrations
  make:middleware <Name>    add a middleware to app/middleware
  make:agent <Name>         add an AI agent to app/agents
  make:crud <Model> <field:type>...  add a model, its table, and pages to list, show, create, edit and delete it
  make:auth                 add accounts: registration, login, email verification, password reset, API tokens
  make:admin                add the admin interface at /admin (after make:auth)
  make:admin-resource <Model>  add a model to the admin (app/admin)
  generate [--check] [packages]  generate typed columns for models (default ./...)
  css:build [--check]       compile views/ui/tailwind.css into public/static/app.css (Tailwind CSS)
  css:use [<framework>] [--force]  switch the project's CSS framework (anetos, none, pico, bootstrap, bulma, tailwind)
  add <module>[@version]    install a plugin (go get, plugins.go, .env.example)
  remove <module>           uninstall a plugin
  locale:add <locale>...    add translations of the framework's messages to locales/
  doctor [--strict] [--vuln]  check the project and the app's settings for unsafe values
  key:generate [--show] [--force]  set APP_KEY in .env (--show prints a key instead)
  version                   print the version

Run "anetos help <command>" (or "anetos <command> -h") for a command's flags.
A command's name may be shortened while it stays unique: "g" is generate,
"k:g" key:generate.
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if _, former := formerNames[args[0]]; !former {
		name, candidates := cmdname.Match(commandNames, args[0])
		if len(candidates) > 0 {
			fmt.Fprintf(stderr, "anetos: %q could be %s\n", args[0], strings.Join(candidates, ", "))
			return 2
		}
		if name != "" {
			args = append([]string{name}, args[1:]...) // g: generate, k:g: key:generate
		}
	}
	if name, ok := formerNames[args[0]]; ok {
		fmt.Fprintf(stderr, "anetos: %s is now %s; the old name will be removed in v0.6\n", args[0], name)
		args = append([]string{name}, args[1:]...)
	}
	if len(args) > 1 && args[0] == "add" && args[1] == "lang" {
		fmt.Fprintln(stderr, "anetos: add lang is now locale:add; the old name will be removed in v0.6")
		args = append([]string{"locale:add"}, args[2:]...)
	}
	switch args[0] {
	case "generate":
		return generate(args[1:], stdout, stderr)
	case "new":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return newProject(ctx, args[1:], stdout, stderr)
	case "add", "remove", "locale:add":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		switch args[0] {
		case "locale:add":
			return localeAdd(ctx, args[1:], stdout, stderr)
		case "add":
			return addPlugin(ctx, args[1:], stdout, stderr)
		}
		return removePlugin(ctx, args[1:], stdout, stderr)
	case "dev":
		return dev(args[1:], stdout, stderr)
	case "build":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return build(ctx, args[1:], stdout, stderr)
	case "make:auth":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return makeAuth(ctx, args[1:], stdout, stderr)
	case "make:crud":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return makeCrud(ctx, args[1:], stdout, stderr)
	case "make:admin":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return makeAdmin(ctx, args[1:], stdout, stderr)
	case "doctor":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return doctor(ctx, args[1:], stdout, stderr)
	case "css:build":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return cssBuild(ctx, args[1:], stdout, stderr)
	case "css:use":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return cssUse(ctx, args[1:], stdout, stderr)
	case "make:admin-resource":
		return makeAdminResource(args[1:], stdout, stderr)
	case "make:handler", "make:model", "make:migration", "make:middleware", "make:agent":
		return makeCmd(args[0], args[1:], stdout, stderr)
	case "key:generate":
		return keyGenerate(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, "anetos", version())
		return 0
	case "help", "-h", "-help", "--help":
		if len(args) > 1 && args[0] == "help" && !strings.HasPrefix(args[1], "-") && args[1] != "help" {
			// anetos help make:crud is anetos make:crud -h, on stdout
			// (help add lang: add lang -h).
			return run(append(slices.Clone(args[1:]), "-h"), stdout, stdout)
		}
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "anetos: unknown command %q\n\n%s", args[0], usage)
	return 2
}

// commandNames are the commands, which a shortened name may mean ("g"
// is generate, "k:g" key:generate): run's switch must have exactly these
// (TestCommandNames checks).
var commandNames = []string{
	"new", "dev", "build", "generate", "add", "remove", "doctor", "version", "help",
	"make:handler", "make:model", "make:migration", "make:middleware", "make:agent",
	"make:crud", "make:auth", "make:admin", "make:admin-resource",
	"css:build", "css:use", "key:generate", "locale:add",
}

// formerNames are the commands' names before v0.5, still run (with a
// warning) until v0.6. (gen, generate's, is now a short form of it.)
var formerNames = map[string]string{
	"lang:add":            "locale:add",
	"make:admin:resource": "make:admin-resource",
}

func generate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "don't write; exit with status 1 if any generated file is out of date")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: anetos generate [--check] [packages]

Writes models_gen.go in each package with models, declaring the typed
columns of every model (PostCols for Post) and the handles of its relation
fields (PostRels, for fields with a rel tag). A model is a struct that embeds
db.Model, db.Timestamps or db.SoftDeletes, has a TableName method, or has a
//anetos:model comment; //anetos:skip excludes one. Packages default to ./...

Flags:
`)
		printFlags(stderr, fs)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "anetos generate:", err)
		return 1
	}
	changes, err := modelgen.Generate(dir, fs.Args()...)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *check {
		for _, c := range changes {
			fmt.Fprintf(stderr, "anetos generate: %s is out of date\n", rel(dir, c.Path))
		}
		if len(changes) > 0 {
			fmt.Fprintln(stderr, "anetos generate: run `go tool anetos generate` and commit the result")
			return 1
		}
		return 0
	}
	if err := modelgen.Apply(changes); err != nil {
		fmt.Fprintln(stderr, "anetos generate:", err)
		return 1
	}
	for _, c := range changes {
		if c.Content == nil {
			fmt.Fprintf(stdout, "removed %s (no models left)\n", rel(dir, c.Path))
		} else {
			fmt.Fprintf(stdout, "wrote %s (%s)\n", rel(dir, c.Path), strings.Join(c.Models, ", "))
		}
	}
	return 0
}

func rel(dir, path string) string {
	if r, err := filepath.Rel(dir, path); err == nil {
		return r
	}
	return path
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "anetos.dev/anetos/cli" {
				return d.Version
			}
		}
		if bi.Main.Path == "anetos.dev/anetos/cli" {
			return bi.Main.Version
		}
	}
	return "(devel)"
}
