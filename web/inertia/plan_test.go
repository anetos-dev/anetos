// SPDX-License-Identifier: Apache-2.0

package inertia

import (
	"reflect"
	"strings"
	"testing"
)

// Two fields tagged with one name at one depth are a mistake (go vet
// reports it in source, so the type is made here).
func TestPlanTwoTags(t *testing.T) {
	inner := func(name string) reflect.Type {
		return reflect.StructOf([]reflect.StructField{{Name: name, Type: reflect.TypeFor[string](), Tag: `json:"x"`}})
	}
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: inner("X"), Anonymous: true},
		{Name: "B", Type: inner("Y"), Anonymous: true},
	})
	if _, err := planFor(typ); err == nil || !strings.Contains(err.Error(), `two fields tagged "x" at one depth`) {
		t.Errorf("planFor: %v", err)
	}
}
