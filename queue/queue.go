// SPDX-License-Identifier: Apache-2.0

package queue

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
)

// Job is work to do in the background. Its exported fields are encoded
// as JSON when it is dispatched, so keep them small: IDs, not records.
//
//	type SendWelcome struct {
//		UserID int64 `json:"user_id"`
//	}
//
//	func (j SendWelcome) Handle(ctx context.Context) error { … }
//
// Delivery is at-least-once: a job can run more than once (if a worker
// dies, or its lease runs out), so Handle must be idempotent. ctx carries
// what the app provides (the database, the cache, the queue), the job's
// [Info], and its timeout.
type Job interface {
	// Handle does the work. An error retries the job, after a backoff,
	// until it has used its tries; a [Permanent] error fails it at once.
	Handle(ctx context.Context) error
}

// Failer is implemented by jobs that need to know when they fail for
// good: Failed runs once, after the last attempt, with its error. If the
// last attempt didn't record its outcome (its worker died, or it ran past
// its lease), Failed runs instead of another attempt, although that
// attempt's work may have been done.
type Failer interface {
	// Failed handles the job's failure, say by telling someone.
	Failed(ctx context.Context, err error)
}

// Queue dispatches jobs to a [Store] and runs them in workers. Create it
// with [New] (or [NewWithStore]), and register each job type with [Register].
type Queue struct {
	store Store
	sync  bool
	cfg   Config
	app   *anetos.App
	log   *slog.Logger

	mu     sync.RWMutex
	byName map[string]*jobType
	byType map[reflect.Type]*jobType

	obsMu     sync.RWMutex
	observers []func(context.Context, Dispatched)
	fake      atomic.Bool // Fake: jobs go to the observers only
}

// Dispatched describes a dispatched job, for [Queue.Observe].
type Dispatched struct {
	// ID is the job's ID.
	ID string
	// Job is the job type's name ("SendWelcome", "mail:send").
	Job string
	// Queue is the queue it went on.
	Queue string
	// Delay is its delay ([Delay]).
	Delay time.Duration
	// Data is the job, or a function job's payload, as JSON.
	Data json.RawMessage
}

// Decode decodes the job's data into v: a pointer to the job type, or to
// a function job's payload type.
func (d Dispatched) Decode(v any) error { return json.Unmarshal(d.Data, v) }

// Observe calls fn with each job dispatched from now on: once the store
// has it (with the database driver, inside a transaction, once that
// commits), or, with the sync driver, before it runs; with
// [AfterCommit], after the commit. A job the store refuses isn't
// passed. In a transaction of db.WithTx, whose commit db doesn't see,
// the database driver's jobs aren't passed either. For tests and
// instrumentation. fn must be quick and safe for concurrent use.
// anetostest uses it to record jobs.
func (q *Queue) Observe(fn func(ctx context.Context, d Dispatched)) {
	q.obsMu.Lock()
	defer q.obsMu.Unlock()
	q.observers = append(q.observers, fn)
}

// Fake makes the queue, from now on, pass dispatched jobs to the
// [Queue.Observe] functions only: they are neither stored nor run. For
// tests (anetostest.FakeQueue); it can't be undone.
func (q *Queue) Fake() { q.fake.Store(true) }

// NameOf returns the name job's type is registered with ([Register]).
func (q *Queue) NameOf(job Job) (string, error) {
	jt, err := q.typeOf(job)
	if err != nil {
		return "", err
	}
	return jt.name, nil
}

// notify passes d, a job just dispatched, to the observers and the
// dispatch's [OnDispatched] function.
func (q *Queue) notify(ctx context.Context, d Dispatched, o dispatchOptions) {
	q.obsMu.RLock()
	obs := q.observers
	q.obsMu.RUnlock()
	for _, fn := range obs {
		fn(ctx, d)
	}
	for _, fn := range o.onDispatched {
		fn(ctx, d)
	}
}

// jobType is a registered job type.
type jobType struct {
	name    string
	payload reflect.Type // a function's payload type (RegisterFunc); nil for Job types
	tries   int
	timeout time.Duration
	backoff []time.Duration // nil: exponential from the config
	decode  func(data []byte) (Job, error)
}

// Option configures a [Queue] made with [NewWithStore].
type Option func(*Queue)

