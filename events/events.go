// SPDX-License-Identifier: Apache-2.0

package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/queue"
)

// Bus delivers events to their listeners. Create it with [ForApp] (or
// [New]), add listeners with [On], [OnAsync] and [OnQueued], and emit
// events with [Emit].
type Bus struct {
	app   *anetos.App
	queue *queue.Queue // for New's WithQueue; ForApp resolves the app's
	log   *slog.Logger

	mu        sync.RWMutex
	listeners map[reflect.Type][]*listener
	closed    bool // closing: only async listeners may still emit
	stopped   bool // the async workers have stopped: nobody may emit

	pending counter        // async events not handled yet
	sending sync.WaitGroup // enqueues past the closed check, until their send ends
	stop    chan struct{}  // closed when the async workers must stop
	once    sync.Once      // closes stop
	base    context.Context
	cancel  context.CancelFunc // cancels base: async handlers' contexts

	obsMu     sync.RWMutex
	observers []func(context.Context, any)
	fakeAll   bool                  // Fake(): no event reaches listeners
	faked     map[reflect.Type]bool // Fake(types…): these don't
}

// Observe calls fn with each event emitted from now on, before its
// listeners get it, for tests and instrumentation. fn must be quick and
// safe for concurrent use. anetostest uses it to record events.
func (b *Bus) Observe(fn func(ctx context.Context, e any)) {
	b.obsMu.Lock()
	defer b.obsMu.Unlock()
	b.observers = append(b.observers, fn)
}

// Fake stops events of the types of the given values (all events, with
// none; nil values are ignored; a pointer type and its value type are
// different types) from reaching their listeners from now on: [Emit] passes them to
// the [Bus.Observe] functions only, and returns nil. For tests
// (anetostest.FakeEvents); it can't be undone.
//
//	bus.Fake(OrderPlaced{}) // OrderPlaced's listeners don't run; other events' do
func (b *Bus) Fake(events ...any) {
	b.obsMu.Lock()
	defer b.obsMu.Unlock()
	if len(events) == 0 {
		b.fakeAll = true
		return
	}
	if b.faked == nil {
		b.faked = map[reflect.Type]bool{}
	}
	for _, e := range events {
		if e != nil {
			b.faked[reflect.TypeOf(e)] = true
		}
	}
}

// observe passes e to the observers, and reports whether its type is
// faked.
func (b *Bus) observe(ctx context.Context, t reflect.Type, e any) bool {
	b.obsMu.RLock()
	obs, fake := b.observers, b.fakeAll || b.faked[t]
	b.obsMu.RUnlock()
	for _, fn := range obs {
		fn(ctx, e)
	}
	return fake
}

type kind int

const (
	syncKind kind = iota
	asyncKind
	queuedKind
)

func (k kind) String() string {
	return [...]string{"On", "OnAsync", "OnQueued"}[k]
}

// listener is a registered listener.
type listener struct {
	name string
	kind kind
	call func(ctx context.Context, e any) error // sync and async

	// async
	timeout     time.Duration
	concurrency int
	ch          chan delivery
	started     sync.Once

	// queued
	job      string
	dispatch []queue.DispatchOption
}

// BusOption configures a [Bus] made with [New].
type BusOption func(*Bus)

// WithLogger sets the logger for async listeners' failures. Default
// slog.Default().
func WithLogger(l *slog.Logger) BusOption { return func(b *Bus) { b.log = l } }

// WithQueue sets the queue of queued listeners. [ForApp] uses the app's
// (from queue.ForApp).
func WithQueue(q *queue.Queue) BusOption { return func(b *Bus) { b.queue = q } }

