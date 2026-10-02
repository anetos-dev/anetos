// SPDX-License-Identifier: Apache-2.0

package anetostest

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/pubsub"
	"anetos.dev/anetos/queue"
)

// FakeQueue keeps dispatched jobs from running or being stored: they
// are only recorded, for [AssertDispatched] and [Jobs]. Without it, jobs
// are recorded and handled by the queue as usual (QUEUE_DRIVER: with
// sync, they run at once). Queued email and queued event listeners are
// jobs too.
func FakeQueue() Option { return func(o *options) { o.fakeQueue = true } }

// FakeEvents keeps events of the types of the given values (all events,
// with none) from reaching their listeners (a pointer type and its
// value type are different event types): they are only recorded, for
// [AssertEmitted] and [Events]. Without it, events are recorded and
// delivered as usual.
//
//	app := anetostest.New(t, setup, anetostest.FakeEvents(OrderPlaced{}))
func FakeEvents(events ...any) Option {
	return func(o *options) {
		o.fakeEvents = true
		o.fakedEvents = append(o.fakedEvents, events...)
		if len(events) == 0 {
			o.fakeAllEvents = true
		}
	}
}

// FakePubSub keeps published messages from reaching the broker: they
// are only recorded, for [AssertPublished] and [Messages]. Without it,
// messages are recorded and published as usual.
func FakePubSub() Option { return func(o *options) { o.fakePubSub = true } }

// recorder keeps what the app dispatched, emitted, mailed and published.
type recorder struct {
	mu       sync.Mutex
	jobs     []queue.Dispatched
	events   []any
	mail     []mailer.Record
	messages []pubsub.Published
	repeated []db.RepeatedQuery
}

// record observes the app's services, and fakes those the options say.
func (a *App) record(o *options) {
	a.rec = &recorder{}
	app, r := a.App, a.rec
	q, err := anetos.Resolve[*queue.Queue](app)
	switch {
	case err == nil:
		q.Observe(func(_ context.Context, d queue.Dispatched) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.jobs = append(r.jobs, d)
		})
		if o.fakeQueue {
			q.Fake()
		}
	case o.fakeQueue:
		a.t.Fatalf("anetostest: FakeQueue: the app has no queue (queue.ForApp in setup)")
	}
	bus, err := anetos.Resolve[*events.Bus](app)
	switch {
	case err == nil:
		bus.Observe(func(_ context.Context, e any) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.events = append(r.events, e)
		})
		if slices.Contains(o.fakedEvents, nil) {
			a.t.Fatalf("anetostest: FakeEvents(nil): pass a value of each event type, such as OrderPlaced{}")
		}
		if o.fakeAllEvents {
			bus.Fake()
		} else if len(o.fakedEvents) > 0 {
			bus.Fake(o.fakedEvents...)
		}
	case o.fakeEvents:
		a.t.Fatalf("anetostest: FakeEvents: the app has no event bus (events.ForApp in setup)")
	}
	if d, err := anetos.Resolve[*db.DB](app); err == nil {
		d.OnRepeatedQuery(func(_ context.Context, q db.RepeatedQuery) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.repeated = append(r.repeated, q)
		})
	}
	if m, err := anetos.Resolve[*mailer.Mailer](app); err == nil {
		m.Observe(func(_ context.Context, rec mailer.Record) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.mail = append(r.mail, rec)
		})
	}
	a.recordAI(o)
	ps, err := anetos.Resolve[*pubsub.PubSub](app)
	switch {
	case err == nil:
		ps.Observe(func(_ context.Context, m pubsub.Published) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.messages = append(r.messages, m)
		})
		if o.fakePubSub {
			ps.Fake()
		}
	case o.fakePubSub:
		a.t.Fatalf("anetostest: FakePubSub: the app has no pub/sub (pubsub.ForApp in setup)")
	}
}

// --- Queue

// Dispatched returns the jobs dispatched so far, oldest first: those of
// every type, function jobs ("mail:send") included.
func (a *App) Dispatched() []queue.Dispatched {
	a.rec.mu.Lock()
	defer a.rec.mu.Unlock()
	return slices.Clone(a.rec.jobs)
}

