---
title: Events
since: v0.2.0
---

# Events

Emit an event when something happens, and let listeners react: write an
audit log, email a receipt, update a dashboard. The code that places an
order then doesn't need to know about all of them. Events are typed Go
values, and listeners are functions of their type. The complete app is
[`examples/queue`](../../../examples/queue).

## Before you start

You have an app created with `anetos.New()`. Listeners that run as queue
jobs need the [queue](queues.md) set up first.

## Steps

### 1. Define an event

An event is any type, usually a struct with what listeners need to know:

```go
// OrderPlaced is emitted when an order is saved. Each listener gets the
// value (queued listeners, as JSON), so keep it free of pointers they
// could change.
type OrderPlaced struct {
	OrderID int64  `json:"order_id"`
	Item    string `json:"item"`
	Cents   int64  `json:"cents"`
	Email   string `json:"email"`
}
```

(Copied from [`examples/queue/events.go`](../../../examples/queue/events.go), region `event`.)

### 2. Write listeners

A listener is a function of the event (or a method value):

```go
// recordAudit writes to the audit log in the order's transaction: if it
// fails, the order isn't placed.
func recordAudit(ctx context.Context, e OrderPlaced) error {
	return db.Create(ctx, &AuditEntry{OrderID: e.OrderID, Message: "placed: " + e.Item})
}

// countSale updates the sales counts in the background, once the order
// is committed. If the process stops first, the count is lost: fine for
// a dashboard.
func (s *Sales) countSale(_ context.Context, e OrderPlaced) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Orders++
	s.Cents += e.Cents
	return nil
}

// emailReceipt sends the receipt, as a queue job: retried if the mail
// server fails, and not lost if the process stops. It skips addresses
// Postmark stopped sending to (the postmark plugin's list).
func emailReceipt(ctx context.Context, e OrderPlaced) error {
	if bad, err := postmark.Suppressed(ctx, e.Email); err != nil || bad {
		return err
	}
	return mailer.Send(ctx, ReceiptMail{Order: e})
}
```

(Copied from [`examples/queue/events.go`](../../../examples/queue/events.go), region `listeners`.)

### 3. Add the listeners

In `setup`, after the services listeners use (`db.Connect`,
`queue.ForApp`, …), so the bus is closed before them at shutdown:

```go
bus, err := events.ForApp(app) // after queue.ForApp: queued listeners use the app's queue
if err != nil {
	return nil, err
}
if err := events.On(bus, recordAudit); err != nil {
	return nil, err
}
if err := events.OnAsync(bus, sales.countSale, events.Name("count-sale")); err != nil {
	return nil, err
}
if err := events.OnQueued(bus, emailReceipt, events.Job(queue.Tries(10))); err != nil {
	return nil, err
}
```

(Copied from [`examples/queue`](../../../examples/queue/main.go), region `events-setup`.)

Choose how each listener runs:

| | `events.On` | `events.OnAsync` | `events.OnQueued` |
|---|---|---|---|
| Runs | In `Emit`, with its context | In the background, in a pool of goroutines of its own | As a queue job, in a worker |
| Transaction | Emit's: its writes commit or roll back with it | After Emit's transaction commits; never if it rolls back | Dispatched after the commit (with the queue's database driver, written in the transaction) |
| Its error | Stops the other `On` listeners; `Emit` returns it | Logged | Retried, then a failed job (with the sync driver, outside a transaction, returned by `Emit`) |
| If the process stops | — | Events not yet handled are lost | Kept; the job runs later |
| Its context | Emit's | The app's values, and its timeout; nothing of the request | A job's: the app's values and `queue.Current` |
| Use it for | Work that must succeed with the change | Quick, optional work: metrics, cache warming | Work that must happen, maybe slowly: email, other services |

Options:

| Option | For | |
|---|---|---|
| `events.Name(s)` | All | The listener's name in logs. A queued listener's job is `event:<name>`: anonymous and generic functions must have one, and it must stay the same across deploys. Default: the function's name, `main.emailReceipt` |
| `events.Concurrency(n)` | `OnAsync` | Events handled at once (default 1) |
| `events.Buffer(n)` | `OnAsync` | Events that wait for the listener (default 1000). When it is full, `Emit` waits for room, until its context ends (a listener emitting to itself: until its timeout) |
| `events.Timeout(d)` | `OnAsync` | How long one event may take (default 1m) |
| `events.Job(opts…)` | `OnQueued` | `queue.Tries`, `queue.Timeout`, `queue.Backoff` |
| `events.Dispatch(opts…)` | `OnQueued` | `queue.OnQueue`, `queue.Delay` |

> **Warning:** Async listeners are **not durable**: events they haven't
> handled when the process stops (after the shutdown budget) or crashes
> are lost. Use `OnQueued` for anything that must happen.

### 4. Emit events

Call `events.Emit` with a request's (or a job's) context:

```go
// PlaceOrder saves the order and dispatches the job that charges it.
func PlaceOrder(c *web.Ctx, in OrderInput) (web.Responder, error) {
	o := &Order{Item: in.Item, Cents: in.Cents, Email: in.Email, Status: "pending"}
	err := db.Tx(c, func(ctx context.Context) error {
		if err := db.Create(ctx, o); err != nil {
			return err
		}
		if err := events.Emit(ctx, OrderPlaced{OrderID: o.ID, Item: o.Item, Cents: o.Cents, Email: o.Email}); err != nil {
			return err // an On listener failed: no order
		}
		// AfterCommit: no charge for an order that isn't saved. (The
		// database driver writes the job in the transaction instead.)
		return queue.Dispatch(ctx, ChargeOrder{OrderID: o.ID}, queue.OnQueue("payments"), queue.AfterCommit())
	})
	if err != nil {
		return nil, err
	}
	return web.JSON(http.StatusAccepted, o), nil // 202: it is being charged
}
```