// New returns a bus without an app: async listeners get a context with
// the bus only. Call [Bus.Close] when done with it.
func New(opts ...BusOption) *Bus {
	b := &Bus{log: slog.Default(), listeners: map[reflect.Type][]*listener{}, stop: make(chan struct{})}
	b.base, b.cancel = context.WithCancel(context.Background())
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// ForApp returns the app's bus: it is available in every context the app
// creates (for [Emit]) and to [anetos.Resolve], and closed at shutdown
// (async listeners finish the events they have, within the shutdown
// budget). Call it last in setup, after the services async listeners use
// (db.Connect, queue.ForApp, the cache…): shutdown hooks run in reverse
// order, so the bus is then closed before them.
func ForApp(app *anetos.App) (*Bus, error) {
	if _, err := anetos.Resolve[*Bus](app); err == nil {
		return nil, errors.New("events: ForApp called twice for one app")
	}
	b := New(WithLogger(app.Logger().With("component", "events")))
	b.app = app
	app.AddContextValue(busKey{}, b)
	anetos.Provide(app, b)
	app.OnShutdown("events", b.Close)
	return b, nil
}

// context returns a new context for an async listener: the app's values
// and the bus, canceled if Close gives up on the listener.
func (b *Bus) context() context.Context {
	ctx := context.WithValue(WithBus(b.base, b), inListener{}, b)
	if b.app != nil {
		ctx = b.app.Context(ctx)
	}
	return ctx
}

// Option configures a listener.
type Option func(*listenerOptions) error

type listenerOptions struct {
	kind        kind
	name        string
	timeout     time.Duration
	concurrency int
	buffer      int
	job         []queue.JobOption
	dispatch    []queue.DispatchOption
}

// only reports an error if the option doesn't apply to the listener's
// kind.
func (o *listenerOptions) only(option string, kinds ...kind) error {
	if slices.Contains(kinds, o.kind) {
		return nil
	}
	return fmt.Errorf("events: %s doesn't apply to %s listeners", option, o.kind)
}

// Name names the listener, in logs and, for queued listeners, in the
// queue. Default: its function's name, such as "listeners.SendWelcome".
// Queued listeners that are anonymous functions need one: the name must
// stay the same across deploys, or queued events won't find it.
func Name(name string) Option {
	return func(o *listenerOptions) error {
		if name == "" || len(name) > 150 {
			return fmt.Errorf("events: listener name %q must have 1 to 150 bytes", name)
		}
		o.name = name
		return nil
	}
}

// Concurrency sets how many events an async listener handles at once.
// Default 1.
func Concurrency(n int) Option {
	return func(o *listenerOptions) error {
		if n < 1 {
			return fmt.Errorf("events: Concurrency(%d) must be at least 1", n)
		}
		o.concurrency = n
		return o.only("Concurrency", asyncKind)
	}
}

// Buffer sets how many events wait for an async listener: when they are
// all taken, Emit waits for room (or for its context to end). Default
// 1000. A listener that emits events to itself waits for its own room:
// with a full buffer, until its timeout.
func Buffer(n int) Option {
	return func(o *listenerOptions) error {
		if n < 1 {
			return fmt.Errorf("events: Buffer(%d) must be at least 1", n)
		}
		o.buffer = n
		return o.only("Buffer", asyncKind)
	}
}

// Timeout bounds an async listener's handling of one event: then its
// context is canceled. Default 1m. (For a queued listener, use
// Job(queue.Timeout(d)).)
func Timeout(d time.Duration) Option {
	return func(o *listenerOptions) error {
		if d <= 0 {
			return fmt.Errorf("events: Timeout(%s) must be positive", d)
		}
		o.timeout = d
		return o.only("Timeout", asyncKind)
	}
}

// Job sets a queued listener's job options: queue.Tries, queue.Timeout,
// queue.Backoff.
func Job(opts ...queue.JobOption) Option {
	return func(o *listenerOptions) error {
		o.job = append(o.job, opts...)
		return o.only("Job", queuedKind)
	}
}

// Dispatch sets the options a queued listener's jobs are dispatched with:
// queue.OnQueue, queue.Delay.
func Dispatch(opts ...queue.DispatchOption) Option {
	return func(o *listenerOptions) error {
		o.dispatch = append(o.dispatch, opts...)
		return o.only("Dispatch", queuedKind)
	}
}

var anonymous = regexp.MustCompile(`\.func\d+(\.\d+)*$`)

// funcName returns fn's name without its package path: "listeners.Send",
// "listeners.(*Mailer).Send", "main.setup.func1".
func funcName(fn any) string {
	f := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
	if f == nil {
		return "listener"
	}
	name := strings.TrimSuffix(f.Name(), "-fm") // method values
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

func newListener[E any](b *Bus, k kind, fn func(context.Context, E) error, opts []Option) (*listener, reflect.Type, error) {
	t := reflect.TypeFor[E]()
	if fn == nil {
		return nil, t, fmt.Errorf("events: %s with a nil function for %s", k, t)
	}
	o := listenerOptions{kind: k, timeout: time.Minute, concurrency: 1, buffer: 1000}
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return nil, t, err
		}
	}
	if t.Kind() == reflect.Interface {
		return nil, t, fmt.Errorf("events: %s for the interface %s: events are matched by their concrete type, so it would never run", k, t)
	}
	if o.name == "" {
		o.name = funcName(fn)
		if k == queuedKind && (anonymous.MatchString(o.name) || strings.Contains(o.name, "[")) {
			return nil, t, fmt.Errorf("events: the queued listener %s is an anonymous or generic function: give it a name with events.Name, which must stay the same across deploys", o.name)
		}
	}
	l := &listener{name: o.name, kind: k, timeout: o.timeout, concurrency: o.concurrency, dispatch: o.dispatch}
	l.call = func(ctx context.Context, e any) error { return fn(ctx, e.(E)) }
	switch k {
	case asyncKind:
		l.ch = make(chan delivery, o.buffer)
	case queuedKind:
		q, err := b.queueOf()
		if err != nil {
			return nil, t, err
		}
		l.job = "event:" + o.name
		if err := queue.RegisterFunc(q, l.job, func(ctx context.Context, e E) error { return fn(ctx, e) }, o.job...); err != nil {
			return nil, t, fmt.Errorf("events: queued listener %s: %w", o.name, err)
		}
	}
	return l, t, nil
}

