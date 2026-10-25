// SPDX-License-Identifier: Apache-2.0

// Package db is a stub of Anetos's, for the analyzer's tests.
package db

import "context"

type Model struct{ ID int64 }

type Q[T any] struct{}

func Query[T any](ctx context.Context) *Q[T]                                 { return nil }
func (q *Q[T]) Where(e any) *Q[T]                                            { return q }
func (q *Q[T]) WhereRaw(sql string, args ...any) *Q[T]                       { return q }
func (q *Q[T]) Find(id any) (T, error)                                       { var t T; return t, nil }
func Find[T any](ctx context.Context, id any) (T, error)                     { var t T; return t, nil }
func Raw[T any](ctx context.Context, query string, args ...any) ([]T, error) { return nil, nil }
func Exec(ctx context.Context, query string, args ...any) (any, error)       { return nil, nil }
func SQL(sql string, args ...any) any                                        { return nil }
