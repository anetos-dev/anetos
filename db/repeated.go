// SPDX-License-Identifier: Apache-2.0

package db

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"

	"anetos.dev/anetos"
)

// RepeatedQuery is a query a unit of work (a request, a job…) ran many
// times: the sign of an N+1, a query in a loop that one query (eager
// loading with With, WhereIn, a join) could replace.
type RepeatedQuery struct {
	// Unit is the unit of work: "request GET /posts", "job SendDigest".
	Unit anetos.Unit
	// SQL is the query, with placeholders: every run had this text, with
	// whatever arguments.
	SQL string
	// Count is how many times the unit ran it.
	Count int
	// Caller is where the app ran it from ("handlers/posts.go:42"): the
	// first frame outside the framework and the standard library when
	// Count reached the threshold (a plugin's code counts as the app's);
	// empty when there is none.
	Caller string
}

// String describes r in one line.
func (r RepeatedQuery) String() string {
	at := ""
	if r.Caller != "" {
		at = " at " + r.Caller
	}
	return fmt.Sprintf("%s %s ran the same query %d times%s: %s", r.Unit.Kind, r.Unit.Name, r.Count, at, r.SQL)
}

// WithRepeatedQueries makes [DB.Track] report queries a unit of work
// runs n or more times; n below 2 disables it. [Connect] sets it from
// DB_REPEATED_QUERIES (with a default in development and testing), and
// [Open] from Config.RepeatedQueries when set. For a database opened
// with Open, have the app's units tracked with app.AroundUnits(d.Track).
func WithRepeatedQueries(n int) Option { return func(d *DB) { d.repeated = n } }

// OnRepeatedQuery calls fn, besides logging a warning, for each repeated
// query [DB.Track] finds from now on: anetostest records them. fn must be
// safe for concurrent use.
func (d *DB) OnRepeatedQuery(fn func(ctx context.Context, r RepeatedQuery)) {
	d.repMu.Lock()
	defer d.repMu.Unlock()
	d.repObservers = append(d.repObservers, fn)
}

// trackKey holds a DB's tracker in a context.
type trackKey struct{ d *DB }

// untrackedKey marks contexts whose queries aren't counted. Track resets
// it (a nil value), so a unit started in an untracked loop is tracked.
type untrackedKey struct{}

// batchKey holds the batch of a chunked operation ([inBatch]).
type batchKey struct{}

// batch is one operation the db package splits into statements (IN lists
// and inserts in chunks): repeats of one statement in it count once.
type batch struct {
	mu   sync.Mutex
	seen map[string]bool
}

// trackingKey marks contexts of tracked units, whatever the DB.
type trackingKey struct{}

// inBatch returns ctx for an operation run in chunks: ctx itself when no
// unit tracks queries.
func inBatch(ctx context.Context) context.Context {
	if ctx.Value(trackingKey{}) == nil {
		return ctx
	}
	return context.WithValue(ctx, batchKey{}, &batch{seen: map[string]bool{}})
}

// tracker counts a unit's queries by their SQL.
type tracker struct {
	mu     sync.Mutex
	counts map[string]*seen
}

type seen struct {
	n      int
	caller string
}

// Track counts the queries made on d with the returned context, until
// the returned function is called; then it logs a warning (and calls the
// [DB.OnRepeatedQuery] functions) for each query run at least the
// threshold number of times ([WithRepeatedQueries]). [Connect] has every
// unit of work of the app tracked this way (anetos.App.AroundUnits).
// Without a threshold, it returns ctx and a no-op.
func (d *DB) Track(ctx context.Context, u anetos.Unit) (context.Context, func()) {
	if d.repeated < 2 {
		return ctx, func() {}
	}
	t := &tracker{counts: map[string]*seen{}}
	// A fresh start: neither an outer Untracked nor an outer batch applies.
	ctx = context.WithValue(ctx, untrackedKey{}, nil)
	ctx = context.WithValue(ctx, batchKey{}, nil)
	ctx = context.WithValue(ctx, trackingKey{}, true)
	ctx = context.WithValue(ctx, trackKey{d}, t)
	return ctx, func() { d.report(ctx, u, t) }
}

// Untracked returns ctx with its queries left out of repeated-query
// detection ([DB.Track]), for code whose repeated queries are by design:
// the framework's database stores use it.
func Untracked(ctx context.Context) context.Context {
	return context.WithValue(ctx, untrackedKey{}, true)
}

