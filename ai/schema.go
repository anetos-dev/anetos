// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"bytes"
	"cmp"
	"encoding"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Schema is a JSON Schema: what a tool's input or a structured output
// must look like. [SchemaFor] builds one from a Go type; providers
// translate it to their API's dialect. It marshals to standard JSON
// Schema, with properties in the struct's field order (which models
// follow when they write).
type Schema struct {
	// Type is "object", "array", "string", "integer", "number",
	// "boolean", or empty for any value.
	Type string
	// Nullable says null is allowed too (a pointer field).
	Nullable bool
	// Description is the field's description tag.
	Description string
	// Properties are an object's fields, in order.
	Properties []Property
	// Required lists the properties that must be present (those with a
	// `validate:"required"` rule).
	Required []string
	// AdditionalProperties is the schema of a map's values; nil for a
	// struct, whose properties are the only ones allowed.
	AdditionalProperties *Schema
	// Items is an array's element schema.
	Items *Schema
	// Enum lists the allowed values (the in rule).
	Enum []any
	// Format is a string's format: email, uri, uuid, date, date-time,
	// ipv4, ipv6.
	Format string
	// MinLength and MaxLength bound a string's length, in characters.
	MinLength, MaxLength *int
	// Minimum and Maximum bound a number.
	Minimum, Maximum *float64
	// MinItems and MaxItems bound an array's length.
	MinItems, MaxItems *int
	// MinProperties and MaxProperties bound a map's size.
	MinProperties, MaxProperties *int
}

// Property is one property of an object [Schema].
type Property struct {
	// Name is the JSON name.
	Name string
	// Schema is the property's schema.
	Schema *Schema
}

