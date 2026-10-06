// SPDX-License-Identifier: Apache-2.0

// Command anetos is Anetos's developer tool. Install it as a tool of your
// module and run it with go tool:
//
//	go get -tool anetos.dev/anetos/cli/cmd/anetos@latest
//	go tool anetos gen
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
//	make:admin:resource <Model>  add a model to the admin
//	gen [-check] [packages]   generate typed columns for models (default ./...)
//	add <module>[@version]    install a plugin
//	remove <module>           uninstall a plugin
//	lang:add <locale>...      add translations of the framework's messages (also: add lang)
//	key:generate              print a new APP_KEY line
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
	"strings"
	"syscall"

	"anetos.dev/anetos/cli/internal/modelgen"
	"anetos.dev/anetos/internal/appkey"
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
  make:admin:resource <Model>  add a model to the admin (app/admin)
  gen [-check] [packages]   generate typed columns for models (default ./...)
  add <module>[@version]    install a plugin (go get, plugins.go, .env.example)
  remove <module>           uninstall a plugin
  lang:add <locale>...      add translations of the framework's messages to locales/ (also: add lang)
  key:generate              print a new APP_KEY line (append it to .env)
  version                   print the version

Run "anetos <command> -h" for a command's flags.
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "gen":
		return gen(args[1:], stdout, stderr)
	case "new":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return newProject(ctx, args[1:], stdout, stderr)
	case "add", "remove", "lang:add":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		switch {
		case args[0] == "lang:add":
			return langAdd(ctx, args[1:], stdout, stderr)
		case args[0] == "add" && len(args) > 1 && args[1] == "lang":
			return langAdd(ctx, args[2:], stdout, stderr)
		case args[0] == "add":
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
	case "make:admin:resource":
		return makeAdminResource(args[1:], stdout, stderr)
	case "make:handler", "make:model", "make:migration", "make:middleware", "make:agent":
		return makeCmd(args[0], args[1:], stdout, stderr)
	case "key:generate":
		if len(args) > 1 {
			fmt.Fprintln(stderr, "Usage: anetos key:generate\n\nPrints APP_KEY=… with a new random key, for example: go tool anetos key:generate >> .env")
			if args[1] == "-h" || args[1] == "-help" || args[1] == "--help" {
				return 0
			}
			return 2
		}
		fmt.Fprintln(stdout, "APP_KEY="+appkey.Generate())
		return 0
	case "version":
		fmt.Fprintln(stdout, "anetos", version())
		return 0
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "anetos: unknown command %q\n\n%s", args[0], usage)
	return 2
}

func gen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "don't write; exit with status 1 if any generated file is out of date")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: anetos gen [-check] [packages]

Writes models_gen.go in each package with models, declaring the typed
columns of every model (PostCols for Post) and the handles of its relation
fields (PostRels, for fields with a rel tag). A model is a struct that embeds
db.Model, db.Timestamps or db.SoftDeletes, has a TableName method, or has a
//anetos:model comment; //anetos:skip excludes one. Packages default to ./...

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "anetos gen:", err)
		return 1
	}
	changes, err := modelgen.Generate(dir, fs.Args()...)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *check {
		for _, c := range changes {
			fmt.Fprintf(stderr, "anetos gen: %s is out of date\n", rel(dir, c.Path))
		}
		if len(changes) > 0 {
			fmt.Fprintln(stderr, "anetos gen: run `go tool anetos gen` and commit the result")
			return 1
		}
		return 0
	}
	if err := modelgen.Apply(changes); err != nil {
		fmt.Fprintln(stderr, "anetos gen:", err)
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
