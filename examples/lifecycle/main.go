// SPDX-License-Identifier: Apache-2.0

// Command lifecycle is a minimal Anetos application: typed configuration, a
// provider, a supervised background task and graceful shutdown.
//
//	HEARTBEAT_INTERVAL=500ms APP_ENV=development go run ./examples/lifecycle
//
// Press Ctrl+C to watch the ordered shutdown.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/supervisor"
)

// region: config
// HeartbeatConfig is this app's own configuration, bound from the environment.
type HeartbeatConfig struct {
	Interval time.Duration `env:"HEARTBEAT_INTERVAL" default:"2s"`
	FailAt   int           `env:"HEARTBEAT_FAIL_AT"` // simulate a crash after N beats (0 = never)
}

func (c HeartbeatConfig) Validate() error {
	if c.Interval < 10*time.Millisecond {
		return errors.New("HEARTBEAT_INTERVAL must be at least 10ms")
	}
	return nil
}

// endregion

// region: provider
// Counter is a service shared through the container.
type Counter struct{ n atomic.Int64 }

// CounterProvider provides a *Counter and reports its total at shutdown.
type CounterProvider struct{}

func (CounterProvider) Name() string { return "counter" }

func (CounterProvider) Register(app *anetos.App) error {
	anetos.Provide(app, &Counter{})
	return nil
}

func (CounterProvider) Boot(_ context.Context, app *anetos.App) error {
	counter := anetos.MustResolve[*Counter](app)
	app.OnShutdown("counter-report", func(context.Context) error {
		app.Logger().Info("total heartbeats", "count", counter.n.Load())
		return nil
	})
	return nil
}

// endregion

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	app, err := anetos.New()
	if err != nil {
		return err
	}
	cfg, err := config.Get[HeartbeatConfig](app.Source())
	if err != nil {
		return err
	}

	app.Use(CounterProvider{})

	// region: task
	err = app.Go("heartbeat", func(ctx context.Context) error {
		counter := anetos.MustResolve[*Counter](app)
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				n := counter.n.Add(1)
				app.Logger().Info("beat", "n", n)
				if cfg.FailAt > 0 && n%int64(cfg.FailAt) == 0 {
					return errors.New("simulated failure")
				}
			}
		}
	},
		anetos.ProcessTypes("worker"),
		anetos.Restart(supervisor.RestartOnFailure),
		anetos.Backoff(supervisor.Backoff{Initial: time.Second, Max: 10 * time.Second}),
	)
	if err != nil {
		return err
	}
	// endregion

	// region: run
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx)
	// endregion
}
