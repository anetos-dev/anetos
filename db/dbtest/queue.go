// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/queue/queuetest"
)

func init() {
	extra = append(extra, test{"QueueStore", testQueueStore}, test{"QueueWorker", testQueueWorker})
}

// queueTables creates the queue's tables under test names, and drops them
// when the test ends.
func queueTables(t *testing.T, ctx context.Context) *queue.DatabaseStore {
	t.Helper()
	set := migrate.NewSet("st_queue")
	set.AddFunc("2026_10_01_000400_create_st_jobs",
		func(s *migrate.Schema) error { return queue.CreateTables(s, "st_jobs", "st_failed_jobs") },
		func(s *migrate.Schema) error { return errors.Join(s.Drop("st_failed_jobs"), s.Drop("st_jobs")) })
	r, err := migrate.NewRunner(d(ctx), []*migrate.Set{set}, migrate.WithTable("st_queue_migrations"))
	check(t, err)
	_, err = r.Up(ctx)
	check(t, err)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		for _, table := range []string{"st_jobs", "st_failed_jobs", "st_queue_migrations"} {
			_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS "+table)
		}
	})
	return queue.NewDatabaseStore(d(ctx), "st_jobs", "st_failed_jobs")
}

// testQueueStore runs the queue store conformance suite on the database
// store, and checks that dispatches join the context's transaction.
func testQueueStore(t *testing.T, ctx context.Context) {
	store := queueTables(t, ctx)
	queuetest.Run(t, func(t *testing.T) queue.Store {
		_, err := db.Exec(ctx, "DELETE FROM st_jobs")
		check(t, err)
		_, err = db.Exec(ctx, "DELETE FROM st_failed_jobs")
		check(t, err)
		return store
	})

	q := queue.New(store, queue.Config{})
	check(t, queue.Register[stJob](q))
	ctx = queue.WithQueue(ctx, q)
	size := func() int64 {
		t.Helper()
		n, err := store.Size(ctx, "default")
		check(t, err)
		return n
	}
	// A dispatch in a transaction that rolls back never happened.
	errRollback := errors.New("rollback")
	err := db.Tx(ctx, func(ctx context.Context) error {
		if err := queue.Dispatch(ctx, stJob{Text: "rolled back"}); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("Tx = %v", err)
	}
	if n := size(); n != 0 {
		t.Errorf("after the rollback, %d job(s)", n)
	}
	// With AfterCommit too: the job is written in the transaction.
	err = db.Tx(ctx, func(ctx context.Context) error {
		if err := queue.Dispatch(ctx, stJob{Text: "rolled back"}, queue.AfterCommit()); err != nil {
			return err
		}
		n, err := db.RawFirst[int64](ctx, "SELECT COUNT(*) FROM st_jobs")
		check(t, err)
		if n != 1 {
			t.Errorf("with AfterCommit, the transaction sees %d job(s), want 1: written in it", n)
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("Tx = %v", err)
	}
	if n := size(); n != 0 {
		t.Errorf("after the rollback, with AfterCommit, %d job(s)", n)
	}
	// One that commits is dispatched, and so is one after the commit.
	check(t, db.Tx(ctx, func(ctx context.Context) error {
		if err := queue.Dispatch(ctx, stJob{Text: "in"}); err != nil {
			return err
		}
		return queue.Dispatch(ctx, stJob{Text: "after"}, queue.AfterCommit())
	}))
	if n := size(); n != 2 {
		t.Errorf("after the commit, %d job(s), want 2", n)
	}
	_, err = store.Clear(ctx, "default")
	check(t, err)
}

// stJob writes a note with the job's database.
type stJob struct {
	Text string `json:"text"`
}

func (j stJob) Handle(ctx context.Context) error {
	if j.Text == "bad bytes" {
		return queue.Permanent(errors.New("bad \x00 \xff bytes")) // stores must still keep the error
	}
	return db.Create(ctx, &stNote{Text: j.Text})
}

// testQueueWorker runs jobs from the database store with workers: they
// get the worker's context, with the database.
func testQueueWorker(t *testing.T, ctx context.Context) {
	store := queueTables(t, ctx)
	q := queue.New(store, queue.Config{Poll: 10 * time.Millisecond})
	check(t, queue.Register[stJob](q))
	for _, text := range []string{"one", "two", "three", "bad bytes"} {
		check(t, q.Dispatch(ctx, stJob{Text: text}))
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- q.Run(runCtx, queue.Concurrency(2)) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		n, err := db.Query[stNote](ctx).Count()
		check(t, err)
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d notes after 10s, want 3", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for {
		failed, err := store.Failed(ctx, 0, 10)
		check(t, err)
		if len(failed) == 1 {
			if failed[0].Error != "bad \uFFFD \uFFFD bytes" {
				t.Errorf("failed job's error = %q", failed[0].Error)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the bad bytes job didn't fail")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	check(t, <-done)
	if n, err := store.Size(ctx, "default"); err != nil || n != 0 {
		t.Errorf("Size = %d, %v; want 0", n, err)
	}
}
