// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"slices"
	"testing"

	"anetos.dev/anetos"
)

func TestInternalFrames(t *testing.T) {
	mods := []string{ // longest first, as modules() sorts them
		"anetos.dev/anetos/examples/database",
		"anetos.dev/anetos/drivers/sqlite",
		"anetos.dev/anetos-contrib",
		"github.com/acme/shop.example",
		"anetos.dev/anetos",
		"golang.org/x/sync",
		"blog",
	}
	for fn, want := range map[string]bool{
		"anetos.dev/anetos/db.(*DB).query":                   true,
		"anetos.dev/anetos/db.Query[...].Get":                true,
		"anetos.dev/anetos.(*App).Execute":                   true,
		"anetos.dev/anetos/drivers/sqlite.(*conn).Query":     true,
		"anetos.dev/anetos/examples/database.Blog.ListPosts": false,
		"anetos.dev/anetos-contrib/pagination.Links":         false,
		"blog/app/handlers.Posts.Index":                               false,
		"blog.Handler":                                                false,
		"github.com/acme/shop.example.Handler":                        false,
		"github.com/acme/shop.example/handlers.Index":                 false,
		"blog/app/handlers.Posts.Index.func1":                         false,
		"main.TestNoNPlusOne":                                         false,
		"main.(*Blog).ListPosts":                                      false,
		"golang.org/x/sync/errgroup.(*Group).Go.func1":                false,
		"slices.Chunk[...].func1":                                     true,
		"net/http.HandlerFunc.ServeHTTP":                              true,
		"testing.tRunner":                                             true,
		"runtime.goexit":                                              true,
		"encoding/json.Marshal":                                       true,
	} {
		if got := internal(fn, mods); got != want {
			t.Errorf("internal(%s) = %v", fn, got)
		}
	}
	// Without build information, names decide.
	for fn, want := range map[string]bool{
		"anetos.dev/anetos/db.(*DB).query":                   true,
		"anetos.dev/anetos/examples/database.Blog.ListPosts": false,
		"anetos.dev/anetosapp/handlers.Index":                false,
		"blog/handlers.Index":                                         false,
		"slices.Chunk[...].func1":                                     true,
	} {
		if got := internal(fn, nil); got != want {
			t.Errorf("internal(%s, nil) = %v", fn, got)
		}
	}
	// From this package's test, every frame is the framework's or the
	// standard library's.
	if c := caller(); c != "" {
		t.Errorf("caller() = %q", c)
	}
}

func TestTrackingContexts(t *testing.T) {
	d := New(nil, nil, WithRepeatedQueries(2))
	var reps []RepeatedQuery
	d.OnRepeatedQuery(func(_ context.Context, r RepeatedQuery) { reps = append(reps, r) })
	u := anetos.Unit{Kind: "request", Name: "GET /"}

	ctx, end := d.Track(context.Background(), u)
	b := inBatch(ctx) // a chunked operation: counted once
	for range 3 {
		d.count(b, "INSERT chunk")
	}
	quiet := Untracked(ctx)
	for range 3 {
		d.count(quiet, "SELECT quiet")
	}
	// A unit inside Untracked, or inside a batch, is tracked afresh.
	for _, outer := range []context.Context{quiet, b} {
		jctx, jend := d.Track(outer, anetos.Unit{Kind: "job", Name: "J"})
		d.count(jctx, "SELECT job")
		d.count(jctx, "SELECT job")
		jend()
	}
	d.count(ctx, "SELECT b")
	d.count(ctx, "SELECT b")
	d.count(ctx, "SELECT a")
	d.count(ctx, "SELECT a")
	end()
	var got []string
	for _, r := range reps {
		got = append(got, r.Unit.Name+" "+r.SQL)
	}
	if want := []string{"J SELECT job", "J SELECT job", "GET / SELECT a", "GET / SELECT b"}; !slices.Equal(got, want) {
		t.Errorf("reports %v, want %v", got, want)
	}
	// Without tracking, inBatch adds nothing.
	if bg := context.Background(); inBatch(bg) != bg {
		t.Error("inBatch without a tracked unit changed the context")
	}
}