// WithLogger sets the logger of the queue's workers. Default
// slog.Default().
func WithLogger(l *slog.Logger) Option { return func(q *Queue) { q.log = l } }

// NewWithStore returns a queue keeping its jobs in store. Zero fields of cfg take
// their defaults (those of the QUEUE_* settings). A nil store runs jobs
// at once, like the sync driver. Run its workers with [Queue.Run]; for a
// queue made with [New], [Queue.Work] adds them to the app.
func NewWithStore(store Store, cfg Config, opts ...Option) *Queue {
	q := &Queue{store: store, cfg: cfg.withDefaults(), log: slog.Default(),
		byName: map[string]*jobType{}, byType: map[reflect.Type]*jobType{}}
	if store == nil {
		q.sync = true
	}
	for _, opt := range opts {
		opt(q)
	}
	return q
}

// Store returns the queue's store; nil for the sync driver.
func (q *Queue) Store() Store {
	if q.sync {
		return nil
	}
	return q.store
}

// Config returns the queue's settings, with defaults filled in.
func (q *Queue) Config() Config { return q.cfg }

// JobOption configures a job type for [Register].
type JobOption func(*jobType) error

// Name sets the name a job type is stored under. Default its Go type,
// such as "jobs.SendWelcome". Set it to keep jobs already queued working
// when the type is renamed or moved.
func Name(name string) JobOption {
	return func(t *jobType) error {
		if name == "" || len(name) > 200 {
			return fmt.Errorf("queue: job name %q must have 1 to 200 bytes", name)
		}
		t.name = name
		return nil
	}
}

// Tries sets how many times a job runs before it fails for good: 1 for
// no retries. Default QUEUE_TRIES (3).
func Tries(n int) JobOption {
	return func(t *jobType) error {
		if n < 1 {
			return fmt.Errorf("queue: Tries(%d): a job runs at least once", n)
		}
		t.tries = n
		return nil
	}
}

// Timeout sets how long one attempt may run: then its context is
// canceled, and the attempt fails. Default QUEUE_TIMEOUT (1m).
func Timeout(d time.Duration) JobOption {
	return func(t *jobType) error {
		if d <= 0 {
			return fmt.Errorf("queue: Timeout(%s) must be positive", d)
		}
		t.timeout = d
		return nil
	}
}

// Backoff sets the waits before retries: the first before the second
// attempt, and so on; the last is used for the rest. Each varies by up to
// 20%, so failed jobs don't all come back at once. Default QUEUE_BACKOFF
// (10s), doubling each time up to QUEUE_BACKOFF_MAX (10m).
func Backoff(delays ...time.Duration) JobOption {
	return func(t *jobType) error {
		if len(delays) == 0 {
			return errors.New("queue: Backoff needs at least one delay")
		}
		for _, d := range delays {
			if d < 0 {
				return fmt.Errorf("queue: Backoff(%s) can't be negative", d)
			}
		}
		t.backoff = delays
		return nil
	}
}

