# Benchmarks

Anetos's overhead compared with plain `net/http` doing the same work
(design §22). Each release records a baseline here; performance targets
are set from the v0.1 baseline and published with their method in v0.3
(roadmap M6).

| File | What |
|---|---|
| [v0.1-baseline.md](v0.1-baseline.md) | The v0.1 baseline: results, method, analysis |
| [v0.1-baseline.txt](v0.1-baseline.txt) | Raw `go test -bench` output of that run, for `benchstat` |

The code is in [`bench/`](../../bench/bench_test.go). To compare a change
with the baseline:

```sh
cd bench
go test -run '^$' -bench . -benchmem -count 8 . > new.txt
go run golang.org/x/perf/cmd/benchstat@latest ../docs/benchmarks/v0.1-baseline.txt new.txt
```

Compare runs from the same machine only.
