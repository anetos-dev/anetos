// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
)

// Commands lists the command names [Runner.Command] handles.
var Commands = []string{"migrate", "migrate:rollback", "migrate:reset", "migrate:fresh", "migrate:status", "db:seed", "search:reindex"}

var commandHelp = map[string][2]string{ // usage, description
	"migrate":          {"[--seed [--force]]", "Run pending migrations (then the seeders)"},
	"migrate:rollback": {"[--step=N] [--force]", "Roll back the last batch of migrations, or N batches"},
	"migrate:reset":    {"[--force]", "Roll back every migration"},
	"migrate:fresh":    {"[--seed]", "Drop all tables and migrate again (development and testing only)"},
	"migrate:status":   {"", "List migrations and whether they ran"},
	"db:seed":          {"[--seeder=NAME] [--force]", "Run the seeders, or one"},
	"search:reindex":   {"[table…]", "Rebuild the search indexes (or those of the tables) for DB_SEARCH_LANGUAGE and DB_SEARCH_RANKING"},
}

// AppCommands returns the migration commands as commands of the app
// binary. [New] registers them, so `./app migrate` works with
// app.Execute.
func (r *Runner) AppCommands() []cmd.Command {
	out := make([]cmd.Command, 0, len(Commands))
	for _, name := range Commands {
		out = append(out, cmd.Command{
			Name:          name,
			Usage:         commandHelp[name][0],
			Description:   commandHelp[name][1],
			ChangesSchema: name != "db:seed", // they run even when the search indexes are out of date
			Run: func(ctx context.Context, args *cmd.Args) error {
				_, err := r.Command(ctx, append([]string{name}, args.Args...), args.Stdout)
				return err
			},
		})
	}
	return out
}

// Command runs a migration command given as command-line arguments, such
// as os.Args[1:], printing progress to out. It reports handled=false
// (doing nothing) when args[0] isn't one of [Commands], so a main function
// can fall through to running the app:
//
//	if handled, err := runner.Command(ctx, os.Args[1:], os.Stdout); handled {
//		return err
//	}
//	return app.Run(ctx)
//
// Commands and flags:
//
//	migrate [--seed]               apply pending migrations (then seed)
//	migrate:rollback [--step=N]    undo the last N batches (default 1)
//	migrate:reset                  undo every migration
//	migrate:fresh [--seed]         drop all tables and migrate (development and testing only)
//	migrate:status                 list migrations and whether they ran
//	db:seed [--seeder=NAME]        run all seeders, or one
//	search:reindex [table…]        rebuild search indexes for the SEARCH_* settings
//
// In production (any APP_ENV but development, testing and staging),
// rollback, reset, db:seed and migrate --seed also need --force. Flags a
// command doesn't take are errors (wrapping cmd.ErrUsage); -h prints a
// command's flags.
//
// Apps using app.Execute don't need this: [New] registers the commands
// (see [Runner.AppCommands]).
func (r *Runner) Command(ctx context.Context, args []string, out io.Writer) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	name := args[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var step *int
	var force, seed *bool
	var seeder *string
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
	case "db:seed":
		seeder = fs.String("seeder", "", "run only this seeder")
		force = fs.Bool("force", false, "allow in production")
	case "search:reindex":
	default:
		return false, nil
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(out, "Usage: %s %s\n\n%s\n", name, commandHelp[name][0], commandHelp[name][1])
			fs.SetOutput(out)
			fs.PrintDefaults()
			return true, nil
		}
		return true, cmd.Usagef("%w", err)
	}
	if fs.NArg() > 0 && name != "search:reindex" {
		return true, cmd.Usagef("%s: unexpected arguments %v", name, fs.Args())
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
		err = r.printStatus(ctx, out)
	case "db:seed":
		err = r.seed(ctx, out, *seeder)
	case "search:reindex":
		var tables []string
		tables, err = r.Reindex(ctx, fs.Args()...)
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

func (r *Runner) printStatus(ctx context.Context, out io.Writer) error {
	list, err := r.Status(ctx)
	if err != nil {
		return err
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
