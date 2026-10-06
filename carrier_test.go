// SPDX-License-Identifier: Apache-2.0

package anetos_test

import (
	"context"
	"io"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

type carriedKey struct{}

func TestCarriers(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing"}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	ctx := context.Background()
	if got := app.Carried(ctx); got != nil {
		t.Errorf("Carried without carriers = %v", got)
	}
	if app.WithCarried(ctx, map[string]string{"x": "1"}) != ctx {
		t.Error("WithCarried without carriers changed the context")
	}
	c := anetos.Carrier{
		Name:    "test.value",
		Capture: func(ctx context.Context) string { s, _ := ctx.Value(carriedKey{}).(string); return s },
		Restore: func(ctx context.Context, v string) context.Context {
			return context.WithValue(ctx, carriedKey{}, "restored "+v)
		},
	}
	app.AddCarrier(c)
	if got := app.Carried(ctx); got != nil {
		t.Errorf("Carried with nothing to carry = %v", got)
	}
	got := app.Carried(context.WithValue(ctx, carriedKey{}, "42"))
	if got["test.value"] != "42" || len(got) != 1 {
		t.Errorf("Carried = %v", got)
	}
	back := app.WithCarried(ctx, map[string]string{"test.value": "42", "unknown": "x"})
	if v := back.Value(carriedKey{}); v != "restored 42" {
		t.Errorf("WithCarried restored %v", v)
	}
	for name, bad := range map[string]anetos.Carrier{
		"twice":      c,
		"bad name":   {Name: "Bad Name", Capture: c.Capture, Restore: c.Restore},
		"no capture": {Name: "x", Restore: c.Restore},
		"no restore": {Name: "y", Capture: c.Capture},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("AddCarrier(%s) didn't panic", name)
				}
			}()
			app.AddCarrier(bad)
		}()
	}
}
