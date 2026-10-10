// SPDX-License-Identifier: Apache-2.0

package queue_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/queue"
)

// TestDoctorQueueAndCache checks the queue's and the cache's doctor
// checks (the cache's here, where both are set up).
func TestDoctorQueueAndCache(t *testing.T) {
	tests := []struct {
		env  config.Map
		want []string
	}{
		{config.Map{"APP_ENV": "development"}, []string{"ok queue", "ok cache"}},
		{config.Map{"APP_ENV": "production"}, []string{"note queue: QUEUE_DRIVER=sync", "note cache: CACHE_DRIVER=memory"}},
		{config.Map{"APP_ENV": "production", "QUEUE_DRIVER": "memory"}, []string{"warning queue: QUEUE_DRIVER=memory: waiting jobs are lost"}},
	}
	for _, tt := range tests {
		app, err := anetos.New(anetos.WithSource(tt.env), anetos.WithLogger(slog.New(slog.DiscardHandler)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cache.New(app); err != nil {
			t.Fatal(err)
		}
		if _, err := queue.New(app); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		app.ExecuteArgs(context.Background(), []string{"doctor"}, &out, &out)
		got := strings.Join(strings.Fields(out.String()), " ")
		for _, w := range tt.want {
			if !strings.Contains(got, w) {
				t.Errorf("%v: no %q in:\n%s", tt.env, w, out.String())
			}
		}
	}
}
