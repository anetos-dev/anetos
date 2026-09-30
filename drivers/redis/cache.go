// SPDX-License-Identifier: Apache-2.0

package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	goredis "github.com/redis/go-redis/v9"
)

// CacheDriver is the Redis cache store's driver (CACHE_STORE=redis), on
// the app's client from [Connect]:
//
//	c, err := cache.ForApp(app, redis.CacheDriver())
func CacheDriver() cache.Driver {
	return cache.Driver{Name: "redis", Open: func(app *anetos.App, _ cache.Config) (cache.Store, error) {
		client, err := Connect(context.Background(), app)
		if err != nil {
			return nil, err
		}
		s := NewCacheStore(client)
		s.shared = true
		return s, nil
	}}
}

// CacheStore is a cache store in Redis: every instance of the app shares
// its items and locks. Items expire in Redis itself. The lock operations
// run as Lua scripts, so they are atomic.
type CacheStore struct {
	client goredis.UniversalClient
	shared bool // the client belongs to the app, which closes it
}

// NewCacheStore returns a store using client (a *redis.Client of
// go-redis, or a cluster, failover or ring client). Closing the store
// closes the client.
func NewCacheStore(client goredis.UniversalClient) *CacheStore {
	return &CacheStore{client: client}
}

var (
	// deleteIf deletes KEYS[1] if it holds ARGV[1].
	deleteIf = goredis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
	return redis.call('DEL', KEYS[1])
end
return 0`)
	// expireIf sets the ttl ARGV[2] (ms) of KEYS[1] if it holds ARGV[1].
	expireIf = goredis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
	return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0`)
)

// ms is ttl in whole milliseconds, at least 1 for a positive ttl.
func ms(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	return max(ttl.Milliseconds(), 1)
}

// Get implements [cache.Store].
func (s *CacheStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := s.client.Get(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// Set implements [cache.Store].
func (s *CacheStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return s.client.Set(ctx, key, value, time.Duration(ms(ttl))*time.Millisecond).Err()
}

// Add implements [cache.Store], with SET NX.
func (s *CacheStore) Add(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return s.client.SetNX(ctx, key, value, time.Duration(ms(ttl))*time.Millisecond).Result()
}

// Delete implements [cache.Store].
func (s *CacheStore) Delete(ctx context.Context, key string) error {
	return s.client.Del(ctx, key).Err()
}

// Increment implements [cache.Store], in a MULTI transaction: SET NX
// creates a missing counter with its ttl, and INCRBY adds to it, keeping
// its expiry.
func (s *CacheStore) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	var incr *goredis.IntCmd
	_, err := s.client.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		if ttl > 0 {
			p.SetNX(ctx, key, "0", time.Duration(ms(ttl))*time.Millisecond)
		}
		incr = p.IncrBy(ctx, key, delta)
		return nil
	})
	switch {
	case err == nil:
		return incr.Val(), nil
	case strings.Contains(err.Error(), "would overflow"):
		return 0, fmt.Errorf("cache: incrementing %s by %d would overflow", key, delta)
	case strings.Contains(err.Error(), "not an integer"):
		return 0, fmt.Errorf("cache: %s doesn't hold an integer", key)
	}
	return 0, err
}

// DeleteIf implements [cache.Store].
func (s *CacheStore) DeleteIf(ctx context.Context, key string, value []byte) (bool, error) {
	n, err := deleteIf.Run(ctx, s.client, []string{key}, value).Int64()
	return n == 1, err
}

// ExpireIf implements [cache.Store].
func (s *CacheStore) ExpireIf(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, fmt.Errorf("cache: ExpireIf needs a positive ttl, got %s", ttl)
	}
	n, err := expireIf.Run(ctx, s.client, []string{key}, value, ms(ttl)).Int64()
	return n == 1, err
}

// Flush implements [cache.Store]: it finds the keys with SCAN and removes
// them with UNLINK, so it doesn't block the server on a large cache (and
// keys written meanwhile may survive it).
func (s *CacheStore) Flush(ctx context.Context, prefix string) error {
	match := globEscape(prefix) + "*"
	switch c := s.client.(type) {
	case *goredis.ClusterClient:
		return c.ForEachMaster(ctx, func(ctx context.Context, node *goredis.Client) error {
			return flushNode(ctx, node, match, false)
		})
	case *goredis.Ring:
		return c.ForEachShard(ctx, func(ctx context.Context, shard *goredis.Client) error {
			return flushNode(ctx, shard, match, true)
		})
	}
	return flushNode(ctx, s.client, match, true)
}

// flushNode removes the keys matching match on one server; batch removes
// them with one UNLINK per SCAN page (not in a cluster, whose keys may
// be in different slots).
func flushNode(ctx context.Context, c goredis.Cmdable, match string, batch bool) error {
	var cursor uint64
	for {
		keys, next, err := c.Scan(ctx, cursor, match, 1000).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if batch {
				err = c.Unlink(ctx, keys...).Err()
			} else {
				for _, k := range keys {
					if err = c.Unlink(ctx, k).Err(); err != nil {
						break
					}
				}
			}
			if err != nil {
				return err
			}
		}
		if cursor = next; cursor == 0 {
			return nil
		}
	}
}

// globEscape escapes the characters SCAN's MATCH patterns treat
// specially.
func globEscape(s string) string {
	var b strings.Builder
	for i := range len(s) { // bytes: keys needn't be valid UTF-8
		switch s[i] {
		case '*', '?', '[', ']', '\\', '^', '-':
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Close implements [cache.Store]: it closes the client, unless it is the
// app's (from [CacheDriver]), which the app closes at shutdown.
func (s *CacheStore) Close() error {
	if s.shared {
		return nil
	}
	return s.client.Close()
}
