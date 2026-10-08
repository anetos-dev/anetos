// SPDX-License-Identifier: Apache-2.0

package openapi

import (
	"bytes"
	"cmp"
	"encoding"
	"encoding/json"
	"fmt"
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

// schema is a JSON Schema (draft 2020-12, OpenAPI 3.1's dialect).
type schema struct {
	ref         *component // a named struct: only $ref is written
	any         bool       // any value: {}
	typ         string
	nullable    bool
	format      string
	encoding    string // contentEncoding: base64 for []byte
	description string
	properties  []property
	required    []string
	additional  *schema // a map's values
	items       *schema
	enum        []any
	minLength   *int
	maxLength   *int
	minItems    *int
	maxItems    *int
	minProps    *int
	maxProps    *int
	pattern     string
	minimum     *float64
	maximum     *float64
	unique      bool // uniqueItems
	notZero     bool // not 0: a required number
	mustTrue    bool // true: a required bool
	anyOf       []*schema
}

type property struct {
	name   string
	schema *schema
}

// component is a named schema of components/schemas: a struct type,
// as a request's body (input) or as a response (output). Its name is
// chosen once every component is known.
type component struct {
	typ    reflect.Type
	input  bool
	name   string
	schema *schema
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

// schemas builds the schemas of Go types as encoding/json writes and
// reads them; named structs become components.
type schemas struct {
	comps    map[componentKey]*component
	order    []*component
	warnings map[string]bool
}

func newSchemas() *schemas {
	return &schemas{comps: map[componentKey]*component{}, warnings: map[string]bool{}}
}

func (s *schemas) warn(format string, args ...any) {
	s.warnings[fmt.Sprintf(format, args...)] = true
}

// of returns the schema of a value of type t. input says it's read from
// a request: its validate rules (tag) apply and say what's required;
// otherwise it's written in a response, where every field without
// omitempty is present. required is the value's own required rule.
func (s *schemas) of(t reflect.Type, tag string, required, input bool) *schema {
	ptr := false
	for t.Kind() == reflect.Pointer {
		t, ptr = t.Elem(), true
	}
	sc := &schema{nullable: ptr && (!input || !required)}
	switch {
	case t == timeType:
		sc.typ, sc.format = "string", "date-time"
	case t == dateType:
		sc.typ, sc.format = "string", "date"
	case t == fileHeaderType:
		sc.typ, sc.format = "string", "binary"
	case t == rawMessageType:
		return &schema{any: true}
	case t == numberType:
		sc.typ = "number"
	case implements(t, jsonMarshalerType):
		s.warn("%s has its own MarshalJSON: described as any value", t)
		return &schema{any: true}
	case implements(t, textMarshalerType) && t.Kind() != reflect.String:
		sc.typ = "string"
	default:
		switch t.Kind() {
		case reflect.String:
			sc.typ = "string"
		case reflect.Bool:
			sc.typ = "boolean"
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			sc.typ = "integer"
			if t.Kind() == reflect.Int32 || t.Kind() == reflect.Int64 {
				sc.format = "int" + strconv.Itoa(t.Bits())
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			sc.typ, sc.minimum = "integer", new(0.0)
		case reflect.Float32:
			sc.typ, sc.format = "number", "float"
		case reflect.Float64:
			sc.typ, sc.format = "number", "double"
		case reflect.Slice, reflect.Array:
			if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
				sc.typ, sc.encoding = "string", "base64" // encoding/json's []byte
				break
			}
			sc.typ = "array"
			sc.items = s.of(t.Elem(), jsonfield.EachRules(tag), false, input)
		case reflect.Map:
			sc.typ = "object"
			sc.additional = s.of(t.Elem(), "", false, input)
		case reflect.Struct:
			if t.Name() == "" {
				s.object(t, sc, input)
				break
			}
			ref := &schema{ref: s.component(t, input)}
			if !sc.nullable {
				return ref
			}
			return &schema{nullable: true, ref: ref.ref}
		case reflect.Interface:
			return &schema{any: true}
		default:
			s.warn("%s can't be written in JSON: described as any value", t)
			return &schema{any: true}
		}
	}
	if !input {
		return sc
	}
	restricted := applyRules(sc, tag, required)
	switch {
	case required && !ptr && (sc.typ == "integer" || sc.typ == "number"):
		sc.notZero = true // validate's required rejects 0 in a non-pointer field
	case required && !ptr && sc.typ == "boolean":
		sc.mustTrue = true // and false
	case !required && restricted:
		// validate skips the other rules on an empty value: a blank
		// string, an empty list or map passes them.
		return &schema{anyOf: []*schema{sc, blank(sc.typ)}}
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
func blank(typ string) *schema {
	switch typ {
	case "array":
		return &schema{typ: "array", maxItems: new(0)}
	case "object":
		return &schema{typ: "object", additional: &schema{any: true}, maxProps: new(0)}
	}
	return &schema{typ: "string", pattern: `^\s*$`}
}

// component returns the component of struct t, building it the first
// time (a recursive type refers to the component being built).
func (s *schemas) component(t reflect.Type, input bool) *component {
	key := componentKey{t, input}
	if c, ok := s.comps[key]; ok {
		return c
	}
	c := &component{typ: t, input: input, schema: &schema{}}
	s.comps[key] = c
	s.order = append(s.order, c)
	s.object(t, c.schema, input)
	return c
}

// object fills sc with struct t's properties. In a request, fields read
// from the path, the query or a header aren't in the body.
func (s *schemas) object(t reflect.Type, sc *schema, input bool) {
	sc.typ = "object"
	sc.properties = []property{}
	for _, f := range jsonfield.Of(t) {
		sf := f.Field
		if input && notInBody(sf) {
			continue
		}
		tag := sf.Tag.Get("validate")
		if tag == "-" {
			tag = ""
		}
		required := input && jsonfield.Has(tag, "required")
		ps := s.of(sf.Type, tag, required, input)
		if f.Quoted && (ps.typ == "integer" || ps.typ == "number" || ps.typ == "boolean") {
			// json:",string": the value is written inside a string.
			ps.typ, ps.format, ps.minimum, ps.maximum = "string", "", nil, nil
			for i, v := range ps.enum {
				if v != nil {
					ps.enum[i] = fmt.Sprint(v)
				}
			}
		}
		if d := sf.Tag.Get("description"); d != "" {
			ps = describe(ps, d)
		}
		sc.properties = append(sc.properties, property{f.Name, ps})
		if required || !input && !f.OmitEmpty && !f.ViaPointer {
			sc.required = append(sc.required, f.Name)
		}
	}
}

// describe returns sc with a description; a reference is wrapped, as a
// $ref's siblings would describe the referenced schema.
func describe(sc *schema, d string) *schema {
	if sc.ref != nil {
		return &schema{description: d, ref: sc.ref, nullable: sc.nullable}
	}
	sc.description = d
	return sc
}

// notInBody reports whether a field of a request's input is read from
// the path, the query or a header only (web.H's binding), or is an
// uploaded file, read from a multipart form.
func notInBody(sf reflect.StructField) bool {
	if isFile(sf.Type) {
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

func isFile(t reflect.Type) bool {
	return t == reflect.PointerTo(fileHeaderType) || t == reflect.SliceOf(reflect.PointerTo(fileHeaderType))
}

// applyRules sets what a request value's validate rules say that JSON
// Schema can: its bounds (min, max, size, between), its values (in), its
// format (email, url, uuid, date, datetime, ipv4, ipv6) and distinct
// items (distinct). required also refuses blank strings and empty arrays
// and maps, as validate's required does. It reports whether the rules
// refuse a blank or empty value, which validate lets through when the
// value isn't required.
func applyRules(sc *schema, tag string, required bool) (restricted bool) {
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
		case sc.typ == "string" && sc.format != "binary":
			minp, maxp = &sc.minLength, &sc.maxLength
		case sc.typ == "array":
			minp, maxp = &sc.minItems, &sc.maxItems
		case sc.typ == "object" && sc.additional != nil:
			minp, maxp = &sc.minProps, &sc.maxProps
		case sc.typ == "integer" || sc.typ == "number":
			if lo != "" {
				sc.minimum = number(lo)
			}
			if hi != "" {
				sc.maximum = number(hi)
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
	typeFormat := sc.format // time.Time's date-time isn't a rule's
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
		case r.Name == "in" && sc.typ != "array" && sc.typ != "object":
			sc.enum = nil
			for _, p := range r.Params {
				sc.enum = append(sc.enum, enumValue(sc.typ, p))
			}
		case jsonfield.Formats[r.Name] != "" && sc.typ == "string":
			sc.format = jsonfield.Formats[r.Name]
			if r.Name == "url" {
				sc.pattern = schemePattern(r.Params) // uri allows any scheme; url, http and https
			}
		case r.Name == "distinct" && sc.typ == "array":
			sc.unique = true
		}
	}
	if required {
		switch {
		case sc.typ == "string" && sc.format != "binary" && (sc.minLength == nil || *sc.minLength < 1):
			sc.minLength = new(1)
		case sc.typ == "array" && (sc.minItems == nil || *sc.minItems < 1):
			sc.minItems = new(1)
		case sc.typ == "object" && sc.additional != nil && (sc.minProps == nil || *sc.minProps < 1):
			sc.minProps = new(1)
		}
	}
	restricted = len(sc.enum) > 0 || sc.typ == "string" && sc.format != typeFormat ||
		sc.minLength != nil && *sc.minLength > 0 || sc.minItems != nil && *sc.minItems > 0 || sc.minProps != nil && *sc.minProps > 0
	if sc.nullable && len(sc.enum) > 0 {
		sc.enum = append(sc.enum, nil)
	}
	return restricted && sc.format != "binary"
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
func (sc *schema) MarshalJSON() ([]byte, error) {
	var o object
	if sc.ref != nil {
		ref := map[string]string{"$ref": "#/components/schemas/" + sc.ref.name}
		switch {
		case sc.nullable:
			o.add("anyOf", []any{ref, map[string]string{"type": "null"}})
		case sc.description != "":
			o.add("allOf", []any{ref}) // siblings of a $ref would describe the target
		default:
			o.add("$ref", ref["$ref"])
		}
		if sc.description != "" {
			o.add("description", sc.description)
		}
		return o.marshal()
	}
	if sc.any {
		if sc.description != "" {
			o.add("description", sc.description)
		}
		return o.marshal()
	}
	if len(sc.anyOf) > 0 {
		o.add("anyOf", sc.anyOf)
		if sc.description != "" {
			o.add("description", sc.description)
		}
		return o.marshal()
	}
	if sc.nullable {
		o.add("type", []string{sc.typ, "null"})
	} else {
		o.add("type", sc.typ)
	}
	if sc.format != "" {
		o.add("format", sc.format)
	}
	if sc.encoding != "" {
		o.add("contentEncoding", sc.encoding)
	}
	if sc.description != "" {
		o.add("description", sc.description)
	}
	if sc.typ == "object" && sc.additional == nil {
		var props object
		for _, p := range sc.properties {
			props.add(p.name, p.schema)
		}
		o.add("properties", props)
		if len(sc.required) > 0 {
			o.add("required", sc.required)
		}
	}
	if sc.additional != nil {
		o.add("additionalProperties", sc.additional)
	}
	if sc.items != nil {
		o.add("items", sc.items)
	}
	if sc.enum != nil {
		o.add("enum", sc.enum)
	}
	for _, kv := range []struct {
		k string
		v *int
	}{{"minLength", sc.minLength}, {"maxLength", sc.maxLength}, {"minItems", sc.minItems}, {"maxItems", sc.maxItems},
		{"minProperties", sc.minProps}, {"maxProperties", sc.maxProps}} {
		if kv.v != nil {
			o.add(kv.k, *kv.v)
		}
	}
	if sc.minimum != nil {
		o.add("minimum", json.Number(strconv.FormatFloat(*sc.minimum, 'f', -1, 64)))
	}
	if sc.maximum != nil {
		o.add("maximum", json.Number(strconv.FormatFloat(*sc.maximum, 'f', -1, 64)))
	}
	if sc.pattern != "" {
		o.add("pattern", sc.pattern)
	}
	if sc.notZero {
		o.add("not", map[string]int{"const": 0})
	}
	if sc.mustTrue {
		o.add("const", true)
	}
	if sc.unique {
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

// name chooses the components' names: the Go type's ("ProductResponse";
// "PageProductResponse" for db.Page[ProductResponse]), with its package's
// name first ("handlers.Order") where two types share one, and "Input"
// after a type's request body when the type is also a response.
func nameComponents(comps []*component) {
	byType := map[reflect.Type][]*component{}
	for _, c := range comps {
		byType[c.typ] = append(byType[c.typ], c)
	}
	typesByBase := map[string][]reflect.Type{}
	for t := range byType {
		b := baseName(t)
		typesByBase[b] = append(typesByBase[b], t)
	}
	used := map[string]bool{"Problem": true} // web's problem details
	var pending []*component
	for _, ts := range typesByBase {
		for _, t := range ts {
			base := baseName(t)
			if len(ts) > 1 {
				base = pkgName(t) + "." + base
			}
			for _, c := range byType[t] {
				c.name = base
				if c.input && len(byType[t]) > 1 {
					c.name += "Input"
				}
				pending = append(pending, c)
			}
		}
	}
	// Names still shared (a NoteInput type beside Note's body, named
	// NoteInput too): numbered, in a fixed order, a type's own name
	// first.
	derived := func(c *component) bool {
		return strings.HasSuffix(c.name, "Input") && c.input && len(byType[c.typ]) > 1
	}
	first := map[*component]int{} // the order they were met in: by route
	for i, c := range comps {
		first[c] = i
	}
	slices.SortFunc(pending, func(a, b *component) int {
		return cmp.Or(strings.Compare(a.name, b.name), compareBool(derived(a), derived(b)),
			strings.Compare(pkgPath(a.typ)+"."+a.typ.Name(), pkgPath(b.typ)+"."+b.typ.Name()), compareBool(a.input, b.input),
			cmp.Compare(first[a], first[b])) // types declared in two functions, of one name
	})
	for _, c := range pending {
		name := c.name
		for i := 2; used[name]; i++ {
			name = c.name + strconv.Itoa(i)
		}
		c.name = name
		used[name] = true
	}
}

// baseName is t's name made a component's: type arguments' names follow
// a generic type's, without their packages.
func baseName(t reflect.Type) string {
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
