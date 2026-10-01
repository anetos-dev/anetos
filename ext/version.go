// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"fmt"
	"strconv"
	"strings"
)

// semver is a version's numbers; a pre-release suffix is ignored.
type semver [3]int

func parseVersion(v string) (semver, error) {
	s, ok := strings.CutPrefix(strings.TrimSpace(v), "v")
	if !ok {
		return semver{}, fmt.Errorf("version %q must start with v (v0.2.0)", v)
	}
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("version %q must be vMAJOR.MINOR.PATCH", v)
	}
	var out semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return semver{}, fmt.Errorf("version %q must be vMAJOR.MINOR.PATCH", v)
		}
		out[i] = n
	}
	return out, nil
}

func (a semver) compare(b semver) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Satisfies reports whether version satisfies constraint: comparisons
// (>=, >, <=, <, =) joined by commas, such as ">= v0.2.0, < v0.4.0". A
// pre-release version counts as its release: v0.2.0-dev satisfies
// ">= v0.2.0".
func Satisfies(version, constraint string) (bool, error) {
	v, err := parseVersion(version)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(constraint) == "" {
		return false, fmt.Errorf("empty version constraint")
	}
	ok := true
	for c := range strings.SplitSeq(constraint, ",") {
		c = strings.TrimSpace(c)
		var op string
		for _, o := range []string{">=", "<=", ">", "<", "="} {
			if strings.HasPrefix(c, o) {
				op = o
				break
			}
		}
		if op == "" {
			return false, fmt.Errorf("version constraint %q: use >=, >, <=, < or = and a version", c)
		}
		w, err := parseVersion(strings.TrimSpace(c[len(op):]))
		if err != nil {
			return false, fmt.Errorf("version constraint %q: %w", c, err)
		}
		cmp := v.compare(w)
		switch op {
		case ">=":
			ok = ok && cmp >= 0
		case ">":
			ok = ok && cmp > 0
		case "<=":
			ok = ok && cmp <= 0
		case "<":
			ok = ok && cmp < 0
		case "=":
			ok = ok && cmp == 0
		}
	}
	return ok, nil
}
