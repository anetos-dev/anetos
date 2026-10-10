// SPDX-License-Identifier: Apache-2.0

package cmdname

import (
	"slices"
	"testing"
)

func TestMatch(t *testing.T) {
	names := []string{"migrate", "migrate:fresh", "migrate:rollback", "make:model", "make:migration", "make:crud", "make:admin", "make:admin-resource", "route:list", "run", "generate", "doctor", "dev"}
	for _, tc := range []struct {
		in, want   string
		candidates []string
	}{
		{"route:list", "route:list", nil},
		{"r:l", "route:list", nil},
		{"ro:li", "route:list", nil},
		{"mi", "migrate", nil},
		{"m:f", "migrate:fresh", nil},
		{"m:c", "make:crud", nil},
		{"g", "generate", nil},
		{"r", "run", nil}, // route:list has two parts
		{"d", "", []string{"dev", "doctor"}},
		{"m:m", "", []string{"make:migration", "make:model"}},
		{"m:admin", "make:admin", nil},
		{"m:admin-r", "make:admin-resource", nil},
		{"m:a", "", []string{"make:admin", "make:admin-resource"}},
		{"x", "", nil},
		{"r:", "", nil},
		{":l", "", nil},
		{"", "", nil},
		{"R:L", "", nil},
	} {
		got, c := Match(names, tc.in)
		if got != tc.want || !slices.Equal(c, tc.candidates) {
			t.Errorf("Match(%q) = %q %v, want %q %v", tc.in, got, c, tc.want, tc.candidates)
		}
	}
}
