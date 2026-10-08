// SPDX-License-Identifier: Apache-2.0

// Package jsonfield lists a struct's fields as encoding/json reads and
// writes them, and parses validate tags as package validate does: what
// the JSON Schemas of package ai and web/openapi are built from.
package jsonfield

import (
	"cmp"
	"reflect"
	"slices"
	"strings"
)

// Field is a struct field as encoding/json sees it.
type Field struct {
	Name      string // the JSON name
	Tagged    bool   // the name is from the json tag
	Quoted    bool   // json:",string"
	OmitEmpty bool   // json:",omitempty" or ",omitzero": left out when empty
	// ViaPointer says the field is promoted through an embedded pointer,
	// which leaves it out when nil.
	ViaPointer bool
	Index      []int // for reflect.Value.FieldByIndex
	Field      reflect.StructField
}

// Of returns the fields encoding/json reads and writes for t, a struct
// type, in order: embedded structs' fields are promoted, a shallower
// field hides deeper ones of its name, then a tagged one untagged ones,
// and fields left ambiguous are dropped.
func Of(t reflect.Type) []Field {
	type level struct {
		typ   reflect.Type
		index []int
		ptr   bool // reached through an embedded pointer
	}
	type candidate struct {
		Field
		depth int
	}
	var all []candidate
	next := []level{{typ: t}}
	visited := map[reflect.Type]bool{}
	for depth := 0; len(next) > 0; depth++ {
		current := next
		next = nil
		count := map[reflect.Type]int{}
		for _, l := range current {
			count[l.typ]++
		}
		for _, l := range current {
			if visited[l.typ] {
				continue
			}
			visited[l.typ] = true
			for i := range l.typ.NumField() {
				sf := l.typ.Field(i)
				if sf.Anonymous {
					ft := sf.Type
					if ft.Kind() == reflect.Pointer {
						if !sf.IsExported() {
							continue // json can't set an embedded pointer to an unexported struct
						}
						ft = ft.Elem()
					}
					if !sf.IsExported() && ft.Kind() != reflect.Struct {
						continue
					}
				} else if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, opts, _ := strings.Cut(tag, ",")
				index := append(slices.Clone(l.index), i)
				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if name == "" && sf.Anonymous && ft.Kind() == reflect.Struct {
					next = append(next, level{typ: ft, index: index, ptr: l.ptr || sf.Type.Kind() == reflect.Pointer})
					continue
				}
				f := candidate{Field{Name: cmp.Or(name, sf.Name), Tagged: name != "", Index: index, Field: sf, ViaPointer: l.ptr}, depth}
				for o := range strings.SplitSeq(opts, ",") {
					f.Quoted = f.Quoted || o == "string"
					f.OmitEmpty = f.OmitEmpty || o == "omitempty" || o == "omitzero"
				}
				all = append(all, f)
				if count[l.typ] > 1 {
					all = append(all, f) // the same struct embedded twice: ambiguous
				}
			}
		}
	}
	slices.SortStableFunc(all, func(a, b candidate) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), cmp.Compare(a.depth, b.depth), compareBool(b.Tagged, a.Tagged))
	})
	var out []Field
	for i := 0; i < len(all); {
		j := i + 1
		for j < len(all) && all[j].Name == all[i].Name {
			j++
		}
		group := all[i:j]
		if len(group) == 1 || group[0].depth != group[1].depth || group[0].Tagged != group[1].Tagged {
			out = append(out, group[0].Field)
		}
		i = j
	}
	slices.SortFunc(out, func(a, b Field) int { return slices.Compare(a.Index, b.Index) })
	return out
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

// Rule is one rule of a validate tag: "max:200" is {"max", ["200"]}.
type Rule struct {
	Name   string
	Params []string
}

// Rules parses a validate tag as package validate does: rules separated
// by "|", parameters after ":" separated by ",", spaces around them
// ignored. It doesn't check the tag: validate.Compile does.
func Rules(tag string) []Rule {
	var out []Rule
	for r := range strings.SplitSeq(tag, "|") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		name, params, hasParams := strings.Cut(r, ":")
		rl := Rule{Name: strings.TrimSpace(name)}
		if hasParams {
			for p := range strings.SplitSeq(params, ",") {
				rl.Params = append(rl.Params, strings.TrimSpace(p))
			}
		}
		out = append(out, rl)
	}
	return out
}

// Has reports whether tag has the rule name.
func Has(tag, name string) bool {
	return slices.ContainsFunc(Rules(tag), func(r Rule) bool { return r.Name == name })
}

// Formats maps validate's format rules to JSON Schema's formats.
var Formats = map[string]string{
	"email": "email", "url": "uri", "uuid": "uuid", "date": "date", "datetime": "date-time",
	"ipv4": "ipv4", "ipv6": "ipv6",
}

// EachRules returns the rules of a slice field that apply to each
// element (formats and in), as package validate applies them.
func EachRules(tag string) string {
	var out []string
	for _, r := range Rules(tag) {
		if r.Name == "in" || Formats[r.Name] != "" {
			out = append(out, r.Name+":"+strings.Join(r.Params, ","))
		}
	}
	return strings.Join(out, "|")
}
