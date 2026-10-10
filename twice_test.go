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

// TestSettingsCheck: doctor names the settings read under their former
// names, and .env keys of the framework's areas that nothing reads.
func TestSettingsCheck(t *testing.T) {
	var logs strings.Builder
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(),
		"CACHE_STORE": "memory", "QUEUE_CONNECTION": "redis", "QUEUE_DRIVR": "sync", "MYAPP_THING": "x",
		"QUEUE_POLL": "2s", "QUEUE_POLL_INTERVAL": "3s", "MAIL_POSTMARK_TOKEN": "unused"}),
		anetos.WithLogOutput(&logs))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, err := cache.New(app); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.New(app); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "CACHE_STORE is renamed CACHE_DRIVER") {
		t.Errorf("log: %s", logs.String())
	}
	_, out := doctor(t, app)
	for _, want := range []string{
		"CACHE_STORE is renamed CACHE_DRIVER",
		"QUEUE_CONNECTION in your .env isn't a setting Anetos reads: use QUEUE_DRIVER",
		"QUEUE_DRIVR in your .env isn't a setting Anetos reads: did you mean QUEUE_DRIVER?",
		"QUEUE_POLL in your .env is the former name of QUEUE_POLL_INTERVAL, which is set: remove QUEUE_POLL",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "MYAPP_THING") || strings.Contains(out, "MAIL_POSTMARK_TOKEN") {
		t.Errorf("doctor reports the app's own key:\n%s", out)
	}
}
