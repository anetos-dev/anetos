// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"context"
	"fmt"
	"regexp"
	"sync"
)

// Carrier moves a value from the context of work that hands off to the
// context of the work it starts: a queue job keeps it from the code that
// dispatched it, an async event listener from the code that emitted the
// event. Package audit carries the actor this way, so a job's changes are
// attributed to the user who asked for them.
//
// Carried values travel as text, in the queue's storage among other
// places: don't carry secrets.
type Carrier struct {
	// Name identifies the value: lowercase letters, digits and . _ -, up
	// to 50 characters, unique in the app.
	Name string
	// Capture returns the value to carry from ctx, "" for none.
	Capture func(ctx context.Context) string
	// Restore returns ctx with the value restored.
	Restore func(ctx context.Context, value string) context.Context
}

type carriers struct {
	carryMu sync.RWMutex
	carry   []Carrier
}

var carrierName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,49}$`)

// AddCarrier adds a value that queue jobs and async event listeners carry
// from the work that started them. It panics if the carrier is invalid or
// its name is taken, which are programming errors.
func (a *App) AddCarrier(c Carrier) {
	if !carrierName.MatchString(c.Name) || c.Capture == nil || c.Restore == nil {
		panic(fmt.Sprintf("anetos: invalid carrier %q: it needs a name (lowercase letters, digits and . _ -, up to 50 characters), Capture and Restore", c.Name))
	}
	a.carryMu.Lock()
	defer a.carryMu.Unlock()
	for _, have := range a.carry {
		if have.Name == c.Name {
			panic(fmt.Sprintf("anetos: carrier %q added twice", c.Name))
		}
	}
	a.carry = append(a.carry, c)
}

// Carried returns the values the app's carriers capture from ctx, nil if
// there are none. Packages that hand work off (queue, events) call it, and
// [App.WithCarried] where the work runs.
func (a *App) Carried(ctx context.Context) map[string]string {
	a.carryMu.RLock()
	cs := a.carry
	a.carryMu.RUnlock()
	var out map[string]string
	for _, c := range cs {
		if v := c.Capture(ctx); v != "" {
			if out == nil {
				out = make(map[string]string, len(cs))
			}
			out[c.Name] = v
		}
	}
	return out
}

// WithCarried returns ctx with the carried values restored by their
// carriers. Values no carrier of the app knows are ignored.
func (a *App) WithCarried(ctx context.Context, values map[string]string) context.Context {
	if len(values) == 0 {
		return ctx
	}
	a.carryMu.RLock()
	cs := a.carry
	a.carryMu.RUnlock()
	for _, c := range cs {
		if v, ok := values[c.Name]; ok && v != "" {
			ctx = c.Restore(ctx, v)
		}
	}
	return ctx
}
