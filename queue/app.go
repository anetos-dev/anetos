// SPDX-License-Identifier: Apache-2.0

package queue

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
)

// Config selects and configures the app's queue.
type Config struct {
	// Driver is where jobs are kept: sync (run at once), memory, database,
	// or one passed to ForApp (redis). QUEUE_DRIVER, default sync.
	Driver string `env:"QUEUE_DRIVER" default:"sync"`
	// Default is the queue jobs go on without [OnQueue], and the one
	// workers take jobs from without [Queues]. QUEUE_DEFAULT, default
	// "default".
	Default string `env:"QUEUE_DEFAULT" default:"default"`
	// Tries is how many times a job runs before it fails for good, unless
	// its type says otherwise ([Tries]). QUEUE_TRIES, default 3.
	Tries int `env:"QUEUE_TRIES" default:"3"`
	// Timeout is how long an attempt may run ([Timeout]). QUEUE_TIMEOUT,
	// default 1m.
	Timeout time.Duration `env:"QUEUE_TIMEOUT" default:"1m"`
	// Backoff is the wait before the first retry, doubling for each
	// later one ([Backoff]). QUEUE_BACKOFF, default 10s.
	Backoff time.Duration `env:"QUEUE_BACKOFF" default:"10s"`
	// MaxBackoff caps the doubling. QUEUE_BACKOFF_MAX, default 10m.
	MaxBackoff time.Duration `env:"QUEUE_BACKOFF_MAX" default:"10m"`
	// Poll is how long an idle worker waits before looking for jobs
	// again. QUEUE_POLL, default 1s.
	Poll time.Duration `env:"QUEUE_POLL" default:"1s"`
	// Table is the database driver's table of jobs. QUEUE_TABLE, default
	// jobs.
	Table string `env:"QUEUE_TABLE" default:"jobs"`
	// FailedTable is the database driver's table of failed jobs.
	// QUEUE_FAILED_TABLE, default failed_jobs.
	FailedTable string `env:"QUEUE_FAILED_TABLE" default:"failed_jobs"`
	// Prefix starts the keys of the Redis driver, so apps (and the cache
	// and sessions) can share a server. QUEUE_PREFIX, default APP_NAME
	// followed by ":queue:" ("blog:queue:").
	Prefix string `env:"QUEUE_PREFIX"`
}

// withDefaults fills in zero fields.
func (c Config) withDefaults() Config {
	d := Config{Driver: "sync", Default: "default", Tries: 3, Timeout: time.Minute, Backoff: 10 * time.Second,
		MaxBackoff: 10 * time.Minute, Poll: time.Second, Table: "jobs", FailedTable: "failed_jobs"}
	if c.Driver == "" {
		c.Driver = d.Driver
	}
	if c.Default == "" {
		c.Default = d.Default
	}
	if c.Tries <= 0 {
		c.Tries = d.Tries
	}
	if c.Timeout <= 0 {
		c.Timeout = d.Timeout
	}
	if c.Backoff <= 0 {
		c.Backoff = d.Backoff
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = d.MaxBackoff
	}
	c.MaxBackoff = max(c.MaxBackoff, c.Backoff)
	if c.Poll <= 0 {
		c.Poll = d.Poll
	}
	if c.Table == "" {
		c.Table = d.Table
	}
	if c.FailedTable == "" {
		c.FailedTable = d.FailedTable
	}
	return c
}

// LoadConfig reads the QUEUE_* settings.
func LoadConfig(src config.Source) (Config, error) {
	cfg, err := config.Get[Config](src)
	if err != nil {
		return cfg, err
	}
	var errs []error
	if err := checkQueue(cfg.Default); err != nil {
		errs = append(errs, fmt.Errorf("QUEUE_DEFAULT: %w", err))
	}
	if cfg.Tries < 1 {
		errs = append(errs, errors.New("QUEUE_TRIES must be at least 1"))
	}
	for name, d := range map[string]time.Duration{"QUEUE_TIMEOUT": cfg.Timeout, "QUEUE_BACKOFF": cfg.Backoff,
		"QUEUE_BACKOFF_MAX": cfg.MaxBackoff, "QUEUE_POLL": cfg.Poll} {
		if d <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive", name))
		}
	}
	return cfg, errors.Join(errs...)
}

// Driver opens a store for [ForApp]. The sync, memory and database
// drivers are built in; driver modules provide others
// (redis.QueueDriver()).
type Driver struct {
	// Name is the value of QUEUE_DRIVER that selects the driver.
	Name string
	// Open returns the store for the app. It may add providers to the
	// app, for example to check a server when the app boots. A nil store
	// runs jobs at once.
	Open func(app *anetos.App, cfg Config) (Store, error)
}

