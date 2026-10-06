# Benchmarks

Anetos's overhead compared with plain `net/http` and `database/sql`
doing the same work, and with other Go frameworks (design §22). Each
release records its results here, with the method.

| File | What |
|---|---|
| [v0.3.md](v0.3.md) | v0.3 (M6): requests, queries, the page of an app and its parts, each middleware, chi, Gin and Echo; M6's optimizations; the gate |
| [v0.3.txt](v0.3.txt) | Raw `go test -bench` output of that run, for `benchstat` |
| [v0.3-m6-before.txt](v0.3-m6-before.txt), [v0.3-m6-after.txt](v0.3-m6-after.txt) | Before and after M6's optimizations, run in turns |
| [v0.1-baseline.md](v0.1-baseline.md) | The v0.1 baseline: results, method, analysis |
| [v0.1-baseline.txt](v0.1-baseline.txt) | Raw output of that run |

The code is in [`bench/`](../../bench), a module of its own (its
comparison frameworks never reach an app). To run everything:

```sh
cd bench
go test -run '^$' -bench . -benchmem -count 8 . > new.txt
go run golang.org/x/perf/cmd/benchstat@latest ../docs/benchmarks/v0.3.txt new.txt
```

Compare runs from the same machine only. To compare a change with
`main` on this machine, both built and run in turns, as the CI does for
pull requests:

```sh
make bench-compare            # BASE=main by default
make bench-check              # the allocation budgets (bench/budget_test.go)
```

`bench-compare` fails when a benchmark makes more allocations, or is
more than 20% slower in every sample (design D251).
