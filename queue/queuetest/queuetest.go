// SPDX-License-Identifier: Apache-2.0

// Package queuetest is a conformance suite for queue stores. Each store
// runs it:
//
//	func TestStore(t *testing.T) {
//		queuetest.Run(t, func(t *testing.T) queue.Store { return newStore(t) })
//	}
//
// newStore returns an empty store for each test: no jobs, and no failed
// jobs.
package queuetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos/queue"
)

// short is a delay or lease short enough for quick tests; wait is
// comfortably longer.
const (
	short = 300 * time.Millisecond
	wait  = 800 * time.Millisecond
	long  = time.Minute
)

type test struct {
	name string
	fn   func(t *testing.T, ctx context.Context, s queue.Store, q string)
}

// Run runs the suite against the stores newStore returns.
func Run(t *testing.T, newStore func(t *testing.T) queue.Store) {
	t.Helper()
	for _, tt := range []test{
		{"PushReserve", testPushReserve},
		{"Delay", testDelay},
		{"Lease", testLease},
		{"Release", testRelease},
		{"Fail", testFail},
		{"RetryForgetFlush", testRetryForgetFlush},
		{"SizeClear", testSizeClear},
		{"Concurrency", testConcurrency},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			b := make([]byte, 4)
			_, _ = rand.Read(b)
			tt.fn(t, context.Background(), s, "qt-"+hex.EncodeToString(b))
		})
	}
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var counter atomic.Int64

// id returns a new job ID: a UUID-shaped string, later ones larger.
func id() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x-0000-7000-8000-%s", counter.Add(1), hex.EncodeToString(b))
}

func push(t *testing.T, ctx context.Context, s queue.Store, q string, payload string, delay time.Duration) string {
	t.Helper()
	jid := id()
	check(t, s.Push(ctx, queue.Message{ID: jid, Queue: q, Payload: []byte(payload)}, delay))
	return jid
}

func reserve(t *testing.T, ctx context.Context, s queue.Store, q string, lease time.Duration) *queue.Reservation {
	t.Helper()
	r, err := s.Reserve(ctx, q, lease)
	check(t, err)
	return r
}

func none(t *testing.T, ctx context.Context, s queue.Store, q string) {
	t.Helper()
	if r := reserve(t, ctx, s, q, long); r != nil {
		t.Fatalf("Reserve = job %s, want none", r.ID)
	}
}

func testPushReserve(t *testing.T, ctx context.Context, s queue.Store, q string) {
	none(t, ctx, s, q)
	payloads := []string{`{"n":1}`, `{"text":"héllo, 世界 \u0000 \"quoted\""}`, `{"n":3}`}
	var ids []string
	for _, p := range payloads {
		ids = append(ids, push(t, ctx, s, q, p, 0))
		time.Sleep(5 * time.Millisecond) // a later available time
	}
	push(t, ctx, s, q+"-other", `{}`, 0)
	tokens := map[string]bool{}
	for i, p := range payloads {
		r := reserve(t, ctx, s, q, long)
		if r == nil {
			t.Fatalf("job %d: Reserve = nil", i)
		}
		if r.ID != ids[i] || r.Queue != q || !bytes.Equal(r.Payload, []byte(p)) || r.Attempts != 1 {
			t.Errorf("job %d: Reserve = %+v, want ID %s, queue %s, payload %s, 1 attempt", i, r, ids[i], q, p)
		}
		if r.Token == "" || tokens[r.Token] {
			t.Errorf("job %d: token %q isn't new", i, r.Token)
		}
		tokens[r.Token] = true
		check(t, s.Delete(ctx, r))
	}
	none(t, ctx, s, q)
	if r := reserve(t, ctx, s, q+"-other", long); r == nil {
		t.Error("the other queue's job is gone")
	}
}

func testDelay(t *testing.T, ctx context.Context, s queue.Store, q string) {
	later := push(t, ctx, s, q, `{"later":true}`, short)
	none(t, ctx, s, q)
	now := push(t, ctx, s, q, `{"now":true}`, 0)
	if r := reserve(t, ctx, s, q, long); r == nil || r.ID != now {
		t.Fatalf("Reserve = %+v, want the job without a delay", r)
	}
	time.Sleep(wait)
	if r := reserve(t, ctx, s, q, long); r == nil || r.ID != later {
		t.Fatalf("after the delay, Reserve = %+v, want the delayed job", r)
	}
}