// MarshalJSON writes the schema as JSON Schema.
func (s Schema) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	field := func(name string, v any) error {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(strconv.Quote(name))
		b.WriteByte(':')
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		b.Write(data)
		return nil
	}
	var err error
	add := func(name string, v any) {
		if err == nil {
			err = field(name, v)
		}
	}
	switch {
	case s.Type != "" && s.Nullable:
		add("type", []string{s.Type, "null"})
	case s.Type != "":
		add("type", s.Type)
	}
	if s.Description != "" {
		add("description", s.Description)
	}
	if s.Type == "object" {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(`"properties":{`)
		for i, p := range s.Properties {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Quote(p.Name))
			b.WriteByte(':')
			data, perr := json.Marshal(p.Schema)
			if perr != nil {
				return nil, perr
			}
			b.Write(data)
		}
		b.WriteByte('}')
		if len(s.Required) > 0 {
			add("required", s.Required)
		}
		if s.AdditionalProperties != nil {
			add("additionalProperties", s.AdditionalProperties)
		} else {
			add("additionalProperties", false)
		}
	}
	if s.Items != nil {
		add("items", s.Items)
	}
	if len(s.Enum) > 0 {
		add("enum", s.Enum)
	}
	if s.Format != "" {
		add("format", s.Format)
	}
	for _, n := range []struct {
		name string
		v    *int
	}{{"minLength", s.MinLength}, {"maxLength", s.MaxLength}, {"minItems", s.MinItems}, {"maxItems", s.MaxItems},
		{"minProperties", s.MinProperties}, {"maxProperties", s.MaxProperties}} {
		if n.v != nil {
			add(n.name, *n.v)
		}
	}
	if s.Minimum != nil {
		add("minimum", *s.Minimum)
	}
	if s.Maximum != nil {
		add("maximum", *s.Maximum)
	}
	if err != nil {
		return nil, err
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

var schemas sync.Map // reflect.Type → schemaResult

type schemaResult struct {
	s   *Schema
	err error
}

// SchemaFor returns the JSON Schema of T, a struct, as encoding/json
// writes and reads it: its exported fields by their json names (json:"-"
// skips one; embedded structs are flattened, a shallower or tagged field
// hiding others of its name), a field's `description` tag as its
// description, and what its `validate` tag says that JSON Schema can:
// required (present, not null, not blank), min, max, size and between
// (lengths, values, item counts), in (an enum), and the formats email,
// url, uuid, date, datetime, ipv4 and ipv6. Other rules are still
// checked on the answer. time.Time is a date-time string, other
// encoding.TextMarshalers strings, json.Number a number; interface
// values allow anything. Types with their own MarshalJSON or
// UnmarshalJSON, and recursive types, are an error: their JSON shape
// can't be known. Schemas are built once per type and shared: don't
// modify one.
func SchemaFor[T any]() (*Schema, error) { return schemaOf(reflect.TypeFor[T]()) }

func schemaOf(t reflect.Type) (*Schema, error) {
	if r, ok := schemas.Load(t); ok {
		return r.(schemaResult).s, r.(schemaResult).err
	}
	var s *Schema
	var err error
	if t.Kind() != reflect.Struct {
		err = fmt.Errorf("ai: %s isn't a struct: tool inputs and structured outputs are JSON objects", t)
	} else {
		s, err = (&schemaBuilder{seen: map[reflect.Type]bool{}}).build(t, "", false)
		switch {
		case err != nil:
			err = fmt.Errorf("ai: the schema of %s: %w", t, err)
		case s.Type != "object":
			s, err = nil, fmt.Errorf("ai: %s is written in JSON as a %s, not an object", t, s.Type)
		default:
			if _, merr := json.Marshal(s); merr != nil { // providers marshal it on every request
				s, err = nil, fmt.Errorf("ai: the schema of %s: %w", t, merr)
			}
		}
	}
	r, _ := schemas.LoadOrStore(t, schemaResult{s, err})
	return r.(schemaResult).s, r.(schemaResult).err
}

type schemaBuilder struct {
	seen map[reflect.Type]bool // structs being built: a repeat is recursion
}

var (
	timeType            = reflect.TypeFor[time.Time]()
	textMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
	rawMessageType      = reflect.TypeFor[json.RawMessage]()
	numberType          = reflect.TypeFor[json.Number]()
	jsonMarshalerType   = reflect.TypeFor[json.Marshaler]()
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
)

// implements reports whether t or *t implements iface.
func implements(t, iface reflect.Type) bool {
	return t.Implements(iface) || reflect.PointerTo(t).Implements(iface)
}

// build returns the schema of t, a field's type with the validate rules
// (required says they include required).
func (b *schemaBuilder) build(t reflect.Type, rules string, required bool) (*Schema, error) {
	nullable := false
	for t.Kind() == reflect.Pointer {
		t, nullable = t.Elem(), true
	}
	s := &Schema{Nullable: nullable && !required}
	switch {
	case t == timeType:
		s.Type, s.Format = "string", "date-time"
		return s, nil
	case t == rawMessageType:
		s.Nullable = false // any value
		return s, nil
	case t == numberType:
		s.Type = "number" // validate sees its text: no rules
		return s, nil
	case implements(t, jsonMarshalerType) || implements(t, jsonUnmarshalerType):
		return nil, fmt.Errorf("%s has its own JSON encoding (MarshalJSON or UnmarshalJSON), so its schema can't be known: use a plain type", t)
	case implements(t, textMarshalerType) && t.Kind() != reflect.String:
		// Its text form: validate's rules see its Go value, which says
		// nothing about the text.
		s.Type = "string"
		if required {
			s.MinLength = new(1)
		}
		return s, nil
	default:
		switch t.Kind() {
		case reflect.String:
			s.Type = "string"
		case reflect.Bool:
			s.Type = "boolean"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			s.Type = "integer"
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			s.Type, s.Minimum = "integer", new(0.0)
		case reflect.Float32, reflect.Float64:
			s.Type = "number"
		case reflect.Slice, reflect.Array:
			if t.Elem().Kind() == reflect.Uint8 && t.Kind() == reflect.Slice {
				s.Type = "string" // encoding/json writes []byte as base64
				break
			}
			s.Type = "array"
			items, err := b.build(t.Elem(), eachRules(rules), false)
			if err != nil {
				return nil, err
			}
			s.Items = items
		case reflect.Map:
			switch k := t.Key(); {
			case k.Kind() == reflect.String, implements(k, textMarshalerType):
			case k.Kind() >= reflect.Int && k.Kind() <= reflect.Uint64:
			default:
				return nil, fmt.Errorf("%s: encoding/json can't write a map with %s keys", t, k)
			}
			s.Type = "object"
			values, err := b.build(t.Elem(), "", false)
			if err != nil {
				return nil, err
			}
			s.AdditionalProperties = values
		case reflect.Struct:
			if err := b.object(t, s); err != nil {
				return nil, err
			}
		case reflect.Interface:
			s.Nullable = false // any value
		default:
			return nil, fmt.Errorf("%s can't be written in JSON", t)
		}
	}
	applyRules(s, rules)
	if required {
		// validate's required rejects blank strings and empty slices
		// and maps, which JSON Schema's required lets through.
		switch {
		case s.Type == "string" && (s.MinLength == nil || *s.MinLength < 1):
			s.MinLength = new(1)
		case s.Type == "array" && (s.MinItems == nil || *s.MinItems < 1):
			s.MinItems = new(1)
		case s.Type == "object" && s.AdditionalProperties != nil && (s.MinProperties == nil || *s.MinProperties < 1):
			s.MinProperties = new(1)
		}
	}
	return s, nil
}

// object fills s with struct t's properties.
func (b *schemaBuilder) object(t reflect.Type, s *Schema) error {
	if b.seen[t] {
		return fmt.Errorf("%s refers to itself, which a schema here can't express", t)
	}
	b.seen[t] = true
	defer delete(b.seen, t)
	s.Type = "object"
	s.Properties = []Property{} // an empty object still lists none
	for _, f := range jsonFields(t) {
		rules := f.sf.Tag.Get("validate")
		if rules == "-" {
			rules = ""
		}
		required := hasRule(rules, "required")
		ps, err := b.build(f.sf.Type, rules, required)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", t, f.sf.Name, err)
		}
		if f.quoted && (ps.Type == "integer" || ps.Type == "number" || ps.Type == "boolean") {
			// json:",string": the value is written inside a string.
			ps.Type, ps.Minimum, ps.Maximum = "string", nil, nil
			for i, v := range ps.Enum {
				if v != nil {
					ps.Enum[i] = fmt.Sprint(v)
				}
			}
		}
		if d := f.sf.Tag.Get("description"); d != "" {
			ps.Description = d
		}
		s.Properties = append(s.Properties, Property{Name: f.name, Schema: ps})
		if required {
			s.Required = append(s.Required, f.name)
		}
	}
	return nil
}

