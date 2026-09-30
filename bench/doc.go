// SPDX-License-Identifier: Apache-2.0

// Package bench compares Anetos with plain net/http on the same requests:
// a hello-world response, a typed JSON handler with validation, and a
// single-row database read. It is a module of its own; run it with
//
//	cd bench && go test -run '^$' -bench . -benchmem -count 8 . | tee new.txt
//
// and compare runs with benchstat. docs/benchmarks/ records the results.
package bench