func testLease(t *testing.T, ctx context.Context, s queue.Store, q string) {
	jid := push(t, ctx, s, q, `{}`, 0)
	first := reserve(t, ctx, s, q, short)
	if first == nil {
		t.Fatal("Reserve = nil")
	}
	none(t, ctx, s, q)
	time.Sleep(wait)
	second := reserve(t, ctx, s, q, long)
	if second == nil || second.ID != jid || second.Attempts != 2 || second.Token == first.Token {
		t.Fatalf("after the lease, Reserve = %+v, want the job again, attempt 2, a new token", second)
	}
	for name, err := range map[string]error{
		"Delete":  s.Delete(ctx, first),
		"Release": s.Release(ctx, first, 0, false),
		"Fail":    s.Fail(ctx, first, "x"),
	} {
		if !errors.Is(err, queue.ErrLeaseLost) {
			t.Errorf("%s with the ended reservation = %v, want ErrLeaseLost", name, err)
		}
	}
	failed, err := s.Failed(ctx, 0, 10)
	check(t, err)
	if len(failed) != 0 {
		t.Errorf("Fail with the ended reservation kept %d failed job(s)", len(failed))
	}
	check(t, s.Delete(ctx, second))
	if err := s.Delete(ctx, second); !errors.Is(err, queue.ErrLeaseLost) {
		t.Errorf("Delete twice = %v, want ErrLeaseLost", err)
	}
	none(t, ctx, s, q)
}

func testRelease(t *testing.T, ctx context.Context, s queue.Store, q string) {
	jid := push(t, ctx, s, q, `{}`, 0)
	r := reserve(t, ctx, s, q, long)
	check(t, s.Release(ctx, r, 0, false))
	if err := s.Release(ctx, r, 0, false); !errors.Is(err, queue.ErrLeaseLost) {
		t.Errorf("Release twice = %v, want ErrLeaseLost", err)
	}
	r = reserve(t, ctx, s, q, long)
	if r == nil || r.ID != jid || r.Attempts != 2 {
		t.Fatalf("after Release, Reserve = %+v, want attempt 2", r)
	}
	check(t, s.Release(ctx, r, 0, true)) // refund: attempt 2 doesn't count
	r = reserve(t, ctx, s, q, long)
	if r == nil || r.Attempts != 2 {
		t.Fatalf("after a refund, Reserve = %+v, want attempt 2 again", r)
	}
	check(t, s.Release(ctx, r, short, false))
	none(t, ctx, s, q)
	time.Sleep(wait)
	r = reserve(t, ctx, s, q, long)
	if r == nil || r.Attempts != 3 {
		t.Fatalf("after the delay, Reserve = %+v, want attempt 3", r)
	}
}

func testFail(t *testing.T, ctx context.Context, s queue.Store, q string) {
	payload := `{"user_id":7}`
	jid := push(t, ctx, s, q, payload, 0)
	r := reserve(t, ctx, s, q, long)
	r2 := *r
	check(t, s.Release(ctx, r, 0, false))
	r = reserve(t, ctx, s, q, long)
	before := time.Now()
	msg := "boom\nwith a second line"
	check(t, s.Fail(ctx, r, msg))
	none(t, ctx, s, q)
	if err := s.Fail(ctx, &r2, "again"); !errors.Is(err, queue.ErrLeaseLost) {
		t.Errorf("Fail with an old token = %v, want ErrLeaseLost", err)
	}
	failed, err := s.Failed(ctx, 0, 10)
	check(t, err)
	if len(failed) != 1 {
		t.Fatalf("Failed = %d jobs, want 1", len(failed))
	}
	f := failed[0]
	if f.ID != jid || f.Queue != q || string(f.Payload) != payload || f.Error != msg || f.Attempts != 2 {
		t.Errorf("Failed = %+v", f)
	}
	// The store's clock may differ a little from the test's.
	if d := f.FailedAt.Sub(before); d < -5*time.Second || d > 5*time.Second {
		t.Errorf("FailedAt = %s, want about %s", f.FailedAt, before)
	}
	if n, err := s.Size(ctx, q); err != nil || n != 0 {
		t.Errorf("Size = %d, %v; want 0", n, err)
	}
}

// fail pushes a job and fails it.
func fail(t *testing.T, ctx context.Context, s queue.Store, q, payload string) string {
	t.Helper()
	jid := push(t, ctx, s, q, payload, 0)
	r := reserve(t, ctx, s, q, long)
	if r == nil || r.ID != jid {
		t.Fatalf("Reserve = %+v, want %s", r, jid)
	}
	check(t, s.Fail(ctx, r, "failed "+payload))
	return jid
}

