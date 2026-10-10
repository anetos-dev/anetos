---
title: Pub/sub listeners
since: v0.2.0
group: "Background work"
weight: 404
---

# Pub/sub listeners

Listen to message streams other services publish, such as
`orders.created`, and publish your own, from the same binary as your web
app. Where the [queue](queues.md) runs an app's own jobs, pub/sub
connects services: each subscription gets every message of a topic. The
complete app is [`examples/pubsub`](../../../examples/pubsub), a billing
service.

## Before you start

Choose the broker with `PUBSUB_DRIVER`:

| Driver | Broker | Use it for |
|---|---|---|
| `memory` (default) | In the process | Development and tests |
| `redis` | Redis Streams, Redis 7 or later (`drivers/redis`, `REDIS_URL`) | Services sharing a Redis server |
| `gcp` | Google Cloud Pub/Sub (`drivers/gcppubsub`, `PUBSUB_GCP_PROJECT`) | Services on Google Cloud |

Words used here:

- A **topic** is a named stream of messages: `orders.created`.
- A **subscription** is a named reader of a topic. Every subscription gets
  every message published after it was created; the processes listening
  with one subscription share its messages. By default a listener's
  subscription is named after the topic and your app (`APP_NAME`):
  `orders.created.billing`.

## Steps

### 1. Define the messages

Messages are JSON. Define a struct for each topic you read or write:

```go
// OrderCreated is what the shop publishes to "orders.created".
type OrderCreated struct {
	OrderID  int64  `json:"order_id"`
	Customer string `json:"customer"`
	Cents    int64  `json:"cents"`
}

// InvoiceCreated is what billing publishes to "invoices.created".
type InvoiceCreated struct {
	InvoiceID int64 `json:"invoice_id"`
	OrderID   int64 `json:"order_id"`
}
```

(Copied from [`examples/pubsub/billing.go`](../../../examples/pubsub/billing.go), region `messages`.)

### 2. Write a listener

A listener is a function of the message:

```go
// CreateInvoice bills an order. A message may arrive more than once, so
// an order that has its invoice is skipped.
func CreateInvoice(ctx context.Context, o OrderCreated) error {
	if o.OrderID <= 0 || o.Cents <= 0 {
		return pubsub.Permanent(fmt.Errorf("invalid order: %+v", o)) // straight to the dead-letter topic
	}
	return db.Tx(ctx, func(ctx context.Context) error {
		billed, err := db.Query[Invoice](ctx).Where(colOrderID.Eq(o.OrderID)).Exists()
		if err != nil || billed {
			return err
		}
		inv := &Invoice{OrderID: o.OrderID, Customer: o.Customer, Cents: o.Cents}
		if err := db.Create(ctx, inv); err != nil {
			return err // another delivery won the race: retried, then skipped
		}
		return pubsub.Publish(ctx, "invoices.created", InvoiceCreated{InvoiceID: inv.ID, OrderID: o.OrderID}, pubsub.AfterCommit())
	})
}
```

(Copied from [`examples/pubsub/billing.go`](../../../examples/pubsub/billing.go), region `listener`.)

- **Return nil** to acknowledge the message: it's done.
- **Return an error** to have it delivered again, after a backoff.
- **Return `pubsub.Permanent(err)`** to give up on it at once, for
  messages that will never work, such as malformed ones. A message that
  isn't valid JSON for the type fails this way too.
- **`ctx`** has the app's values (the database, the queue…), and
  `pubsub.Current(ctx)` returns the message: its ID, attributes and
  attempt.
- Take `[]byte` (or `json.RawMessage`) instead of a struct to get the
  body as it is.
- `queue.Permanent` works too, for code shared with queue jobs.

> **Warning:** Delivery is **at-least-once**: a message can arrive twice,
> for example if a listener stops after handling it but before
> acknowledging it. Make listeners idempotent, as the example does with a
> unique index and a check.

### 3. Listen

In `setup`:

```go
ps, err := pubsub.New(app, redis.PubSubDriver(), gcppubsub.Driver()) // PUBSUB_DRIVER: memory, redis or gcp
if err != nil {
	return nil, err
}
err = pubsub.Listen(ps, "orders.created", CreateInvoice,
	pubsub.Concurrency(8),                   // messages at once, in each process
	pubsub.MaxAttempts(5),                   // then...
	pubsub.DeadLetter("orders.created.dlq"), // ...to this topic
)
if err != nil {
	return nil, err
}
```

