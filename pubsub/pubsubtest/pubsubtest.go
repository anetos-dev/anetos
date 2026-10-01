// SPDX-License-Identifier: Apache-2.0

// Package pubsubtest is a conformance suite for pub/sub brokers. Each
// broker runs it:
//
//	func TestBroker(t *testing.T) {
//		pubsubtest.Run(t, func(t *testing.T) pubsub.Broker { return newBroker(t) }, pubsubtest.Features{Attempts: true, Delays: true})
//	}
//
// Each test uses topics and subscriptions of its own.
package pubsubtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos/pubsub"
)

// Features says what the broker does beyond the contract's minimum.
type Features struct {
	// Attempts: Message.Attempt counts deliveries.
	Attempts bool
	// Delays: Outcome.RetryAfter delays the next delivery.
	Delays bool
	// AckTimeout: a message handled for longer than AckTimeout is
	// delivered again (brokers whose clients extend deadlines don't).
	AckTimeout bool
	// Ordered: a subscription gets a topic's messages in the order they
	// were published (with concurrency 1).
	Ordered bool
}

type test struct {
	name string
	fn   func(t *testing.T, b pubsub.Broker, f Features, topic string)
}

// Run runs the suite against the brokers newBroker returns.
func Run(t *testing.T, newBroker func(t *testing.T) pubsub.Broker, f Features) {
	t.Helper()
	for _, tt := range []test{
		{"PublishSubscribe", testPublishSubscribe},
		{"Subscriptions", testSubscriptions},
		{"OnlyAfterPrepare", testOnlyAfterPrepare},
		{"Retry", testRetry},
		{"LongRetry", testLongRetry},
		{"ManyWaiting", testManyWaiting},
		{"SharedRetries", testSharedRetries},
		{"AckTimeout", testAckTimeout},
		{"Concurrency", testConcurrency},
		{"Stop", testStop},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := newBroker(t)
			buf := make([]byte, 4)
			_, _ = rand.Read(buf)
			tt.fn(t, b, f, "pst-"+hex.EncodeToString(buf))
		})
	}
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func spec(topic, name string) pubsub.SubscriptionSpec {
	return pubsub.SubscriptionSpec{Topic: topic, Name: topic + "." + name, Concurrency: 1, AckTimeout: 30 * time.Second}
}

// collector subscribes in the background and records what it gets.
type collector struct {
	mu   sync.Mutex
	msgs []pubsub.Message
	stop context.CancelFunc
	done chan error
}

func subscribe(t *testing.T, b pubsub.Broker, s pubsub.SubscriptionSpec, handle func(m *pubsub.Message) pubsub.Outcome) *collector {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := &collector{stop: cancel, done: make(chan error, 1)}
	go func() {
		c.done <- b.Subscribe(ctx, s, func(_ context.Context, m *pubsub.Message) pubsub.Outcome {
			c.mu.Lock()
			c.msgs = append(c.msgs, *m)
			c.mu.Unlock()
			if handle != nil {
				return handle(m)
			}
			return pubsub.Outcome{Ack: true}
		})
	}()
	t.Cleanup(func() { c.close(t) })
	return c
}

