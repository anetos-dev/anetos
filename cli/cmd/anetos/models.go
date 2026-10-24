// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"anetos.dev/anetos/cli/internal/modelgen"
)

const modelListUsage = `Usage: anetos model:list [--json] [packages]

Lists the project's models, read from the code as anetos generate reads
them (the app needn't run): each model's table, its columns (the column,
the typed column's field in <Model>Cols, the Go type) and its relations.
Packages default to ./... --json prints them as JSON.
`

// modelList runs anetos model:list.
func modelList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos model:list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the models as JSON")
	pos, code := parse(fs, args, stderr, modelListUsage)
	if code >= 0 {
		return code
	}
	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(stderr, "anetos model:list:", err)
		return 1
	}
	models, err := modelgen.List(root, pos...)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if len(models) == 0 {
			return 1
		}
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if encErr := enc.Encode(models); encErr != nil {
			fmt.Fprintln(stderr, "anetos model:list:", encErr)
			return 1
		}
		if err != nil {
			return 1 // the models of the packages that load, and the errors on stderr
		}
		return 0
	}
	if len(models) == 0 {
		fmt.Fprintln(stdout, "No models.")
		return 0
	}
	for i, m := range models {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		table := m.Table
		if m.TableMethod && table == "" {
			table = "(its TableName method's)"
		}
		fmt.Fprintf(stdout, "%s  table %s  (%s)\n", m.Name, table, m.File)
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		for _, c := range m.Columns {
			typ := c.Type
			if c.JSON {
				typ += " (json)"
			}
			fmt.Fprintf(tw, "  %s\t%sCols.%s\t%s\n", c.Name, m.Name, c.Field, typ)
		}
		_ = tw.Flush()
		for _, r := range m.Relations {
			fmt.Fprintf(stdout, "  %s: %s %s (%sRels.%s)\n", r.Field, strings.ReplaceAll(r.Kind, "_", " "), r.Model, m.Name, r.Field)
		}
	}
	if err != nil {
		return 1
	}
	return 0
}
