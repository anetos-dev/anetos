// SPDX-License-Identifier: Apache-2.0

package anetos_test

import (
	"context"
	"io"
	"slices"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

type unitKey struct{}

func TestUnits(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing"}), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	ctx := context.Background()
	got, end := app.StartOperation(ctx, anetos.Operation{Kind: "job", Name: "x"})
	if got != ctx {
		t.Error("StartOperation without functions changed the context")
	}
	end()

	var log []string
	for _, name := range []string{"a", "b"} {
		app.AroundOperations(func(ctx context.Context, u anetos.Operation) (context.Context, func()) {
			log = append(log, "start "+name+" "+u.Kind+" "+u.Name)
			return context.WithValue(ctx, unitKey{}, name), func() { log = append(log, "end "+name) }
		})
	}
	app.AroundOperations(func(ctx context.Context, _ anetos.Operation) (context.Context, func()) { return ctx, nil }) // no end
	got, end = app.StartOperation(ctx, anetos.Operation{Kind: "request", Name: "GET /"})
	if got.Value(unitKey{}) != "b" {
		t.Errorf("context value %v", got.Value(unitKey{}))
	}
	end()
	if want := []string{"start a request GET /", "start b request GET /", "end b", "end a"}; !slices.Equal(log, want) {
		t.Errorf("log %v", log)
	}
}