func (c *collector) close(t *testing.T) {
	t.Helper()
	if c.stop == nil {
		return
	}
	c.stop()
	c.stop = nil
	select {
	case err := <-c.done:
		if err != nil {
			t.Errorf("Subscribe = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("Subscribe didn't return 10s after its context ended")
	}
}

func (c *collector) got() []pubsub.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.msgs)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func publish(t *testing.T, b pubsub.Broker, topic, data string, attrs map[string]string) string {
	t.Helper()
	id, err := b.Publish(context.Background(), topic, pubsub.Outgoing{Data: []byte(data), Attributes: attrs})
	check(t, err)
	if id == "" {
		t.Fatal("Publish returned an empty ID")
	}
	return id
}

func testPublishSubscribe(t *testing.T, b pubsub.Broker, f Features, topic string) {
	s := spec(topic, "a")
	check(t, b.Prepare(context.Background(), s))
	check(t, b.Prepare(context.Background(), s)) // again: fine
	before := time.Now()
	ids := map[string]string{}
	attrsOf := map[string]map[string]string{}
	for i, data := range []string{`{"n":1}`, "héllo \x00 bytes", `{"n":3}`} {
		attrs := map[string]string{"i": fmt.Sprint(i)}
		if i == 1 {
			attrs = nil
		}
		attrsOf[data] = attrs
		ids[data] = publish(t, b, topic, data, attrs)
	}
	c := subscribe(t, b, s, nil)
	eventually(t, "3 messages", func() bool { return len(c.got()) >= 3 })
	time.Sleep(200 * time.Millisecond) // and no more
	got := c.got()
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}
	for i, m := range got {
		data := string(m.Data)
		if ids[data] != m.ID || m.Topic != topic {
			t.Errorf("message %d: ID %q topic %q data %q", i, m.ID, m.Topic, data)
		}
		if want := attrsOf[data]; len(m.Attributes) != len(want) || m.Attributes["i"] != want["i"] {
			t.Errorf("message %q: attributes %v, want %v", data, m.Attributes, want)
		}
		if f.Attempts && m.Attempt != 1 {
			t.Errorf("message %d: attempt %d, want 1", i, m.Attempt)
		}
		if d := m.PublishedAt.Sub(before); d < -5*time.Second || d > 30*time.Second {
			t.Errorf("message %d: PublishedAt %s, want about %s", i, m.PublishedAt, before)
		}
	}
	if f.Ordered && !slices.EqualFunc(got, []string{`{"n":1}`, "héllo \x00 bytes", `{"n":3}`}, func(m pubsub.Message, d string) bool { return string(m.Data) == d }) {
		t.Errorf("order: %+v", got)
	}
	// Acknowledged messages don't come back.
	c.close(t)
	c2 := subscribe(t, b, s, nil)
	time.Sleep(500 * time.Millisecond)
	if n := len(c2.got()); n != 0 {
		t.Errorf("after acknowledging, %d message(s) came back", n)
	}
}

func testSubscriptions(t *testing.T, b pubsub.Broker, _ Features, topic string) {
	a, bb := spec(topic, "a"), spec(topic, "b")
	check(t, b.Prepare(context.Background(), a))
	check(t, b.Prepare(context.Background(), bb))
	ca := subscribe(t, b, a, nil)
	// Two listeners of one subscription share its messages.
	cb1 := subscribe(t, b, bb, nil)
	cb2 := subscribe(t, b, bb, nil)
	for i := range 10 {
		publish(t, b, topic, fmt.Sprint(i), nil)
	}
	eventually(t, "every message to each subscription", func() bool {
		return len(ca.got()) >= 10 && len(cb1.got())+len(cb2.got()) >= 10
	})
	time.Sleep(200 * time.Millisecond)
	if n := len(ca.got()); n != 10 {
		t.Errorf("subscription a got %d messages, want 10", n)
	}
	seen := map[string]int{}
	for _, m := range append(cb1.got(), cb2.got()...) {
		seen[string(m.Data)]++
	}
	if len(seen) != 10 {
		t.Errorf("subscription b got %d distinct messages, want 10", len(seen))
	}
	for d, n := range seen {
		if n != 1 {
			t.Errorf("subscription b got message %s %d times", d, n)
		}
	}
}

func testOnlyAfterPrepare(t *testing.T, b pubsub.Broker, _ Features, topic string) {
	s := spec(topic, "late")
	other := spec(topic, "other") // the topic exists, but not s
	check(t, b.Prepare(context.Background(), other))
	publish(t, b, topic, "before", nil)
	check(t, b.Prepare(context.Background(), s))
	publish(t, b, topic, "after", nil)
	c := subscribe(t, b, s, nil)
	eventually(t, "the message", func() bool { return len(c.got()) >= 1 })
	time.Sleep(300 * time.Millisecond)
	if got := c.got(); len(got) != 1 || string(got[0].Data) != "after" {
		t.Errorf("got %+v, want only the message published after Prepare", got)
	}
}