// Register adds the job type J, so the queue can dispatch and run it.
// Register every job type at startup, in the app that dispatches it and
// in the workers' (usually the same program):
//
//	err := queue.Register[jobs.SendWelcome](q, queue.Tries(5))
//
// J is a struct type (or a pointer to one) implementing [Job]. A worker
// fails jobs whose type it doesn't know, so deploy workers before the
// code that dispatches new job types.
func Register[J Job](q *Queue, opts ...JobOption) error {
	t := reflect.TypeFor[J]()
	elem := t
	if elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	if elem.Kind() != reflect.Struct {
		return fmt.Errorf("queue: job type %s isn't a struct", t)
	}
	jt := &jobType{
		name: elem.String(),
		decode: func(data []byte) (Job, error) {
			var j J
			if t.Kind() == reflect.Pointer {
				j = reflect.New(elem).Interface().(J)
				if err := json.Unmarshal(data, j); err != nil {
					return nil, err
				}
				return j, nil
			}
			if err := json.Unmarshal(data, &j); err != nil {
				return nil, err
			}
			return j, nil
		},
	}
	for _, opt := range opts {
		if err := opt(jt); err != nil {
			return err
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.byType[t]; ok {
		return fmt.Errorf("queue: job type %s registered twice", t)
	}
	if _, ok := q.byName[jt.name]; ok {
		return fmt.Errorf("queue: two job types are named %q: give one another with queue.Name", jt.name)
	}
	q.byType[t] = jt
	q.byName[jt.name] = jt
	return nil
}

// RegisterFunc registers fn as a job type named name, whose jobs carry a
// payload of type T, stored as JSON. Dispatch them with [DispatchFunc]:
//
//	err := queue.RegisterFunc(q, "reports.send", func(ctx context.Context, r ReportRequest) error { … })
//	err = queue.DispatchFunc(ctx, "reports.send", ReportRequest{Month: "2026-09"})
//
// It suits jobs that need dependencies a struct's fields can't carry (fn
// can be a closure), and packages building on the queue (events.OnQueued).
// The name works like [Name]'s, and the other options like [Register]'s.
func RegisterFunc[T any](q *Queue, name string, fn func(ctx context.Context, payload T) error, opts ...JobOption) error {
	if fn == nil {
		return fmt.Errorf("queue: RegisterFunc(%q) with a nil function", name)
	}
	if t := reflect.TypeFor[T](); t.Kind() == reflect.Interface {
		return fmt.Errorf("queue: RegisterFunc(%q): the payload type %s is an interface, which JSON can't decode into", name, t)
	}
	jt := &jobType{
		payload: reflect.TypeFor[T](),
		decode: func(data []byte) (Job, error) {
			var v T
			if err := json.Unmarshal(data, &v); err != nil {
				return nil, err
			}
			return funcJob[T]{fn: fn, payload: v}, nil
		},
	}
	for _, opt := range append([]JobOption{Name(name)}, opts...) {
		if err := opt(jt); err != nil {
			return err
		}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.byName[jt.name]; ok {
		return fmt.Errorf("queue: two job types are named %q", jt.name)
	}
	q.byName[jt.name] = jt
	return nil
}

// funcJob is a job of RegisterFunc: its function and payload.
type funcJob[T any] struct {
	fn      func(context.Context, T) error
	payload T
}

func (j funcJob[T]) Handle(ctx context.Context) error { return j.fn(ctx, j.payload) }

// DispatchFunc dispatches a job of the function registered as name (see
// [RegisterFunc]) with payload, on the queue in ctx. payload must have
// the function's payload type (or point to a value of it).
func DispatchFunc(ctx context.Context, name string, payload any, opts ...DispatchOption) error {
	q, err := From(ctx)
	if err != nil {
		return err
	}
	return q.DispatchFunc(ctx, name, payload, opts...)
}

// DispatchFunc dispatches a job of a function; see the function
// [DispatchFunc].
func (q *Queue) DispatchFunc(ctx context.Context, name string, payload any, opts ...DispatchOption) error {
	jt := q.lookup(name)
	if jt == nil || jt.payload == nil {
		return fmt.Errorf("queue: no function registered as %q: call queue.RegisterFunc at startup", name)
	}
	t := reflect.TypeOf(payload)
	if t != jt.payload && (t == nil || t.Kind() != reflect.Pointer || t.Elem() != jt.payload) {
		return fmt.Errorf("queue: job %q takes a %s, not a %v", name, jt.payload, t)
	}
	return q.dispatch(ctx, jt, payload, opts)
}

// typeOf returns the registered type of job: its own, or, for a pointer,
// that of the value it points to.
func (q *Queue) typeOf(job Job) (*jobType, error) {
	t := reflect.TypeOf(job)
	if t == nil {
		return nil, errors.New("queue: dispatch of a nil job")
	}
	q.mu.RLock()
	jt, ok := q.byType[t]
	if !ok && t.Kind() == reflect.Pointer {
		jt, ok = q.byType[t.Elem()]
	}
	q.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("queue: job type %s isn't registered: call queue.Register[%s] at startup", t, t)
	}
	return jt, nil
}

// lookup returns the job type registered as name.
func (q *Queue) lookup(name string) *jobType {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.byName[name]
}

// tries, timeout and backoff are a job type's settings, or the defaults
// for a job whose type isn't known.
func (q *Queue) tries(jt *jobType) int {
	if jt == nil || jt.tries == 0 {
		return q.cfg.Tries
	}
	return jt.tries
}

func (q *Queue) timeout(jt *jobType) time.Duration {
	if jt == nil || jt.timeout == 0 {
		return q.cfg.Timeout
	}
	return jt.timeout
}

// backoff returns the wait before the attempt after attempt (1-based).
func (q *Queue) backoff(jt *jobType, attempt int) time.Duration {
	var d time.Duration
	if jt != nil && jt.backoff != nil {
		d = jt.backoff[min(attempt, len(jt.backoff))-1]
	} else {
		d = q.cfg.Backoff
		for i := 1; i < attempt && d < q.cfg.MaxBackoff; i++ {
			d *= 2
		}
		d = min(d, q.cfg.MaxBackoff)
	}
	return jitter(d)
}

// jitter varies d by up to 20%.
func jitter(d time.Duration) time.Duration {
	var b [8]byte
	_, _ = rand.Read(b[:])
	f := 0.8 + 0.4*float64(binary.LittleEndian.Uint64(b[:])>>11)/(1<<53)
	return time.Duration(float64(d) * f)
}

// DispatchOption configures one dispatch.
type DispatchOption func(*dispatchOptions)

type dispatchOptions struct {
	queue        string
	delay        time.Duration
	afterCommit  bool
	onDispatched []func(context.Context, Dispatched)
}

// OnDispatched calls fn once the job is dispatched, when [Queue.Observe]
// functions are: once the store has it (after the commit with
// [AfterCommit]); not if the store refuses it or the transaction rolls
// back. With the sync driver, fn is called before the job runs, whatever
// its outcome. Each OnDispatched option adds a function. mailer.Queue
// uses it to record queued email.
func OnDispatched(fn func(ctx context.Context, d Dispatched)) DispatchOption {
	return func(o *dispatchOptions) { o.onDispatched = append(o.onDispatched, fn) }
}

// OnQueue puts the job on the queue name: workers take jobs from the
// queues they were given, in order. Default QUEUE_DEFAULT ("default").
func OnQueue(name string) DispatchOption {
	return func(o *dispatchOptions) { o.queue = name }
}

// Delay makes the job available only after d. The sync driver ignores it.
func Delay(d time.Duration) DispatchOption {
	return func(o *dispatchOptions) { o.delay = max(d, 0) }
}

// AfterCommit dispatches the job only once the database transaction in
// the context commits, and not at all if it rolls back, so the job never
// looks for data that isn't there (yet). Since Dispatch has returned by
// then, a failure to dispatch is logged, not returned. Without a
// transaction, the job is dispatched at once, and Dispatch returns the
// error. The database driver writes the job in the transaction instead,
// when it is on the queue's database: then the job is dispatched if and
// only if the transaction commits.
func AfterCommit() DispatchOption {
	return func(o *dispatchOptions) { o.afterCommit = true }
}

var queueName = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,99}$`)

// checkQueue validates a queue name.
func checkQueue(name string) error {
	if !queueName.MatchString(name) {
		return fmt.Errorf("queue: invalid queue name %q: use up to 100 lower-case letters, digits and . _ : -", name)
	}
	return nil
}

// envelope is a job's encoding. Locale and Zone are those of the
// context that dispatched it (package i18n), when not the defaults: the
// job runs in them. Carried holds the values of the app's carriers
// (anetos.App.AddCarrier) in that context, restored in the job's.
type envelope struct {
	ID      string            `json:"id"`
	Job     string            `json:"job"`
	Data    json.RawMessage   `json:"data"`
	Locale  string            `json:"locale,omitempty"`
	Zone    string            `json:"zone,omitempty"`
	Carried map[string]string `json:"carried,omitempty"`
}

// localeOf returns the locale and time zone of ctx to carry in a job,
// "" for the defaults.
func localeOf(ctx context.Context) (locale, zone string) {
	if l := i18n.Locale(ctx); l != i18n.From(ctx).Default() {
		locale = l
	}
	if z := i18n.TimeZone(ctx); z != anetos.Location(ctx) {
		zone = z.String()
	}
	return locale, zone
}

// inLocale returns ctx in a job's locale and time zone.
func (e envelope) inLocale(ctx context.Context) context.Context {
	if e.Locale != "" {
		ctx = i18n.WithLocale(ctx, e.Locale)
	}
	if e.Zone != "" {
		if loc, err := time.LoadLocation(e.Zone); err == nil {
			ctx = i18n.WithTimeZone(ctx, loc)
		}
	}
	return ctx
}

// Dispatch dispatches job on the queue in ctx (from [New]):
//
//	err := queue.Dispatch(ctx, jobs.SendWelcome{UserID: u.ID}, queue.OnQueue("emails"))
//
// With the database driver, a dispatch inside a transaction is part of
// it. With the sync driver, the job runs at once and its error is
// returned.
func Dispatch(ctx context.Context, job Job, opts ...DispatchOption) error {
	q, err := From(ctx)
	if err != nil {
		return err
	}
	return q.Dispatch(ctx, job, opts...)
}

// Dispatch dispatches job; see the function [Dispatch].
func (q *Queue) Dispatch(ctx context.Context, job Job, opts ...DispatchOption) error {
	jt, err := q.typeOf(job)
	if err != nil {
		return err
	}
	return q.dispatch(ctx, jt, job, opts)
}

// dispatch encodes value (a job, or a function's payload) and sends it.
func (q *Queue) dispatch(ctx context.Context, jt *jobType, value any, opts []DispatchOption) error {
	o := dispatchOptions{queue: q.cfg.Default}
	for _, opt := range opts {
		opt(&o)
	}
	if err := checkQueue(o.queue); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("queue: encode %s: %w", jt.name, err)
	}
	id := newID()
	locale, zone := localeOf(ctx)
	var carried map[string]string
	if q.app != nil {
		carried = q.app.Carried(ctx)
	}
	payload, err := json.Marshal(envelope{ID: id, Job: jt.name, Data: data, Locale: locale, Zone: zone, Carried: carried})
	if err != nil {
		return err
	}
	d := Dispatched{ID: id, Job: jt.name, Queue: o.queue, Delay: o.delay, Data: data}
	send := func(ctx context.Context) error {
		switch {
		case q.fake.Load():
			q.notify(ctx, d, o) // recorded only
			return nil
		case q.sync:
			q.notify(ctx, d, o) // before it runs
			return q.runSync(ctx, jt, id, o.queue, data)
		}
		if err := q.store.Push(ctx, Message{ID: id, Queue: o.queue, Payload: payload}, o.delay); err != nil {
			return fmt.Errorf("queue: dispatch %s: %w", jt.name, err)
		}
		if q.joinsTx(ctx) {
			// Written in the transaction: dispatched if it commits.
			db.AfterCommit(ctx, func(ctx context.Context) { q.notify(ctx, d, o) })
		} else {
			q.notify(ctx, d, o)
		}
		return nil
	}
	if !o.afterCommit || q.joinsTx(ctx) {
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
			q.log.ErrorContext(ctx, "queue: dispatch after commit failed", "job", jt.name, "id", id, "queue", o.queue, "error", err)
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

// joinsTx reports whether the store writes jobs in the transaction of
// ctx, so that waiting for its commit is unnecessary.
func (q *Queue) joinsTx(ctx context.Context) bool {
	s, ok := q.store.(interface{ joinsTx(context.Context) bool })
	return ok && !q.sync && !q.fake.Load() && s.joinsTx(ctx)
}

// runSync runs a job at once, for the sync driver: one attempt, whose
// error is returned.
func (q *Queue) runSync(ctx context.Context, jt *jobType, id, queue string, data []byte) error {
	job, err := jt.decode(data)
	if err != nil {
		return fmt.Errorf("queue: decode %s: %w", jt.name, err)
	}
	info := Info{ID: id, Job: jt.name, Queue: queue, Attempt: 1, Tries: 1}
	err = q.call(ctx, job, info, q.timeout(jt))
	if err != nil {
		q.failed(ctx, job, info, q.timeout(jt), err)
		return fmt.Errorf("queue: %s: %w", jt.name, err)
	}
	return nil
}

// call runs one attempt of job, with its timeout, recovering a panic.
func (q *Queue) call(ctx context.Context, job Job, info Info, timeout time.Duration) (err error) {
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, infoKey{}, info), timeout)
	defer cancel()
	if q.app != nil {
		var end func()
		ctx, end = q.app.StartOperation(ctx, anetos.Operation{Kind: "job", Name: info.Job})
		defer end()
	}
	defer func() {
		if v := recover(); v != nil {
			q.log.ErrorContext(ctx, "queue: job panicked", "job", info.Job, "id", info.ID, "panic", v, "stack", string(debug.Stack()))
			err = fmt.Errorf("panic: %v", v)
		}
	}()
	err = job.Handle(ctx)
	if err != nil && ctx.Err() == context.DeadlineExceeded && errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("timed out after %s: %w", timeout, err)
	}
	return err
}

// failed calls the job's Failed method, if it has one.
func (q *Queue) failed(ctx context.Context, job Job, info Info, timeout time.Duration, cause error) {
	f, ok := job.(Failer)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, infoKey{}, info), timeout)
	defer cancel()
	defer func() {
		if v := recover(); v != nil {
			q.log.ErrorContext(ctx, "queue: Failed panicked", "job", info.Job, "id", info.ID, "panic", v, "stack", string(debug.Stack()))
		}
	}()
	f.Failed(ctx, cause)
}

// Info describes the job running with a context.
type Info struct {
	// ID identifies the job across its attempts: use it to make the job
	// idempotent.
	ID string
	// Job is the job type's name.
	Job string
	// Queue is the queue it came from.
	Queue string
	// Attempt is this attempt's number, from 1.
	Attempt int
	// Tries is the number of attempts the job may have.
	Tries int
}

type infoKey struct{}

// Current returns the job running with ctx, in its Handle and Failed
// methods.
func Current(ctx context.Context) (Info, bool) {
	i, ok := ctx.Value(infoKey{}).(Info)
	return i, ok
}

// permanent wraps an error that fails a job for good.
type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

// Permanent marks the error as permanent, for [IsPermanent].
func (p permanent) Permanent() bool { return true }

// Permanent wraps err so that the job fails for good, without retries:
// for errors that won't go away, such as a record that doesn't exist.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanent{err}
}

// IsPermanent reports whether err, or an error it wraps, has a
// Permanent() bool method returning true: errors from [Permanent], and
// from pubsub.Permanent, so code shared by jobs and listeners can use
// either.
func IsPermanent(err error) bool {
	var p interface{ Permanent() bool }
	return errors.As(err, &p) && p.Permanent()
}

type queueKey struct{}

// WithQueue returns ctx with q, for [Dispatch]. [New] makes the queue
// available in every context the app creates.
func WithQueue(ctx context.Context, q *Queue) context.Context {
	return context.WithValue(ctx, queueKey{}, q)
}

// ErrNoQueue is returned by [From] (and [Dispatch]) when the context
// has no queue.
var ErrNoQueue = errors.New("queue: no queue in the context: call queue.New at startup, or queue.WithQueue")

// From returns the queue in ctx.
func From(ctx context.Context) (*Queue, error) {
	if q, ok := ctx.Value(queueKey{}).(*Queue); ok {
		return q, nil
	}
	return nil, ErrNoQueue
}

var (
	idMu   sync.Mutex
	idLast int64  // the last ID's milliseconds
	idSeq  uint16 // and its counter
)

// newID returns a UUIDv7: IDs made later in this process sort after those
// made earlier, even in the same millisecond (RFC 9562, method 1).
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	idMu.Lock()
	ms := time.Now().UnixMilli()
	if ms <= idLast {
		ms = idLast
		idSeq++
		if idSeq > 0x0fff { // the counter ran out: borrow the next millisecond
			ms++
			idSeq = 0
		}
	} else {
		idSeq = uint16(b[6])<<4&0x0ff0 | uint16(b[7]>>4) // a random start, room to count
		idSeq &= 0x07ff
	}
	idLast = ms
	seq := idSeq
	idMu.Unlock()
	b[0], b[1], b[2], b[3], b[4], b[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	b[6] = 0x70 | byte(seq>>8)
	b[7] = byte(seq)
	b[8] = b[8]&0x3f | 0x80
	var s [36]byte
	hex.Encode(s[0:8], b[0:4])
	s[8] = '-'
	hex.Encode(s[9:13], b[4:6])
	s[13] = '-'
	hex.Encode(s[14:18], b[6:8])
	s[18] = '-'
	hex.Encode(s[19:23], b[8:10])
	s[23] = '-'
	hex.Encode(s[24:], b[10:])
	return string(s[:])
}
