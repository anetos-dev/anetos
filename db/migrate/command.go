// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
)

// commandNames are the migration commands ([Runner.AppCommands]).
var commandNames = []string{"migrate", "migrate:rollback", "migrate:reset", "migrate:fresh", "migrate:status", "db:seed", "db:schema", "search:reindex"}

var commandHelp = map[string][2]string{ // usage, description
	"migrate":          {"[--seed [--force]]", "Run pending migrations (then the seeders)"},
	"migrate:rollback": {"[--step=N] [--force]", "Roll back the last batch of migrations, or N batches"},
	"migrate:reset":    {"[--force]", "Roll back every migration"},
	"migrate:fresh":    {"[--seed]", "Drop all tables and migrate again (development and testing only)"},
	"migrate:status":   {"[--json]", "List migrations and whether they ran"},
	"db:seed":          {"[--seeder=NAME] [--force]", "Run the seeders, or one"},
	"db:schema":        {"[--json] [table…]", "Describe the database's tables: columns, indexes, foreign keys"},
	"search:reindex":   {"[table…]", "Rebuild the search indexes (or those of the tables) for DB_SEARCH_LANGUAGE and DB_SEARCH_RANKING"},
}

// AppCommands returns the migration commands as commands of the app
// binary. [New] registers them, so `./app migrate` works with
// app.Execute.
func (r *Runner) AppCommands() []cmd.Command {
	out := make([]cmd.Command, 0, len(commandNames))
	for _, name := range commandNames {
		out = append(out, cmd.Command{
			Name:          name,
			Usage:         commandHelp[name][0],
			Description:   commandHelp[name][1],
			ChangesSchema: name != "db:seed", // they run even when the search indexes are out of date (db:schema describes them then)
			Run: func(ctx context.Context, args *cmd.Args) error {
				_, err := r.command(ctx, append([]string{name}, args.Args...), args.Stdout)
				return err
			},
		})
	}
	return out
}

// command runs a migration command given as command-line arguments,
// printing progress to out; handled is false (doing nothing) when args[0]
// isn't one of commandNames. Flags:
//
//	migrate [--seed]               apply pending migrations (then seed)
//	migrate:rollback [--step=N]    undo the last N batches (default 1)
//	migrate:reset                  undo every migration
//	migrate:fresh [--seed]         drop all tables and migrate (development and testing only)
//	migrate:status [--json]        list migrations and whether they ran
//	db:seed [--seeder=NAME]        run all seeders, or one
//	db:schema [--json] [table…]    describe the tables
//	search:reindex [table…]        rebuild search indexes for the DB_SEARCH_* settings
//
// In production (any APP_ENV but development, testing and staging),
// rollback, reset, db:seed and migrate --seed also need --force. Flags a
// command doesn't take are errors (wrapping cmd.ErrUsage).
func (r *Runner) command(ctx context.Context, args []string, out io.Writer) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	name := args[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var step *int
	var force, seed *bool
	var seeder *string
	var asJSON *bool
	switch name {
	case "migrate":
		seed = fs.Bool("seed", false, "run seeders after migrating")
		force = fs.Bool("force", false, "allow --seed in production")
	case "migrate:rollback":
		step = fs.Int("step", 1, "number of batches to roll back")
		force = fs.Bool("force", false, "allow in production")
	case "migrate:reset":
		force = fs.Bool("force", false, "allow in production")
	case "migrate:fresh":
		seed = fs.Bool("seed", false, "run seeders after migrating")
	case "migrate:status":
		asJSON = fs.Bool("json", false, "print them as JSON")
	case "db:seed":
		seeder = fs.String("seeder", "", "run only this seeder")
		force = fs.Bool("force", false, "allow in production")
	case "db:schema":
		asJSON = fs.Bool("json", false, "print the tables as JSON")
	case "search:reindex":
	default:
		return false, nil
	}
	// Flags may come after the arguments too: db:schema posts --json.
	var positional []string
	for rest := args[1:]; ; {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprintf(out, "Usage: %s %s\n\n%s\n", name, commandHelp[name][0], commandHelp[name][1])
				fs.SetOutput(out)
				fs.PrintDefaults()
				return true, nil
			}
			return true, cmd.Usagef("%w", err)
		}
		if rest = fs.Args(); len(rest) == 0 {
			break
		}
		positional, rest = append(positional, rest[0]), rest[1:]
	}
	if len(positional) > 0 && name != "search:reindex" && name != "db:schema" {
		return true, cmd.Usagef("%s: unexpected arguments %v", name, positional)
	}
	production := r.env != anetos.Development && r.env != anetos.Testing && r.env != anetos.Staging
	if force != nil && production && !*force && (name != "migrate" || *seed) {
		what := name
		if name == "migrate" {
			what = "migrate --seed" // migrating alone is the normal deploy step
		}
		return true, fmt.Errorf("%s changes data in production (APP_ENV=%s); run it with --force if you mean it", what, cmpEnv(r.env))
	}

	var results []Result
	switch name {
	case "migrate", "migrate:fresh":
		if name == "migrate" {
			results, err = r.Up(ctx)
		} else {
			results, err = r.Fresh(ctx)
			if err != nil && len(results) == 0 {
				return true, err
			}
			fmt.Fprintln(out, "Dropped all tables.")
		}
		report(out, "Migrated", results, "Nothing to migrate.", err)
		if err == nil && *seed {
			err = r.seed(ctx, out, "")
		}
	case "migrate:rollback":
		results, err = r.Rollback(ctx, *step)
		report(out, "Rolled back", results, "Nothing to roll back.", err)
	case "migrate:reset":
		results, err = r.Reset(ctx)
		report(out, "Rolled back", results, "Nothing to roll back.", err)
	case "migrate:status":
		err = r.printStatus(ctx, out, *asJSON)
	case "db:schema":
		err = r.printSchema(ctx, out, *asJSON, positional)
	case "db:seed":
		err = r.seed(ctx, out, *seeder)
	case "search:reindex":
		var tables []string
		tables, err = r.Reindex(ctx, positional...)
		for _, t := range tables {
			fmt.Fprintf(out, "Reindexed:   %s\n", t)
		}
		if len(tables) == 0 && err == nil {
			fmt.Fprintln(out, "No search indexes.")
		}
	}
	return true, err
}