// SyncDriver runs each job at once, in [Dispatch], which returns its
// error (QUEUE_DRIVER=sync): for development and tests. Jobs run once,
// without a delay, and workers do nothing.
func SyncDriver() Driver {
	return Driver{Name: "sync", Open: func(*anetos.App, Config) (Store, error) { return nil, nil }}
}

// MemoryDriver keeps jobs in the process's memory (QUEUE_DRIVER=memory):
// workers in the same process run them, and they are lost when it stops.
// For development and tests.
func MemoryDriver() Driver {
	return Driver{Name: "memory", Open: func(*anetos.App, Config) (Store, error) { return NewMemoryStore(), nil }}
}

// ForApp sets up the app's queue from the QUEUE_* settings: it opens the
// store with the driver QUEUE_DRIVER names (sync, memory and database are
// built in; pass others, such as redis.QueueDriver()), makes the queue
// available in every context the app creates (for [Dispatch]) and to
// [anetos.Resolve], closes the store at shutdown, and adds the
// queue:failed, queue:retry, queue:forget, queue:flush and queue:clear
// commands.
//
//	q, err := queue.ForApp(app, redis.QueueDriver())
//
// The database driver needs db.Connect first, and the tables from
// [Migrations]. Then register the job types ([Register]) and start the
// workers ([Queue.Work]).
func ForApp(app *anetos.App, drivers ...Driver) (*Queue, error) {
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	all := append([]Driver{SyncDriver(), MemoryDriver(), DatabaseDriver()}, drivers...)
	i := slices.IndexFunc(all, func(d Driver) bool { return d.Name == cfg.Driver })
	if i < 0 {
		names := make([]string, len(all))
		for j, d := range all {
			names[j] = d.Name
		}
		return nil, fmt.Errorf("queue: QUEUE_DRIVER is %q, but the drivers are [%s]; pass its driver to queue.ForApp (redis.QueueDriver() from drivers/redis)", cfg.Driver, strings.Join(names, ", "))
	}
	if cfg.Prefix == "" {
		cfg.Prefix = app.Config().Name + ":queue:"
	}
	store, err := all[i].Open(app, cfg)
	if err != nil {
		return nil, fmt.Errorf("queue: open the %s store: %w", cfg.Driver, err)
	}
	q := New(store, cfg, WithLogger(app.Logger().With("component", "queue")))
	q.app = app
	if err := q.addCommands(app); err != nil {
		if store != nil {
			err = errors.Join(err, store.Close())
		}
		return nil, err
	}
	env := app.Config().Env
	app.AddCheck(anetos.Check{Name: "queue", Run: func(context.Context) []anetos.Finding {
		switch {
		case !env.Deployed():
		case cfg.Driver == "memory":
			return []anetos.Finding{{Severity: anetos.Warning, Message: "QUEUE_DRIVER=memory: waiting jobs are lost when the app stops; use database or redis"}}
		case cfg.Driver == "sync":
			return []anetos.Finding{{Severity: anetos.Note, Message: "QUEUE_DRIVER=sync: jobs run at once, in the code that queues them (a request), once and without retries; use database or redis for background jobs"}}
		}
		return nil
	}})
	app.AddContextValue(queueKey{}, q)
	anetos.Provide(app, q)
	if store != nil {
		app.OnShutdown("queue", func(context.Context) error { return store.Close() })
	}
	return q, nil
}