func testRetry(t *testing.T, b pubsub.Broker, f Features, topic string) {
	s := spec(topic, "retry")
	check(t, b.Prepare(context.Background(), s))
	var n atomic.Int32
	var mu sync.Mutex
	var times [3]time.Time
	c := subscribe(t, b, s, func(*pubsub.Message) pubsub.Outcome {
		mu.Lock()
		i := n.Load() + 1
		if i <= 3 {
			times[i-1] = time.Now()
		}
		n.Store(i)
		mu.Unlock()
		if i == 1 {
			return pubsub.Outcome{RetryAfter: 500 * time.Millisecond}
		}
		return pubsub.Outcome{Ack: true}
	})
	publish(t, b, topic, "again", nil)
	eventually(t, "the second delivery", func() bool { return n.Load() >= 2 })
	got := c.got()
	mu.Lock()
	d := times[1].Sub(times[0])
	mu.Unlock()
	if f.Delays && d < 400*time.Millisecond {
		t.Errorf("redelivered %s after the nack, want about 500ms", d)
	}
	if got[0].ID != got[1].ID || string(got[1].Data) != "again" {
		t.Errorf("redelivered %+v, want %+v", got[1], got[0])
	}
	if f.Attempts && (got[0].Attempt != 1 || got[1].Attempt != 2) {
		t.Errorf("attempts %d, %d; want 1, 2", got[0].Attempt, got[1].Attempt)
	}
	time.Sleep(300 * time.Millisecond)
	if n.Load() != 2 {
		t.Errorf("%d deliveries, want 2", n.Load())
	}
}

// testLongRetry checks a retry delay longer than the ack timeout.
func testLongRetry(t *testing.T, b pubsub.Broker, f Features, topic string) {
	if !f.Delays {
		t.Skip("the broker doesn't delay deliveries")
	}
	s := spec(topic, "long")
	s.AckTimeout = 300 * time.Millisecond
	check(t, b.Prepare(context.Background(), s))
	var mu sync.Mutex
	var times []time.Time
	var attempts []int
	subscribe(t, b, s, func(m *pubsub.Message) pubsub.Outcome {
		mu.Lock()
		defer mu.Unlock()
		times = append(times, time.Now())
		attempts = append(attempts, m.Attempt)
		if len(times) == 1 {
			return pubsub.Outcome{RetryAfter: 1500 * time.Millisecond}
		}
		return pubsub.Outcome{Ack: true}
	})
	publish(t, b, topic, "later", nil)
	eventually(t, "the second delivery", func() bool { mu.Lock(); defer mu.Unlock(); return len(times) >= 2 })
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if d := times[1].Sub(times[0]); d < 1400*time.Millisecond {
		t.Errorf("redelivered %s after the nack, want about 1.5s (beyond the ack timeout)", d)
	}
	if len(times) != 2 {
		t.Errorf("%d deliveries, want 2", len(times))
	}
	if f.Attempts && (attempts[0] != 1 || attempts[1] != 2) {
		t.Errorf("attempts %v, want [1 2]: waiting must not count as deliveries", attempts)
	}
}

// testManyWaiting checks that many messages waiting to be retried don't
// delay one whose retry time comes first.
func testManyWaiting(t *testing.T, b pubsub.Broker, f Features, topic string) {
	if !f.Delays {
		t.Skip("the broker doesn't delay deliveries")
	}
	s := spec(topic, "many")
	s.AckTimeout = 300 * time.Millisecond
	check(t, b.Prepare(context.Background(), s))
	var mu sync.Mutex
	var failedAt, again time.Time
	subscribe(t, b, s, func(m *pubsub.Message) pubsub.Outcome {
		mu.Lock()
		defer mu.Unlock()
		if string(m.Data) != "soon" {
			return pubsub.Outcome{RetryAfter: time.Hour}
		}
		if failedAt.IsZero() {
			failedAt = time.Now()
			return pubsub.Outcome{RetryAfter: 500 * time.Millisecond}
		}
		again = time.Now()
		return pubsub.Outcome{Ack: true}
	})
	for range 40 {
		publish(t, b, topic, "later", nil)
	}
	time.Sleep(500 * time.Millisecond) // they all wait an hour
	publish(t, b, topic, "soon", nil)
	eventually(t, "the redelivery", func() bool { mu.Lock(); defer mu.Unlock(); return !again.IsZero() })
	mu.Lock()
	defer mu.Unlock()
	if d := again.Sub(failedAt); d > 3*time.Second {
		t.Errorf("redelivered after %s, want about 500ms despite 40 messages waiting", d)
	}
}

