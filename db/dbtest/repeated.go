// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
)

func init() {
	extra = append(extra, test{"RepeatedQueries", testRepeatedQueries})
}

// testRepeatedQueries checks that a tracked unit reports a query it ran
// at least the threshold number of times, once, with the caller.
func testRepeatedQueries(t *testing.T, ctx context.Context) {
	base, err := db.From(ctx)
	check(t, err)
	d := db.New(base.SQL(), base.Dialect(), db.WithRepeatedQueries(3))
	var mu sync.Mutex
	var reps []db.RepeatedQuery
	d.OnRepeatedQuery(func(_ context.Context, r db.RepeatedQuery) {
		mu.Lock()
		defer mu.Unlock()
		reps = append(reps, r)
	})
	ctx = db.WithDB(ctx, d)
	for i := range 4 {
		check(t, db.Create(ctx, &stAuthor{Name: fmt.Sprint("a", i), Email: fmt.Sprintf("a%d@example.com", i)}))
	}
	authors, err := db.Query[stAuthor](ctx).OrderBy(db.C("id").Asc()).Get()
	check(t, err)

	unit := anetos.Unit{Kind: "request", Name: "GET /authors"}
	tctx, end := d.Track(ctx, unit)
	for _, a := range authors { // an N+1: one query per author
		_, err := db.Query[stAuthor](tctx).Where(db.C("id").Eq(a.ID)).First()
		check(t, err)
	}
	for range 2 { // twice: under the threshold
		_, err := db.Query[stAuthor](tctx).Count()
		check(t, err)
	}
	quiet := db.Untracked(tctx)
	for range 5 { // left out
		_, err := db.Query[stAuthor](quiet).Exists()
		check(t, err)
	}
	// A nested unit counts its own queries.
	nctx, nend := d.Track(tctx, anetos.Unit{Kind: "job", Name: "Nested"})
	for range 3 {
		_, err := db.Query[stAuthor](nctx).Where(db.C("name").Eq("a0")).First()
		check(t, err)
	}
	nend()
	end()

	if len(reps) != 2 {
		t.Fatalf("reports: %+v", reps)
	}
	job, req := reps[0], reps[1]
	// The caller is the app's code: here, framework code called by the
	// testing package, so there is none.
	if req.Unit != unit || req.Count != 4 || !strings.Contains(req.SQL, "WHERE") || req.Caller != "" {
		t.Errorf("report %+v", req)
	}
	if job.Unit.Name != "Nested" || job.Count != 3 {
		t.Errorf("nested report %+v", job)
	}
	if s := req.String(); !strings.HasPrefix(s, "request GET /authors ran the same query 4 times: SELECT") {
		t.Errorf("String = %q", s)
	}
	// Off: nothing tracked.
	off := db.New(base.SQL(), base.Dialect())
	octx, oend := off.Track(ctx, unit)
	if octx != ctx {
		t.Error("Track without a threshold changed the context")
	}
	oend()
}
