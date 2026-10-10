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
// and logger. [New] makes the app's; [NewWithProvider] makes one by hand. Calls
// find it in their context ([WithClient], [From]), or take it from
// [Using]. A Client is safe for concurrent use.
type Client struct {
	mu       sync.RWMutex
	provider Provider
	defaults []Option
	log      *slog.Logger
	units    func(context.Context, anetos.Operation) (context.Context, func())
	usage    *UsageConfig // TrackUsage
	// embed is the embeddings provider when it isn't provider
	// (AI_EMBEDDING_PROVIDER); embedModel is AI_EMBEDDING_MODEL.
	embed      Embedder
	embedModel string
}

// NewWithProvider returns a client for p; defaults ([Model], [MaxTokens],
// [Timeout]…) apply to every call before the call's own options. It
// logs to slog's default logger (the one current when it logs).
func NewWithProvider(p Provider, defaults ...Option) *Client {
	return &Client{provider: p, defaults: slices.Clip(defaults)}
}

// Provider returns the client's provider. A driver's provider type gives
// access to its official SDK client (see the driver's documentation).
func (c *Client) Provider() Provider {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.provider
}

// Fake replaces the client's provider, and its embeddings provider, with
// a [Fake] answering with replies (one per request, in order), and
// returns it; if the provider is already a Fake, it adds replies to it.
// For tests: anetostest.FakeAI calls it.
func (c *Client) Fake(replies ...FakeReply) *Fake {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := c.provider.(*Fake)
	if !ok {
		f = NewFake()
		c.provider = f
	}
	if c.embed != nil {
		c.embed = f
	}
	f.Add(replies...)
	return f
}

// SetEmbedder sets the client's embeddings provider and model ([Embed]),
// for a client made with [NewWithProvider]; by default it is the provider, if it
// has embeddings. [New] sets them from AI_EMBEDDING_PROVIDER and
// AI_EMBEDDING_MODEL.
func (c *Client) SetEmbedder(e Embedder, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.embed, c.embedModel = e, model
}

type clientKey struct{}

// WithClient returns ctx with c, for [Generate], [Stream],
// [GenerateObject] and agents. [New] makes the app's client available
// in every context the app creates.
func WithClient(ctx context.Context, c *Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

// ErrNoClient is returned when a call's context has no client and the
// call has no [Using] option.
var ErrNoClient = errors.New("ai: no AI client in the context: call ai.New at startup, or ai.WithClient")

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
	// replies, for tests), or one passed to New. AI_PROVIDER,
	// required.
	Provider string `env:"AI_PROVIDER"`
	// Model is the default model, by the provider's name for it; the
	// provider drivers require it (the fake doesn't). AI_MODEL.
	Model string `env:"AI_MODEL"`
	// MaxTokens bounds every answer's length, unless a call sets
	// [MaxTokens]. AI_MAX_TOKENS, default 4096.
	MaxTokens int `env:"AI_MAX_TOKENS" default:"4096"`
	// Timeout bounds each request to the model, unless a call sets
	// [Timeout]. AI_TIMEOUT, default 10m (the official SDKs' default:
	// long answers and reasoning models take minutes).
	Timeout time.Duration `env:"AI_TIMEOUT" default:"10m"`
	// QueueTimeout bounds a queued reply ([Conversation.QueueReply]):
	// all its requests and tool calls. AI_QUEUE_TIMEOUT, default 15m. The
	// queue's workers wait this long, plus a margin, before taking back a
	// job of any type whose worker died (the lease is the longest
	// timeout).
	QueueTimeout time.Duration `env:"AI_QUEUE_TIMEOUT" default:"15m"`
	// EmbeddingProvider is the driver that makes embeddings ([Embed]),
	// when it isn't AI_PROVIDER's (Anthropic has none): openai, gemini,
	// openai-compatible, or fake. AI_EMBEDDING_PROVIDER, default
	// AI_PROVIDER.
	EmbeddingProvider string `env:"AI_EMBEDDING_PROVIDER"`
	// EmbeddingModel is the embedding model, by the provider's name for
	// it (text-embedding-3-small, gemini-embedding-001); embeddings
	// record it, and searches use only its own. AI_EMBEDDING_MODEL,
	// required for embeddings except with the fake (default
	// fake-embedding).
	EmbeddingModel string `env:"AI_EMBEDDING_MODEL"`
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
	if cfg.QueueTimeout <= 0 {
		errs = append(errs, fmt.Errorf("AI_QUEUE_TIMEOUT is %s: it must be positive", cfg.QueueTimeout))
	}
	return cfg, errors.Join(errs...)
}

