// SPDX-License-Identifier: Apache-2.0

// Package jsonschema builds the JSON Schemas (draft 2020-12, OpenAPI
// 3.1's dialect) of Go types as encoding/json writes and reads them, with
// what their validate rules say: the schemas of web/openapi's document
// and web/typescript's types.
package jsonschema

import (
	"bytes"
	"cmp"
	"encoding"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"mime/multipart"
	"reflect"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"anetos.dev/anetos"
	"anetos.dev/anetos/internal/jsonfield"
)

// Schema is a JSON Schema (draft 2020-12, OpenAPI 3.1's dialect).
type Schema struct {
	Ref         *Component // a named struct: only $ref is written
	Any         bool       // any value: {}
	Type        string
	Nullable    bool
	Format      string
	Encoding    string // contentEncoding: base64 for []byte
	Description string
	Properties  []Property
	Required    []string
	Additional  *Schema // a map's values
	Items       *Schema
	Enum        []any
	MinLength   *int
	MaxLength   *int
	MinItems    *int
	MaxItems    *int
	MinProps    *int
	MaxProps    *int
	Pattern     string
	Minimum     *float64
	Maximum     *float64
	Unique      bool // uniqueItems
	NotZero     bool // not 0: a required number
	MustTrue    bool // true: a required bool
	AnyOf       []*Schema
}

// Property is an object schema's property.
type Property struct {
	Name   string
	Schema *Schema
}

// Component is a named schema of components/schemas: a struct type,
// as a request's body (input) or as a response (output). Its name is
// chosen once every component is known.
type Component struct {
	Type   reflect.Type
	Input  bool
	Name   string
	Schema *Schema
}

type componentKey struct {
	typ   reflect.Type
	input bool
}

var (
	timeType          = reflect.TypeFor[time.Time]()
	dateType          = reflect.TypeFor[anetos.Date]()
	fileHeaderType    = reflect.TypeFor[multipart.FileHeader]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	rawMessageType    = reflect.TypeFor[json.RawMessage]()
	numberType        = reflect.TypeFor[json.Number]()
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
)

// implements reports whether t or *t implements iface.
func implements(t, iface reflect.Type) bool {
	return t.Implements(iface) || reflect.PointerTo(t).Implements(iface)
}

// Set builds the schemas of Go types as encoding/json writes and
// reads them; named structs become components.
type Set struct {
	comps    map[componentKey]*Component
	order    []*Component
	warnings map[string]bool
}

// NewSet returns an empty Set.
func NewSet() *Set {
	return &Set{comps: map[componentKey]*Component{}, warnings: map[string]bool{}}
}

// Components returns the named structs' schemas, in the order they were
// met.
func (s *Set) Components() []*Component { return s.order }

// Warnings returns what couldn't be described, sorted.
func (s *Set) Warnings() []string { return slices.Sorted(maps.Keys(s.warnings)) }

// Warn records what couldn't be described.
func (s *Set) Warn(format string, args ...any) {
	s.warnings[fmt.Sprintf(format, args...)] = true
}