// queueOf returns the queue of queued listeners.
func (b *Bus) queueOf() (*queue.Queue, error) {
	if b.queue != nil {
		return b.queue, nil
	}
	if b.app != nil {
		if q, err := anetos.Resolve[*queue.Queue](b.app); err == nil {
			return q, nil
		}
	}
	return nil, errors.New("events: queued listeners need the queue: call queue.ForApp before events.OnQueued")
}

func add[E any](b *Bus, k kind, fn func(context.Context, E) error, opts []Option) error {
	l, t, err := newListener(b, k, fn, opts)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners[t] = append(b.listeners[t], l)
	return nil
}

// On adds a listener for events of type E that runs in [Emit], with its
// context (and transaction): listeners run in the order they were added,
// and the first error stops the others and is returned by Emit.
//
//	err := events.On(bus, func(ctx context.Context, e OrderPlaced) error { … })
func On[E any](b *Bus, fn func(ctx context.Context, e E) error, opts ...Option) error {
	return add(b, syncKind, fn, opts)
}

// OnAsync adds a listener for events of type E that runs in the
// background, in a pool of goroutines of its own ([Concurrency]), after
// the transaction Emit ran in commits (or at once, outside one). Its
// context has the app's values but nothing of Emit's: no transaction, no
// request. Its errors and panics are logged.
//
// Async events are lost if the process stops before they are handled
// (they get the shutdown budget to finish). Use [OnQueued] for work that
// must happen.
func OnAsync[E any](b *Bus, fn func(ctx context.Context, e E) error, opts ...Option) error {
	return add(b, asyncKind, fn, opts)
}

// OnQueued adds a listener for events of type E that runs as a queue job,
// durably, with the queue's retries: [Emit] dispatches a job carrying the
// event (encoded as JSON) after the transaction it ran in commits (with
// the queue's database driver, in the transaction). The listener's name
// ([Name]) names the job, "event:<name>". It needs queue.ForApp first
// (or [WithQueue]); register it in the workers' app too.
func OnQueued[E any](b *Bus, fn func(ctx context.Context, e E) error, opts ...Option) error {
	return add(b, queuedKind, fn, opts)
}