(Copied from [`examples/pubsub`](../../../examples/pubsub/main.go), region `setup`.)

Pass only the drivers you use: each is a module with its own
dependencies.

| Option | Default | |
|---|---|---|
| `pubsub.Subscription(name)` | `<topic>.<APP_NAME>` | The subscription's name |
| `pubsub.Concurrency(n)` | 1 | Messages handled at once, in each process. The listener stops taking messages while that many are being handled |
| `pubsub.Timeout(d)` | 1m | How long one message may take. The broker may deliver it again 30s after that |
| `pubsub.MaxAttempts(n)` | 0: no limit | Deliveries before giving up on a message |
| `pubsub.Backoff(d1, d2, …)` | 10s, doubling up to 10m | Waits before deliveries after failures; the last repeats. Each varies by up to 20% |
| `pubsub.DeadLetter(topic)` | none | Where messages given up on go. Without one, they are dropped and logged |
| `pubsub.ShutdownGrace(d)` | Half of `APP_SHUTDOWN_TIMEOUT` | How long messages being handled may finish at shutdown |

If publishing to the dead-letter topic fails, the listener tries again
for a few seconds, then leaves the message to be delivered again. A
message sent to the dead-letter topic keeps its body and attributes,
plus `anetos.topic`, `anetos.subscription`, `anetos.error` and
`anetos.attempts`. Listen to the dead-letter topic like any other to
inspect or replay them.

The listener runs with the app (`go run .`) as a component with the role
`listeners`. You can run it apart:

```bash
./billing run --only=listeners
```

The subscription is created when the app boots (for Redis and the
memory broker, and for Google with `PUBSUB_GCP_CREATE=true`), so messages
published after the first deploy are kept even before a listener runs.

### 4. Publish

```go
// illustrative
err := pubsub.Publish(ctx, "invoices.created", InvoiceCreated{InvoiceID: inv.ID})
```

The message is encoded as JSON; byte slices (`[]byte`, `json.RawMessage`)
are sent as they are. `pubsub.Attributes(map[string]string{…})` sends string pairs
with it. Inside a transaction, pass `pubsub.AfterCommit()`, as the
listener above does, so that the message is published only once the
transaction commits. Publish has returned by then, so if publishing
fails then, the failure is only logged: when a message must not be lost,
publish it from a [queue job](queues.md) (retried) dispatched in the
transaction.

To publish by hand (or replay a dead letter), with a broker other than
`memory` (whose messages never leave their process):

```bash
./billing pubsub:publish orders.created '{"order_id":1,"customer":"Ada","cents":1500}'
```

### 5. Test

With the memory broker, run the listeners with `app.Run` and publish:

```go
// The listener runs with app.Run, as in production, with the memory
// broker (PUBSUB_DRIVER's default). The test publishes what the shop
// would, and checks what billing did.
func TestCreateInvoice(t *testing.T) {
	app := anetostest.New(t, setup)
	ps := anetos.MustResolve[*pubsub.PubSub](app.App)
	invoices := listen(t, ps, "invoices.created")
	dead := listen(t, ps, "orders.created.dlq")

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, "listeners") }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	order := OrderCreated{OrderID: 1, Customer: "Ada", Cents: 1500}
	for _, o := range []OrderCreated{order, order, {OrderID: 2}} { // a duplicate, and an invalid order
		if err := pubsub.Publish(app.Context(), "orders.created", o); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { return len(invoices()) == 1 && len(dead()) == 1 })

	var created InvoiceCreated
	if err := json.Unmarshal(invoices()[0].Data, &created); err != nil || created.OrderID != 1 {
		t.Errorf("invoices.created: %+v, %v", created, err)
	}
	if got := dead()[0].Attributes["anetos.error"]; got != "invalid order: {OrderID:2 Customer: Cents:0}" {
		t.Errorf("dead letter error: %q", got)
	}
	app.Get("/invoices").AssertOK().AssertJSONPath("0.customer", "Ada")
	anetostest.AssertDatabaseCount[Invoice](app, 1)
}
```

(Copied from [`examples/pubsub/main_test.go`](../../../examples/pubsub/main_test.go), region `test`.)