// Of returns the schema of a value of type t. input says it's read from
// a request: its validate rules (tag) apply and say what's required;
// otherwise it's written in a response, where every field without
// omitempty is present. required is the value's own required rule.
func (s *Set) Of(t reflect.Type, tag string, required, input bool) *Schema {
	ptr := false
	for t.Kind() == reflect.Pointer {
		t, ptr = t.Elem(), true
	}
	sc := &Schema{Nullable: ptr && (!input || !required)}
	switch {
	case t == timeType:
		sc.Type, sc.Format = "string", "date-time"
	case t == dateType:
		sc.Type, sc.Format = "string", "date"
	case t == fileHeaderType:
		sc.Type, sc.Format = "string", "binary"
	case t == rawMessageType:
		return &Schema{Any: true}
	case t == numberType:
		sc.Type = "number"
	case implements(t, jsonMarshalerType):
		s.Warn("%s has its own MarshalJSON: described as any value", t)
		return &Schema{Any: true}
	case implements(t, textMarshalerType) && t.Kind() != reflect.String:
		sc.Type = "string"
	default:
		switch t.Kind() {
		case reflect.String:
			sc.Type = "string"
		case reflect.Bool:
			sc.Type = "boolean"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			sc.Type = "integer"
			if t.Kind() == reflect.Int32 || t.Kind() == reflect.Int64 {
				sc.Format = "int" + strconv.Itoa(t.Bits())
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			sc.Type, sc.Minimum = "integer", new(0.0)
		case reflect.Float32:
			sc.Type, sc.Format = "number", "float"
		case reflect.Float64:
			sc.Type, sc.Format = "number", "double"
		case reflect.Slice, reflect.Array:
			if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
				sc.Type, sc.Encoding = "string", "base64" // encoding/json's []byte
				break
			}
			sc.Type = "array"
			sc.Items = s.Of(t.Elem(), jsonfield.EachRules(tag), false, input)
		case reflect.Map:
			sc.Type = "object"
			sc.Additional = s.Of(t.Elem(), "", false, input)
		case reflect.Struct:
			if t.Name() == "" {
				s.object(t, sc, input)
				break
			}
			ref := &Schema{Ref: s.component(t, input)}
			if !sc.Nullable {
				return ref
			}
			return &Schema{Nullable: true, Ref: ref.Ref}
		case reflect.Interface:
			return &Schema{Any: true}
		default:
			s.Warn("%s can't be written in JSON: described as any value", t)
			return &Schema{Any: true}
		}
	}
	if !input {
		return sc
	}
	restricted := applyRules(sc, tag, required)
	if t == dateType && !required && !ptr {
		// "" is the zero date: an optional date may be empty, clearing it.
		return &Schema{AnyOf: []*Schema{sc, {Type: "string", MaxLength: new(0)}}}
	}
	switch {
	case required && !ptr && (sc.Type == "integer" || sc.Type == "number"):
		sc.NotZero = true // validate's required rejects 0 in a non-pointer field
	case required && !ptr && sc.Type == "boolean":
		sc.MustTrue = true // and false
	case !required && restricted:
		// validate skips the other rules on an empty value: a blank
		// string, an empty list or map passes them.
		return &Schema{AnyOf: []*Schema{sc, blank(sc.Type)}}
	}
	return sc
}

