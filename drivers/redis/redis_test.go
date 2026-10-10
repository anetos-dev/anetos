// SPDX-License-Identifier: Apache-2.0

package redis_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/cache/cachetest"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/drivers/redis"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
	goredis "github.com/redis/go-redis/v9"
)

// redisURL returns the server in ANETOS_TEST_REDIS_URL, e.g.
// redis://127.0.0.1:6379/0, or skips the test.
func redisURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("ANETOS_TEST_REDIS_URL")
	if url == "" {
		t.Skip("ANETOS_TEST_REDIS_URL not set")
	}
	return url
}

func TestCacheStore(t *testing.T) {
	opts, err := goredis.ParseURL(redisURL(t))
	if err != nil {
		t.Fatal(err)
	}
	cachetest.Run(t, func(t *testing.T) cache.Store {
		s := redis.NewCacheStore(goredis.NewClient(opts))
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

func newApp(t *testing.T, env config.Map) *anetos.App {
	t.Helper()
	app, err := anetos.New(anetos.WithSource(env), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestCacheDriver(t *testing.T) {
	url := redisURL(t)
	app := newApp(t, config.Map{"APP_NAME": "redistest", "CACHE_DRIVER": "redis", "REDIS_URL": url})
	c, err := cache.New(app, redis.CacheDriver())
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx := app.Context(t.Context())
	if err := cache.Set(ctx, "k", "v", 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Flush(context.WithoutCancel(ctx)) })
	// The client is the app's: Connect returns it, and it has the key.
	client, err := redis.Connect(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := anetos.Resolve[*goredis.Client](app); err != nil || got != client {
		t.Errorf("Resolve = %v, %v", got, err)
	}
	if v, err := client.Get(ctx, c.Prefix()+"k").Result(); err != nil || v != `"v"` {
		t.Errorf("GET = %q, %v", v, err)
	}
	// Closing the store leaves the app's client open.
	if err := c.Store().Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx).Err(); err != nil {
		t.Errorf("the store closed the app's client: %v", err)
	}
}

func TestConnectErrors(t *testing.T) {
	app := newApp(t, config.Map{"REDIS_URL": "http://example.com"})
	if _, err := redis.Connect(t.Context(), app); err == nil || !strings.Contains(err.Error(), "REDIS_URL") {
		t.Errorf("bad URL: %v", err)
	}

	// Nothing listens on port 1: booting fails, and help wouldn't need Redis.
	app = newApp(t, config.Map{"REDIS_URL": "redis://127.0.0.1:1/0"})
	if _, err := redis.Connect(t.Context(), app); err != nil {
		t.Fatalf("Connect before boot: %v", err)
	}
	if err := app.Boot(t.Context()); err == nil || !strings.Contains(err.Error(), "redis: connect to 127.0.0.1:1") {
		t.Errorf("Boot = %v", err)
	}

	// After boot, Connect checks right away.
	app = newApp(t, config.Map{"REDIS_URL": "redis://127.0.0.1:1/0"})
	if err := app.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := redis.Connect(t.Context(), app); err == nil {
		t.Error("Connect after boot to a closed port: no error")
	}
}

func TestLoadConfig(t *testing.T) {
	cfg, err := redis.LoadConfig(config.Map{})
	if err != nil || cfg.URL != "redis://127.0.0.1:6379/0" {
		t.Errorf("default = %+v, %v", cfg, err)
	}
}

// TestAnetostest checks that a test app's items in a shared store are
// removed when its test ends.
func TestAnetostest(t *testing.T) {
	url := redisURL(t)
	var prefix string
	t.Run("app", func(t *testing.T) {
		app := anetostest.New(t, func(app *anetos.App) (*web.Server, error) {
			c, err := cache.New(app, redis.CacheDriver())
			if c != nil {
				prefix = c.Prefix()
			}
			return nil, err
		}, anetostest.Env(map[string]string{"CACHE_DRIVER": "redis", "REDIS_URL": url}))
		if err := cache.Set(app.Context(), "k", 1, cache.Forever); err != nil {
			t.Fatal(err)
		}
	})
	opts, err := goredis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := goredis.NewClient(opts)
	defer client.Close()
	if n, err := client.Exists(t.Context(), prefix+"k").Result(); err != nil || n != 0 {
		t.Errorf("the test app's item is still there: %d, %v", n, err)
	}
}

func TestSessionDriver(t *testing.T) {
	url := redisURL(t)
	prefix := "redistest-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ":session:"
	app := newApp(t, config.Map{"APP_KEY": encryption.GenerateKey(), "SESSION_DRIVER": "redis", "SESSION_PREFIX": prefix, "REDIS_URL": url})
	m, err := session.New(app, redis.SessionDriver())
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}
	store, _ := m.Store()
	t.Cleanup(func() { _ = store.Flush(context.WithoutCancel(t.Context()), prefix) })
	ck, err := m.Edit(httptest.NewRequest(http.MethodGet, "/", nil), func(s *session.Session) { s.Put("user_id", 7) })
	if err != nil {
		t.Fatal(err)
	}
	client, _ := redis.Connect(t.Context(), app)
	var keys []string
	it := client.Scan(t.Context(), 0, prefix+"*", 1000).Iterator()
	for it.Next(t.Context()) {
		keys = append(keys, it.Val())
	}
	if err := it.Err(); err != nil || len(keys) != 1 {
		t.Fatalf("session keys: %v, %v", keys, err)
	}
	if ttl := client.PTTL(t.Context(), keys[0]).Val(); ttl <= 0 || ttl > 2*time.Hour {
		t.Errorf("session ttl %v, want SESSION_TTL", ttl)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(ck)
	if v, _ := session.Value[int](m.Load(r), "user_id"); v != 7 {
		t.Errorf("loaded user_id = %d", v)
	}
}
