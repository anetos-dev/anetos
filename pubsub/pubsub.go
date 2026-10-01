// SPDX-License-Identifier: Apache-2.0

package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
)

// Config selects and configures the app's broker.
type Config struct {
	// Driver is the broker: memory (in the process), or one passed to
	// ForApp (redis, gcp). PUBSUB_DRIVER, default memory.
	Driver string `env:"PUBSUB_DRIVER" default:"memory"`
	// Prefix starts the names of topics in brokers that share a namespace
	// with other data (Redis keys). PUBSUB_PREFIX, default none: topics
	// are shared with the other apps using the broker.
	Prefix string `env:"PUBSUB_PREFIX"`
}

// LoadConfig reads the PUBSUB_* settings.
func LoadConfig(src config.Source) (Config, error) {
	return config.Get[Config](src)
}

// Driver opens a broker for [ForApp]. The memory driver is built in;
// driver modules provide others (redis.PubSubDriver(),
// gcppubsub.Driver()).
type Driver struct {
	// Name is the value of PUBSUB_DRIVER that selects the driver.
	Name string
	// Open returns the broker for the app. It may add providers to the
	// app, for example to check a server when the app boots.
	Open func(app *anetos.App, cfg Config) (Broker, error)
}

// MemoryDriver is the memory broker's driver (PUBSUB_DRIVER=memory): for
// development and tests, in one process.
func MemoryDriver() Driver {
	return Driver{Name: "memory", Open: func(*anetos.App, Config) (Broker, error) { return NewMemoryBroker(), nil }}
}

// PubSub publishes messages to a [Broker] and runs listeners of its
// subscriptions. Create it with [ForApp] (or [New]).
type PubSub struct {
	broker Broker
	driver string // PUBSUB_DRIVER, with ForApp
	app    *anetos.App
	name   string // the app's name, for default subscription names
	log    *slog.Logger

	mu        sync.Mutex
	listeners []*listener
	prepared  bool // the app booted: new listeners prepare at once
}

// Option configures a [PubSub] made with [New].
type Option func(*PubSub)

// WithLogger sets the logger of the listeners. Default slog.Default().
func WithLogger(l *slog.Logger) Option { return func(p *PubSub) { p.log = l } }

// WithName sets the name default subscription names end with: the
// app's name. Default "anetos".
func WithName(name string) Option { return func(p *PubSub) { p.name = name } }

