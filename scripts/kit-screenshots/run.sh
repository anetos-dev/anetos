#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Regenerates the design kits' screenshots in docs/site/images/kits/: a
# project per kit (anetos new --css=<kit>, make:crud, make:auth) against
# this checkout, served on free local ports (8180 and up), shot in light and dark by
# shots.py. Run it after changing a kit; see README.md for what it needs.
#
#   scripts/kit-screenshots/run.sh            every kit
#   scripts/kit-screenshots/run.sh pico bulma some of them
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
out=$repo/docs/site/images/kits
kits=${*:-anetos none pico bootstrap bulma tailwind}
work=$(mktemp -d)
pids=""
cleanup() {
	for p in $pids; do kill "$p" 2>/dev/null || true; done
	rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# up answers whether something answers on the port.
up() { curl -s -o /dev/null --max-time 2 "http://127.0.0.1:$1/"; }

(cd "$repo/cli" && go build -o "$work/bin/anetos" ./cmd/anetos)
port=8180
targets=""
for kit in $kits; do
	dir=$work/kits/$kit
	mkdir -p "$dir"
	echo "$kit: making the project"
	(cd "$dir" && "$work/bin/anetos" new shop --css="$kit" --replace="$repo" >"$work/$kit-new.log" 2>&1) ||
		{ cat "$work/$kit-new.log" >&2; exit 1; }
	(
		cd "$dir/shop"
		go tool anetos make:crud Product name:string price:float in_stock:bool notes:text:optional >/dev/null
		go tool anetos make:auth >/dev/null
		go build -o "$work/bin/$kit-shop" .
		"$work/bin/$kit-shop" migrate >/dev/null
	)
	while up "$port"; do port=$((port + 1)); done # taken
	(cd "$dir/shop" && HTTP_ADDR=127.0.0.1:$port exec "$work/bin/$kit-shop" serve >"$work/$kit-serve.log" 2>&1) &
	pid=$!
	pids="$pids $pid"
	tries=0
	until up "$port"; do
		tries=$((tries + 1))
		if ! kill -0 "$pid" 2>/dev/null || [ "$tries" -ge 40 ]; then
			echo "$kit: the app didn't start on port $port" >&2
			cat "$work/$kit-serve.log" >&2
			exit 1
		fi
		sleep 0.5
	done
	targets="$targets $kit=http://127.0.0.1:$port"
	port=$((port + 1))
done
mkdir -p "$out"
python3 "$here/shots.py" "$out" $targets