// schemePattern matches the schemes the url rule takes (http and https
// unless it names others), whatever their case.
func schemePattern(schemes []string) string {
	if len(schemes) == 0 {
		schemes = []string{"http", "https"}
	}
	alts := make([]string, len(schemes))
	for i, s := range schemes {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if u := unicode.ToUpper(r); u != r {
				b.WriteString("[" + string(r) + string(u) + "]")
			} else {
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
		alts[i] = b.String()
	}
	return "^(" + strings.Join(alts, "|") + "):"
}

// blank is the empty value of JSON type typ that validate's rules skip.
func blank(typ string) *Schema {
	switch typ {
	case "array":
		return &Schema{Type: "array", MaxItems: new(0)}
	case "object":
		return &Schema{Type: "object", Additional: &Schema{Any: true}, MaxProps: new(0)}
	}
	return &Schema{Type: "string", Pattern: `^\s*$`}
}

// component returns the component of struct t, building it the first
// time (a recursive type refers to the component being built).
func (s *Set) component(t reflect.Type, input bool) *Component {
	key := componentKey{t, input}
	if c, ok := s.comps[key]; ok {
		return c
	}
	c := &Component{Type: t, Input: input, Schema: &Schema{}}
	s.comps[key] = c
	s.order = append(s.order, c)
	s.object(t, c.Schema, input)
	return c
}

// object fills sc with struct t's properties: those encoding/json reads
// in a request, where fields read from the path, the query or a header
// aren't in the body, and those it writes in a response.
func (s *Set) object(t reflect.Type, sc *Schema, input bool) {
	sc.Type = "object"
	sc.Properties = []Property{}
	fields := jsonfield.Of(t)
	if !input {
		fields, _ = jsonfield.Written(t)
	}
	for _, f := range fields {
		if input && NotInBody(f.Field) {
			continue
		}
		ps, required := s.Field(f, input)
		sc.Properties = append(sc.Properties, Property{f.Name, ps})
		if required {
			sc.Required = append(sc.Required, f.Name)
		}
	}
}

// Field returns the schema of a struct's field, with its validate rules
// (in a request), its json:",string" and its description tag, and
// whether the field is required: in a request, by its rules; in a
// response, unless it may be left out.
func (s *Set) Field(f jsonfield.Field, input bool) (*Schema, bool) {
	sf := f.Field
	tag := sf.Tag.Get("validate")
	if tag == "-" {
		tag = ""
	}
	required := input && jsonfield.Has(tag, "required")
	ps := s.Of(sf.Type, tag, required, input)
	if f.Quoted && (ps.Type == "integer" || ps.Type == "number" || ps.Type == "boolean") {
		// json:",string": the value is written inside a string.
		ps.Type, ps.Format, ps.Minimum, ps.Maximum = "string", "", nil, nil
		for i, v := range ps.Enum {
			if v != nil {
				ps.Enum[i] = fmt.Sprint(v)
			}
		}
	}
	if d := sf.Tag.Get("description"); d != "" {
		ps = describe(ps, d)
	}
	return ps, required || !input && !f.OmitEmpty && !f.ViaPointer
}

// describe returns sc with a description; a reference is wrapped, as a
// $ref's siblings would describe the referenced schema.
func describe(sc *Schema, d string) *Schema {
	if sc.Ref != nil {
		return &Schema{Description: d, Ref: sc.Ref, Nullable: sc.Nullable}
	}
	sc.Description = d
	return sc
}

// NotInBody reports whether a field of a request's input is read from
// the path, the query or a header only (web.H's binding), or is an
// uploaded file, read from a multipart form.
func NotInBody(sf reflect.StructField) bool {
	if IsFile(sf.Type) {
		return true
	}
	if _, ok := sf.Tag.Lookup("form"); ok {
		return false
	}
	for _, k := range []string{"path", "query", "header"} {
		if _, ok := sf.Tag.Lookup(k); ok {
			return true
		}
	}
	return false
}

// IsFile reports whether t is an uploaded file's type, or a list of them.
func IsFile(t reflect.Type) bool {
	return t == reflect.PointerTo(fileHeaderType) || t == reflect.SliceOf(reflect.PointerTo(fileHeaderType))
}

// applyRules sets what a request value's validate rules say that JSON
// Schema can: its bounds (min, max, size, between), its values (in), its
// format (email, url, uuid, date, datetime, ipv4, ipv6) and distinct
// items (distinct). required also refuses blank strings and empty arrays
// and maps, as validate's required does. It reports whether the rules
// refuse a blank or empty value, which validate lets through when the
// value isn't required.
func applyRules(sc *Schema, tag string, required bool) (restricted bool) {
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
		case sc.Type == "string" && sc.Format != "binary":
			minp, maxp = &sc.MinLength, &sc.MaxLength
		case sc.Type == "array":
			minp, maxp = &sc.MinItems, &sc.MaxItems
		case sc.Type == "object" && sc.Additional != nil:
			minp, maxp = &sc.MinProps, &sc.MaxProps
		case sc.Type == "integer" || sc.Type == "number":
			if lo != "" {
				sc.Minimum = number(lo)
			}
			if hi != "" {
				sc.Maximum = number(hi)
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
	typeFormat := sc.Format // time.Time's date-time isn't a rule's
	for _, r := range jsonfield.Rules(tag) {
		switch {
		case r.Name == "min" && len(r.Params) == 1:
			bound(r.Params[0], "")
		case r.Name == "max" && len(r.Params) == 1:
			bound("", r.Params[0])
		case r.Name == "size" && len(r.Params) == 1:
			bound(r.Params[0], r.Params[0])
		case r.Name == "between" && len(r.Params) == 2:
			bound(r.Params[0], r.Params[1])
		case r.Name == "in" && sc.Type != "array" && sc.Type != "object":
			sc.Enum = nil
			for _, p := range r.Params {
				sc.Enum = append(sc.Enum, enumValue(sc.Type, p))
			}
		case jsonfield.Formats[r.Name] != "" && sc.Type == "string":
			sc.Format = jsonfield.Formats[r.Name]
			if r.Name == "url" {
				sc.Pattern = schemePattern(r.Params) // uri allows any scheme; url, http and https
			}
		case r.Name == "distinct" && sc.Type == "array":
			sc.Unique = true
		}
	}
	if required {
		switch {
		case sc.Type == "string" && sc.Format != "binary" && (sc.MinLength == nil || *sc.MinLength < 1):
			sc.MinLength = new(1)
		case sc.Type == "array" && (sc.MinItems == nil || *sc.MinItems < 1):
			sc.MinItems = new(1)
		case sc.Type == "object" && sc.Additional != nil && (sc.MinProps == nil || *sc.MinProps < 1):
			sc.MinProps = new(1)
		}
	}
	restricted = len(sc.Enum) > 0 || sc.Type == "string" && sc.Format != typeFormat ||
		sc.MinLength != nil && *sc.MinLength > 0 || sc.MinItems != nil && *sc.MinItems > 0 || sc.MinProps != nil && *sc.MinProps > 0
	if sc.Nullable && len(sc.Enum) > 0 {
		sc.Enum = append(sc.Enum, nil)
	}
	return restricted && sc.Format != "binary"
}

// enumValue is p, an in rule's parameter, as a value of JSON type typ.
func enumValue(typ, p string) any {
	switch typ {
	case "integer", "number":
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

// MarshalJSON writes the schema, its keywords in a fixed order.
func (sc *Schema) MarshalJSON() ([]byte, error) {
	var o object
	if sc.Ref != nil {
		ref := map[string]string{"$ref": "#/components/schemas/" + sc.Ref.Name}
		switch {
		case sc.Nullable:
			o.add("anyOf", []any{ref, map[string]string{"type": "null"}})
		case sc.Description != "":
			o.add("allOf", []any{ref}) // siblings of a $ref would describe the target
		default:
			o.add("$ref", ref["$ref"])
		}
		if sc.Description != "" {
			o.add("description", sc.Description)
		}
		return o.marshal()
	}
	if sc.Any {
		if sc.Description != "" {
			o.add("description", sc.Description)
		}
		return o.marshal()
	}
	if len(sc.AnyOf) > 0 {
		o.add("anyOf", sc.AnyOf)
		if sc.Description != "" {
			o.add("description", sc.Description)
		}
		return o.marshal()
	}
	if sc.Nullable {
		o.add("type", []string{sc.Type, "null"})
	} else {
		o.add("type", sc.Type)
	}
	if sc.Format != "" {
		o.add("format", sc.Format)
	}
	if sc.Encoding != "" {
		o.add("contentEncoding", sc.Encoding)
	}
	if sc.Description != "" {
		o.add("description", sc.Description)
	}
	if sc.Type == "object" && sc.Additional == nil {
		var props object
		for _, p := range sc.Properties {
			props.add(p.Name, p.Schema)
		}
		o.add("properties", props)
		if len(sc.Required) > 0 {
			o.add("required", sc.Required)
		}
	}
	if sc.Additional != nil {
		o.add("additionalProperties", sc.Additional)
	}
	if sc.Items != nil {
		o.add("items", sc.Items)
	}
	if sc.Enum != nil {
		o.add("enum", sc.Enum)
	}
	for _, kv := range []struct {
		k string
		v *int
	}{{"minLength", sc.MinLength}, {"maxLength", sc.MaxLength}, {"minItems", sc.MinItems}, {"maxItems", sc.MaxItems},
		{"minProperties", sc.MinProps}, {"maxProperties", sc.MaxProps}} {
		if kv.v != nil {
			o.add(kv.k, *kv.v)
		}
	}
	if sc.Minimum != nil {
		o.add("minimum", json.Number(strconv.FormatFloat(*sc.Minimum, 'f', -1, 64)))
	}
	if sc.Maximum != nil {
		o.add("maximum", json.Number(strconv.FormatFloat(*sc.Maximum, 'f', -1, 64)))
	}
	if sc.Pattern != "" {
		o.add("pattern", sc.Pattern)
	}
	if sc.NotZero {
		o.add("not", map[string]int{"const": 0})
	}
	if sc.MustTrue {
		o.add("const", true)
	}
	if sc.Unique {
		o.add("uniqueItems", true)
	}
	return o.marshal()
}

// object is a JSON object whose keys keep their order.
type object struct {
	keys []string
	vals []any
}

func (o *object) add(k string, v any) {
	o.keys = append(o.keys, k)
	o.vals = append(o.vals, v)
}

func (o object) MarshalJSON() ([]byte, error) { return o.marshal() }

func (o object) marshal() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		vb, err := marshal(o.vals[i])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshal is json.Marshal without HTML escaping, so descriptions keep
// their < and &.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// NameComponents chooses the components' names: the Go type's ("ProductResponse";
// "PageProductResponse" for db.Page[ProductResponse]), with its package's
// name first ("handlers.Order") where two types share one, and "Input"
// after a type's request body when the type is also a response.
func NameComponents(comps []*Component) {
	byType := map[reflect.Type][]*Component{}
	for _, c := range comps {
		byType[c.Type] = append(byType[c.Type], c)
	}
	typesByBase := map[string][]reflect.Type{}
	for t := range byType {
		b := BaseName(t)
		typesByBase[b] = append(typesByBase[b], t)
	}
	used := map[string]bool{"Problem": true} // web's problem details
	var pending []*Component
	for _, ts := range typesByBase {
		for _, t := range ts {
			base := BaseName(t)
			if len(ts) > 1 {
				base = pkgName(t) + "." + base
			}
			for _, c := range byType[t] {
				c.Name = base
				if c.Input && len(byType[t]) > 1 {
					c.Name += "Input"
				}
				pending = append(pending, c)
			}
		}
	}
	// Names still shared (a NoteInput type beside Note's body, named
	// NoteInput too): numbered, in a fixed order, a type's own name
	// first.
	derived := func(c *Component) bool {
		return strings.HasSuffix(c.Name, "Input") && c.Input && len(byType[c.Type]) > 1
	}
	first := map[*Component]int{} // the order they were met in: by route
	for i, c := range comps {
		first[c] = i
	}
	slices.SortFunc(pending, func(a, b *Component) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), compareBool(derived(a), derived(b)),
			strings.Compare(pkgPath(a.Type)+"."+a.Type.Name(), pkgPath(b.Type)+"."+b.Type.Name()), compareBool(a.Input, b.Input),
			cmp.Compare(first[a], first[b])) // types declared in two functions, of one name
	})
	for _, c := range pending {
		name := c.Name
		for i := 2; used[name]; i++ {
			name = c.Name + strconv.Itoa(i)
		}
		c.Name = name
		used[name] = true
	}
}

// BaseName is t's name made a component's: type arguments' names follow
// a generic type's, without their packages.
func BaseName(t reflect.Type) string {
	name := t.Name()
	base, args, generic := strings.Cut(name, "[")
	if !generic {
		return cmp.Or(sanitize(name), "Schema")
	}
	var b strings.Builder
	b.WriteString(cmp.Or(sanitize(base), "Schema"))
	// Each argument's last identifier: "shop/app/handlers.ProductResponse".
	for _, word := range strings.FieldsFunc(args, func(r rune) bool {
		return r != '_' && r != '.' && r != '/' && (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z')
	}) {
		if i := strings.LastIndexByte(word, '.'); i >= 0 {
			word = word[i+1:]
		}
		word = sanitize(word)
		if word == "" {
			continue
		}
		b.WriteString(strings.ToUpper(word[:1]) + word[1:])
	}
	return b.String()
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

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return -1
	}, s)
}

func pkgName(t reflect.Type) string {
	p := pkgPath(t)
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return cmp.Or(sanitize(p), "pkg")
}

// pkgPath is t's package's path, "main" for the program's main package
// under go test too, where it has its import path: the names don't
// change between go run and go test.
func pkgPath(t reflect.Type) string {
	if p := t.PkgPath(); p != mainPath() {
		return p
	}
	return "main"
}

// mainPath is the main package's import path when it's compiled as a
// test's package ("example.com/shop" for the binary shop.test).
var mainPath = sync.OnceValue(func() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok || !strings.HasSuffix(bi.Path, ".test") {
		return "main"
	}
	return strings.TrimSuffix(bi.Path, ".test")
})
