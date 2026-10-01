// SPDX-License-Identifier: Apache-2.0

package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"sync"
	"time"
)

// MemoryStore keeps jobs in the process's memory: only workers in the
// same process see them, and they are lost when it stops. For development
// and tests.
type MemoryStore struct {
	mu     sync.Mutex
	jobs   map[string]*memJob // by ID
	failed map[string]FailedJob
	seq    uint64
	now    func() time.Time
}

type memJob struct {
	queue     string
	payload   []byte
	attempts  int
	available time.Time // reserved jobs: when the lease ends
	token     string
	seq       uint64 // push order, for ties
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{jobs: map[string]*memJob{}, failed: map[string]FailedJob{}, now: time.Now}
}

// Push implements [Store].
func (s *MemoryStore) Push(_ context.Context, m Message, delay time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.jobs[m.ID] = &memJob{queue: m.Queue, payload: slices.Clone(m.Payload), available: s.now().Add(max(delay, 0)), seq: s.seq}
	return nil
}

// Reserve implements [Store].
func (s *MemoryStore) Reserve(_ context.Context, queue string, lease time.Duration) (*Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var id string
	var next *memJob
	for jid, j := range s.jobs {
		if j.queue != queue || j.available.After(now) {
			continue
		}
		if next == nil || j.available.Before(next.available) || j.available.Equal(next.available) && j.seq < next.seq {
			id, next = jid, j
		}
	}
	if next == nil {
		return nil, nil
	}
	next.attempts++
	next.available = now.Add(lease)
	next.token = newToken()
	return &Reservation{ID: id, Queue: queue, Payload: slices.Clone(next.payload), Attempts: next.attempts, Token: next.token}, nil
}

// newToken returns a random reservation token.
func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// reserved returns r's job if it is still reserved with r's token.
func (s *MemoryStore) reserved(r *Reservation) *memJob {
	j := s.jobs[r.ID]
	if j == nil || j.token == "" || j.token != r.Token {
		return nil
	}
	return j
}

// Delete implements [Store].
func (s *MemoryStore) Delete(_ context.Context, r *Reservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reserved(r) == nil {
		return ErrLeaseLost
	}
	delete(s.jobs, r.ID)
	return nil
}

// Release implements [Store].
func (s *MemoryStore) Release(_ context.Context, r *Reservation, delay time.Duration, refund bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.reserved(r)
	if j == nil {
		return ErrLeaseLost
	}
	j.token = ""
	j.available = s.now().Add(max(delay, 0))
	if refund && j.attempts > 0 {
		j.attempts--
	}
	return nil
}

// Fail implements [Store].
func (s *MemoryStore) Fail(_ context.Context, r *Reservation, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.reserved(r)
	if j == nil {
		return ErrLeaseLost
	}
	delete(s.jobs, r.ID)
	s.failed[r.ID] = FailedJob{ID: r.ID, Queue: j.queue, Payload: j.payload, Error: errMsg, Attempts: j.attempts, FailedAt: s.now()}
	return nil
}

// Size implements [Store].
func (s *MemoryStore) Size(_ context.Context, queue string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, j := range s.jobs {
		if j.queue == queue {
			n++
		}
	}
	return n, nil
}

// Clear implements [Store].
func (s *MemoryStore) Clear(_ context.Context, queue string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for id, j := range s.jobs {
		if j.queue == queue {
			delete(s.jobs, id)
			n++
		}
	}
	return n, nil
}

// Failed implements [Store].
func (s *MemoryStore) Failed(_ context.Context, offset, limit int) ([]FailedJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]FailedJob, 0, len(s.failed))
	for _, f := range s.failed {
		f.Payload = slices.Clone(f.Payload)
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b FailedJob) int {
		if c := b.FailedAt.Compare(a.FailedAt); c != 0 {
			return c
		}
		if a.ID > b.ID { // later IDs first
			return -1
		}
		return 1
	})
	out = out[min(max(offset, 0), len(out)):]
	return out[:min(max(limit, 0), len(out))], nil
}

// Retry implements [Store].
func (s *MemoryStore) Retry(_ context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.failed[id]
	if !ok {
		return false, nil
	}
	delete(s.failed, id)
	s.seq++
	s.jobs[id] = &memJob{queue: f.Queue, payload: f.Payload, available: s.now(), seq: s.seq}
	return true, nil
}

// Forget implements [Store].
func (s *MemoryStore) Forget(_ context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.failed[id]
	delete(s.failed, id)
	return ok, nil
}

// Flush implements [Store].
func (s *MemoryStore) Flush(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := int64(len(s.failed))
	clear(s.failed)
	return n, nil
}

// Close implements [Store]; it does nothing.
func (s *MemoryStore) Close() error { return nil }