// jsonField is a struct field as encoding/json sees it.
type jsonField struct {
	name   string
	tagged bool // the name is from the json tag
	quoted bool // json:",string"
	index  []int
	sf     reflect.StructField
}

// jsonFields returns the fields encoding/json reads and writes for t,
// in order, with its rules: embedded structs' fields are promoted, a
// shallower field hides deeper ones of its name, then a tagged one
// untagged ones, and fields left ambiguous are dropped.
func jsonFields(t reflect.Type) []jsonField {
	type level struct {
		typ   reflect.Type
		index []int
	}
	type candidate struct {
		jsonField
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
					next = append(next, level{typ: ft, index: index})
					continue
				}
				f := candidate{jsonField{name: cmp.Or(name, sf.Name), tagged: name != "", index: index, sf: sf}, depth}
				for o := range strings.SplitSeq(opts, ",") {
					f.quoted = f.quoted || o == "string"
				}
				all = append(all, f)
				if count[l.typ] > 1 {
					all = append(all, f) // the same struct embedded twice: ambiguous
				}
			}
		}
	}
	slices.SortStableFunc(all, func(a, b candidate) int {
		return cmp.Or(strings.Compare(a.name, b.name), cmp.Compare(a.depth, b.depth), compareBool(b.tagged, a.tagged))
	})
	var out []jsonField
	for i := 0; i < len(all); {
		j := i + 1
		for j < len(all) && all[j].name == all[i].name {
			j++
		}
		group := all[i:j]
		if len(group) == 1 || group[0].depth != group[1].depth || group[0].tagged != group[1].tagged {
			out = append(out, group[0].jsonField)
		}
		i = j
	}
	slices.SortFunc(out, func(a, b jsonField) int { return slices.Compare(a.index, b.index) })
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

