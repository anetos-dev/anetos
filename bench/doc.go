// SPDX-License-Identifier: Apache-2.0

// Package bench measures Anetos against the same work written by hand
// with net/http and database/sql (hello, a typed JSON handler, a row by
// key, a list of 20 rows), measures each part of the page an app made
// with anetos new and make:auth serves and each middleware, and runs chi,
// Gin and Echo on the same hello and JSON work. budget_test.go holds the
// framework to its allocation budgets (make bench-check), and cmd/benchcmp
// compares two runs (make bench-compare, and the CI on pull requests).
// It is a module of its own, so the frameworks it compares with never
// reach an app; run it with
//
//	cd bench && go test -run '^$' -bench . -benchmem -count 8 . | tee new.txt
//
// and compare runs with benchstat. docs/benchmarks/ records the results.
package bench