// Driver opens a provider for [New]. The fake driver is built in;
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

// New sets up the app's AI client from the AI_* settings: it opens
// the provider AI_PROVIDER names (fake is built in; driver modules
// provide the others, passed here) and makes the client available in
// every context the app creates. Each tool call is an operation
// (anetos.Operation, kind "tool"), so N+1 detection and the app's other
// operation wrappers see it.
//
//	client, err := ai.New(app) // AI_PROVIDER; pass providers' drivers here
func New(app *anetos.App, drivers ...Driver) (*Client, error) {
	if _, err := anetos.Resolve[*Client](app); err == nil {
		return nil, errors.New("ai: New called twice for one app")
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
	find := func(setting, name string) (Driver, error) {
		i := slices.IndexFunc(all, func(d Driver) bool { return d.Name == name })
		if i < 0 {
			return Driver{}, fmt.Errorf("ai: %s is %q, but the drivers are [%s]: check its spelling, or pass its driver to ai.New", setting, name, strings.Join(names, ", "))
		}
		return all[i], nil
	}
	driver, err := find("AI_PROVIDER", cfg.Provider)
	if err != nil {
		return nil, err
	}
	var embed Driver
	if cfg.EmbeddingProvider != "" {
		if embed, err = find("AI_EMBEDDING_PROVIDER", cfg.EmbeddingProvider); err != nil {
			return nil, err
		}
		if cfg.EmbeddingModel == "" && embed.Name != "fake" {
			return nil, fmt.Errorf("ai: AI_EMBEDDING_PROVIDER is %s, but AI_EMBEDDING_MODEL isn't set: set it to the embedding model's name", embed.Name)
		}
		if embed.Name == cfg.Provider {
			embed = Driver{} // the provider's own
		}
	}
	p, err := driver.Open(app, cfg)
	if err != nil {
		return nil, fmt.Errorf("ai: open the %s provider: %w", cfg.Provider, err)
	}
	if closer, ok := p.(io.Closer); ok {
		app.OnShutdown("ai", func(context.Context) error { return closer.Close() })
	}
	var e Embedder
	if embed.Open != nil {
		// The embeddings driver is opened with the embedding model as its
		// model (drivers require one).
		ecfg := cfg
		ecfg.Provider, ecfg.Model = embed.Name, cfg.EmbeddingModel
		ep, err := embed.Open(app, ecfg)
		if err != nil {
			return nil, fmt.Errorf("ai: open the %s embeddings provider: %w", embed.Name, err)
		}
		if closer, ok := ep.(io.Closer); ok {
			app.OnShutdown("ai embeddings", func(context.Context) error { return closer.Close() })
		}
		var ok bool
		if e, ok = ep.(Embedder); !ok {
			return nil, fmt.Errorf("ai: AI_EMBEDDING_PROVIDER is %s, which has no embeddings: choose openai, gemini or openai-compatible", embed.Name)
		}
	}
	defaults := []Option{MaxTokens(cfg.MaxTokens), Timeout(cfg.Timeout)}
	if cfg.Model != "" {
		defaults = append(defaults, Model(cfg.Model))
	}
	c := NewWithProvider(p, defaults...)
	c.embed, c.embedModel = e, cfg.EmbeddingModel
	if c.embedModel == "" {
		if _, fake := c.embed.(*Fake); fake || (c.embed == nil && cfg.Provider == "fake") {
			c.embedModel = "fake-embedding"
		}
	}
	c.log = app.Logger().With("component", "ai")
	c.units = app.StartOperation
	if cfg.Provider == "fake" && app.Config().Env.IsProduction() {
		c.log.Warn("ai: AI_PROVIDER is fake in production: models are never called")
	}
	app.AddContextValue(clientKey{}, c)
	anetos.Provide(app, c)
	return c, nil
}

// ForApp is [New].
//
// Deprecated: Use New; ForApp is removed in v0.6.
//
//go:fix inline
func ForApp(app *anetos.App, drivers ...Driver) (*Client, error) {
	return New(app, drivers...)
}
