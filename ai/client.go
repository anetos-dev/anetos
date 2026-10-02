// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

// Client is a provider with the app's defaults (model, length, timeout)
// and logger. [ForApp] makes the app's; [New] makes one by hand. Calls
// find it in their context ([WithClient], [From]), or take it from
// [Using]. A Client is safe for concurrent use.
type Client struct {
	mu       sync.RWMutex
	provider Provider
	defaults []Option
	log      *slog.Logger
	units    func(context.Context, anetos.Unit) (context.Context, func())
}

// New returns a client for p; defaults ([Model], [MaxTokens],
// [Timeout]…) apply to every call before the call's own options. It
// logs to slog's default logger (the one current when it logs).
func New(p Provider, defaults ...Option) *Client {
	return &Client{provider: p, defaults: slices.Clip(defaults)}
}

// Provider returns the client's provider. A driver's provider type gives
// access to its official SDK client (see the driver's documentation).
func (c *Client) Provider() Provider {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.provider
}

// Fake replaces the client's provider with a [Fake] answering with
// replies (one per request, in order), and returns it; if the provider
// is already a Fake, it adds replies to it. For tests: anetostest.FakeAI
// calls it.
func (c *Client) Fake(replies ...FakeReply) *Fake {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.provider.(*Fake)
	if !ok {
		f = NewFake()
		c.provider = f
	}
	f.Add(replies...)
	return f
}

type clientKey struct{}

// WithClient returns ctx with c, for [Generate], [Stream],
// [GenerateObject] and agents. [ForApp] makes the app's client available
// in every context the app creates.
func WithClient(ctx context.Context, c *Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

// ErrNoClient is returned when a call's context has no client and the
// call has no [Using] option.
var ErrNoClient = errors.New("ai: no AI client in the context: call ai.ForApp at startup, or ai.WithClient")

// From returns the client in ctx.
func From(ctx context.Context) (*Client, error) {
	if c, ok := ctx.Value(clientKey{}).(*Client); ok {
		return c, nil
	}
	return nil, ErrNoClient
}

// Config selects and configures the app's AI provider.
type Config struct {
	// Provider is the driver that calls the models: fake (scripted
	// replies, for tests), or one passed to ForApp. AI_PROVIDER,
	// required.
	Provider string `env:"AI_PROVIDER"`
	// Model is the default model; empty means the provider's own
	// default. AI_MODEL.
	Model string `env:"AI_MODEL"`
	// MaxTokens bounds every answer's length, unless a call sets
	// [MaxTokens]. AI_MAX_TOKENS, default 4096.
	MaxTokens int `env:"AI_MAX_TOKENS" default:"4096"`
	// Timeout bounds each request to the model, unless a call sets
	// [Timeout]. AI_TIMEOUT, default 10m (the official SDKs' default:
	// long answers and reasoning models take minutes).
	Timeout time.Duration `env:"AI_TIMEOUT" default:"10m"`
}

// LoadConfig reads the AI_* settings.
func LoadConfig(src config.Source) (Config, error) {
	cfg, err := config.Get[Config](src)
	if err != nil {
		return cfg, err
	}
	var errs []error
	if cfg.MaxTokens < 1 {
		errs = append(errs, fmt.Errorf("AI_MAX_TOKENS is %d: it must be at least 1", cfg.MaxTokens))
	}
	if cfg.Timeout <= 0 {
		errs = append(errs, fmt.Errorf("AI_TIMEOUT is %s: it must be positive", cfg.Timeout))
	}
	return cfg, errors.Join(errs...)
}

// Driver opens a provider for [ForApp]. The fake driver is built in;
// driver modules provide the others.
type Driver struct {
	// Name is the value of AI_PROVIDER that selects the driver.
	Name string
	// Open returns the provider for the app; it reads its own settings
	// (an API key) from app.Source(). If the provider implements
	// io.Closer, it is closed when the app shuts down.
	Open func(app *anetos.App, cfg Config) (Provider, error)
}

// FakeDriver is the [Fake] provider (AI_PROVIDER=fake): it answers
// with scripted replies and never calls a model. anetostest sets it for
// every test.
func FakeDriver() Driver {
	return Driver{Name: "fake", Open: func(*anetos.App, Config) (Provider, error) { return NewFake(), nil }}
}

// ForApp sets up the app's AI client from the AI_* settings: it opens
// the provider AI_PROVIDER names (fake is built in; driver modules
// provide the others, passed here) and makes the client available in
// every context the app creates. Each tool call is a unit of work
// (anetos.Unit, kind "tool"), so N+1 detection and the app's other unit
// wrappers see it.
//
//	client, err := ai.ForApp(app) // AI_PROVIDER; pass providers' drivers here
func ForApp(app *anetos.App, drivers ...Driver) (*Client, error) {
	if _, err := anetos.Resolve[*Client](app); err == nil {
		return nil, errors.New("ai: ForApp called twice for one app")
	}
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	all := append([]Driver{FakeDriver()}, drivers...)
	names := make([]string, len(all))
	for i, d := range all {
		names[i] = d.Name
	}
	if cfg.Provider == "" {
		return nil, fmt.Errorf("ai: AI_PROVIDER isn't set: set it to one of [%s]", strings.Join(names, ", "))
	}
	i := slices.IndexFunc(all, func(d Driver) bool { return d.Name == cfg.Provider })
	if i < 0 {
		return nil, fmt.Errorf("ai: AI_PROVIDER is %q, but the drivers are [%s]: check its spelling, or pass its driver to ai.ForApp", cfg.Provider, strings.Join(names, ", "))
	}
	p, err := all[i].Open(app, cfg)
	if err != nil {
		return nil, fmt.Errorf("ai: open the %s provider: %w", cfg.Provider, err)
	}
	defaults := []Option{MaxTokens(cfg.MaxTokens), Timeout(cfg.Timeout)}
	if cfg.Model != "" {
		defaults = append(defaults, Model(cfg.Model))
	}
	c := New(p, defaults...)
	c.log = app.Logger().With("component", "ai")
	c.units = app.StartUnit
	if cfg.Provider == "fake" && app.Config().Env.IsProduction() {
		c.log.Warn("ai: AI_PROVIDER is fake in production: models are never called")
	}
	if closer, ok := p.(io.Closer); ok {
		app.OnShutdown("ai", func(context.Context) error { return closer.Close() })
	}
	app.AddContextValue(clientKey{}, c)
	anetos.Provide(app, c)
	return c, nil
}
