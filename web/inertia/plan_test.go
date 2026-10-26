// SPDX-License-Identifier: Apache-2.0

package inertia

import (
	"reflect"
	"strings"
	"testing"
)

// Two fields tagged with one name at one depth: encoding/json would leave
// both out; NewPage refuses them (go vet reports them too, so the type is
// built here).
func TestPlanTwoTagged(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeFor[string](), Tag: `json:"t"`},
		{Name: "B", Type: reflect.TypeFor[string](), Tag: `json:"t"`},
	})
	if _, err := planFor(typ); err == nil || !strings.Contains(err.Error(), `two fields tagged "t" at one depth`) {
		t.Errorf("planFor: %v", err)
	}
}
