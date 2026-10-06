// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
)

// Config selects and configures the app's cache.
type Config struct {
	// Store is the store's driver: memory, database, or one passed to
	// ForApp (redis). CACHE_STORE, default memory.
	Store string `env:"CACHE_STORE" default:"memory"`
	// Prefix starts every key, so apps (and, in Redis, sessions and
	// queues) can share a store: cache:clear removes only its keys.
	// CACHE_PREFIX, default APP_NAME followed by ":cache:" ("blog:cache:").
	Prefix string `env:"CACHE_PREFIX"`
	// Table is the database store's table. CACHE_TABLE, default cache.
	Table string `env:"CACHE_TABLE" default:"cache"`
}

// LoadConfig reads the CACHE_* settings.
func LoadConfig(src config.Source) (Config, error) {
	return config.Get[Config](src)
}

// Driver opens a store for [ForApp]. The memory and database drivers are
// built in; driver modules provide others (redis.CacheDriver()).
type Driver struct {
	// Name is the value of CACHE_STORE that selects the driver.
	Name string
	// Open returns the store for the app. It may add providers to the app,
	// for example to check a server when the app boots.
	Open func(app *anetos.App, cfg Config) (Store, error)
}

// MemoryDriver is the memory store's driver (CACHE_STORE=memory).
func MemoryDriver() Driver {
	return Driver{Name: "memory", Open: func(app *anetos.App, _ Config) (Store, error) {
		s := NewMemoryStore()
		s.now = app.Now // expiry on the app's clock, which tests can move
		return s, nil
	}}
}

// ForApp sets up the app's cache from the CACHE_* settings: it opens the
// store with the driver CACHE_STORE names (memory and database are
// built in; pass others, such as redis.CacheDriver()), makes the cache
// available in every context the app creates (for [Get], [Set],
// [Remember], …) and to [anetos.Resolve], closes the store at shutdown,
// and adds the cache:clear command.
//
//	c, err := cache.ForApp(app, redis.CacheDriver())
//
// The database store needs db.Connect first, and its table from
// [Migrations].
func ForApp(app *anetos.App, drivers ...Driver) (*Cache, error) {
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	all := append([]Driver{MemoryDriver(), DatabaseDriver()}, drivers...)
	i := slices.IndexFunc(all, func(d Driver) bool { return d.Name == cfg.Store })
	if i < 0 {
		names := make([]string, len(all))
		for j, d := range all {
			names[j] = d.Name
		}
		return nil, fmt.Errorf("cache: CACHE_STORE is %q, but the drivers are [%s]; pass its driver to cache.ForApp (redis.CacheDriver() from drivers/redis)", cfg.Store, strings.Join(names, ", "))
	}
	store, err := all[i].Open(app, cfg)
	if err != nil {
		return nil, fmt.Errorf("cache: open the %s store: %w", cfg.Store, err)
	}
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = app.Config().Name + ":cache:"
	}
	c := New(store, prefix)
	c.log = app.Logger().With("component", "cache")
	if err := app.AddCommand(cmd.Command{
		Name:        "cache:clear",
		Description: "Remove every item from the cache",
		Run: func(ctx context.Context, args *cmd.Args) error {
			if len(args.Args) > 0 {
				return cmd.Usagef("cache:clear takes no arguments")
			}
			if err := store.Flush(ctx, c.prefix); err != nil {
				return err
			}
			_, err := fmt.Fprintf(args.Stdout, "Cleared the %s cache (keys starting with %q).\n", cfg.Store, c.prefix)
			return err
		},
	}); err != nil {
		return nil, errors.Join(err, store.Close())
	}
	if cfg.Store == "memory" {
		env := app.Config().Env
		app.AddCheck(anetos.Check{Name: "cache", Run: func(context.Context) []anetos.Finding {
			if !env.Deployed() {
				return nil
			}
			return []anetos.Finding{{Severity: anetos.Note, Message: "CACHE_STORE=memory: each instance of the app has its own cache, rate limits and locks (cache.WithLock); with more than one instance, use redis or database"}}
		}})
	}
	app.AddContextValue(cacheKey{}, c)
	anetos.Provide(app, c)
	app.OnShutdown("cache", func(context.Context) error { return store.Close() })
	return c, nil
}