// AssertNothingDispatched checks that no job was dispatched.
func (a *App) AssertNothingDispatched() *App {
	a.t.Helper()
	if d := a.Dispatched(); len(d) > 0 {
		a.t.Errorf("anetostest: jobs were dispatched: %s", jobNames(d))
	}
	return a
}

// Jobs returns the jobs of type J dispatched so far, oldest first,
// decoded from what was dispatched (as a worker would).
//
//	jobs := anetostest.Jobs[ChargeOrder](app)
func Jobs[J queue.Job](a *App) []J {
	a.t.Helper()
	name := jobName[J](a)
	var out []J
	for _, d := range a.Dispatched() {
		if d.Job != name {
			continue
		}
		var j J
		if err := d.Decode(&j); err != nil {
			a.t.Fatalf("anetostest: decode a %s job: %v", name, err)
		}
		out = append(out, j)
	}
	return out
}

// AssertDispatched checks that a job of type J for which match returns
// true (any, for a nil match) was dispatched.
//
//	anetostest.AssertDispatched(app, func(j ChargeOrder) bool { return j.OrderID == id })
func AssertDispatched[J queue.Job](a *App, match func(J) bool) {
	a.t.Helper()
	if !slices.ContainsFunc(Jobs[J](a), orAny(match)) {
		a.t.Errorf("anetostest: no %s job%s was dispatched; dispatched: %s", jobName[J](a), matching(match), jobNames(a.Dispatched()))
	}
}

// AssertNotDispatched checks that no job of type J for which match
// returns true (none at all, for a nil match) was dispatched.
func AssertNotDispatched[J queue.Job](a *App, match func(J) bool) {
	a.t.Helper()
	if n := countMatch(Jobs[J](a), match); n > 0 {
		a.t.Errorf("anetostest: %d %s job(s)%s were dispatched", n, jobName[J](a), matching(match))
	}
}

func jobName[J queue.Job](a *App) string {
	a.t.Helper()
	q, err := anetos.Resolve[*queue.Queue](a.App)
	if err != nil {
		a.t.Fatalf("anetostest: the app has no queue (queue.ForApp in setup)")
	}
	if reflect.TypeFor[J]().Kind() == reflect.Interface {
		a.t.Fatalf("anetostest: %s is an interface: name a job type, such as ChargeOrder", reflect.TypeFor[J]())
	}
	var zero J
	name, err := q.NameOf(zero)
	if err != nil {
		a.t.Fatalf("anetostest: %v", err)
	}
	return name
}

func jobNames(ds []queue.Dispatched) string {
	if len(ds) == 0 {
		return "none"
	}
	names := make([]string, len(ds))
	for i, d := range ds {
		names[i] = d.Job
	}
	return strings.Join(names, ", ")
}

// --- Events

// Emitted returns the events emitted so far, oldest first.
func (a *App) Emitted() []any {
	a.rec.mu.Lock()
	defer a.rec.mu.Unlock()
	return slices.Clone(a.rec.events)
}

// AssertNothingEmitted checks that no event was emitted.
func (a *App) AssertNothingEmitted() *App {
	a.t.Helper()
	if e := a.Emitted(); len(e) > 0 {
		a.t.Errorf("anetostest: events were emitted: %s", typeNames(e))
	}
	return a
}

// Events returns the events of type E (or, for an interface type, that
// implement it) emitted so far, oldest first.
func Events[E any](a *App) []E {
	var out []E
	for _, e := range a.Emitted() {
		if v, ok := e.(E); ok {
			out = append(out, v)
		}
	}
	return out
}

// AssertEmitted checks that an event of type E for which match returns
// true (any, for a nil match) was emitted.
//
//	anetostest.AssertEmitted(app, func(e OrderPlaced) bool { return e.Cents == 1500 })
func AssertEmitted[E any](a *App, match func(E) bool) {
	a.t.Helper()
	if !slices.ContainsFunc(Events[E](a), orAny(match)) {
		a.t.Errorf("anetostest: no %s event%s was emitted; emitted: %s", reflect.TypeFor[E](), matching(match), typeNames(a.Emitted()))
	}
}

