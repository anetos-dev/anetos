// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"anetos.dev/anetos/internal/convert"
)

// ErrMissing is wrapped by a [FieldError] when a required key is not set.
var ErrMissing = errors.New("required but not set")

// Validator is implemented by config structs that check their own values.
// [Bind] calls Validate after all fields are populated, on nested structs
// first and then on the outer struct.
type Validator interface {
	// Validate reports what is wrong with the values, or nil.
	Validate() error
}

// FieldError describes a problem with a single configuration key.
type FieldError struct {
	Key   string // environment key, e.g. "DB_PORT"
	Field string // Go field path, e.g. "Database.Port"
	Err   error  // what is wrong: ErrMissing, a parse error, …
}

// Error implements the error interface.
func (e *FieldError) Error() string {
	return fmt.Sprintf("config: %s (%s): %v", e.Key, e.Field, e.Err)
}

// Unwrap returns Err, for errors.Is and errors.As.
func (e *FieldError) Unwrap() error { return e.Err }

// Get returns a new T populated from src. See [Bind] for the rules.
func Get[T any](src Source) (T, error) {
	var v T
	err := Bind(src, &v)
	return v, err
}

// Bind populates the struct pointed to by dst from src.
//
// Fields are mapped with struct tags:
//
//	Port    int           `env:"DB_PORT" default:"5432"`
//	URL     string        `env:"DB_URL,required"`
//	Timeout time.Duration `env:"DB_TIMEOUT" default:"5s"`
//	Hosts   []string      `env:"DB_HOSTS"`            // comma-separated
//	Mail    MailConfig    `prefix:"MAIL_"`            // nested struct, keys prefixed
//
// Rules:
//   - Only exported fields are considered. `env:"-"` skips a field.
//   - A key that is missing or set to the empty string is "unset": the
//     default applies if there is one; otherwise ",required" makes it an
//     error, and the field keeps its zero value.
//   - Nested structs without an env tag are populated recursively; a
//     `prefix` tag is prepended to their keys. A nil pointer to a struct is
//     allocated only if at least one of its keys is set, so optional
//     sections stay nil. Recursive types are not followed.
//   - Supported types: string, bool, all int, uint and float kinds,
//     time.Duration, slices of those (comma-separated), pointers to them, and
//     any type whose pointer implements encoding.TextUnmarshaler (e.g.
//     slog.Level).
//
// Bind reports every problem it finds, joined with errors.Join, so one run
// shows all missing or invalid keys. Each problem is a *[FieldError]. If
// binding succeeds, [Validator] implementations are called.
//
// Bind uses reflection and is meant to run once at startup.
func Bind(src Source, dst any) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config: Bind requires a non-nil pointer to a struct, got %T", dst)
	}
	if src == nil {
		src = Map(nil)
	}

	var errs []error
	root := rv.Elem()
	bindStruct(src, root, "", root.Type().Name(), &errs, map[reflect.Type]bool{})
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return validateTree(root, root.Type().Name())
}

