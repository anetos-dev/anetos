// SPDX-License-Identifier: Apache-2.0

// Package cache keeps values for a while in a shared store (memory, the
// database, or Redis through drivers/redis), and provides locks across the
// app's instances.
//
// Set it up once, then use the package functions with any context the app
// created:
//
//	cache.ForApp(app, redis.CacheDriver()) // CACHE_STORE picks the store
//
//	stats, err := cache.Remember(ctx, "stats", 10*time.Minute, computeStats)
//	err = cache.Set(ctx, "profile:7", profile, time.Hour)
//	profile, ok, err := cache.Get[Profile](ctx, "profile:7")
//	n, err := cache.Increment(ctx, "logins:"+ip, 1, time.Minute)
//	err = cache.TryWithLock(ctx, "reports", 10*time.Minute, sendReports)
//
// Values are encoded as JSON. Keys get a prefix (CACHE_PREFIX, default the
// app's name and ":cache:") so apps can share a store.
//
// See docs/site/guides/cache.md.
package cache
