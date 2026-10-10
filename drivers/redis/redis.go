// SPDX-License-Identifier: Apache-2.0

// Package redis connects Anetos apps to Redis (or a compatible server such
// as Valkey), using github.com/redis/go-redis, and provides the Redis
// cache store:
//
//	c, err := cache.New(app, redis.CacheDriver()) // with CACHE_DRIVER=redis
//
// The server is REDIS_URL (default redis://127.0.0.1:6379/0; rediss://
// for TLS, with the password and database in the URL:
// redis://:secret@cache.internal:6379/2). The app shares one client, made
// by [Connect], between the cache and the other features that use Redis.
package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	goredis "github.com/redis/go-redis/v9"
)

// Config holds the Redis settings.
type Config struct {
	// URL is the server's address, with its password and database:
	// redis://user:password@host:6379/0, or rediss:// for TLS. REDIS_URL,
	// default redis://127.0.0.1:6379/0.
	URL string `env:"REDIS_URL" default:"redis://127.0.0.1:6379/0"`
}

// LoadConfig reads the REDIS_* settings.
func LoadConfig(src config.Source) (Config, error) {
	return config.Get[Config](src)
}

// Connect returns the app's Redis client, making it on the first call:
// it connects to REDIS_URL, provides the client as a *redis.Client
// service (of go-redis), pings the server when the app boots (right away
// if it already has) and closes the client at shutdown.
//
//	client, err := redis.Connect(ctx, app)
func Connect(ctx context.Context, app *anetos.App) (*goredis.Client, error) {
	if c, ok := anetos.Lookup[*goredis.Client](app); ok {
		return c, nil
	}
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	opts, err := goredis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("redis: REDIS_URL: %w", err)
	}
	client := goredis.NewClient(opts)
	check := &connCheck{client: client, addr: opts.Addr}
	if app.Booted() {
		if err := check.Boot(ctx, app); err != nil {
			return nil, errors.Join(err, client.Close())
		}
	} else {
		app.Use(check) // checked when the app boots, so help works without Redis
	}
	anetos.Provide(app, client)
	app.OnShutdown("redis", func(context.Context) error { return client.Close() })
	return client, nil
}

type connCheck struct {
	client *goredis.Client
	addr   string
}

func (c *connCheck) Name() string               { return "redis.Connect(" + c.addr + ")" }
func (c *connCheck) Register(*anetos.App) error { return nil }
func (c *connCheck) Boot(ctx context.Context, _ *anetos.App) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis: connect to %s: %w", c.addr, err)
	}
	return nil
}