// New returns a PubSub on broker. Run its listeners with [PubSub.Run];
// with [ForApp], they run as components of the app.
func New(broker Broker, opts ...Option) *PubSub {
	p := &PubSub{broker: broker, log: slog.Default(), name: "anetos"}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Broker returns the PubSub's broker.
func (p *PubSub) Broker() Broker { return p.broker }

// ForApp sets up the app's pub/sub from the PUBSUB_* settings: it opens
// the broker with the driver PUBSUB_DRIVER names (memory is built in;
// pass others, such as redis.PubSubDriver() or gcppubsub.Driver()), makes
// it available in every context the app creates (for [Publish]) and to
// [anetos.Resolve], prepares the listeners' subscriptions when the app
// boots, closes the broker at shutdown, and adds the pubsub:publish
// command.
//
//	ps, err := pubsub.ForApp(app, redis.PubSubDriver())
func ForApp(app *anetos.App, drivers ...Driver) (*PubSub, error) {
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	all := append([]Driver{MemoryDriver()}, drivers...)
	i := slices.IndexFunc(all, func(d Driver) bool { return d.Name == cfg.Driver })
	if i < 0 {
		names := make([]string, len(all))
		for j, d := range all {
			names[j] = d.Name
		}
		return nil, fmt.Errorf("pubsub: PUBSUB_DRIVER is %q, but the drivers are [%s]; pass its driver to pubsub.ForApp (redis.PubSubDriver() from drivers/redis, gcppubsub.Driver() from drivers/gcppubsub)", cfg.Driver, strings.Join(names, ", "))
	}
	broker, err := all[i].Open(app, cfg)
	if err != nil {
		return nil, fmt.Errorf("pubsub: open the %s broker: %w", cfg.Driver, err)
	}
	p := New(broker, WithLogger(app.Logger().With("component", "pubsub")), WithName(app.Config().Name))
	p.app = app
	p.driver = cfg.Driver
	if err := app.AddCommand(p.publishCommand()); err != nil {
		return nil, errors.Join(err, broker.Close())
	}
	app.AddContextValue(psKey{}, p)
	anetos.Provide(app, p)
	if app.Booted() {
		p.prepared = true // Listen prepares each subscription itself
	} else {
		app.Use(preparer{p})
	}
	app.OnShutdown("pubsub", func(context.Context) error { return broker.Close() })
	return p, nil
}

// preparer prepares the listeners' subscriptions when the app boots.
type preparer struct{ p *PubSub }

func (preparer) Name() string               { return "pubsub" }
func (preparer) Register(*anetos.App) error { return nil }
func (r preparer) Boot(ctx context.Context, _ *anetos.App) error {
	r.p.mu.Lock()
	ls := slices.Clone(r.p.listeners)
	r.p.prepared = true
	r.p.mu.Unlock()
	for _, l := range ls {
		if err := r.p.broker.Prepare(ctx, l.sub); err != nil {
			return fmt.Errorf("pubsub: prepare the subscription %s of %s: %w", l.sub.Name, l.sub.Topic, err)
		}
	}
	return nil
}

var topicName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:~+%-]{0,199}$`)

// checkName validates a topic or subscription name.
func checkName(kind, name string) error {
	if !topicName.MatchString(name) {
		return fmt.Errorf("pubsub: invalid %s name %q: use up to 200 letters, digits and . _ : ~ + %% -, starting with a letter or digit", kind, name)
	}
	return nil
}

// PublishOption configures one publish.
type PublishOption func(*publishOptions)

type publishOptions struct {
	attrs       map[string]string
	afterCommit bool
}

// Attributes sends string pairs with the message.
func Attributes(attrs map[string]string) PublishOption {
	return func(o *publishOptions) {
		if o.attrs == nil {
			o.attrs = map[string]string{}
		}
		maps.Copy(o.attrs, attrs)
	}
}

// AfterCommit publishes the message once the database transaction in the
// context commits, and not at all if it rolls back. Publish has returned
// by then, so a failure is logged, not returned. Without a transaction,
// the message is published at once and Publish returns the error.
func AfterCommit() PublishOption { return func(o *publishOptions) { o.afterCommit = true } }

// Publish publishes v to topic, with the pub/sub in ctx (from [ForApp]).
// v is encoded as JSON, except byte slices ([]byte, json.RawMessage),
// sent as they are:
//
//	err := pubsub.Publish(ctx, "orders.created", OrderCreated{ID: o.ID})
func Publish(ctx context.Context, topic string, v any, opts ...PublishOption) error {
	p, err := From(ctx)
	if err != nil {
		return err
	}
	return p.Publish(ctx, topic, v, opts...)
}

// Publish publishes v; see the function [Publish].
func (p *PubSub) Publish(ctx context.Context, topic string, v any, opts ...PublishOption) error {
	if err := checkName("topic", topic); err != nil {
		return err
	}
	var o publishOptions
	for _, opt := range opts {
		opt(&o)
	}
	var data []byte
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
		data = rv.Bytes() // []byte, json.RawMessage, other byte slices: as they are
	} else {
		var err error
		if data, err = json.Marshal(v); err != nil {
			return fmt.Errorf("pubsub: encode a message for %s: %w", topic, err)
		}
	}
	send := func(ctx context.Context) error {
		if _, err := p.broker.Publish(ctx, topic, Outgoing{Data: data, Attributes: o.attrs}); err != nil {
			return fmt.Errorf("pubsub: publish to %s: %w", topic, err)
		}
		return nil
	}
	if !o.afterCommit {
		return send(ctx)
	}
	// db.AfterCommit runs the function at once without a transaction:
	// then return its error.
	var (
		mu     sync.Mutex
		inline = true
		ran    bool
		result error
	)
	db.AfterCommit(ctx, func(ctx context.Context) {
		err := send(ctx)
		mu.Lock()
		defer mu.Unlock()
		if inline {
			ran, result = true, err
			return
		}
		if err != nil {
			p.log.ErrorContext(ctx, "pubsub: publish after commit failed", "topic", topic, "error", err)
		}
	})
	mu.Lock()
	defer mu.Unlock()
	inline = false
	if ran {
		return result
	}
	return nil
}

// ListenOption configures a listener.
type ListenOption func(*listenOptions) error

type listenOptions struct {
	name        string
	concurrency int
	timeout     time.Duration
	maxAttempts int
	backoff     []time.Duration
	deadLetter  string
	grace       time.Duration
}

// Subscription sets the subscription's name: the processes listening
// with one name share its messages, and each name gets every message.
// Default the topic and the app's name: "orders.created.billing".
func Subscription(name string) ListenOption {
	return func(o *listenOptions) error {
		o.name = name
		return checkName("subscription", name)
	}
}

// Concurrency sets how many messages the listener handles at once, in
// each process. Default 1.
func Concurrency(n int) ListenOption {
	return func(o *listenOptions) error {
		if n < 1 {
			return fmt.Errorf("pubsub: Concurrency(%d) must be at least 1", n)
		}
		o.concurrency = n
		return nil
	}
}

// Timeout bounds the handling of one message: then its context is
// canceled, and the attempt fails. Default 1m. Brokers may deliver a
// message again 30s after its timeout.
func Timeout(d time.Duration) ListenOption {
	return func(o *listenOptions) error {
		if d <= 0 {
			return fmt.Errorf("pubsub: Timeout(%s) must be positive", d)
		}
		o.timeout = d
		return nil
	}
}

// MaxAttempts sets how many deliveries a message gets: after the last
// failed one it goes to the [DeadLetter] topic, or is dropped (and
// logged) without one. Default 0: no limit. It needs a broker that counts
// deliveries (Message.Attempt); Google Pub/Sub counts them only for
// subscriptions with a dead-letter policy.
func MaxAttempts(n int) ListenOption {
	return func(o *listenOptions) error {
		if n < 0 {
			return fmt.Errorf("pubsub: MaxAttempts(%d) can't be negative", n)
		}
		o.maxAttempts = n
		return nil
	}
}

// Backoff sets the waits before deliveries after failures: the first
// before the second delivery, and so on; the last is used for the rest.
// Each varies by up to 20%. Default 10s, doubling up to 10m. Brokers that
// can't delay deliveries (Google Pub/Sub) use their own retry policy.
func Backoff(delays ...time.Duration) ListenOption {
	return func(o *listenOptions) error {
		if len(delays) == 0 {
			return errors.New("pubsub: Backoff needs at least one delay")
		}
		for _, d := range delays {
			if d < 0 {
				return fmt.Errorf("pubsub: Backoff(%s) can't be negative", d)
			}
		}
		o.backoff = delays
		return nil
	}
}

// DeadLetter publishes the messages the listener gives up on (after
// [MaxAttempts], or a [Permanent] error) to topic, with their attributes
// plus "anetos.topic", "anetos.subscription", "anetos.error" and
// "anetos.attempts".
func DeadLetter(topic string) ListenOption {
	return func(o *listenOptions) error {
		o.deadLetter = topic
		return checkName("topic", topic)
	}
}

// ShutdownGrace sets how long messages being handled may take to finish
// once the listener stops: then their contexts are canceled, and those
// that fail because of it are delivered again later. Default: half of
// APP_SHUTDOWN_TIMEOUT (never more than the shutdown budget leaves, less
// 2s), or 15s without an app.
func ShutdownGrace(d time.Duration) ListenOption {
	return func(o *listenOptions) error {
		if d < 0 {
			return fmt.Errorf("pubsub: ShutdownGrace(%s) can't be negative", d)
		}
		o.grace = d
		return nil
	}
}

// Listen adds a listener of topic: fn gets each message of its
// subscription, decoded from JSON into a T (a byte slice type, such as
// []byte or json.RawMessage, gets the body as it is). Returning nil acknowledges the message; an error has it delivered
// again after a backoff; a [Permanent] error gives up on it at once.
//
//	err := pubsub.Listen(ps, "orders.created", billing.OrderCreated, pubsub.Concurrency(20),
//		pubsub.MaxAttempts(5), pubsub.DeadLetter("orders.created.dlq"))
//
// With [ForApp], the listener runs as a component with the role
// "listeners" (so `run --only=listeners` runs only listeners), stopping
// after the HTTP server and before the queue's workers. Delivery is
// at-least-once: fn must be idempotent. [Current] returns the message.
func Listen[T any](p *PubSub, topic string, fn func(ctx context.Context, msg T) error, opts ...ListenOption) error {
	if err := checkName("topic", topic); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("pubsub: Listen(%q) with a nil function", topic)
	}
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.Interface {
		return fmt.Errorf("pubsub: Listen(%q): the message type %s is an interface, which JSON can't decode into", topic, t)
	}
	o := listenOptions{concurrency: 1, timeout: time.Minute, backoff: nil, grace: -1}
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return err
		}
	}
	if o.name == "" {
		o.name = topic + "." + p.name
		if err := checkName("subscription", o.name); err != nil {
			return fmt.Errorf("%w (set it with pubsub.Subscription)", err)
		}
	}
	raw := t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 // []byte, json.RawMessage
	l := &listener{
		p:   p,
		sub: SubscriptionSpec{Topic: topic, Name: o.name, Concurrency: o.concurrency, AckTimeout: o.timeout + ackMargin},
		o:   o,
		call: func(ctx context.Context, data []byte) error {
			var v T
			if raw {
				reflect.ValueOf(&v).Elem().SetBytes(data)
			} else if err := json.Unmarshal(data, &v); err != nil {
				return Permanent(fmt.Errorf("decode the message: %w", err))
			}
			return fn(ctx, v)
		},
	}
	p.mu.Lock()
	for _, other := range p.listeners {
		if other.sub.Topic == topic && other.sub.Name == o.name {
			p.mu.Unlock()
			return fmt.Errorf("pubsub: two listeners of %s with the subscription %s: give one another name with pubsub.Subscription", topic, o.name)
		}
	}
	p.listeners = append(p.listeners, l)
	prepared := p.prepared
	p.mu.Unlock()
	err := func() error {
		if prepared { // added after the app booted
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := p.broker.Prepare(ctx, l.sub); err != nil {
				return fmt.Errorf("pubsub: prepare the subscription %s of %s: %w", l.sub.Name, topic, err)
			}
		}
		if p.app != nil {
			return p.app.Component(l, anetos.Roles("listeners"), anetos.Stage(stageListeners), anetos.Restart(restartOnFailure))
		}
		return nil
	}()
	if err != nil { // forget it, so a corrected Listen can add it
		p.mu.Lock()
		p.listeners = slices.DeleteFunc(p.listeners, func(x *listener) bool { return x == l })
		p.mu.Unlock()
	}
	return err
}

// Current returns the message being handled with ctx, in a listener.
func Current(ctx context.Context) (*Message, bool) {
	m, ok := ctx.Value(msgKey{}).(*Message)
	return m, ok
}

type msgKey struct{}

// permanent wraps an error that gives up on a message.
type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

// Permanent marks the error as permanent, for [IsPermanent].
func (p permanent) Permanent() bool { return true }

// Permanent wraps err so that the listener gives up on the message at
// once (dead letter, or drop), without more deliveries: for messages that
// will never be handled, such as malformed ones.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanent{err}
}

// IsPermanent reports whether err, or an error it wraps, has a
// Permanent() bool method returning true: errors from [Permanent], and
// from queue.Permanent, so code shared by jobs and listeners can use
// either.
func IsPermanent(err error) bool {
	var p interface{ Permanent() bool }
	return errors.As(err, &p) && p.Permanent()
}

type psKey struct{}

// WithPubSub returns ctx with p, for [Publish]. [ForApp] makes it
// available in every context the app creates.
func WithPubSub(ctx context.Context, p *PubSub) context.Context {
	return context.WithValue(ctx, psKey{}, p)
}

// ErrNoPubSub is returned by [From] (and [Publish]) when the context has
// no pub/sub.
var ErrNoPubSub = errors.New("pubsub: no pub/sub in the context: call pubsub.ForApp at startup, or pubsub.WithPubSub")

// From returns the pub/sub in ctx.
func From(ctx context.Context) (*PubSub, error) {
	if p, ok := ctx.Value(psKey{}).(*PubSub); ok {
		return p, nil
	}
	return nil, ErrNoPubSub
}

// publishCommand is pubsub:publish.
func (p *PubSub) publishCommand() cmd.Command {
	return cmd.Command{
		Name:        "pubsub:publish",
		Usage:       "<topic> <message>",
		Description: "Publish a message (its body as given, usually JSON) to a topic",
		Run: func(ctx context.Context, args *cmd.Args) error {
			if len(args.Args) != 2 {
				return cmd.Usagef("pubsub:publish takes a topic and a message")
			}
			if err := checkName("topic", args.Args[0]); err != nil {
				return cmd.Usagef("%v", err)
			}
			if p.driver == "memory" {
				return errors.New("the memory broker lives in one process: a message published by this command reaches no listener (set PUBSUB_DRIVER)")
			}
			id, err := p.broker.Publish(ctx, args.Args[0], Outgoing{Data: []byte(args.Args[1])})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(args.Stdout, "Published message %s to %s.\n", id, args.Args[0])
			return err
		},
	}
}