// AssertNotEmitted checks that no event of type E for which match
// returns true (none at all, for a nil match) was emitted.
func AssertNotEmitted[E any](a *App, match func(E) bool) {
	a.t.Helper()
	if n := countMatch(Events[E](a), match); n > 0 {
		a.t.Errorf("anetostest: %d %s event(s)%s were emitted", n, reflect.TypeFor[E](), matching(match))
	}
}

// --- Mail

// Mail returns the emails sent with mailer.Send and queued with
// mailer.Queue so far, oldest first, with their mailables. (The emails
// the transport got, queued ones the queue ran included, are the
// mailer's MemoryTransport's.)
func (a *App) Mail() []mailer.Record {
	a.rec.mu.Lock()
	defer a.rec.mu.Unlock()
	return slices.Clone(a.rec.mail)
}

// AssertNoMail checks that no email was sent or queued.
func (a *App) AssertNoMail() *App {
	a.t.Helper()
	if m := a.Mail(); len(m) > 0 {
		a.t.Errorf("anetostest: emails were sent or queued: %s", mailNames(m))
	}
	return a
}

// Mailables returns the mailables of type M sent or queued so far,
// oldest first.
func Mailables[M mailer.Mailable](a *App) []M {
	var out []M
	for _, r := range a.Mail() {
		if m, ok := r.Mailable.(M); ok {
			out = append(out, m)
		}
	}
	return out
}

// AssertMailSent checks that a mailable of type M for which match
// returns true (any, for a nil match) was sent with mailer.Send.
//
//	anetostest.AssertMailSent(app, func(m mails.Welcome) bool { return m.User.ID == u.ID })
func AssertMailSent[M mailer.Mailable](a *App, match func(M) bool) {
	a.t.Helper()
	assertMail(a, false, match)
}

// AssertMailQueued checks that a mailable of type M for which match
// returns true (any, for a nil match) was queued with mailer.Queue.
func AssertMailQueued[M mailer.Mailable](a *App, match func(M) bool) {
	a.t.Helper()
	assertMail(a, true, match)
}

// AssertMailNotSent checks that no mailable of type M for which match
// returns true (none at all, for a nil match) was sent or queued.
func AssertMailNotSent[M mailer.Mailable](a *App, match func(M) bool) {
	a.t.Helper()
	if n := countMatch(Mailables[M](a), match); n > 0 {
		a.t.Errorf("anetostest: %d %s email(s)%s were sent or queued", n, reflect.TypeFor[M](), matching(match))
	}
}

func assertMail[M mailer.Mailable](a *App, queued bool, match func(M) bool) {
	a.t.Helper()
	ok, other := false, false
	for _, r := range a.Mail() {
		m, isM := r.Mailable.(M)
		if !isM || !orAny(match)(m) {
			continue
		}
		if r.Queued == queued {
			ok = true
		} else {
			other = true
		}
	}
	if ok {
		return
	}
	verb, hint := "sent", ""
	if queued {
		verb = "queued"
	}
	if other {
		hint = " (one was sent with mailer.Send)"
		if !queued {
			hint = " (one was queued with mailer.Queue: AssertMailQueued)"
		}
	}
	a.t.Errorf("anetostest: no %s email%s was %s%s; sent or queued: %s", reflect.TypeFor[M](), matching(match), verb, hint, mailNames(a.Mail()))
}

func mailNames(rs []mailer.Record) string {
	vs := make([]any, len(rs))
	for i, r := range rs {
		vs[i] = r.Mailable
	}
	return typeNames(vs)
}

// --- Pub/sub

// Published returns the messages published so far, oldest first.
func (a *App) Published() []pubsub.Published {
	a.rec.mu.Lock()
	defer a.rec.mu.Unlock()
	return slices.Clone(a.rec.messages)
}