func testRetryForgetFlush(t *testing.T, ctx context.Context, s queue.Store, q string) {
	a := fail(t, ctx, s, q, `{"a":1}`)
	time.Sleep(10 * time.Millisecond)
	b := fail(t, ctx, s, q+"-b", `{"b":1}`)
	time.Sleep(10 * time.Millisecond)
	c := fail(t, ctx, s, q, `{"c":1}`)

	failed, err := s.Failed(ctx, 0, 10)
	check(t, err)
	if len(failed) != 3 || failed[0].ID != c || failed[1].ID != b || failed[2].ID != a {
		t.Fatalf("Failed = %v, want c, b, a", ids(failed))
	}
	// Counted and found, by the store or by reading them (plain).
	plain := struct{ queue.Store }{s}
	for _, st := range []queue.Store{s, plain} {
		if n, err := queue.CountFailed(ctx, st); err != nil || n != 3 {
			t.Errorf("CountFailed = %d, %v; want 3", n, err)
		}
		j, ok, err := queue.FindFailed(ctx, st, b)
		check(t, err)
		if !ok || j.ID != b || j.Queue != q+"-b" || string(j.Payload) != `{"b":1}` || j.Error != `failed {"b":1}` || j.Attempts != 1 {
			t.Errorf("FindFailed = %+v, %v", j, ok)
		}
		for _, other := range []string{"nope", b[:len(b)-1], b + "x", "%"} {
			if _, ok, err := queue.FindFailed(ctx, st, other); err != nil || ok {
				t.Errorf("FindFailed(%q) = %v, %v; want none", other, ok, err)
			}
		}
	}
	failed, err = s.Failed(ctx, 0, 2)
	check(t, err)
	if len(failed) != 2 || failed[0].ID != c {
		t.Errorf("Failed(0, 2) = %v, want c, b", ids(failed))
	}
	failed, err = s.Failed(ctx, 1, 1)
	check(t, err)
	if len(failed) != 1 || failed[0].ID != b {
		t.Errorf("Failed(1, 1) = %v, want b", ids(failed))
	}
	failed, err = s.Failed(ctx, 3, 10)
	check(t, err)
	if len(failed) != 0 {
		t.Errorf("Failed(3, 10) = %v, want none", ids(failed))
	}

	ok, err := s.Retry(ctx, b)
	check(t, err)
	if !ok {
		t.Fatal("Retry = false")
	}
	if ok, err := s.Retry(ctx, b); err != nil || ok {
		t.Errorf("Retry twice = %v, %v; want false", ok, err)
	}
	r := reserve(t, ctx, s, q+"-b", long)
	if r == nil || r.ID != b || r.Attempts != 1 || string(r.Payload) != `{"b":1}` {
		t.Fatalf("after Retry, Reserve = %+v, want b with attempt 1", r)
	}
	check(t, s.Delete(ctx, r))

	if ok, err := s.Forget(ctx, a); err != nil || !ok {
		t.Errorf("Forget = %v, %v", ok, err)
	}
	if ok, err := s.Forget(ctx, a); err != nil || ok {
		t.Errorf("Forget twice = %v, %v; want false", ok, err)
	}
	if ok, err := s.Retry(ctx, a); err != nil || ok {
		t.Errorf("Retry of a forgotten job = %v, %v; want false", ok, err)
	}
	fail(t, ctx, s, q, `{"d":1}`)
	n, err := s.Flush(ctx)
	check(t, err)
	if n != 2 {
		t.Errorf("Flush = %d, want 2", n)
	}
	failed, err = s.Failed(ctx, 0, 10)
	check(t, err)
	if len(failed) != 0 {
		t.Errorf("after Flush, Failed = %v", ids(failed))
	}
	if n, err := queue.CountFailed(ctx, s); err != nil || n != 0 {
		t.Errorf("after Flush, CountFailed = %d, %v", n, err)
	}
	// A retried job that fails again replaces its record.
	e := fail(t, ctx, s, q, `{"e":1}`)
	_, err = s.Retry(ctx, e)
	check(t, err)
	r = reserve(t, ctx, s, q, long)
	check(t, s.Fail(ctx, r, "again"))
	failed, err = s.Failed(ctx, 0, 10)
	check(t, err)
	if len(failed) != 1 || failed[0].ID != e || failed[0].Error != "again" {
		t.Errorf("after failing again, Failed = %+v", failed)
	}
}

func ids(jobs []queue.FailedJob) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.ID
	}
	return out
}

func testSizeClear(t *testing.T, ctx context.Context, s queue.Store, q string) {
	for range 3 {
		push(t, ctx, s, q, `{}`, 0)
	}
	push(t, ctx, s, q, `{}`, long)
	push(t, ctx, s, q+"-other", `{}`, 0)
	reserve(t, ctx, s, q, long) // reserved jobs count
	if n, err := s.Size(ctx, q); err != nil || n != 4 {
		t.Errorf("Size = %d, %v; want 4", n, err)
	}
	n, err := s.Clear(ctx, q)
	check(t, err)
	if n != 4 {
		t.Errorf("Clear = %d, want 4", n)
	}
	if n, err := s.Size(ctx, q); err != nil || n != 0 {
		t.Errorf("after Clear, Size = %d, %v", n, err)
	}
	if n, err := s.Size(ctx, q+"-other"); err != nil || n != 1 {
		t.Errorf("Clear touched another queue: Size = %d, %v", n, err)
	}
}

func testConcurrency(t *testing.T, ctx context.Context, s queue.Store, q string) {
	const jobs, workers = 60, 8
	want := map[string]bool{}
	for range jobs {
		want[push(t, ctx, s, q, `{}`, 0)] = true
	}
	var mu sync.Mutex
	got := map[string]int{}
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Go(func() {
			for {
				r, err := s.Reserve(ctx, q, long)
				if err != nil {
					errs <- err
					return
				}
				if r == nil {
					return
				}
				mu.Lock()
				got[r.ID]++
				mu.Unlock()
				if err := s.Delete(ctx, r); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if len(got) != jobs {
		t.Errorf("reserved %d jobs, want %d", len(got), jobs)
	}
	for jid, n := range got {
		if n != 1 || !want[jid] {
			t.Errorf("job %s reserved %d times", jid, n)
		}
	}
}
