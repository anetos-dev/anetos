// SPDX-License-Identifier: Apache-2.0

// Package factory makes model values for tests and seeders:
//
//	var Posts = factory.New(func(n int) models.Post {
//		return models.Post{Title: fmt.Sprintf("Post %d", n), Body: "Lorem ipsum."}
//	})
//
//	post, err := Posts.Create(ctx)                        // one row
//	drafts, err := Posts.With(func(p *models.Post) {
//		p.PublishedAt = nil
//	}).CreateMany(ctx, 3)                                  // three rows
//	unsaved := Posts.Make()                                // a value, not saved
//
// The definition gets a sequence number, unique per factory and increasing
// from 1, for values that must differ (emails, slugs). Factories are
// immutable: With returns a new factory, so a base factory can be shared.
// A Factory is safe for concurrent use.
package factory

import (
	"context"
	"slices"
	"sync/atomic"

	"anetos.dev/anetos/db"
)

// Factory makes values of model T.
type Factory[T any] struct {
	def    func(n int) T
	states []func(*T)
	seq    *atomic.Int64
}

// New returns a factory that makes values with def.
func New[T any](def func(n int) T) *Factory[T] {
	return &Factory[T]{def: def, seq: new(atomic.Int64)}
}

// With returns a factory that also applies fns, in order, to every value
// it makes: a state ("unpublished") or specific values for one test.
func (f *Factory[T]) With(fns ...func(*T)) *Factory[T] {
	g := *f
	g.states = append(slices.Clip(f.states), fns...)
	return &g
}

// Make returns a new value, not saved.
func (f *Factory[T]) Make() T {
	v := f.def(int(f.seq.Add(1)))
	for _, fn := range f.states {
		fn(&v)
	}
	return v
}

// MakeMany returns n new values, not saved.
func (f *Factory[T]) MakeMany(n int) []T {
	out := make([]T, n)
	for i := range out {
		out[i] = f.Make()
	}
	return out
}

// Create makes a value and inserts it with db.Create (so hooks run and
// the key and timestamps are set) using the database in ctx.
func (f *Factory[T]) Create(ctx context.Context) (T, error) {
	v := f.Make()
	err := db.Create(ctx, &v)
	return v, err
}

// CreateMany makes and inserts n values, one db.Create each (so every
// value gets its key on every database).
func (f *Factory[T]) CreateMany(ctx context.Context, n int) ([]T, error) {
	out := make([]T, 0, n)
	for range n {
		v, err := f.Create(ctx)
		if err != nil {
			return out, err
		}
		out = append(out, v)
	}
	return out, nil
}
