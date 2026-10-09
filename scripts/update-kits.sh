#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Fetches a CSS framework's files for its design kit from the npm
# registry, into cli/internal/scaffold/templates/kits/<kit>/public/static,
# as released, with its license. The versions are scaffold.KitVersions:
# change one there, run this script (TestKitVersions checks they match),
# then check the kit's pages, light and dark, and a phone's width.
#
#   scripts/update-kits.sh pico 2.1.1
#   scripts/update-kits.sh bootstrap 5.3.8
#   scripts/update-kits.sh bulma 1.0.4
#   scripts/update-kits.sh tailwind 4.3.3
#
# Tailwind CSS is run, not shipped: the CLI downloads its standalone
# binary (cli/internal/tailwind). For a new release, the script prints
# Version and the digests of its binaries to paste into
# cli/internal/tailwind/tailwind.go; run it again once they're there, and
# it compiles the kit's app.css with that release (go test
# -update-tailwind) and fetches the license.
set -eu

kit=${1:?kit: pico, bootstrap, bulma or tailwind}
version=${2:?version}
repo=$(cd "$(dirname "$0")/.." && pwd)
dir=$repo/cli/internal/scaffold/templates/kits/$kit/public/static
case $kit in
pico) package=@picocss/pico files="css/pico.min.css" ;;
bootstrap) package=bootstrap files="dist/css/bootstrap.min.css dist/js/bootstrap.bundle.min.js" ;;
bulma) package=bulma files="css/bulma.min.css" ;;
tailwind) package=tailwindcss files="" ;;
*) echo "unknown kit $kit" >&2; exit 2 ;;
esac

if [ "$kit" = tailwind ] && ! grep -q "^const Version = \"$version\"" "$repo/cli/internal/tailwind/tailwind.go"; then
	sums=$(curl -fsSL "https://github.com/tailwindlabs/tailwindcss/releases/download/v$version/sha256sums.txt")
	echo "In cli/internal/tailwind/tailwind.go:"
	echo
	echo "const Version = \"$version\""
	echo
	echo "var checksums = map[string]string{"
	echo "$sums" | awk '{ sub(/^\.\//, "", $2); printf "\t\"%s\": \"%s\",\n", $2, $1 }'
	echo "}"
	echo
	echo "then run this script again."
	exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
(cd "$tmp" && npm pack --silent "$package@$version" >/dev/null && tar xzf ./*.tgz)
mkdir -p "$dir"
for f in $files; do
	cp "$tmp/package/$f" "$dir/$(basename "$f")"
	echo "$dir/$(basename "$f")"
done
for lic in "$tmp"/package/LICENSE*; do
	[ -f "$lic" ] || { echo "$package@$version has no LICENSE" >&2; exit 1; }
	cp "$lic" "$dir/$kit.LICENSE.txt"
	echo "$dir/$kit.LICENSE.txt"
	break
done
if [ "$kit" = tailwind ]; then
	# The stylesheet the release compiles from the kit's components.
	(cd "$repo/cli" && unset ANETOS_TAILWIND && ANETOS_TEST_TAILWIND=1 go test -count=1 -run TestTailwindKitCSS ./internal/scaffold -update-tailwind >"$tmp/test.log" 2>&1) || { cat "$tmp/test.log" >&2; exit 1; }
	echo "$dir/app.css"
fi
if [ "$kit" = bootstrap ]; then
	# bootstrap.bundle.min.js includes Popper (MIT): its license too.
	popper=$(cd "$tmp" && npm view --silent "bootstrap@$version" peerDependencies.@popperjs/core)
	popper=${popper#^}
	case $popper in
	[0-9]*.[0-9]*.[0-9]*) ;;
	*) echo "bootstrap@$version: unexpected Popper version '$popper'" >&2; exit 1 ;;
	esac
	(cd "$tmp" && npm pack --silent "@popperjs/core@$popper" >/dev/null && mkdir popper && tar xzf popperjs-core-*.tgz -C popper)
	cp "$tmp/popper/package/LICENSE.md" "$dir/popper.LICENSE.txt"
	echo "$dir/popper.LICENSE.txt"
fi
