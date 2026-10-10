// SPDX-License-Identifier: Apache-2.0

// Package cmdname finds the command a shortened name means, much as
// Laravel's artisan does: each part of the name, between colons, may be
// cut short while the name still means one command with as many parts
// ("r:l" is route:list, "g" is generate). It is shared by the app binary
// and the anetos tool.
package cmdname

import (
	"slices"
	"strings"
)

// Match returns the command of names that name means: name itself, or
// the only one whose parts each start with name's (with as many parts),
// or of several such, the only one with the most of name's parts whole.
// It returns "" and the candidates, sorted, when name is ambiguous, and
// "" and none when nothing matches.
func Match(names []string, name string) (string, []string) {
	if slices.Contains(names, name) {
		return name, nil
	}
	parts := strings.Split(name, ":")
	if slices.Contains(parts, "") {
		return "", nil
	}
	var found []string
	for _, n := range names {
		np := strings.Split(n, ":")
		if len(np) != len(parts) {
			continue
		}
		ok := true
		for i, p := range parts {
			if !strings.HasPrefix(np[i], p) {
				ok = false
				break
			}
		}
		if ok {
			found = append(found, n)
		}
	}
	slices.Sort(found)
	if len(found) == 1 {
		return found[0], nil
	}
	// Of several, the one with the most parts written whole: "m:admin"
	// is make:admin, not make:admin-resource.
	best, top, tie := "", -1, false
	for _, n := range found {
		whole := 0
		for i, p := range strings.Split(n, ":") {
			if p == parts[i] {
				whole++
			}
		}
		switch {
		case whole > top:
			best, top, tie = n, whole, false
		case whole == top:
			tie = true
		}
	}
	if top > 0 && !tie {
		return best, nil
	}
	return "", found
}
