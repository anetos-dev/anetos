// SPDX-License-Identifier: Apache-2.0

package inertia

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"
)

// Prop is a page prop that Inertia treats specially: computed only when
// the response includes it, left out of the first visit (optional and
// deferred props), merged with the value the page holds, or loaded once
// and remembered by the client. A props struct declares it as a field,
//
//	Stats inertia.Prop[Stats] `json:"stats"`
//
// and the handler sets it with one of [Value], [Func], [Always],
// [Optional], [Defer], [Merge] or [Once], whose result its methods
// refine (inertia.Defer(h.stats).Group("charts")). A Prop the handler
// doesn't set is left out of the page. A plain field (Posts []Post) is a
// regular prop.
type Prop[T any] struct{ s *spec }

// spec is a Prop's behavior, the same whatever its T.
type spec struct {
	get                        func(context.Context) (any, error)
	always, optional, deferred bool
	once, fresh, rescue        bool
	merge                      bool // Merge's: appended, at the root unless Append or Prepend says where
	group                      string
	appendAt, prependAt        []string // merge paths inside the prop; "" is the prop itself
	deep                       bool
	matchOn                    []string
	onceKey                    string
	expiresAt                  time.Time
	expiresIn                  time.Duration
}

// propValue is what a Prop of any T is to the renderer.
type propValue interface {
	propSpec() *spec
	valueType() reflect.Type
}

func (p Prop[T]) propSpec() *spec { return p.s }

func (Prop[T]) valueType() reflect.Type { return reflect.TypeFor[T]() }

func valueGetter[T any](v T) func(context.Context) (any, error) {
	return func(context.Context) (any, error) { return v, nil }
}

func funcGetter[T any](fn func(context.Context) (T, error)) func(context.Context) (any, error) {
	if fn == nil {
		return func(context.Context) (any, error) { return nil, errors.New("the prop's function is nil") }
	}
	return func(ctx context.Context) (any, error) { return fn(ctx) }
}

// Value is a regular prop with value v, for the methods that refine it
// (inertia.Value(user).Once()).
func Value[T any](v T) Prop[T] { return Prop[T]{&spec{get: valueGetter(v)}} }

// Func is a regular prop whose value fn computes, only when the response
// includes the prop: a partial reload that leaves it out doesn't run fn.
// fn gets the request's context.
func Func[T any](fn func(ctx context.Context) (T, error)) Prop[T] {
	return Prop[T]{&spec{get: funcGetter(fn)}}
}

// Always is a prop sent with every response, even a partial reload that
// asks for other props.
func Always[T any](v T) Prop[T] { return Prop[T]{&spec{get: valueGetter(v), always: true}} }

// Optional is a prop the first visit leaves out, and doesn't announce:
// fn runs only for a partial reload that asks for it
// (router.reload({ only: ['users'] })).
func Optional[T any](fn func(ctx context.Context) (T, error)) Prop[T] {
	return Prop[T]{&spec{get: funcGetter(fn), optional: true}}
}

// Defer is a prop the first visit leaves out and announces: the client
// then asks for it in a partial reload of its own, after the page shows,
// so a slow fn doesn't hold the page. Deferred props of one group
// ([Prop.Group], "default" otherwise) come in one request. With
// [Prop.Rescue], fn's error leaves the prop out instead of failing the
// response.
func Defer[T any](fn func(ctx context.Context) (T, error)) Prop[T] {
	return Prop[T]{&spec{get: funcGetter(fn), deferred: true, group: "default"}}
}

// Merge is a prop whose value the client appends to the one it holds on
// a partial reload, instead of replacing it: the next page of a list
// (see [Prop.Append], [Prop.Prepend] and [Prop.DeepMerge]). A full visit
// replaces it.
func Merge[T any](v T) Prop[T] { return Prop[T]{&spec{get: valueGetter(v), merge: true}} }

// Once is a prop the client loads once and keeps: later visits to pages
// with the same prop don't compute it again, until it expires
// ([Prop.ExpiresIn], [Prop.Until]) or the server sends a fresh one
// ([Prop.Fresh]).
func Once[T any](fn func(ctx context.Context) (T, error)) Prop[T] {
	return Func(fn).Once()
}

func (p Prop[T]) with(f func(*spec)) Prop[T] {
	var s spec
	if p.s != nil {
		s = *p.s
		s.appendAt, s.prependAt, s.matchOn = slices.Clone(s.appendAt), slices.Clone(s.prependAt), slices.Clone(s.matchOn)
	}
	f(&s)
	return Prop[T]{&s}
}

// Group puts a deferred prop in a group: the props of a group come in one
// request, and groups in parallel ones.
func (p Prop[T]) Group(name string) Prop[T] { return p.with(func(s *spec) { s.group = name }) }

// Rescue lets a deferred prop fail without failing the response: its
// error is logged, and the prop is left out and listed as rescued, for
// the page to show that part failed.
func (p Prop[T]) Rescue() Prop[T] { return p.with(func(s *spec) { s.rescue = true }) }

// Append makes the client append the prop's value to the one it holds on
// a partial reload: the prop's own array, or the arrays at paths inside
// it (Append("data") for {data: [...], next: 3}). On a [Merge] prop, the
// paths replace its root.
func (p Prop[T]) Append(paths ...string) Prop[T] {
	return p.with(func(s *spec) { s.appendAt = append(s.appendAt, orRoot(paths)...) })
}

// Prepend is [Prop.Append], putting the new items first.
func (p Prop[T]) Prepend(paths ...string) Prop[T] {
	return p.with(func(s *spec) { s.prependAt = append(s.prependAt, orRoot(paths)...) })
}

// DeepMerge makes the client merge the prop's value into the one it
// holds, at every depth, on a partial reload.
func (p Prop[T]) DeepMerge() Prop[T] { return p.with(func(s *spec) { s.deep = true }) }

// MatchOn makes merging replace the items whose field at path is equal,
// instead of adding them again: MatchOn("id") for an array of objects
// with an id, MatchOn("data.id") for the array at data.
func (p Prop[T]) MatchOn(paths ...string) Prop[T] {
	return p.with(func(s *spec) { s.matchOn = append(s.matchOn, paths...) })
}

// Once makes the client keep the prop once loaded (see [Once]).
func (p Prop[T]) Once() Prop[T] { return p.with(func(s *spec) { s.once = true }) }

// OnceAs is [Prop.Once] with the key that identifies the prop across
// pages, when it isn't the prop's name.
func (p Prop[T]) OnceAs(key string) Prop[T] {
	return p.with(func(s *spec) { s.once, s.onceKey = true, key })
}

// ExpiresIn makes a once prop expire d after it is sent: the client then
// asks for it again.
func (p Prop[T]) ExpiresIn(d time.Duration) Prop[T] {
	return p.with(func(s *spec) { s.once, s.expiresIn, s.expiresAt = true, d, time.Time{} })
}

// Until makes a once prop expire at t.
func (p Prop[T]) Until(t time.Time) Prop[T] {
	return p.with(func(s *spec) { s.once, s.expiresAt, s.expiresIn = true, t, 0 })
}

// Fresh sends a once prop's value even when the client holds it, which
// replaces it there.
func (p Prop[T]) Fresh() Prop[T] { return p.with(func(s *spec) { s.fresh = true }) }

func orRoot(paths []string) []string {
	if len(paths) == 0 {
		return []string{""}
	}
	return paths
}
