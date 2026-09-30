// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"anetos.dev/anetos/cli/internal/modelgen"
	"anetos.dev/anetos/cli/internal/scaffold"
)

// makeCmd runs make:handler, make:model, make:migration and
// make:middleware.
func makeCmd(kind string, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos "+kind, flag.ContinueOnError)
	var withMigration *bool
	usage := map[string]string{
		"make:handler":    "Usage: anetos make:handler <Name>\n\nWrites app/handlers/<name>.go with a handler type.\n",
		"make:model":      "Usage: anetos make:model <Name> [--migration]\n\nWrites app/models/<name>.go with a model embedding db.Model, and its typed\ncolumns (anetos gen).\n",
		"make:migration":  "Usage: anetos make:migration <name>\n\nWrites database/migrations/<timestamp>_<name>.go. create_posts_table\ncreates a table; add_x_to_posts_table alters one.\n",
		"make:middleware": "Usage: anetos make:middleware <Name>\n\nWrites app/middleware/<name>.go with a middleware function.\n",
	}[kind]
	if kind == "make:model" {
		withMigration = fs.Bool("migration", false, "also write a migration creating the table")
	}
	pos, code := parse(fs, args, stderr, usage)
	if code >= 0 {
		return code
	}
	if len(pos) != 1 {
		fs.Usage()
		return 2
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "anetos:", err)
		return 1
	}
	root, err := scaffold.FindRoot(wd)
	if err != nil {
		fmt.Fprintln(stderr, "anetos:", err)
		return 1
	}
	var created []string
	var path string
	switch kind {
	case "make:handler":
		path, err = scaffold.MakeHandler(root, pos[0])
	case "make:middleware":
		path, err = scaffold.MakeMiddleware(root, pos[0])
	case "make:migration":
		path, err = scaffold.MakeMigration(root, pos[0], time.Now())
	case "make:model":
		path, err = scaffold.MakeModel(root, pos[0])
		if err == nil && *withMigration {
			created = append(created, path)
			path, err = scaffold.MakeMigration(root, "create_"+scaffold.ModelTable(pos[0])+"_table", time.Now())
		}
	}
	if path != "" && err == nil {
		created = append(created, path)
	}
	for _, p := range created {
		fmt.Fprintln(stdout, "created", p)
	}
	if err != nil {
		fmt.Fprintf(stderr, "anetos %s: %v\n", kind, err)
		return 1
	}
	if kind == "make:model" {
		changes, err := modelgen.Generate(root, "./app/models")
		if err == nil {
			err = modelgen.Apply(changes)
		}
		if err != nil {
			fmt.Fprintf(stderr, "anetos %s: generating columns: %v\n", kind, err)
			return 1
		}
		for _, c := range changes {
			fmt.Fprintln(stdout, "wrote", rel(root, c.Path))
		}
	}
	return 0
}
