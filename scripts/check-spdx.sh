#!/usr/bin/env sh
# SPDX-License-Identifier: Apache-2.0
#
# Fails if any Go file does not start with the Apache-2.0 SPDX header.
# Generated files ("// Code generated … DO NOT EDIT.") are exempt.
# See docs/contributing/documentation-guide.md §6.
set -eu

header='// SPDX-License-Identifier: Apache-2.0'
status=0

for f in $(git ls-files '*.go' 2>/dev/null || find . -name '*.go' -not -path './.git/*'); do
	first=$(head -n 1 "$f")
	case $first in "// Code generated "*" DO NOT EDIT.") continue ;; esac
	if [ "$first" != "$header" ]; then
		echo "missing SPDX header: $f"
		status=1
	fi
done

exit $status