(Copied from [`examples/queue`](../../../examples/queue/main.go), region `dispatch`.)

Listeners are found by the event's exact type: emitting `&OrderPlaced{}`
doesn't reach `OrderPlaced` listeners, and interface types can't be
listened to. An event nobody listens to is fine. Every listener gets the
same value: don't change what its pointer, slice or map fields point to,
since async listeners may be reading them.

Async and queued listeners wait for the transaction `Emit` runs in to
commit. A transaction you began yourself and passed in with `db.WithTx`
commits out of the db package's sight: async listeners (and queued ones,
except with the queue's database driver) never get events emitted in it.

`Emit` returns an `On` listener's error, or an error handing the event to
the others, such as an async listener's full buffer. Inside a
transaction, those happen after the commit, so they are logged instead.

### 5. Test

`On` listeners have run when `Emit` returns, and so have queued ones with
`QUEUE_DRIVER=sync`. Async ones run in the background: wait for them with
`bus.Wait`:

```go
// Each kind of listener: the audit entry is written with the order, the
// receipt sent by the queued listener (the sync driver runs it at once),
// and the sales counted in the background: bus.Wait waits for that.
func TestOrderEvents(t *testing.T) {
	fakeGateway(t, &FakeGateway{})
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"QUEUE_DRIVER": "sync"}))
	ordersBefore, _ := sales.Snapshot()

	id := placeOrder(t, app, "Lamp", 4250)

	anetostest.AssertDatabaseHas[AuditEntry](app, db.Col[int64]("order_id").Eq(id))
	if sent := sentMail(app); len(sent) != 1 || sent[0].Subject != fmt.Sprintf("Your receipt for order %d", id) {
		t.Errorf("emails: %+v", sent)
	}
	bus := anetos.MustResolve[*events.Bus](app.App)
	if err := bus.Wait(app.Context()); err != nil {
		t.Fatal(err)
	}
	if orders, _ := sales.Snapshot(); orders != ordersBefore+1 {
		t.Errorf("sales: %d orders, want %d", orders, ordersBefore+1)
	}
	app.Get("/stats").AssertOK()
}
```

(Copied from [`examples/queue/main_test.go`](../../../examples/queue/main_test.go), region `test-events`.)

In a test's transaction (`anetostest` with a SQLite file, PostgreSQL or
MySQL), async and queued listeners get events when the request's
transaction commits, as they would in production. But async listeners
use their own connections, outside the test's transaction: they don't
see its rows (and, with a SQLite file, their writes wait for it and
fail). Test async listeners that use the database with in-memory SQLite
or `anetostest.WithoutTransaction()`. Queued listeners run by the sync
driver are in the test's transaction.

To check that an event was emitted without running its listeners, fake
it: `anetostest.New(t, setup, anetostest.FakeEvents(OrderPlaced{}))`,
then `anetostest.AssertEmitted(app, func(e OrderPlaced) bool { … })`;
see [Test your app](testing.md#6-check-jobs-events-emails-and-messages).

## How it works

`events.On`, `OnAsync` and `OnQueued` add the listener to a map from the
event's type to its listeners; `Emit` looks the type up there. Listeners
are generic functions, so the compiler checks their event type, and
nothing is reflective at `Emit` but that lookup.

Each async listener has its own buffer and goroutines, started the first
time it gets an event, so a slow listener doesn't slow the others' work
(until its buffer is full: then `Emit` waits for it).

At shutdown, after the app's components have stopped, the bus stops
taking events (except from its async listeners, which may emit while they
finish) and gives the async listeners the rest of the shutdown budget;
then the events still waiting are dropped and the listeners' contexts
canceled. `bus.Close` does the same for a bus made with `events.New`.

A queued listener is a queue job registered with `queue.RegisterFunc`,
named `event:<listener name>`, whose payload is the event as JSON. The
workers' app must add the same listeners, as with any job type.

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `no bus in the context` | `events.ForApp` wasn't called, or the context isn't the app's | Call `events.ForApp` in `setup`; emit with a request's or job's context |
| A listener never runs | It listens to another type: `OrderPlaced` vs `*OrderPlaced`, or another package's type of the same name | Emit the type the listener takes |
| `the queued listener … is an anonymous or generic function` | Queued listeners' job names come from their names | Pass `events.Name("…")` |
| `queued listeners need the queue` | `events.OnQueued` before `queue.ForApp` | Set up the queue first |
| Async work missing after a deploy or crash | Async events are lost when the process stops | Use `OnQueued` for work that must happen |
| `has 1000 events waiting` | An async listener can't keep up | Raise `events.Concurrency` or `events.Buffer`, or make it queued |

## Next steps

- [Queues](queues.md): workers, retries and failed jobs, for queued
  listeners.
- [Transactions](transactions.md): `db.AfterCommit`, which async and
  queued listeners use.
- [Send email](mail.md): the receipt `emailReceipt` sends.

> **Coming from Laravel?** An event is an event class, `events.Emit` is
> `event(new OrderPlaced(...))`, and listeners are registered in code
> instead of discovered. `On` is a plain listener, `OnQueued` a listener
> that `implements ShouldQueue` (and, like `ShouldHandleEventsAfterCommit`,
> waits for the commit), and `OnAsync` has no Laravel equivalent: work in
> the background, in the same process, without a queue. Event
> subscribers are functions that call `On` several times.
