// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"reflect"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
)

// pruneBatch is how many rows one transaction of PruneAllTrashed deletes.
const pruneBatch = 1000

// pruner deletes one model's long-deleted rows.
type pruner struct {
	table string
	after time.Duration
	// run deletes (or, with dryRun, counts) the rows deleted before cutoff.
	run func(ctx context.Context, cutoff time.Time, dryRun bool) (int64, error)
}

// pruners are the models registered with Prunable in an app.
type pruners struct {
	mu   sync.Mutex
	list []pruner
}

type prunersKey struct{}

// Prunable registers model T, which must embed [SoftDeletes]: its rows
// soft-deleted more than after ago are deleted for good by the
// db:prune-trashed command, or by [PruneAllTrashed] (in a scheduled task,
// say). The first call adds the command to the app.
//
//	err := db.Prunable[models.Post](app, 90*24*time.Hour)
//
// Rows are deleted in batches of 1,000, each in its own transaction, as
// [Q.ForceDelete] does: a table watched by the audit log gets a bulk
// entry per batch. Related rows go only where foreign keys cascade.
func Prunable[T any](app *anetos.App, after time.Duration) error {
	m, err := metaOf(reflect.TypeFor[T]())
	if err != nil {
		return err
	}
	if m.deletedAt < 0 {
		return fmt.Errorf("db: Prunable: %s doesn't use SoftDeletes", m.typ)
	}
	if m.pk < 0 {
		return fmt.Errorf("db: Prunable: %s has no primary key", m.typ)
	}
	if after <= 0 {
		return errors.New("db: Prunable needs a positive duration")
	}
	ps, ok := anetos.Lookup[*pruners](app)
	if !ok {
		ps = &pruners{}
		anetos.Provide(app, ps)
		app.AddContextValue(prunersKey{}, ps)
		if err := app.AddCommand(pruneCommand()); err != nil {
			return err
		}
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, p := range ps.list {
		if p.table == m.table {
			return fmt.Errorf("db: Prunable: %s is registered twice", m.table)
		}
	}
	ps.list = append(ps.list, pruner{table: m.table, after: after, run: pruneModel[T]})
	return nil
}

// pruneModel deletes T's rows soft-deleted before cutoff, a batch per
// transaction, and returns how many.
func pruneModel[T any](ctx context.Context, cutoff time.Time, dryRun bool) (int64, error) {
	m, err := metaOf(reflect.TypeFor[T]())
	if err != nil {
		return 0, err
	}
	old := Query[T](ctx).OnlyTrashed().Where(C(m.table + "." + m.cols[m.deletedAt].name).Lt(cutoff))
	if dryRun {
		return old.Count()
	}
	var total int64
	for {
		rows, err := old.OrderBy(C(m.table + "." + m.cols[m.pk].name).Asc()).Limit(pruneBatch).Get()
		if err != nil {
			return total, err
		}
		if len(rows) == 0 {
			return total, nil
		}
		keys := make([]any, len(rows))
		for i := range rows {
			if keys[i], err = rowKey(reflect.ValueOf(&rows[i]).Elem(), m); err != nil {
				return total, err
			}
		}
		var n int64
		err = Tx(ctx, func(ctx context.Context) error {
			var err error
			n, err = old.WithContext(ctx).WhereKeys(keys...).ForceDelete()
			return err
		})
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, fmt.Errorf("db: prune %s: %d rows were selected and none deleted (a trigger or a policy?)", m.table, len(rows))
		}
		if len(rows) < pruneBatch {
			return total, nil
		}
	}
}

// Pruned is what [PruneAllTrashed] did to one table.
type Pruned struct {
	// Table is the model's table.
	Table string
	// Rows is how many rows were deleted (or, in a dry run, would be).
	Rows int64
}

// PruneAllTrashed deletes for good the rows of every model registered with
// [Prunable] that were soft-deleted longer ago than the model's
// duration, and reports how many per table. Run it from a scheduled task:
//
//	err := sched.Add(schedule.Daily(), "db:prune-trashed", func(ctx context.Context) error {
//		_, err := db.PruneAllTrashed(ctx)
//		return err
//	})
//
// It stops at the first error, returning what it did so far.
func PruneAllTrashed(ctx context.Context) ([]Pruned, error) {
	return pruneAll(ctx, false)
}

func pruneAll(ctx context.Context, dryRun bool) ([]Pruned, error) {
	ps, _ := ctx.Value(prunersKey{}).(*pruners)
	if ps == nil {
		return nil, errors.New("db: no model is registered with Prunable in this app")
	}
	ps.mu.Lock()
	list := append([]pruner(nil), ps.list...)
	ps.mu.Unlock()
	t := now(ctx)
	out := make([]Pruned, 0, len(list))
	for _, p := range list {
		n, err := p.run(ctx, t.Add(-p.after), dryRun)
		out = append(out, Pruned{p.table, n})
		if err != nil {
			return out, fmt.Errorf("db: prune %s: %w", p.table, err)
		}
	}
	return out, nil
}

func pruneCommand() cmd.Command {
	return cmd.Command{
		Name:        "db:prune-trashed",
		Usage:       "[--dry-run]",
		Description: "Delete for good the rows soft-deleted longer ago than db.Prunable allows",
		Run: func(ctx context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet("db:prune-trashed", flag.ContinueOnError)
			dry := fs.Bool("dry-run", false, "count the rows, delete nothing")
			if err := args.Parse(fs); err != nil {
				return err
			}
			if fs.NArg() > 0 {
				return cmd.Usagef("db:prune-trashed takes no arguments")
			}
			done, err := pruneAll(ctx, *dry)
			verb := "Deleted"
			if *dry {
				verb = "Would delete"
			}
			for _, p := range done {
				fmt.Fprintf(args.Stdout, "%s %d rows from %s.\n", verb, p.Rows, p.Table)
			}
			return err
		},
	}
}

// PruneTrashed is [Prunable].
//
// Deprecated: Use Prunable; PruneTrashed is removed in v0.6.
//
//go:fix inline
func PruneTrashed[T any](app *anetos.App, after time.Duration) error { return Prunable[T](app, after) }