Listeners use their own database connections, outside the test's
transaction: test them with in-memory SQLite (as here) or
`anetostest.WithoutTransaction()`. With Redis, `anetostest` gives each
test app its own `PUBSUB_PREFIX` and removes its streams when the test
ends.

To check what the app publishes, `anetostest` records every message:
`anetostest.AssertPublished(app, "orders.created", func(m OrderCreated) bool { … })`.
With `anetostest.FakePubSub()`, messages are only recorded, not sent to
the broker; see [Test your app](testing.md#6-check-jobs-events-emails-and-messages).

## How it works

**Redis Streams.** A topic is a stream (key `PUBSUB_PREFIX` + topic) and a
subscription a consumer group, created at the stream's end. A listener
reads new messages with `XREADGROUP`. A message it fails stays pending,
with its retry time in a sorted set next to the stream, and is claimed
again (`XAUTOCLAIM`) once that time has come; those of a listener that
stopped are claimed once their ack timeout (the listener's timeout plus
30s) has passed. `Message.Attempt` is the group's delivery count.
Streams are capped at about `PUBSUB_REDIS_MAXLEN` messages (default
1,000,000): older ones are trimmed even if a slow subscription hasn't read
them, or is retrying them. In a Redis
Cluster, a stream and its retry sets must share a slot: put a hash tag in
`PUBSUB_PREFIX` (`{billing}:`).

**Google Cloud Pub/Sub.** Topic and subscription IDs are the names with
`PUBSUB_PREFIX`, with `%` and the characters Pub/Sub doesn't allow (such
as `:`) escaped as `%XX`; IDs must start with a letter and have 3 to 255
characters. In production, create topics and subscriptions with your
infrastructure tools: the app then needs only to publish and consume
(`roles/pubsub.publisher` and `roles/pubsub.subscriber`), and a missing
subscription shows when its listener starts, in the logs. With
`PUBSUB_GCP_CREATE=true` (development, the emulator), the driver creates
missing ones when the app boots, with a retry policy backing off from 10s
to 10m (that needs the `pubsub.topics.create`, `pubsub.subscriptions.get`
and `pubsub.subscriptions.create` permissions). The client extends a message's deadline while it is
being handled, up to the listener's ack timeout. Two things differ from
Redis:

- A failed message is redelivered as the **subscription's retry policy**
  says, not as `pubsub.Backoff` does.
- Pub/Sub counts deliveries only for subscriptions with a **dead-letter
  policy**: `pubsub.MaxAttempts` needs one. Without one, `Attempt` is 0,
  failed messages are retried until they expire, and the listener logs a
  warning. With one, Pub/Sub's own dead-letter topic also catches messages
  that crash listeners.

Credentials come from Application Default Credentials;
`PUBSUB_EMULATOR_HOST` points the client at the emulator.

**Shutdown.** Listeners stop after the HTTP server and before the queue's
workers. They stop taking messages, give those being handled the grace
period, then cancel them. Those messages aren't given up on, whatever
`MaxAttempts` says: they are delivered again later (and the broker counts
that delivery).

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| Messages published before the first deploy never arrive | A subscription gets the messages published after it was created | Deploy the listener (or create the subscription) before publishing |
| A message is handled twice | At-least-once delivery | Make the listener idempotent |
| The same message keeps failing forever | No `MaxAttempts` (or Google without a dead-letter policy) | Set `MaxAttempts` and a `DeadLetter` topic |
| `NotFound … subscription projects/…` in the listener's logs (Google) | It wasn't created | Create it, or set `PUBSUB_GCP_CREATE=true` in development |
| `NOGROUP` errors (Redis) | The stream was deleted | The listener recreates its group; messages from before are gone |
| Two services share messages instead of each getting all | They use the same subscription name | Give each app its own `APP_NAME`, or `pubsub.Subscription` |

## Next steps

- [Queues](queues.md): background jobs within one app.
- [Events](events.md): in-process events, which can publish to a topic
  from a listener.
- [Runtime supervisor](../concepts/runtime-supervisor.md): roles and
  shutdown stages.

> **Coming from Laravel?** Laravel has no built-in pub/sub consumer: this
> replaces a separate worker process or a package running
> `XREADGROUP` loops. A listener is like a queued job handler that other
> services dispatch to; `pubsub.Publish` is like broadcasting an event to
> other services.
