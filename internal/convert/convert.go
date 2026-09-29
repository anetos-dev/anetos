// SPDX-License-Identifier: Apache-2.0

// Package convert turns strings into typed Go values. It is shared by the
// config binder and the HTTP request binder so both accept the same formats.
//
// [For] inspects a type once (at startup or route registration) and returns
// a [Setter] that does the per-value work without further type switching.
package convert

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Setter parses raw and stores the result in v, which must be settable and
// of the type the Setter was built for.
type Setter func(v reflect.Value, raw string) error

var (
	durationType        = reflect.TypeFor[time.Duration]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// parseBool accepts strconv.ParseBool's forms plus "on"/"off" (what HTML
// checkboxes send) and "yes"/"no", in any case.
func parseBool(raw string) (bool, error) {
	switch strings.ToLower(raw) {
	case "on", "yes":
		return true, nil
	case "off", "no":
		return false, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid boolean %q (use true or false)", raw)
	}
	return b, nil
}

// For returns a Setter for t. Supported: string, bool, all int, uint and
// float kinds, time.Duration, types whose pointer implements
// encoding.TextUnmarshaler (time.Time, slog.Level, netip.Addr, …), and
// pointers to any of these. Slices are not handled here; callers decide how
// multiple values are represented.
func For(t reflect.Type) (Setter, error) {
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return func(v reflect.Value, raw string) error {
			u, _ := reflect.TypeAssert[encoding.TextUnmarshaler](v.Addr())
			if err := u.UnmarshalText([]byte(raw)); err != nil {
				return fmt.Errorf("invalid %s %q: %w", t, raw, err)
			}
			return nil
		}, nil
	}
	if t == durationType {
		return func(v reflect.Value, raw string) error {
			d, err := time.ParseDuration(raw)
			if err != nil {
				return fmt.Errorf("invalid duration %q (use values like 30s, 5m, 1h)", raw)
			}
			v.SetInt(int64(d))
			return nil
		}, nil
	}

	switch t.Kind() {
	case reflect.String:
		return func(v reflect.Value, raw string) error { v.SetString(raw); return nil }, nil
	case reflect.Bool:
		return func(v reflect.Value, raw string) error {
			b, err := parseBool(raw)
			if err != nil {
				return err
			}
			v.SetBool(b)
			return nil
		}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		bits := t.Bits()
		return func(v reflect.Value, raw string) error {
			n, err := strconv.ParseInt(raw, 10, bits)
			if err != nil {
				return fmt.Errorf("invalid integer %q", raw)
			}
			v.SetInt(n)
			return nil
		}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		bits := t.Bits()
		return func(v reflect.Value, raw string) error {
			n, err := strconv.ParseUint(raw, 10, bits)
			if err != nil {
				return fmt.Errorf("invalid unsigned integer %q", raw)
			}
			v.SetUint(n)
			return nil
		}, nil
	case reflect.Float32, reflect.Float64:
		bits := t.Bits()
		return func(v reflect.Value, raw string) error {
			f, err := strconv.ParseFloat(raw, bits)
			if err != nil {
				return fmt.Errorf("invalid number %q", raw)
			}
			v.SetFloat(f)
			return nil
		}, nil
	case reflect.Pointer:
		elem, err := For(t.Elem())
		if err != nil {
			return nil, err
		}
		return func(v reflect.Value, raw string) error {
			p := reflect.New(t.Elem())
			if err := elem(p.Elem(), raw); err != nil {
				return err
			}
			v.Set(p)
			return nil
		}, nil
	}
	return nil, fmt.Errorf("unsupported field type %s", t)
}
