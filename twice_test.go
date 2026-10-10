// SPDX-License-Identifier: Apache-2.0

package anetos_test

import (
	"io"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/pubsub"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/storage"
)

// TestNewTwice: each service's New for one app builds it once; a second
// call is an error (D310), except encryption.New, which returns the same
// encrypter.
func TestNewTwice(t *testing.T) {
	newApp := func() *anetos.App {
		app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(),
			"STORAGE_ROOT": t.TempDir()}), anetos.WithLogOutput(io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { app.Close() })
		return app
	}
	for name, newFn := range map[string]func(*anetos.App) error{
		"cache":    func(a *anetos.App) error { _, err := cache.New(a); return err },
		"queue":    func(a *anetos.App) error { _, err := queue.New(a); return err },
		"pubsub":   func(a *anetos.App) error { _, err := pubsub.New(a); return err },
		"session":  func(a *anetos.App) error { _, err := session.New(a); return err },
		"events":   func(a *anetos.App) error { _, err := events.New(a); return err },
		"schedule": func(a *anetos.App) error { _, err := schedule.New(a); return err },
		"mailer":   func(a *anetos.App) error { _, err := mailer.New(a); return err },
		"storage":  func(a *anetos.App) error { _, err := storage.New(a); return err },
	} {
		app := newApp()
		if err := newFn(app); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := newFn(app); err == nil || !strings.Contains(err.Error(), name+": New called twice for one app") {
			t.Errorf("%s twice: %v", name, err)
		}
	}
	app := newApp()
	e1, err := encryption.New(app)
	if err != nil {
		t.Fatal(err)
	}
	if e2, err := encryption.New(app); err != nil || e2 != e1 {
		t.Errorf("encryption.New again: %p %v, want %p", e2, err, e1)
	}
}
