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
)

// Commands lists the command names [Runner.Command] handles.
var Commands = []string{"migrate", "migrate:rollback", "migrate:reset", "migrate:fresh", "migrate:status", "db:seed"}

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
//
// In production, rollback, reset and db:seed also need --force. Flags a
// command doesn't take are errors; -h prints a command's flags.
//
// The app binary's command framework (roadmap F11) will register these as
// regular commands.
func (r *Runner) Command(ctx context.Context, args []string, out io.Writer) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	name := args[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	var step *int
	var force, seed *bool
	var seeder *string
	switch name {
	case "migrate":
		seed = fs.Bool("seed", false, "run seeders after migrating")
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
	default:
		return false, nil
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return true, err
	}
	if fs.NArg() > 0 {
		return true, fmt.Errorf("%s: unexpected arguments %v", name, fs.Args())
	}
	production := r.env != anetos.Development && r.env != anetos.Testing && r.env != anetos.Staging
	if force != nil && production && !*force {
		return true, fmt.Errorf("%s changes data in production (APP_ENV=%s); run it with --force if you mean it", name, cmpEnv(r.env))
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
