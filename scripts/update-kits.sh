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
set -eu

kit=${1:?kit: pico, bootstrap or bulma}
version=${2:?version}
dir=$(cd "$(dirname "$0")/.." && pwd)/cli/internal/scaffold/templates/kits/$kit/public/static
case $kit in
pico) package=@picocss/pico files="css/pico.min.css" ;;
bootstrap) package=bootstrap files="dist/css/bootstrap.min.css dist/js/bootstrap.bundle.min.js" ;;
bulma) package=bulma files="css/bulma.min.css" ;;
*) echo "unknown kit $kit" >&2; exit 2 ;;
esac

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
