// SPDX-License-Identifier: Apache-2.0

package pubsub_test

import (
	"context"
	"testing"

	"anetos.dev/anetos/pubsub"
)

// countingBroker counts what it is given.
type countingBroker struct {
	*pubsub.MemoryBroker
	n int
}

func (b *countingBroker) Publish(ctx context.Context, topic string, m pubsub.Outgoing) (string, error) {
	b.n++
	return b.MemoryBroker.Publish(ctx, topic, m)
}

func TestObserveAndFake(t *testing.T) {
	ctx := context.Background()
	b := &countingBroker{MemoryBroker: pubsub.NewMemoryBroker()}
	p := pubsub.New(b)
	var seen []pubsub.Published
	p.Observe(func(_ context.Context, m pubsub.Published) { seen = append(seen, m) })
	check(t, p.Publish(ctx, "orders.created", map[string]int{"id": 1}, pubsub.Attributes(map[string]string{"v": "1"})))
	p.Fake()
	check(t, p.Publish(ctx, "orders.created", []byte("raw")))
	var got map[string]int
	if len(seen) != 2 || seen[0].Topic != "orders.created" || seen[0].Attributes["v"] != "1" ||
		seen[0].Decode(&got) != nil || got["id"] != 1 || string(seen[1].Data) != "raw" {
		t.Errorf("seen %+v", seen)
	}
	if b.n != 1 {
		t.Errorf("the broker got %d messages", b.n)
	}
}