// rule is one parsed rule of a validate tag.
type rule struct {
	name   string
	params []string
}

// parseRules parses a validate tag as package validate does: rules
// separated by "|", parameters after ":" separated by ",", spaces
// around them ignored.
func parseRules(tag string) []rule {
	var out []rule
	for r := range strings.SplitSeq(tag, "|") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		name, params, hasParams := strings.Cut(r, ":")
		rl := rule{name: strings.TrimSpace(name)}
		if hasParams {
			for p := range strings.SplitSeq(params, ",") {
				rl.params = append(rl.params, strings.TrimSpace(p))
			}
		}
		out = append(out, rl)
	}
	return out
}

func hasRule(tag, name string) bool {
	return slices.ContainsFunc(parseRules(tag), func(r rule) bool { return r.name == name })
}

// eachRules returns the rules of a slice field that apply to each
// element (formats and in), as package validate applies them.
func eachRules(tag string) string {
	var out []string
	for _, r := range parseRules(tag) {
		if r.name == "in" || formats[r.name] != "" {
			out = append(out, r.name+":"+strings.Join(r.params, ","))
		}
	}
	return strings.Join(out, "|")
}

// formats maps validate's format rules to JSON Schema's formats.
var formats = map[string]string{
	"email": "email", "url": "uri", "uuid": "uuid", "date": "date", "datetime": "date-time",
	"ipv4": "ipv4", "ipv6": "ipv6",
}

// applyRules sets what s's validate rules say that JSON Schema can.
func applyRules(s *Schema, tag string) {
	count := func(p string) *int {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		return &n
	}
	number := func(p string) *float64 {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return nil
		}
		return &f
	}
	bound := func(lo, hi string) {
		var minp, maxp **int
		switch {
		case s.Type == "string":
			minp, maxp = &s.MinLength, &s.MaxLength
		case s.Type == "array":
			minp, maxp = &s.MinItems, &s.MaxItems
		case s.Type == "object" && s.AdditionalProperties != nil:
			minp, maxp = &s.MinProperties, &s.MaxProperties
		case s.Type == "integer" || s.Type == "number":
			if lo != "" {
				s.Minimum = number(lo)
			}
			if hi != "" {
				s.Maximum = number(hi)
			}
			return
		default:
			return
		}
		if lo != "" {
			*minp = count(lo)
		}
		if hi != "" {
			*maxp = count(hi)
		}
	}
	for _, r := range parseRules(tag) {
		switch {
		case r.name == "min" && len(r.params) == 1:
			bound(r.params[0], "")
		case r.name == "max" && len(r.params) == 1:
			bound("", r.params[0])
		case r.name == "size" && len(r.params) == 1:
			bound(r.params[0], r.params[0])
		case r.name == "between" && len(r.params) == 2:
			bound(r.params[0], r.params[1])
		case r.name == "in" && s.Type != "array" && s.Type != "object":
			s.Enum = nil
			for _, p := range r.params {
				s.Enum = append(s.Enum, enumValue(s.Type, p))
			}
		case formats[r.name] != "" && s.Type == "string":
			s.Format = formats[r.name]
		}
	}
	if s.Nullable && len(s.Enum) > 0 {
		s.Enum = append(s.Enum, nil)
	}
}

// enumValue is p, an in rule's parameter, as a value of JSON type typ.
func enumValue(typ, p string) any {
	switch typ {
	case "integer", "number":
		// As a canonical JSON number: exact for integers, which a float
		// would round.
		if n, err := strconv.ParseInt(p, 10, 64); err == nil {
			return json.Number(strconv.FormatInt(n, 10))
		}
		if n, err := strconv.ParseUint(p, 10, 64); err == nil {
			return json.Number(strconv.FormatUint(n, 10))
		}
		if f, err := strconv.ParseFloat(p, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return json.Number(strconv.FormatFloat(f, 'g', -1, 64))
		}
	case "boolean":
		if b, err := strconv.ParseBool(p); err == nil {
			return b
		}
	}
	return p
}