// addCommands adds the queue:* commands.
func (q *Queue) addCommands(app *anetos.App) error {
	production := app.Config().Env.IsProduction()
	needStore := func() error {
		if q.sync {
			return errors.New("the sync driver keeps no jobs (QUEUE_DRIVER=sync)")
		}
		return nil
	}
	cmds := []cmd.Command{{
		Name:        "queue:failed",
		Usage:       "[--limit=N]",
		Description: "List the jobs that failed for good, the latest first",
		Run: func(ctx context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet("queue:failed", flag.ContinueOnError)
			limit := fs.Int("limit", 50, "list at most `N` jobs")
			if err := args.Parse(fs); err != nil {
				return err
			}
			if fs.NArg() > 0 || *limit < 1 {
				return cmd.Usagef("queue:failed takes only --limit=N, with N at least 1")
			}
			if err := needStore(); err != nil {
				return err
			}
			jobs, err := q.store.Failed(ctx, 0, *limit)
			if err != nil {
				return err
			}
			if len(jobs) == 0 {
				_, err := fmt.Fprintln(args.Stdout, "No failed jobs.")
				return err
			}
			tw := tabwriter.NewWriter(args.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tFAILED AT\tQUEUE\tJOB\tATTEMPTS\tERROR")
			for _, j := range jobs {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", j.ID, j.FailedAt.UTC().Format(time.DateTime), j.Queue, j.Job(), j.Attempts, firstLine(j.Error, 80))
			}
			return tw.Flush()
		},
	}, {
		Name:        "queue:retry",
		Usage:       "<id>… | all",
		Description: "Put failed jobs back on their queues",
		Run: func(ctx context.Context, args *cmd.Args) error {
			if len(args.Args) == 0 {
				return cmd.Usagef("queue:retry needs job IDs, or all")
			}
			if err := needStore(); err != nil {
				return err
			}
			ids := args.Args
			if len(ids) == 1 && ids[0] == "all" {
				// The failed jobs now: jobs that fail meanwhile, retried ones
				// included, wait for the next queue:retry.
				all, err := q.allFailed(ctx)
				if err != nil {
					return err
				}
				n := 0
				for _, id := range all {
					ok, err := q.store.Retry(ctx, id)
					if err != nil {
						return err
					}
					if ok {
						n++
					}
				}
				_, err = fmt.Fprintf(args.Stdout, "Put %d failed job(s) back on their queues.\n", n)
				return err
			}
			var missing []string
			for _, id := range ids {
				ok, err := q.store.Retry(ctx, id)
				if err != nil {
					return err
				}
				if !ok {
					missing = append(missing, id)
					continue
				}
				fmt.Fprintf(args.Stdout, "Put job %s back on its queue.\n", id)
			}
			if len(missing) > 0 {
				return fmt.Errorf("no failed job with the ID %s", strings.Join(missing, ", "))
			}
			return nil
		},
	}, {
		Name:        "queue:forget",
		Usage:       "<id>…",
		Description: "Delete failed jobs",
		Run: func(ctx context.Context, args *cmd.Args) error {
			if len(args.Args) == 0 {
				return cmd.Usagef("queue:forget needs job IDs")
			}
			if err := needStore(); err != nil {
				return err
			}
			var missing []string
			for _, id := range args.Args {
				ok, err := q.store.Forget(ctx, id)
				if err != nil {
					return err
				}
				if !ok {
					missing = append(missing, id)
					continue
				}
				fmt.Fprintf(args.Stdout, "Deleted failed job %s.\n", id)
			}
			if len(missing) > 0 {
				return fmt.Errorf("no failed job with the ID %s", strings.Join(missing, ", "))
			}
			return nil
		},
	}, {
		Name:        "queue:flush",
		Usage:       "[--force]",
		Description: "Delete every failed job",
		Run: func(ctx context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet("queue:flush", flag.ContinueOnError)
			force := fs.Bool("force", false, "allow in production")
			if err := args.Parse(fs); err != nil {
				return err
			}
			if fs.NArg() > 0 {
				return cmd.Usagef("queue:flush takes no arguments")
			}
			if production && !*force {
				return errors.New("queue:flush deletes every failed job: add --force to run it in production")
			}
			if err := needStore(); err != nil {
				return err
			}
			n, err := q.store.Flush(ctx)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(args.Stdout, "Deleted %d failed job(s).\n", n)
			return err
		},
	}, {
		Name:        "queue:clear",
		Usage:       "[--force] [queue]",
		Description: "Delete every job waiting on a queue (default QUEUE_DEFAULT)",
		Run: func(ctx context.Context, args *cmd.Args) error {
			fs := flag.NewFlagSet("queue:clear", flag.ContinueOnError)
			force := fs.Bool("force", false, "allow in production")
			if err := args.Parse(fs); err != nil {
				return err
			}
			if fs.NArg() > 1 {
				return cmd.Usagef("queue:clear takes one queue name")
			}
			name := q.cfg.Default
			if fs.NArg() == 1 {
				name = fs.Arg(0)
			}
			if err := checkQueue(name); err != nil {
				return cmd.Usagef("%v", err)
			}
			if production && !*force {
				return fmt.Errorf("queue:clear deletes every job on the %s queue: add --force to run it in production", name)
			}
			if err := needStore(); err != nil {
				return err
			}
			n, err := q.store.Clear(ctx, name)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(args.Stdout, "Deleted %d job(s) from the %s queue.\n", n, name)
			return err
		},
	}}
	for _, c := range cmds {
		if err := app.AddCommand(c); err != nil {
			return err
		}
	}
	return nil
}

// allFailed returns the IDs of the failed jobs. Jobs failing meanwhile
// shift the pages later, so a job may be seen twice (it is listed once);
// jobs removed meanwhile (by queue:forget elsewhere) shift them earlier,
// so a few may be missed.
func (q *Queue) allFailed(ctx context.Context) ([]string, error) {
	const page = 1000
	seen := map[string]bool{}
	var ids []string
	for offset := 0; ; offset += page {
		jobs, err := q.store.Failed(ctx, offset, page)
		if err != nil {
			return nil, err
		}
		for _, j := range jobs {
			if !seen[j.ID] {
				seen[j.ID] = true
				ids = append(ids, j.ID)
			}
		}
		if len(jobs) < page {
			return ids, nil
		}
	}
}

// firstLine returns the first line of s, cut to n characters.
func firstLine(s string, n int) string {
	s, _, _ = strings.Cut(s, "\n")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