// Messages returns the messages published to topic so far, oldest
// first, decoded from JSON as T.
func Messages[T any](a *App, topic string) []T {
	a.t.Helper()
	var out []T
	for _, m := range a.Published() {
		if m.Topic != topic {
			continue
		}
		var v T
		if err := m.Decode(&v); err != nil {
			a.t.Fatalf("anetostest: decode a message of %s as %s: %v", topic, reflect.TypeFor[T](), err)
		}
		out = append(out, v)
	}
	return out
}

// decoded returns the messages of topic that decode as T, and how many
// didn't.
func decoded[T any](a *App, topic string) (vs []T, bad int) {
	for _, m := range a.Published() {
		if m.Topic != topic {
			continue
		}
		var v T
		if m.Decode(&v) != nil {
			bad++
			continue
		}
		vs = append(vs, v)
	}
	return vs, bad
}

// AssertPublished checks that a message for which match returns true
// (any, for a nil match) was published to topic, decoded as T (messages
// that don't decode as T don't match).
//
//	anetostest.AssertPublished(app, "orders.created", func(m OrderCreated) bool { return m.ID == id })
func AssertPublished[T any](a *App, topic string, match func(T) bool) {
	a.t.Helper()
	vs, bad := decoded[T](a, topic)
	if !slices.ContainsFunc(vs, orAny(match)) {
		topics := make([]string, 0)
		for _, m := range a.Published() {
			topics = append(topics, m.Topic)
		}
		if len(topics) == 0 {
			topics = append(topics, "none")
		}
		undecodable := ""
		if bad > 0 {
			undecodable = fmt.Sprintf(" (%d didn't decode as %s)", bad, reflect.TypeFor[T]())
		}
		a.t.Errorf("anetostest: no message%s was published to %s%s; published to: %s", matching(match), topic, undecodable, strings.Join(topics, ", "))
	}
}

// AssertNotPublished checks that no message for which match returns
// true (none at all, for a nil match) was published to topic, decoded as
// T (messages that don't decode as T don't match).
func AssertNotPublished[T any](a *App, topic string, match func(T) bool) {
	a.t.Helper()
	vs, _ := decoded[T](a, topic)
	if n := countMatch(vs, match); n > 0 {
		a.t.Errorf("anetostest: %d message(s)%s were published to %s", n, matching(match), topic)
	}
}

// --- Repeated queries

// RepeatedQueries returns the queries a request (or job, listener…) of
// the test ran repeatedly (DB_REPEATED_QUERIES times or more, default 5
// in tests), oldest first: each is an N+1 to fix. They are also logged as
// warnings.
func (a *App) RepeatedQueries() []db.RepeatedQuery {
	a.rec.mu.Lock()
	defer a.rec.mu.Unlock()
	return slices.Clone(a.rec.repeated)
}

// AssertNoRepeatedQueries checks that no request (or job, listener…) of
// the test ran a query repeatedly: no N+1.
//
//	app.GetJSON("/posts?with=author").AssertOK()
//	app.AssertNoRepeatedQueries()
func (a *App) AssertNoRepeatedQueries() *App {
	a.t.Helper()
	for _, q := range a.RepeatedQueries() {
		a.t.Errorf("anetostest: %s", q)
	}
	return a
}

// --- helpers

func orAny[T any](match func(T) bool) func(T) bool {
	if match == nil {
		return func(T) bool { return true }
	}
	return match
}

func countMatch[T any](vs []T, match func(T) bool) int {
	n := 0
	for _, v := range vs {
		if orAny(match)(v) {
			n++
		}
	}
	return n
}

func matching[T any](match func(T) bool) string {
	if match == nil {
		return ""
	}
	return " matching"
}

func typeNames(vs []any) string {
	if len(vs) == 0 {
		return "none"
	}
	names := make([]string, len(vs))
	for i, v := range vs {
		names[i] = fmt.Sprintf("%T", v)
	}
	return strings.Join(names, ", ")
}

// excerpt shortens s for a message.
func excerpt(s string) string {
	if len(s) <= 200 {
		return s
	}
	return s[:200] + "…"
}
