// SPDX-License-Identifier: Apache-2.0

package events_test

import (
	"context"
	"sync"
	"testing"

	"anetos.dev/anetos/events"
)

type shipped struct{ ID int }
type paid struct{ ID int }

func TestObserveAndFake(t *testing.T) {
	ctx := context.Background()
	b, _ := newBus(t)
	var mu sync.Mutex
	var seen []any
	var ran []string
	b.Observe(func(_ context.Context, e any) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, e)
	})
	check(t, events.On(b, func(context.Context, shipped) error { ran = append(ran, "shipped"); return nil }))
	check(t, events.On(b, func(context.Context, paid) error { ran = append(ran, "paid"); return nil }))
	check(t, events.Emit(events.WithBus(ctx, b), shipped{1}))
	b.Fake(shipped{})
	check(t, b.Emit(ctx, shipped{2}))
	check(t, b.Emit(ctx, paid{3}))
	b.Fake()
	check(t, b.Emit(ctx, paid{4}))
	if len(seen) != 4 || seen[1] != (shipped{2}) || seen[3] != (paid{4}) {
		t.Errorf("seen %v", seen)
	}
	if len(ran) != 2 || ran[0] != "shipped" || ran[1] != "paid" {
		t.Errorf("listeners ran: %v", ran)
	}
}
