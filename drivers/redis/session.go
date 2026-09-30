// SPDX-License-Identifier: Apache-2.0

package redis

import (
	"context"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/session"
)

// SessionDriver keeps sessions in Redis (SESSION_DRIVER=redis), on the
// app's client from [Connect]:
//
//	sessions, err := session.ForApp(app, redis.SessionDriver())
func SessionDriver() session.Driver {
	return session.Driver{Name: "redis", Open: func(app *anetos.App, _ session.Config) (cache.Store, error) {
		client, err := Connect(context.Background(), app)
		if err != nil {
			return nil, err
		}
		s := NewCacheStore(client)
		s.shared = true
		return s, nil
	}}
}
