// SPDX-License-Identifier: Apache-2.0

package pubsub

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"
)

// MemoryBroker keeps messages in the process's memory: only listeners in
// the same process get them, and they are lost when it stops. For
// development and tests.
type MemoryBroker struct {
	mu     sync.Mutex
	topics map[string]map[string]*memSub // topic → subscription name →
	seq    int64
	closed bool
}

type memSub struct {
	msgs []*memMsg
	wake chan struct{} // a message became available
}

type memMsg struct {
	m         Message
	available time.Time // or, while in flight, when the delivery's lease ends
	delivery  int64     // the current delivery
}

// NewMemoryBroker returns an empty broker.
func NewMemoryBroker() *MemoryBroker {
	return &MemoryBroker{topics: map[string]map[string]*memSub{}}
}

// errClosed is returned by a closed memory broker.
var errClosed = errors.New("pubsub: the broker is closed")

// sub returns (creating it) the subscription; b.mu is held.
func (b *MemoryBroker) sub(topic, name string) *memSub {
	subs := b.topics[topic]
	if subs == nil {
		subs = map[string]*memSub{}
		b.topics[topic] = subs
	}
	s := subs[name]
	if s == nil {
		s = &memSub{wake: make(chan struct{}, 1)}
		subs[name] = s
	}
	return s
}

func (s *memSub) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Publish implements [Broker].
func (b *MemoryBroker) Publish(_ context.Context, topic string, m Outgoing) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return "", errClosed
	}
	b.seq++
	id := strconv.FormatInt(b.seq, 10)
	now := time.Now()
	for _, s := range b.topics[topic] {
		s.msgs = append(s.msgs, &memMsg{m: Message{ID: id, Topic: topic, Data: slices.Clone(m.Data),
			Attributes: maps.Clone(m.Attributes), PublishedAt: now}, available: now})
		s.signal()
	}
	return id, nil
}

// Prepare implements [Broker].
func (b *MemoryBroker) Prepare(_ context.Context, s SubscriptionSpec) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errClosed
	}
	b.sub(s.Topic, s.Name)
	return nil
}

// next takes the next available message of s, and returns it with its
// delivery number, or when one may become available.
func (b *MemoryBroker) next(s *memSub, lease time.Duration) (*Message, int64, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	wait := 100 * time.Millisecond // other subscribers of s share the wake-up
	for _, mm := range s.msgs {
		if mm.available.After(now) {
			wait = min(wait, mm.available.Sub(now))
			continue
		}
		mm.available = now.Add(lease)
		mm.m.Attempt++
		b.seq++
		mm.delivery = b.seq
		m := mm.m
		m.Data = slices.Clone(m.Data)
		m.Attributes = maps.Clone(m.Attributes)
		return &m, mm.delivery, 0
	}
	return nil, 0, wait
}

// settle applies an outcome to a delivery, if it is still the current one.
func (b *MemoryBroker) settle(s *memSub, delivery int64, o Outcome) {
	b.mu.Lock()
	defer b.mu.Unlock()
	i := slices.IndexFunc(s.msgs, func(mm *memMsg) bool { return mm.delivery == delivery })
	if i < 0 {
		return // delivered again meanwhile
	}
	if o.Ack {
		s.msgs = slices.Delete(s.msgs, i, i+1)
		return
	}
	mm := s.msgs[i]
	mm.delivery = 0
	mm.available = time.Now().Add(max(o.RetryAfter, 0))
	s.signal()
}

// Subscribe implements [Broker].
func (b *MemoryBroker) Subscribe(ctx context.Context, spec SubscriptionSpec, handle func(ctx context.Context, m *Message) Outcome) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errClosed
	}
	s := b.sub(spec.Topic, spec.Name)
	b.mu.Unlock()
	slots := make(chan struct{}, max(spec.Concurrency, 1))
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case slots <- struct{}{}:
		}
		if ctx.Err() != nil {
			<-slots
			return nil
		}
		m, delivery, wait := b.next(s, spec.AckTimeout)
		if m == nil {
			<-slots
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return nil
			case <-s.wake:
			case <-t.C:
			}
			t.Stop()
			continue
		}
		wg.Go(func() {
			defer func() { <-slots }()
			b.settle(s, delivery, handle(ctx, m))
		})
	}
}

// Close implements [Broker]: publishing and subscribing fail afterwards.
func (b *MemoryBroker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return nil
}