// Emit delivers e to the listeners of its type, through the bus in ctx
// (from [ForApp]): first the [On] listeners, in ctx, stopping at the first
// error; then it dispatches the [OnQueued] ones and hands e to the
// [OnAsync] ones, after the transaction in ctx commits. It returns the
// first On listener's error, or the errors handing e to the others (when
// they happen in Emit, outside a transaction). An event without
// listeners is fine. In a transaction of db.WithTx, whose commit db
// doesn't see, async listeners (and queued ones, but with the database
// driver) never get the event.
//
//	err := events.Emit(ctx, OrderPlaced{OrderID: o.ID})
func Emit(ctx context.Context, e any) error {
	b, err := From(ctx)
	if err != nil {
		return err
	}
	return b.Emit(ctx, e)
}

// Emit delivers e; see the function [Emit].
func (b *Bus) Emit(ctx context.Context, e any) error {
	t := reflect.TypeOf(e)
	if t == nil {
		return errors.New("events: Emit(nil)")
	}
	if b.observe(ctx, t, e) {
		return nil // faked: recorded only
	}
	b.mu.RLock()
	ls := b.listeners[t]
	b.mu.RUnlock()
	var async []*listener
	var errs []error
	for _, l := range ls {
		switch l.kind {
		case syncKind:
			if err := l.call(ctx, e); err != nil {
				return fmt.Errorf("events: %s: %w", l.name, err)
			}
		case asyncKind:
			async = append(async, l)
		}
	}
	for _, l := range ls {
		if l.kind == queuedKind {
			q, err := b.queueOf()
			if err == nil {
				err = q.DispatchFunc(ctx, l.job, e, append(slices.Clone(l.dispatch), queue.AfterCommit())...)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("events: %s: %w", l.name, err))
			}
		}
	}
	if len(async) > 0 {
		err := afterCommit(ctx, func(ctx context.Context) error {
			var errs []error
			for _, l := range async {
				if err := b.enqueue(ctx, l, e); err != nil {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		}, func(err error) {
			b.log.Error("events: hand an event to async listeners after the commit", "event", t.String(), "error", err)
		})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// afterCommit runs fn once the transaction in ctx commits, returning its
// error if that is at once (no transaction), logging it otherwise.
func afterCommit(ctx context.Context, fn func(context.Context) error, logErr func(error)) error {
	var (
		mu     sync.Mutex
		inline = true
		ran    bool
		result error
	)
	db.AfterCommit(ctx, func(ctx context.Context) {
		err := fn(ctx)
		mu.Lock()
		defer mu.Unlock()
		if inline {
			ran, result = true, err
			return
		}
		if err != nil {
			logErr(err)
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

// inListener marks the contexts of a bus's async listeners, which may
// still emit while the bus closes.
type inListener struct{}

// ErrClosed is returned when an event reaches an async listener of a
// closed bus.
var ErrClosed = errors.New("events: the bus is closed")

// delivery is an event on its way to an async listener, with the values
// the app's carriers captured from the emitter's context.
type delivery struct {
	e       any
	carried map[string]string
}

// enqueue hands e to an async listener, waiting for room in its buffer
// until ctx ends.
func (b *Bus) enqueue(ctx context.Context, l *listener, e any) error {
	b.mu.RLock()
	if b.stopped || b.closed && ctx.Value(inListener{}) != b {
		b.mu.RUnlock()
		return fmt.Errorf("events: %s: %w", l.name, ErrClosed)
	}
	b.pending.add(1)
	b.sending.Add(1) // under the lock: Close waits for it before draining
	b.mu.RUnlock()
	defer b.sending.Done()
	d := delivery{e: e}
	if b.app != nil {
		d.carried = b.app.Carried(ctx) // the emitter's actor, for example (anetos.Carrier)
	}
	l.started.Do(func() {
		for range l.concurrency {
			go b.work(l)
		}
	})
	select {
	case l.ch <- d:
		return nil
	default:
	}
	select {
	case l.ch <- d:
		return nil
	case <-b.stop:
		b.pending.done()
		return fmt.Errorf("events: %s: %w", l.name, ErrClosed)
	case <-ctx.Done():
		b.pending.done()
		return fmt.Errorf("events: %s has %d events waiting: %w", l.name, cap(l.ch), context.Cause(ctx))
	}
}

// work handles an async listener's events until the bus stops.
func (b *Bus) work(l *listener) {
	for {
		select {
		case <-b.stop:
			return
		case d := <-l.ch:
			b.handle(l, d)
			b.pending.done()
		}
	}
}

// handle runs an async listener, logging its error or panic.
func (b *Bus) handle(l *listener, d delivery) {
	e := d.e
	ctx, cancel := context.WithTimeout(b.context(), l.timeout)
	defer cancel()
	if b.app != nil {
		ctx = b.app.WithCarried(ctx, d.carried)
		var end func()
		ctx, end = b.app.StartUnit(ctx, anetos.Unit{Kind: "listener", Name: l.name})
		defer end()
	}
	log := b.log.With("listener", l.name, "event", reflect.TypeOf(e).String())
	defer func() {
		if v := recover(); v != nil {
			log.Error("events: async listener panicked", "panic", v, "stack", string(debug.Stack()))
		}
	}()
	if err := l.call(ctx, e); err != nil {
		log.Error("events: async listener failed", "error", err)
	}
}

// Wait waits until every event handed to the async listeners has been
// handled (or dropped by Close), or ctx ends. Under a steady flow of
// events that may not happen; tests use it before checking what async
// listeners did.
func (b *Bus) Wait(ctx context.Context) error {
	return b.pending.wait(ctx)
}

// Close stops the bus: async listeners get no more events (except those
// they emit themselves while finishing), and Close waits (until ctx ends)
// for them to handle what they have. Events still waiting then are lost,
// and the listeners' contexts are canceled. Closing a closed bus does
// nothing. [ForApp] closes the bus at shutdown. Don't call Close from an
// async listener without a deadline: it waits for that listener too.
func (b *Bus) Close(ctx context.Context) error {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()
	err := b.pending.wait(ctx)
	b.mu.Lock()
	b.stopped = true // no enqueue gets past the check now
	b.mu.Unlock()
	b.once.Do(func() { close(b.stop) })
	b.cancel()
	b.sending.Wait() // sends in progress land in a buffer, or give up on stop
	// Drop what is still buffered (after a timeout, or an event a
	// goroutine of a listener emitted late); running listeners finish on
	// their own, their contexts canceled.
	lost := 0
	b.mu.RLock()
	for _, ls := range b.listeners {
		for _, l := range ls {
			for drained := false; !drained && l.ch != nil; {
				select {
				case <-l.ch:
					b.pending.done()
					lost++
				default:
					drained = true
				}
			}
		}
	}
	b.mu.RUnlock()
	if err == nil && lost == 0 {
		return nil
	}
	if err == nil {
		err = errors.New("emitted after the async listeners finished")
	}
	running := b.pending.n()
	b.log.Error("events: async events lost at shutdown", "dropped", lost, "canceled", running)
	return fmt.Errorf("events: %d async event(s) not handled, %d canceled: %w", lost, running, err)
}

// counter counts pending events, and wakes waiters when none are left.
type counter struct {
	mu    sync.Mutex
	count int
	zero  chan struct{} // closed while count is 0; nil means closed
}

func (c *counter) add(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.count == 0 {
		c.zero = make(chan struct{})
	}
	c.count += n
}

func (c *counter) done() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count--
	if c.count == 0 {
		close(c.zero)
	}
}

func (c *counter) n() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

func (c *counter) wait(ctx context.Context) error {
	c.mu.Lock()
	if c.count == 0 {
		c.mu.Unlock()
		return nil
	}
	zero := c.zero
	c.mu.Unlock()
	select {
	case <-zero:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

type busKey struct{}

// WithBus returns ctx with b, for [Emit]. [ForApp] makes the bus
// available in every context the app creates.
func WithBus(ctx context.Context, b *Bus) context.Context {
	return context.WithValue(ctx, busKey{}, b)
}

// ErrNoBus is returned by [From] (and [Emit]) when the context has no
// bus.
var ErrNoBus = errors.New("events: no bus in the context: call events.ForApp at startup, or events.WithBus")

// From returns the bus in ctx.
func From(ctx context.Context) (*Bus, error) {
	if b, ok := ctx.Value(busKey{}).(*Bus); ok {
		return b, nil
	}
	return nil, ErrNoBus
}
