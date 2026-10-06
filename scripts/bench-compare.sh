#!/usr/bin/env sh
# SPDX-License-Identifier: Apache-2.0
#
# Compares the benchmarks of the working tree with those of a git ref
# (default main): both test binaries are built, then run in turns on this
# machine, ROUNDS times each, so a slow moment hits both; then
# bench/cmd/benchcmp judges them (design §22). It exits 1 on a
# regression. The CI runs it on pull requests; run it with
#
#	make bench-compare BASE=main
#
# Settings (environment): BASE, ROUNDS (5), BENCHTIME (0.3s), BENCH (the
# gated benchmarks), THRESHOLD (0.2), OUT (a directory for the results).
set -eu

base=${BASE:-main}
rounds=${ROUNDS:-5}
benchtime=${BENCHTIME:-0.3s}
bench=${BENCH:-'Hello(Mux|Router|Server)$|JSON(Mux|Router|Server)$|DBRead|Query|Page|Middleware'}
threshold=${THRESHOLD:-0.2}
root=$(git rev-parse --show-toplevel)
out=${OUT:-$(mktemp -d)}
work=$(mktemp -d)
cleanup() {
	git -C "$root" worktree remove --force "$work/base" >/dev/null 2>&1 || true
	rm -rf "$work"
	git -C "$root" worktree prune >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'cleanup; exit 130' INT TERM

# A clone may have only the remote's branch.
if ! git -C "$root" rev-parse --verify --quiet "$base^{commit}" >/dev/null &&
	git -C "$root" rev-parse --verify --quiet "origin/$base^{commit}" >/dev/null; then
	base=origin/$base
fi
git -C "$root" worktree add --detach "$work/base" "$base" >/dev/null
if [ ! -d "$work/base/bench" ]; then
	echo "bench-compare: $base has no bench/ module to compare with" >&2
	exit 2
fi
echo "Building the benchmarks of $base and of the working tree…"
(cd "$work/base/bench" && go test -c -o "$work/old.test" .)
(cd "$root/bench" && go test -c -o "$work/new.test" .)

run_old() {
	(cd "$work/base/bench" && "$work/old.test" -test.run '^$' -test.bench "$bench" -test.benchmem -test.benchtime "$benchtime" >>"$out/old.txt")
}
run_new() {
	(cd "$root/bench" && "$work/new.test" -test.run '^$' -test.bench "$bench" -test.benchmem -test.benchtime "$benchtime" >>"$out/new.txt")
}

: >"$out/old.txt"
: >"$out/new.txt"
i=1
while [ "$i" -le "$rounds" ]; do
	echo "Round $i of $rounds"
	# Which runs first alternates, so neither always gets the warmer
	# (or the busier) turn.
	if [ $((i % 2)) -eq 1 ]; then
		run_old
		run_new
	else
		run_new
		run_old
	fi
	i=$((i + 1))
done

echo
status=0
(cd "$root/bench" && go run ./cmd/benchcmp -threshold "$threshold" "$out/old.txt" "$out/new.txt") >"$out/summary.md" || status=$?
cat "$out/summary.md"
echo "Results: $out"
exit "$status"