// testSharedRetries has several listeners share a subscription, each
// message failing once: each must be delivered exactly twice.
func testSharedRetries(t *testing.T, b pubsub.Broker, f Features, topic string) {
	if !f.Delays {
		t.Skip("the broker doesn't delay deliveries")
	}
	s := spec(topic, "shared")
	s.Concurrency = 4
	s.AckTimeout = 5 * time.Second // no redelivery for slowness
	check(t, b.Prepare(context.Background(), s))
	const n = 200
	var mu sync.Mutex
	got := map[string]int{}
	attempts := map[string][]int{}
	for range 5 {
		subscribe(t, b, s, func(m *pubsub.Message) pubsub.Outcome {
			mu.Lock()
			got[string(m.Data)]++
			attempts[string(m.Data)] = append(attempts[string(m.Data)], m.Attempt)
			first := got[string(m.Data)] == 1
			mu.Unlock()
			if first {
				return pubsub.Outcome{RetryAfter: 150 * time.Millisecond}
			}
			return pubsub.Outcome{Ack: true}
		})
	}
	for i := range n {
		publish(t, b, topic, fmt.Sprint(i), nil)
	}
	eventually(t, "every message twice", func() bool {
		mu.Lock()
		defer mu.Unlock()
		done := 0
		for _, c := range got {
			if c >= 2 {
				done++
			}
		}
		return done == n
	})
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	for d, c := range got {
		if c != 2 {
			t.Errorf("message %s delivered %d times, want 2 (attempts %v)", d, c, attempts[d])
		} else if f.Attempts && (attempts[d][0] != 1 || attempts[d][1] != 2) {
			t.Errorf("message %s: attempts %v, want [1 2]", d, attempts[d])
		}
	}
}

func testAckTimeout(t *testing.T, b pubsub.Broker, f Features, topic string) {
	if !f.AckTimeout {
		t.Skip("the broker's client extends ack deadlines")
	}
	s := spec(topic, "slow")
	s.AckTimeout = 500 * time.Millisecond
	s.Concurrency = 2
	check(t, b.Prepare(context.Background(), s))
	release := make(chan struct{})
	var n atomic.Int32
	c := subscribe(t, b, s, func(*pubsub.Message) pubsub.Outcome {
		if n.Add(1) == 1 {
			<-release // outlives its ack timeout
		}
		return pubsub.Outcome{Ack: true}
	})
	publish(t, b, topic, "slow", nil)
	eventually(t, "the redelivery", func() bool { return n.Load() >= 2 })
	close(release)
	got := c.got()
	if got[0].ID != got[1].ID {
		t.Errorf("redelivered another message")
	}
	if f.Attempts && got[1].Attempt != 2 {
		t.Errorf("the redelivery's attempt = %d, want 2", got[1].Attempt)
	}
}

func testConcurrency(t *testing.T, b pubsub.Broker, _ Features, topic string) {
	s := spec(topic, "pool")
	s.Concurrency = 3
	check(t, b.Prepare(context.Background(), s))
	var running, peak atomic.Int32
	release := make(chan struct{})
	c := subscribe(t, b, s, func(*pubsub.Message) pubsub.Outcome {
		n := running.Add(1)
		defer running.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		<-release
		return pubsub.Outcome{Ack: true}
	})
	for i := range 8 {
		publish(t, b, topic, fmt.Sprint(i), nil)
	}
	eventually(t, "3 at once", func() bool { return running.Load() == 3 })
	time.Sleep(300 * time.Millisecond)
	if p := peak.Load(); p != 3 {
		t.Errorf("peak %d, want 3", p)
	}
	close(release)
	eventually(t, "all 8", func() bool { return len(c.got()) >= 8 })
}

func testStop(t *testing.T, b pubsub.Broker, _ Features, topic string) {
	s := spec(topic, "stop")
	check(t, b.Prepare(context.Background(), s))
	started := make(chan struct{})
	var finished atomic.Bool
	c := subscribe(t, b, s, func(*pubsub.Message) pubsub.Outcome {
		close(started)
		time.Sleep(300 * time.Millisecond)
		finished.Store(true)
		return pubsub.Outcome{Ack: true}
	})
	publish(t, b, topic, "x", maps.Clone(map[string]string{"k": "v"}))
	<-started
	c.close(t) // returns only once the handler has
	if !finished.Load() {
		t.Error("Subscribe returned while a message was being handled")
	}
}
