// SPDX-License-Identifier: Apache-2.0

package anetos_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

func TestClock(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing"}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	ctx := app.Context(context.Background()) // made before SetClock: follows it
	if d := time.Since(anetos.Now(ctx)); d < 0 || d > time.Second {
		t.Errorf("Now = %v", anetos.Now(ctx))
	}
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	app.SetClock(func() time.Time { return at })
	if !app.Now().Equal(at) || !anetos.Now(ctx).Equal(at) {
		t.Errorf("after SetClock: %v, %v", app.Now(), anetos.Now(ctx))
	}
	app.SetClock(nil)
	if anetos.Now(ctx).Equal(at) {
		t.Error("SetClock(nil) kept the clock")
	}
	if d := time.Since(anetos.Now(context.Background())); d < 0 || d > time.Second {
		t.Error("Now without an app isn't the system's time")
	}
	if got := anetos.Now(anetos.WithClock(context.Background(), func() time.Time { return at })); !got.Equal(at) {
		t.Errorf("WithClock: %v", got)
	}
}

func TestLogger(t *testing.T) {
	var out strings.Builder
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing", "APP_NAME": "shop"}), anetos.WithLogOutput(&out))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if anetos.Logger(app.Context(context.Background())) != app.Logger() {
		t.Error("Logger(app context) isn't the app's logger")
	}
	anetos.Logger(app.Context(context.Background())).Info("hello")
	if !strings.Contains(out.String(), "app=shop") {
		t.Errorf("log: %q", out.String())
	}
	if anetos.Logger(context.Background()) != slog.Default() {
		t.Error("Logger without an app isn't slog.Default()")
	}
}