func bindStruct(src Source, v reflect.Value, prefix, path string, errs *[]error, visiting map[reflect.Type]bool) (anySet bool) {
	t := v.Type()
	if visiting[t] {
		return false // recursive type: stop instead of looping forever
	}
	visiting[t] = true
	defer delete(visiting, t)

	for i := range t.NumField() {
		sf := t.Field(i)
		fieldPath := joinPath(path, sf.Name)
		tag, hasTag := sf.Tag.Lookup("env")

		if !sf.IsExported() {
			// Exported fields promoted from an embedded unexported struct are
			// still bindable, as in encoding/json.
			if sf.Anonymous && !hasTag {
				switch sf.Type.Kind() {
				case reflect.Struct:
					anySet = bindStruct(src, v.Field(i), prefix+sf.Tag.Get("prefix"), path, errs, visiting) || anySet
				case reflect.Pointer:
					*errs = append(*errs, &FieldError{Key: prefix + "*", Field: fieldPath,
						Err: errors.New("cannot bind through an embedded pointer to an unexported type; embed the struct by value")})
				}
			}
			continue
		}
		if tag == "-" {
			continue
		}
		fv := v.Field(i)
		if !hasTag {
			anySet = bindNested(src, fv, prefix+sf.Tag.Get("prefix"), fieldPath, errs, visiting) || anySet
			continue
		}

		name, required, err := parseEnvTag(tag)
		if err != nil {
			*errs = append(*errs, &FieldError{Key: prefix + name, Field: fieldPath, Err: err})
			continue
		}
		key := prefix + name

		raw, ok := src.Lookup(key)
		if ok && raw == "" {
			ok = false
		}
		if ok {
			anySet = true
		} else if def, hasDef := sf.Tag.Lookup("default"); hasDef {
			raw, ok = def, true
		}
		if !ok {
			if required {
				*errs = append(*errs, &FieldError{Key: key, Field: fieldPath, Err: ErrMissing})
			}
			continue
		}
		if err := setValue(fv, raw); err != nil {
			*errs = append(*errs, &FieldError{Key: key, Field: fieldPath, Err: err})
		}
	}
	return anySet
}

func parseEnvTag(tag string) (name string, required bool, err error) {
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		return name, false, errors.New(`env tag has an empty name; use env:"KEY"`)
	}
	for _, opt := range parts[1:] {
		if opt != "required" {
			return name, false, fmt.Errorf("unknown env tag option %q (supported: required)", opt)
		}
		required = true
	}
	return name, required, nil
}

// bindNested recurses into untagged struct and *struct fields. A nil
// *struct is allocated only if at least one of its keys is present in the
// source, so optional sections (and their Validate methods) stay nil when
// they are not configured.
func bindNested(src Source, fv reflect.Value, prefix, path string, errs *[]error, visiting map[reflect.Type]bool) bool {
	switch {
	case fv.Kind() == reflect.Struct:
		return bindStruct(src, fv, prefix, path, errs, visiting)
	case fv.Kind() == reflect.Pointer && fv.Type().Elem().Kind() == reflect.Struct:
		target := fv
		if fv.IsNil() {
			target = reflect.New(fv.Type().Elem())
		}
		var local []error
		set := bindStruct(src, target.Elem(), prefix, path, &local, visiting)
		if fv.IsNil() && !set {
			return false // section not configured: leave nil, ignore its errors
		}
		if fv.IsNil() {
			fv.Set(target)
		}
		*errs = append(*errs, local...)
		return set
	}
	return false
}

func setValue(fv reflect.Value, raw string) error {
	if fv.Kind() == reflect.Slice && !isTextUnmarshaler(fv.Type()) {
		var parts []string
		for p := range strings.SplitSeq(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		set, err := convert.For(fv.Type().Elem())
		if err != nil {
			return err
		}
		s := reflect.MakeSlice(fv.Type(), len(parts), len(parts))
		for i, p := range parts {
			if err := set(s.Index(i), p); err != nil {
				return fmt.Errorf("item %d: %w", i, err)
			}
		}
		fv.Set(s)
		return nil
	}
	set, err := convert.For(fv.Type())
	if err != nil {
		return err
	}
	return set(fv, raw)
}

func isTextUnmarshaler(t reflect.Type) bool {
	return reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextUnmarshaler]())
}

func validateTree(v reflect.Value, path string) error {
	var errs []error
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() || sf.Tag.Get("env") != "" {
			continue
		}
		fv := v.Field(i)
		switch {
		case fv.Kind() == reflect.Struct:
			if err := validateTree(fv, joinPath(path, sf.Name)); err != nil {
				errs = append(errs, err)
			}
		case fv.Kind() == reflect.Pointer && !fv.IsNil() && fv.Elem().Kind() == reflect.Struct:
			if err := validateTree(fv.Elem(), joinPath(path, sf.Name)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if val, ok := reflect.TypeAssert[Validator](v.Addr()); ok {
		if err := val.Validate(); err != nil {
			return fmt.Errorf("config: %s: %w", path, err)
		}
	}
	return nil
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
