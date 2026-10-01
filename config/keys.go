// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"reflect"
)

// Key is a setting a struct reads, as [Keys] lists them.
type Key struct {
	// Name is the key: "DB_PORT".
	Name string
	// Field is the Go path of the field: "Config.Port".
	Field string
	// Default is the default tag's value.
	Default string
	// HasDefault says the field has a default tag (Default may be empty).
	HasDefault bool
	// Required says the key must be set (env:"KEY,required").
	Required bool
}

// Keys lists the keys [Bind] would read into the struct dst points to
// (or is), in field order, following the same tag rules: for
// documentation, and for checking a plugin's keys.
func Keys(dst any) ([]Key, error) {
	t := reflect.TypeOf(dst)
	if t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("config: Keys requires a struct or a pointer to one, got %T", dst)
	}
	var keys []Key
	var errs []error
	keysOf(t, "", t.Name(), &keys, &errs, map[reflect.Type]bool{})
	return keys, errors.Join(errs...)
}

func keysOf(t reflect.Type, prefix, path string, keys *[]Key, errs *[]error, visiting map[reflect.Type]bool) {
	if visiting[t] {
		return
	}
	visiting[t] = true
	defer delete(visiting, t)
	for sf := range t.Fields() {
		tag, hasTag := sf.Tag.Lookup("env")
		fieldPath := joinPath(path, sf.Name)
		embedded := sf.Anonymous && !hasTag // exported fields of an embedded struct count
		if tag == "-" || !sf.IsExported() && !embedded {
			continue
		}
		if !hasTag {
			ft := sf.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				keysOf(ft, prefix+sf.Tag.Get("prefix"), fieldPath, keys, errs, visiting)
			}
			continue
		}
		name, required, err := parseEnvTag(tag)
		if err != nil {
			*errs = append(*errs, &FieldError{Key: prefix + name, Field: fieldPath, Err: err})
			continue
		}
		def, hasDef := sf.Tag.Lookup("default")
		*keys = append(*keys, Key{Name: prefix + name, Field: fieldPath, Default: def, HasDefault: hasDef, Required: required})
	}
}
