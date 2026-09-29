// SPDX-License-Identifier: Apache-2.0

package convert

import (
	"log/slog"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

func set[T any](t *testing.T, raw string) (T, error) {
	t.Helper()
	var v T
	s, err := For(reflect.TypeFor[T]())
	if err != nil {
		t.Fatalf("For(%T): %v", v, err)
	}
	err = s(reflect.ValueOf(&v).Elem(), raw)
	return v, err
}

func TestFor(t *testing.T) {
	if v, err := set[string](t, "x"); err != nil || v != "x" {
		t.Error("string")
	}
	if v, err := set[bool](t, "true"); err != nil || !v {
		t.Error("bool")
	}
	if v, err := set[int8](t, "-12"); err != nil || v != -12 {
		t.Error("int8")
	}
	if _, err := set[int8](t, "300"); err == nil {
		t.Error("int8 overflow accepted")
	}
	if v, err := set[uint16](t, "65535"); err != nil || v != 65535 {
		t.Error("uint16")
	}
	if v, err := set[float32](t, "1.5"); err != nil || v != 1.5 {
		t.Error("float32")
	}
	if v, err := set[time.Duration](t, "1m30s"); err != nil || v != 90*time.Second {
		t.Error("duration")
	}
	if v, err := set[*int](t, "5"); err != nil || v == nil || *v != 5 {
		t.Error("pointer")
	}
	if v, err := set[time.Time](t, "2026-09-30T10:00:00Z"); err != nil || v.Year() != 2026 {
		t.Errorf("time: %v %v", v, err)
	}
	if v, err := set[netip.Addr](t, "10.0.0.1"); err != nil || v.String() != "10.0.0.1" {
		t.Error("netip.Addr")
	}
	if _, err := set[slog.Level](t, "loud"); err == nil || !strings.Contains(err.Error(), `invalid slog.Level "loud"`) {
		t.Errorf("text unmarshaler error = %v", err)
	}
	for raw, want := range map[string]bool{"on": true, "YES": true, "off": false, "No": false, "1": true, "F": false} {
		if v, err := set[bool](t, raw); err != nil || v != want {
			t.Errorf("bool %q = %v, %v", raw, v, err)
		}
	}
	for _, raw := range []string{"maybe", "x", ""} {
		if _, err := set[bool](t, raw); err == nil {
			t.Errorf("bool %q accepted", raw)
		}
	}
	if _, err := For(reflect.TypeFor[map[string]int]()); err == nil {
		t.Error("map supported")
	}
	if _, err := For(reflect.TypeFor[*chan int]()); err == nil {
		t.Error("pointer to unsupported supported")
	}
}
