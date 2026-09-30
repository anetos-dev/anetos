// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MemoryStore keeps items in the process's memory: fast, and gone when
// the process stops. Each instance of an app has its own, so locks only
// exclude code in the same process; use the database or Redis store to
// share a cache between instances. Expired items are removed as they are
// read, and the rest every minute or so while items are written.
type MemoryStore struct {
	mu        sync.Mutex
	items     map[string]memItem
	now       func() time.Time
	lastSweep time.Time
}

type memItem struct {
	value []byte
	exp   time.Time // zero: never
}

func (it memItem) expired(now time.Time) bool { return !it.exp.IsZero() && !now.Before(it.exp) }

// NewMemoryStore returns an empty memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: map[string]memItem{}, now: time.Now}
}

// sweepEvery is how often writes remove expired items.
const sweepEvery = time.Minute

func (s *MemoryStore) expiry(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return s.now().Add(ttl)
}

// lookup returns the live item under key; call with s.mu held.
func (s *MemoryStore) lookup(key string) (memItem, bool) {
	it, ok := s.items[key]
	if ok && it.expired(s.now()) {
		delete(s.items, key)
		return memItem{}, false
	}
	return it, ok
}

// write stores an item; call with s.mu held.
func (s *MemoryStore) write(key string, it memItem) {
	s.items[key] = it
	if now := s.now(); now.Sub(s.lastSweep) >= sweepEvery {
		s.lastSweep = now
		for k, it := range s.items {
			if it.expired(now) {
				delete(s.items, k)
			}
		}
	}
}

// Get implements [Store].
func (s *MemoryStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.lookup(key)
	if !ok {
		return nil, false, nil
	}
	return bytes.Clone(it.value), true, nil
}

// Set implements [Store].
func (s *MemoryStore) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.write(key, memItem{bytes.Clone(value), s.expiry(ttl)})
	return nil
}

// Add implements [Store].
func (s *MemoryStore) Add(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.lookup(key); ok {
		return false, nil
	}
	s.write(key, memItem{bytes.Clone(value), s.expiry(ttl)})
	return true, nil
}

// Replace implements [Store].
func (s *MemoryStore) Replace(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.lookup(key); !ok {
		return false, nil
	}
	s.write(key, memItem{bytes.Clone(value), s.expiry(ttl)})
	return true, nil
}

// Delete implements [Store].
func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
	return nil
}

// Increment implements [Store].
func (s *MemoryStore) Increment(_ context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.lookup(key)
	n := delta
	if ok {
		var err error
		if n, err = addInt(key, it.value, delta); err != nil {
			return 0, err
		}
	} else {
		it.exp = s.expiry(ttl)
	}
	it.value = strconv.AppendInt(nil, n, 10)
	s.write(key, it)
	return n, nil
}

// addInt adds delta to the counter old, which must hold an integer as
// decimal text (as Increment and Set of an integer store it), and reports
// overflow.
func addInt(key string, old []byte, delta int64) (int64, error) {
	n, err := strconv.ParseInt(string(old), 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != string(old) {
		return 0, fmt.Errorf("cache: %s doesn't hold an integer", key)
	}
	if (delta > 0 && n > math.MaxInt64-delta) || (delta < 0 && n < math.MinInt64-delta) {
		return 0, fmt.Errorf("cache: incrementing %s by %d would overflow", key, delta)
	}
	return n + delta, nil
}

// DeleteIf implements [Store].
func (s *MemoryStore) DeleteIf(_ context.Context, key string, value []byte) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.lookup(key)
	if !ok || !bytes.Equal(it.value, value) {
		return false, nil
	}
	delete(s.items, key)
	return true, nil
}

// ExpireIf implements [Store].
func (s *MemoryStore) ExpireIf(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, fmt.Errorf("cache: ExpireIf needs a positive ttl, got %s", ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.lookup(key)
	if !ok || !bytes.Equal(it.value, value) {
		return false, nil
	}
	it.exp = s.expiry(ttl)
	s.items[key] = it
	return true, nil
}

// Flush implements [Store].
func (s *MemoryStore) Flush(_ context.Context, prefix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.items {
		if strings.HasPrefix(k, prefix) {
			delete(s.items, k)
		}
	}
	return nil
}

// Close implements [Store]; it removes every item.
func (s *MemoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.items)
	return nil
}

// Len returns the number of items, expired ones not yet removed included.
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}