// count counts a query made with ctx, if a unit tracks them.
func (d *DB) count(ctx context.Context, query string) {
	if d.repeated < 2 {
		return
	}
	t, ok := ctx.Value(trackKey{d}).(*tracker)
	if !ok || ctx.Value(untrackedKey{}) != nil {
		return
	}
	if b, ok := ctx.Value(batchKey{}).(*batch); ok {
		b.mu.Lock()
		again := b.seen[query]
		b.seen[query] = true
		b.mu.Unlock()
		if again {
			return
		}
	}
	t.mu.Lock()
	s := t.counts[query]
	if s == nil {
		s = &seen{}
		t.counts[query] = s
	}
	s.n++
	first := s.n == d.repeated
	t.mu.Unlock()
	if first {
		c := caller()
		t.mu.Lock()
		s.caller = c
		t.mu.Unlock()
	}
}

// report reports t's repeated queries.
func (d *DB) report(ctx context.Context, u anetos.Unit, t *tracker) {
	t.mu.Lock()
	var reps []RepeatedQuery
	for q, s := range t.counts {
		if s.n >= d.repeated {
			reps = append(reps, RepeatedQuery{Unit: u, SQL: q, Count: s.n, Caller: s.caller})
		}
	}
	t.mu.Unlock()
	if len(reps) == 0 {
		return
	}
	slices.SortFunc(reps, func(a, b RepeatedQuery) int { return cmp.Or(b.Count-a.Count, strings.Compare(a.SQL, b.SQL)) })
	d.repMu.RLock()
	obs := d.repObservers
	d.repMu.RUnlock()
	for _, r := range reps {
		d.log.WarnContext(ctx, "repeated query: an N+1? Load related rows with With or a single query",
			"unit", u.Kind+" "+u.Name, "count", r.Count, "sql", r.SQL, "at", r.Caller)
		for _, fn := range obs {
			fn(ctx, r)
		}
	}
}

// frameworkModule is the framework's core module; its driver modules
// (drivers/…) are the framework too, while examples and first-party
// plugins (plugins/…) are apps' code.
const frameworkModule = "anetos.dev/anetos"

// caller returns where the app made the current query: the first frame
// that is neither the framework's nor the standard library's, as
// "dir/file.go:line".
func caller() string {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	mods := modules()
	for {
		f, more := frames.Next()
		if f.Function != "" && !internal(f.Function, mods) {
			return filepath.Join(filepath.Base(filepath.Dir(f.File)), filepath.Base(f.File)) + fmt.Sprintf(":%d", f.Line)
		}
		if !more {
			return ""
		}
	}
}

// modules returns the module paths of the binary's build information
// (the main module and its dependencies), longest first; nil without
// build information.
var modules = sync.OnceValue(func() []string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	paths := []string{bi.Main.Path}
	for _, m := range bi.Deps {
		paths = append(paths, m.Path)
	}
	slices.SortFunc(paths, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	return paths
})

// stdCallers are standard packages that call into code, for binaries
// without build information.
var stdCallers = []string{"runtime.", "database/sql.", "iter.", "slices.", "maps.", "sort.", "reflect.", "sync.", "context.", "net/http.", "testing.", "encoding/json.", "text/template.", "html/template."}

// moduleOf returns the module of the function fn (with its package
// path), among mods, longest first: the first whose path, followed by
// "." (its root package) or "/" (another package), starts fn; "" for
// none (the standard library).
func moduleOf(fn string, mods []string) string {
	for _, m := range mods {
		if m != "" && (strings.HasPrefix(fn, m+".") || strings.HasPrefix(fn, m+"/")) {
			return m
		}
	}
	return ""
}

// internal reports whether the function fn (a frame's, with its package
// path) is the framework's or the standard library's, by the module it
// belongs to among mods (without build information, mods is nil and its
// name decides).
func internal(fn string, mods []string) bool {
	if strings.HasPrefix(fn, "main.") {
		return false // the app's main package
	}
	if mods == nil {
		return strings.HasPrefix(fn, frameworkModule+".") ||
			strings.HasPrefix(fn, frameworkModule+"/") && !strings.HasPrefix(fn, frameworkModule+"/examples/") && !strings.HasPrefix(fn, frameworkModule+"/plugins/") ||
			slices.ContainsFunc(stdCallers, func(p string) bool { return strings.HasPrefix(fn, p) })
	}
	m := moduleOf(fn, mods)
	return m == "" || m == frameworkModule || strings.HasPrefix(m, frameworkModule+"/drivers/")
}