func cmpEnv(env anetos.Environment) string {
	if env == "" {
		return "unknown"
	}
	return string(env)
}

func report(out io.Writer, verb string, results []Result, none string, err error) {
	for _, res := range results {
		fmt.Fprintf(out, "%-12s %s %s (%s)\n", verb+":", res.Source, res.ID, res.Took.Round(time.Millisecond))
	}
	if len(results) == 0 && err == nil {
		fmt.Fprintln(out, none)
	}
}

func (r *Runner) seed(ctx context.Context, out io.Writer, name string) error {
	var names []string
	if name != "" {
		names = []string{name}
	} else {
		for _, s := range r.seeders {
			names = append(names, s.Name)
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(out, "No seeders.")
		return nil
	}
	for _, n := range names {
		if err := r.Seed(ctx, n); err != nil {
			return err
		}
		fmt.Fprintf(out, "Seeded:      %s\n", n)
	}
	return nil
}

func (r *Runner) printStatus(ctx context.Context, out io.Writer, asJSON bool) error {
	list, err := r.Status(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		type status struct {
			Source    string     `json:"source"`
			ID        string     `json:"id"`
			Applied   bool       `json:"applied"`
			Batch     int        `json:"batch,omitempty"`
			AppliedAt *time.Time `json:"applied_at,omitempty"`
			Missing   bool       `json:"missing,omitempty"`
		}
		js := make([]status, len(list))
		for i, s := range list {
			js[i] = status{Source: s.Source, ID: s.ID, Applied: s.Applied, Batch: s.Batch, Missing: s.Missing}
			if !s.AppliedAt.IsZero() {
				js[i].AppliedAt = &s.AppliedAt
			}
		}
		return writeJSON(out, js)
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tBATCH\tSOURCE\tMIGRATION")
	for _, s := range list {
		status, batch := "Pending", ""
		if s.Applied {
			status, batch = "Ran", fmt.Sprint(s.Batch)
		}
		id := s.ID
		if s.Missing {
			status = "Missing"
			id += " (applied, but not in any set)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", status, batch, s.Source, id)
	}
	if len(list) == 0 {
		fmt.Fprintln(w, "No migrations.")
	}
	return w.Flush()
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (r *Runner) printSchema(ctx context.Context, out io.Writer, asJSON bool, tables []string) error {
	list, err := r.Inspect(ctx, tables...)
	if err != nil {
		return err
	}
	if asJSON {
		if list == nil {
			list = []TableInfo{}
		}
		return writeJSON(out, list)
	}
	if len(list) == 0 {
		fmt.Fprintln(out, "No tables.")
		return nil
	}
	for i, t := range list {
		if i > 0 {
			fmt.Fprintln(out)
		}
		kind := ""
		if t.Kind != "table" {
			kind = " (" + t.Kind + ")"
		}
		fmt.Fprintf(out, "%s%s\n", t.Name, kind)
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, c := range t.Columns {
			var notes []string
			if c.PrimaryKey {
				notes = append(notes, "primary key")
			}
			if c.Nullable {
				notes = append(notes, "null")
			}
			if c.Default != "" {
				notes = append(notes, "default "+c.Default)
			}
			if c.Identity {
				notes = append(notes, "identity")
			}
			if c.Generated != "" {
				notes = append(notes, "generated "+c.Generated)
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\n", c.Name, c.Type, strings.Join(notes, ", "))
		}
		_ = w.Flush()
		for _, ix := range t.Indexes {
			if ix.Primary {
				continue
			}
			unique := ""
			if ix.Unique {
				unique = " unique"
			}
			where := ""
			if ix.Where != "" {
				where = " where " + ix.Where
			}
			fmt.Fprintf(out, "  index %s (%s)%s%s\n", ix.Name, strings.Join(ix.Columns, ", "), unique, where)
		}
		for _, fk := range t.ForeignKeys {
			fmt.Fprintf(out, "  foreign key (%s) references %s (%s), on delete %s\n",
				strings.Join(fk.Columns, ", "), fk.Table, strings.Join(fk.References, ", "), fk.OnDelete)
		}
	}
	return nil
}
