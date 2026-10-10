// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// txState is a transaction in progress, shared by nested Tx calls.
type txState struct {
	tx *sql.Tx

	mu         sync.Mutex
	savepoints int
	after      []func(context.Context)
	test       bool // from WithTestTx: work at its level counts as committed
}

// spDepth keys the number of savepoints a context is in.
type spDepth struct{ db *DB }

func (d *DB) depth(ctx context.Context) int {
	n, _ := ctx.Value(spDepth{d}).(int)
	return n
}

type txKey struct{ db *DB }

func (d *DB) txIn(ctx context.Context) *txState {
	st, _ := ctx.Value(txKey{d}).(*txState)
	return st
}

// Tx runs fn in a transaction on the database in ctx. Queries made with
// the ctx passed to fn run in the transaction. If fn returns an error or
// panics, the transaction is rolled back; otherwise it is committed.
//
//	err := db.Tx(ctx, func(ctx context.Context) error {
//		if err := db.Create(ctx, &order); err != nil {
//			return err
//		}
//		return db.Create(ctx, &payment)
//	})
//
// Calling Tx inside a transaction starts a nested one using a savepoint:
// an error rolls back only the nested part.
//
// Don't run queries of one transaction from several goroutines at once.
func Tx(ctx context.Context, fn func(ctx context.Context) error) error {
	return TxWithOptions(ctx, nil, fn)
}

// TxWithOptions is like [Tx] with transaction options (isolation level,
// read-only), as [sql.DB.BeginTx] takes them. Options are ignored for
// nested transactions, which inherit the outer one.
func TxWithOptions(ctx context.Context, opts *sql.TxOptions, fn func(ctx context.Context) error) (err error) {
	d, err := From(ctx)
	if err != nil {
		return err
	}
	if st := d.txIn(ctx); st != nil {
		return d.savepoint(ctx, st, fn)
	}

	tx, err := d.sql.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	st := &txState{tx: tx}
	done := false
	defer func() {
		// A panic or runtime.Goexit (t.Fatal) in fn: roll back so the
		// connection and its locks are released. A panic continues.
		if !done {
			_ = tx.Rollback()
		}
	}()
	if err := fn(context.WithValue(ctx, txKey{d}, st)); err != nil {
		done = true
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return errors.Join(err, fmt.Errorf("db: rollback: %w", rbErr))
		}
		return err
	}
	done = true
	if err := tx.Commit(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// database/sql rolls back when the context ends; say why.
			return fmt.Errorf("db: commit: %w", errors.Join(ctxErr, err))
		}
		return fmt.Errorf("db: commit: %w", err)
	}
	for _, f := range st.after {
		f(ctx)
	}
	return nil
}

func (d *DB) savepoint(ctx context.Context, st *txState, fn func(ctx context.Context) error) error {
	st.mu.Lock()
	st.savepoints++
	name := fmt.Sprintf("anetos_sp_%d", st.savepoints)
	mark := len(st.after)
	st.mu.Unlock()

	if _, err := d.exec(ctx, st.tx, "SAVEPOINT "+name, nil); err != nil {
		return fmt.Errorf("db: savepoint: %w", err)
	}
	rollback := func() error {
		st.mu.Lock()
		st.after = st.after[:mark] // callbacks of the undone work must not run
		st.mu.Unlock()
		_, err := d.exec(ctx, st.tx, "ROLLBACK TO SAVEPOINT "+name, nil)
		return err
	}
	done := false
	defer func() {
		if !done { // panic or runtime.Goexit in fn
			_ = rollback()
		}
	}()
	depth := d.depth(ctx)
	err := fn(context.WithValue(ctx, spDepth{d}, depth+1))
	done = true
	if err != nil {
		if rbErr := rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("db: rollback to savepoint: %w", rbErr))
		}
		return err
	}
	if _, err := d.exec(ctx, st.tx, "RELEASE SAVEPOINT "+name, nil); err != nil {
		return fmt.Errorf("db: release savepoint: %w", err)
	}
	if st.test && depth == 0 {
		// Directly in a test's transaction, which never commits: this is
		// the commit, as far as the test can tell.
		st.mu.Lock()
		after := slices.Clone(st.after[mark:])
		st.after = st.after[:mark]
		st.mu.Unlock()
		for _, f := range after {
			f(ctx)
		}
	}
	return nil
}

// AfterCommit runs fn once the transaction in ctx commits, or right away if
// ctx has no transaction (or only a test's, from [WithTestTx]). Use it for work that must only happen if the
// data was saved, such as sending an email or dispatching a job. If the
// transaction (or the nested transaction fn was registered in) rolls back,
// fn never runs. fn gets a context outside the transaction (in a test's
// transaction from [WithTestTx], the test's context).
func AfterCommit(ctx context.Context, fn func(ctx context.Context)) {
	if d, err := From(ctx); err == nil {
		if st := d.txIn(ctx); st != nil && (!st.test || d.depth(ctx) != 0) {
			st.mu.Lock()
			st.after = append(st.after, fn)
			st.mu.Unlock()
			return
		}
	}
	fn(ctx)
}

// WithTx returns ctx with tx, a transaction on the database in ctx that
// the caller began and will commit or roll back itself. Queries made with
// the returned context run in tx, and [Tx] on it uses savepoints. Use it
// to share a transaction with code that works with *sql.Tx directly.
// [AfterCommit] callbacks registered on the returned context never run,
// since the db package doesn't see the commit: neither do what relies on
// them (queue.AfterCommit dispatches, async event listeners, and the
// recording of jobs the database queue driver writes in tx, which
// anetostest and queue.Queue.Observe see).
func WithTx(ctx context.Context, tx *sql.Tx) (context.Context, error) {
	d, err := From(ctx)
	if err != nil {
		return nil, err
	}
	return context.WithValue(ctx, txKey{d}, &txState{tx: tx}), nil
}

// WithTestTx is [WithTx] for test helpers, whose transaction is rolled
// back at the end of the test instead of committed: work done at its
// level counts as committed. [AfterCommit] callbacks registered directly
// in it run at once, and those of a [Tx] directly inside it run when that
// Tx commits (as they would in the app, without the test's transaction).
// anetostest uses it.
func WithTestTx(ctx context.Context, tx *sql.Tx) (context.Context, error) {
	d, err := From(ctx)
	if err != nil {
		return nil, err
	}
	return context.WithValue(ctx, txKey{d}, &txState{tx: tx, test: true}), nil
}

// WithoutTx returns ctx without its transaction on the database in ctx:
// queries made with it use their own connections, and their writes stay
// if the transaction rolls back. Use it for records that must outlive a
// failed transaction, such as an audit log of the attempt. It needs a
// second connection while the transaction holds one. Don't use it
// with SQLite, whose transactions hold the write lock from the start: a
// write made this way waits for the transaction and fails after the busy
// timeout.
func WithoutTx(ctx context.Context) context.Context {
	d, err := From(ctx)
	if err != nil || d.txIn(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, txKey{d}, (*txState)(nil))
}

// InTx reports whether ctx has a transaction on its database.
func InTx(ctx context.Context) bool {
	d, err := From(ctx)
	return err == nil && d.txIn(ctx) != nil
}

// TxWith is [TxWithOptions].
//
// Deprecated: Use TxWithOptions; TxWith is removed in v0.6.
//
//go:fix inline
func TxWith(ctx context.Context, opts *sql.TxOptions, fn func(ctx context.Context) error) error {
	return TxWithOptions(ctx, opts, fn)
}
