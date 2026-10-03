// SPDX-License-Identifier: Apache-2.0

package validate

import (
	"cmp"
	"context"
	"encoding"
	"fmt"
	"mime/multipart"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"anetos.dev/anetos/i18n"
)

// Field describes the field a custom [Rule] is checking.
type Field struct {
	Key    string   // request key, e.g. "address.city"
	Label  string   // human-readable name used in messages
	Value  any      // the field's value, with pointers dereferenced (nil for a nil pointer)
	Params []string // rule parameters from the tag, e.g. ["3"] for "slug_max:3"
	Parent any      // the struct that contains the field
}

// Rule is a custom validation rule. It reports whether f passes. A non-nil
// error means the check itself failed (for example a database was
// unreachable) and aborts validation with that error.
//
// Custom rules are skipped when the field is empty and not required, like
// the built-in rules.
type Rule func(ctx context.Context, f Field) (bool, error)

type customRule struct {
	fn      Rule
	message string
}

var (
	registryMu sync.RWMutex
	registry   = map[string]customRule{}
)

// Register adds a named rule usable in validate tags, for example
// "slug" or "starts_with_any:a,b". message is the default message; it may
// use {label}, and {0}, {1}, … or {list} for the parameters. A catalog's
// validation.<name> message replaces it (package i18n), and an empty
// message means validation.custom: "The {label} field is invalid.".
//
// Like sql.Register, Register is meant to be called from an init function,
// so rules exist before routes compile their validation plans. It panics if
// the name is not made of letters, digits and underscores, is already
// registered, or is a built-in rule.
func Register(name, message string, rule Rule) {
	if !validRuleName(name) {
		panic(fmt.Sprintf("validate: invalid rule name %q (use letters, digits and underscores)", name))
	}
	if rule == nil {
		panic("validate: nil rule " + name)
	}
	if _, ok := builtins[name]; ok {
		panic(fmt.Sprintf("validate: %q is a built-in rule", name))
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("validate: rule %q registered twice", name))
	}
	registry[name] = customRule{fn: rule, message: message}
}

func validRuleName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r != '_' && (r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

func lookupCustom(name string) (customRule, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	r, ok := registry[name]
	return r, ok
}

// Plan is the compiled validation for one struct type. Compile it once
// (Struct and web.H cache plans) and reuse it; a Plan is safe for concurrent
// use.
type Plan struct {
	typ    reflect.Type
	fields []*fieldPlan
}

type fieldPlan struct {
	index    []int // from the plan's struct, possibly through embedded structs
	key      string
	label    string
	labelTag bool // label comes from the label tag
	checks   []check
	nested   *Plan // struct or *struct
	elem     *Plan // slice or array of struct or *struct
	values   *Plan // map with string keys and struct values
}

// check is one compiled rule on one field.
type check struct {
	name     string
	implicit bool   // runs even when the field is empty (required and friends)
	key      string // the message's catalog key: validation.<rule>[.kind]
	message  string // the struct's override, or a custom rule's message
	args     []string
	fn       func(ctx context.Context, fv, parent reflect.Value) (bool, error)
	custom   *customRule
	params   []string
}

// Empty reports whether the plan has no rules at all, including in nested
// structs.
func (p *Plan) Empty() bool { return p == nil || len(p.fields) == 0 }

var plans sync.Map // reflect.Type → *Plan, complete and pruned

// maxDepth bounds struct nesting, so cyclic data (a struct that points to
// itself) returns an error instead of overflowing the stack. It matches
// encoding/json's nesting limit, so decoded requests never reach it.
const maxDepth = 10000

// Struct validates v, a struct or a pointer to one, using its validate
// tags. It returns nil, an *[Errors] listing the failures, or the error of a
// custom rule that could not run. The plan for v's type is compiled on
// first use and cached; tag mistakes are returned as errors.
func Struct(ctx context.Context, v any) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return fmt.Errorf("validate: nil %s", rv.Type())
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("validate: Struct needs a struct, got %T", v)
	}
	p, err := Compile(rv.Type())
	if err != nil {
		return err
	}
	return p.check(ctx, rv)
}

// Validate checks v, which must be the plan's struct type or a non-nil
// pointer to it. See [Struct].
func (p *Plan) Validate(ctx context.Context, v any) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}
	if !rv.IsValid() || rv.Type() != p.typ {
		return fmt.Errorf("validate: plan for %s used with %T", p.typ, v)
	}
	return p.check(ctx, rv)
}

func (p *Plan) check(ctx context.Context, rv reflect.Value) error {
	var errs Errors
	if err := p.run(ctx, rv, path{}, &errs); err != nil {
		return err
	}
	return errs.Err()
}

// Compile builds (or returns the cached) plan for struct type t. It reports
// unknown rules, bad parameters, rules that don't apply to a field's type,
// references to missing fields and unknown message keys.
func Compile(t reflect.Type) (*Plan, error) {
	if cached, ok := plans.Load(t); ok {
		return cached.(*Plan), nil
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("validate: %s is not a struct", t)
	}
	c := &compiler{built: map[reflect.Type]*Plan{}}
	if _, err := c.compile(t); err != nil {
		return nil, err
	}
	c.prune()
	for typ, p := range c.built {
		if typ != t {
			plans.LoadOrStore(typ, p)
		}
	}
	actual, _ := plans.LoadOrStore(t, c.built[t])
	return actual.(*Plan), nil
}

// MustCompile is like [Compile] but panics on error. Use it at startup.
func MustCompile(t reflect.Type) *Plan {
	p, err := Compile(t)
	if err != nil {
		panic(err)
	}
	return p
}

type compiler struct {
	built map[reflect.Type]*Plan // plans made by this compiler, possibly in progress
}

// messager lets a struct override messages: keys are "field.rule" or
// "rule", where field is the request key.
type messager interface {
	ValidationMessages() map[string]string
}

func (c *compiler) compile(t reflect.Type) (*Plan, error) {
	if cached, ok := plans.Load(t); ok {
		return cached.(*Plan), nil
	}
	if p, ok := c.built[t]; ok {
		return p, nil // recursive type: the plan is being built
	}
	p := &Plan{typ: t}
	c.built[t] = p

	fields, err := structFields(t)
	if err != nil {
		return nil, err
	}
	var overrides map[string]string
	if m, ok := reflect.TypeAssert[messager](reflect.New(t)); ok {
		overrides = m.ValidationMessages()
	}
	used := map[string]bool{} // override keys that matched
	for _, f := range fields {
		fp, err := c.compileField(t, f, overrides, used)
		if err != nil {
			return nil, fmt.Errorf("validate: %s.%s: %w", t, fieldPath(t, f.sf.Index), err)
		}
		p.fields = append(p.fields, fp)
	}
	for k := range overrides {
		_, builtin := builtins[k]
		_, custom := lookupCustom(k)
		if !used[k] && !builtin && !custom {
			return nil, fmt.Errorf("validate: %s.ValidationMessages: key %q matches no rule of %s (keys are \"field.rule\" or \"rule\"; messages for nested fields belong on the nested type)", t, k, t)
		}
	}
	return p, nil
}

func (c *compiler) compileField(root reflect.Type, f field, overrides map[string]string, used map[string]bool) (*fieldPlan, error) {
	sf := f.sf
	fp := &fieldPlan{index: sf.Index, key: f.key, label: sf.Tag.Get("label"), labelTag: sf.Tag.Get("label") != ""}
	if fp.label == "" {
		fp.label = humanize(fp.key)
	}
	rules, err := parseTag(sf.Tag.Get("validate"))
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		ch, err := compileRule(r.name, r.params, sf, root)
		if err != nil {
			return nil, err
		}
		if msg, ok := overrides[fp.key+"."+r.name]; ok {
			ch.message, ch.key = msg, ""
			used[fp.key+"."+r.name] = true
		} else if msg, ok := overrides[r.name]; ok {
			ch.message, ch.key = msg, ""
		}
		fp.checks = append(fp.checks, ch)
	}
	sub := func(t reflect.Type) (*Plan, error) {
		if t = derefType(t); !isNestable(t) {
			return nil, nil
		}
		return c.compile(t)
	}
	switch bt := derefType(sf.Type); {
	case isNestable(bt):
		fp.nested, err = sub(bt)
	case bt.Kind() == reflect.Slice || bt.Kind() == reflect.Array:
		fp.elem, err = sub(bt.Elem())
	case bt.Kind() == reflect.Map && bt.Key().Kind() == reflect.String:
		fp.values, err = sub(bt.Elem())
	}
	return fp, err
}

// prune drops nested plans without rules (directly or further down), so a
// struct whose nested types have no rules compiles to an empty plan. Plans
// of recursive types are live if any type in the cycle has rules.
func (c *compiler) prune() {
	mine := map[*Plan]bool{}
	for _, p := range c.built {
		mine[p] = true
	}
	live := map[*Plan]bool{}
	isLive := func(p *Plan) bool {
		if p == nil {
			return false
		}
		if mine[p] {
			return live[p]
		}
		return !p.Empty() // cached plans are already pruned
	}
	for changed := true; changed; {
		changed = false
		for _, p := range c.built {
			if live[p] {
				continue
			}
			for _, fp := range p.fields {
				if len(fp.checks) > 0 || isLive(fp.nested) || isLive(fp.elem) || isLive(fp.values) {
					live[p], changed = true, true
					break
				}
			}
		}
	}
	for _, p := range c.built {
		kept := p.fields[:0]
		for _, fp := range p.fields {
			for _, sub := range []**Plan{&fp.nested, &fp.elem, &fp.values} {
				if !isLive(*sub) {
					*sub = nil
				}
			}
			if len(fp.checks) > 0 || fp.nested != nil || fp.elem != nil || fp.values != nil {
				kept = append(kept, fp)
			}
		}
		p.fields = slices.Clip(kept)
	}
}

// field is a struct field as a client sees it: embedded structs flattened
// and shadowed fields removed, following encoding/json's rules.
type field struct {
	sf    reflect.StructField // Index is the full path from the root
	key   string              // error key: json, form, query, path or header name, or the Go name
	json  string              // name for encoding/json dominance; "" if json ignores the field
	depth int
	tag   bool // json name comes from a tag
}

// structFields lists t's fields that can carry rules, resolved exactly as
// encoding/json resolves them: embedded structs (and pointers to structs)
// without a json name are flattened; for each json name the shallowest
// field wins, then a tagged one, and an ambiguous name is dropped. A field
// with rules that is dropped this way, or two fields with rules reporting
// under the same key, is an error: their rules could never work as written.
func structFields(t reflect.Type) ([]field, error) {
	type embedded struct {
		typ   reflect.Type
		index []int
	}
	var all []field
	next := []embedded{{typ: t}}
	var count, nextCount map[reflect.Type]int
	visited := map[reflect.Type]bool{}
	for depth := 0; len(next) > 0; depth++ {
		current := next
		next = nil
		count, nextCount = nextCount, map[reflect.Type]int{}
		for _, e := range current {
			if visited[e.typ] {
				continue
			}
			visited[e.typ] = true
			for i := range e.typ.NumField() {
				sf := e.typ.Field(i)
				sf.Index = append(slices.Clone(e.index), i)
				vtag, hasRules := sf.Tag.Lookup("validate")
				if vtag == "-" {
					continue
				}
				ft := sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				jsonTag := sf.Tag.Get("json")
				jsonName, _, _ := strings.Cut(jsonTag, ",")
				if sf.Anonymous {
					if !sf.IsExported() && ft.Kind() != reflect.Struct {
						continue
					}
					if jsonTag == "-" {
						continue // encoding/json ignores it, so clients can't fill it
					}
					if jsonName == "" && ft.Kind() == reflect.Struct && isNestable(ft) {
						if hasRules {
							return nil, fmt.Errorf("validate: %s.%s: rules on an embedded struct; put them on its fields, or use validate:\"-\" to skip it", t, fieldPath(t, sf.Index))
						}
						nextCount[ft]++
						if nextCount[ft] == 1 {
							next = append(next, embedded{typ: ft, index: sf.Index})
						}
						continue
					}
				} else if !sf.IsExported() {
					if hasRules {
						return nil, fmt.Errorf("validate: %s.%s: unexported fields can't be validated", t, fieldPath(t, sf.Index))
					}
					continue
				}
				f := field{sf: sf, depth: depth, tag: jsonName != ""}
				f.key = fieldKey(sf)
				if jsonTag != "-" {
					f.json = cmp.Or(jsonName, sf.Name)
				}
				all = append(all, f)
				if count[e.typ] > 1 {
					// The embedding struct appeared twice at this depth, so
					// every name it provides is ambiguous, as in encoding/json.
					all = append(all, f)
				}
			}
		}
	}

	// Dominance by json name, as encoding/json does it.
	slices.SortStableFunc(all, func(a, b field) int {
		if c := cmp.Or(strings.Compare(a.json, b.json), cmp.Compare(a.depth, b.depth)); c != 0 {
			return c
		}
		switch {
		case a.tag && !b.tag:
			return -1
		case b.tag && !a.tag:
			return 1
		}
		return 0
	})
	var out []field
	for i := 0; i < len(all); {
		j := i + 1
		for j < len(all) && all[i].json != "" && all[j].json == all[i].json {
			j++
		}
		group := all[i:j]
		i = j
		if len(group) == 1 || group[0].depth != group[1].depth || group[0].tag != group[1].tag {
			out = append(out, group[0]) // deeper fields are shadowed, as in Go
			continue
		}
		// Ambiguous: encoding/json drops every field with this name.
		for _, f := range group {
			if f.depth != group[0].depth || f.tag != group[0].tag {
				break
			}
			if _, has := f.sf.Tag.Lookup("validate"); has {
				other := group[0]
				if slices.Equal(other.sf.Index, f.sf.Index) {
					other = group[1]
				}
				why := "is ambiguous with " + fieldPath(t, other.sf.Index)
				if slices.Equal(other.sf.Index, f.sf.Index) {
					why = "comes from a struct embedded more than once at the same depth"
				}
				return nil, fmt.Errorf("validate: %s.%s: has rules but %s (json %q), so encoding/json ignores it and clients can't set it", t, fieldPath(t, f.sf.Index), why, f.json)
			}
		}
	}
	slices.SortFunc(out, func(a, b field) int { return slices.Compare(a.sf.Index, b.sf.Index) })

	seen := map[string]field{}
	for _, f := range out {
		if _, has := f.sf.Tag.Lookup("validate"); !has {
			continue
		}
		if prev, dup := seen[f.key]; dup {
			return nil, fmt.Errorf("validate: %s: %s and %s both report errors as %q; give one a different name", t, fieldPath(t, prev.sf.Index), fieldPath(t, f.sf.Index), f.key)
		}
		seen[f.key] = f
	}
	return out, nil
}

// fieldPath names the field at index for error messages, e.g. "Base.Name".
func fieldPath(t reflect.Type, index []int) string {
	var names []string
	for _, i := range index {
		t = derefType(t)
		sf := t.Field(i)
		names = append(names, sf.Name)
		t = sf.Type
	}
	return strings.Join(names, ".")
}

type parsedRule struct {
	name   string
	params []string
}

// parseTag splits a tag like "required|max:200|in:a,b" into rules. Spaces
// around rules and parameters are ignored.
func parseTag(tag string) ([]parsedRule, error) {
	var out []parsedRule
	for spec := range strings.SplitSeq(tag, "|") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		name, param, hasParams := strings.Cut(spec, ":")
		r := parsedRule{name: strings.TrimSpace(name)}
		if !validRuleName(r.name) {
			return nil, fmt.Errorf("malformed rule %q (write rules like required|max:200|in:a,b)", spec)
		}
		if hasParams {
			for p := range strings.SplitSeq(param, ",") {
				p = strings.TrimSpace(p)
				if p == "" {
					return nil, fmt.Errorf("rule %q has an empty parameter", spec)
				}
				r.params = append(r.params, p)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

var (
	timeType            = reflect.TypeFor[time.Time]()
	fileHeaderType      = reflect.TypeFor[multipart.FileHeader]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// isNestable reports whether t is a struct validated field by field: not a
// time, an upload, or a value type that parses itself from text.
func isNestable(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t != timeType && t != fileHeaderType &&
		!reflect.PointerTo(t).Implements(textUnmarshalerType)
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// fieldKey is the name a client uses for the field: the json name, else the
// form, query, path or header name, else the Go field name.
func fieldKey(sf reflect.StructField) string {
	for _, tag := range []string{"json", "form", "query", "path", "header"} {
		if v, ok := sf.Tag.Lookup(tag); ok {
			name, _, _ := strings.Cut(v, ",")
			if name != "" && name != "-" {
				return name
			}
		}
	}
	return sf.Name
}

// humanize turns a key into a label: "first_name", "firstName" and
// "first-name" become "first name"; "UserID" becomes "user id".
func humanize(key string) string {
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		key = key[i+1:]
	}
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(key)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || unicode.IsSpace(r):
			flush()
			continue
		case unicode.IsUpper(r) && i > 0:
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return strings.Join(words, " ")
}

// path is the position of a nested struct in the value being validated.
// It is passed by value so it stays on the stack; keys are built from it
// only when a message is added, so valid input doesn't pay for strings.
type path struct {
	n      int         // elements
	levels int         // nested structs
	elems  [8]pathElem // the first elements
	more   []pathElem  // later elements; siblings reuse the backing array
}

type pathElem struct {
	name    string // field or map key
	index   int    // slice index
	isIndex bool
}

func (p path) push(e pathElem) path {
	if p.n < len(p.elems) {
		p.elems[p.n] = e
	} else {
		// Safe to share: validation is depth-first, so a sibling only
		// overwrites elements its predecessor no longer needs.
		p.more = append(p.more[:p.n-len(p.elems)], e)
	}
	p.n++
	return p
}

// key returns the dotted key of leaf under p, e.g. "items.0.name".
func (p *path) key(leaf string) string {
	if p.n == 0 {
		return leaf
	}
	var b strings.Builder
	for _, e := range append(p.elems[:min(p.n, len(p.elems))], p.more[:max(p.n-len(p.elems), 0)]...) {
		if e.isIndex {
			b.WriteString(strconv.Itoa(e.index))
		} else {
			b.WriteString(e.name)
		}
		b.WriteByte('.')
	}
	b.WriteString(leaf)
	return b.String()
}

// run validates struct value v at position at.
func (p *Plan) run(ctx context.Context, v reflect.Value, at path, errs *Errors) error {
	for _, fp := range p.fields {
		fv, err := v.FieldByIndexErr(fp.index)
		if err != nil {
			fv = reflect.Value{} // inside a nil embedded pointer: treat as empty
		}
		empty := isEmpty(fv)
		for i := range fp.checks {
			ch := &fp.checks[i]
			if empty && !ch.implicit {
				continue
			}
			ok, err := ch.run(ctx, fv, v, &at, fp)
			if err != nil {
				return err
			}
			if !ok {
				errs.Add(at.key(fp.key), ch.text(ctx, fp))
				break
			}
		}
		if err := fp.runNested(ctx, fv, at, errs); err != nil {
			return err
		}
	}
	return nil
}

func (ch *check) run(ctx context.Context, fv, parent reflect.Value, at *path, fp *fieldPlan) (bool, error) {
	if ch.custom == nil {
		return ch.fn(ctx, fv, parent)
	}
	var value any
	if d := deref(fv); d.IsValid() {
		value = d.Interface()
	}
	return ch.custom.fn(ctx, Field{Key: at.key(fp.key), Label: fp.label, Value: value, Params: ch.params, Parent: parent.Interface()})
}

func (fp *fieldPlan) runNested(ctx context.Context, fv reflect.Value, at path, errs *Errors) error {
	if fp.nested == nil && fp.elem == nil && fp.values == nil {
		return nil
	}
	d := deref(fv)
	if !d.IsValid() {
		return nil
	}
	if at.levels >= maxDepth {
		return fmt.Errorf("validate: %s: structs nested more than %d levels deep (cyclic data?)", at.key(fp.key), maxDepth)
	}
	here := at.push(pathElem{name: fp.key})
	here.levels++
	switch {
	case fp.nested != nil:
		return fp.nested.run(ctx, d, here, errs)
	case fp.elem != nil:
		for i := range d.Len() {
			if e := deref(d.Index(i)); e.IsValid() {
				if err := fp.elem.run(ctx, e, here.push(pathElem{index: i, isIndex: true}), errs); err != nil {
					return err
				}
			}
		}
	case fp.values != nil:
		keys := d.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		for _, k := range keys {
			if e := deref(d.MapIndex(k)); e.IsValid() {
				if err := fp.values.run(ctx, e, here.push(pathElem{name: k.String()}), errs); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// deref follows pointers and interfaces; it returns an invalid Value for
// nil.
func deref(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

// isEmpty reports whether a field counts as not provided, in which case
// only implicit rules (required and friends) run. Nil pointers, blank
// strings, empty slices and maps, and zero structs and arrays (a zero time,
// UUID or nested struct) are empty; numbers and booleans never are (use a
// pointer to make them optional).
func isEmpty(fv reflect.Value) bool {
	v := deref(fv)
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.String:
		return strings.TrimSpace(v.String()) == ""
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Array:
		return v.Len() == 0 || v.IsZero()
	case reflect.Struct:
		if t, ok := reflect.TypeAssert[time.Time](v); ok {
			return t.IsZero()
		}
		return v.IsZero()
	}
	return false
}

// isBlank is what "required" rejects: empty values, plus zero numbers and
// false booleans in non-pointer fields.
func isBlank(fv reflect.Value) bool {
	if isEmpty(fv) {
		return true
	}
	switch fv.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return fv.IsZero()
	}
	return false // includes pointers to zero values: explicitly provided
}

// text returns the check's message for fp in ctx's language: the
// catalog's message for its key (validation.<rule>), or the struct's
// override (itself translated if it is a catalog key), or a custom rule's
// message, with the field's label (validation.attributes.<key>, else the
// label tag, translated if it is a key, else the humanized key) and the
// rule's arguments (validation.values.<arg> where the catalog has one).
func (ch *check) text(ctx context.Context, fp *fieldPlan) string {
	msg := ch.message
	switch {
	case ch.key != "":
		if m, ok := i18n.Lookup(ctx, ch.key); ok {
			msg = m
		} else if msg == "" {
			msg, _ = i18n.Lookup(ctx, "validation.custom")
		}
	default:
		if m, ok := i18n.Lookup(ctx, msg); ok {
			msg = m
		}
	}
	label := fp.label
	if l, ok := i18n.Lookup(ctx, "validation.attributes."+fp.key); ok {
		label = l
	} else if l, ok := i18n.Lookup(ctx, fp.label); ok && fp.labelTag {
		label = l
	}
	args := ch.args
	sized := sizeRule(ch.key)
	for i, a := range ch.args {
		v, ok := i18n.Lookup(ctx, "validation.values."+a)
		if !ok && sized { // "at least 3": the locale's digits
			v = i18n.LocalNumber(ctx, a)
			ok = v != a
		}
		if ok {
			if &args[0] == &ch.args[0] {
				args = slices.Clone(ch.args)
			}
			args[i] = v
		}
	}
	return format(msg, label, args)
}

// sizeRule reports whether key is a size rule's message, whose numeric
// arguments are shown as numbers in the locale (other rules' arguments
// are values to type, kept as they are).
func sizeRule(key string) bool {
	rule, _, _ := strings.Cut(strings.TrimPrefix(key, "validation."), ".")
	switch rule {
	case "min", "max", "size", "between", "digits", "digits_between", "decimal", "multiple_of":
		return strings.HasPrefix(key, "validation.")
	}
	return false
}

// format fills {label}, {list} and {0}, {1}, … in a message template.
// {list} is every argument joined with ", ". Unknown placeholders are kept.
func format(msg, label string, args []string) string {
	i := strings.IndexByte(msg, '{')
	if i < 0 {
		return msg
	}
	var b strings.Builder
	b.Grow(len(msg) + len(label) + 16)
	for i >= 0 {
		b.WriteString(msg[:i])
		msg = msg[i:]
		end := strings.IndexByte(msg, '}')
		if end < 0 {
			break
		}
		name := msg[1:end]
		switch n, err := strconv.Atoi(name); {
		case name == "label":
			b.WriteString(label)
		case name == "list":
			for j, a := range args {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(a)
			}
		case err == nil && n >= 0 && n < len(args):
			b.WriteString(args[n])
		default: // not a placeholder: keep the brace, look inside it
			b.WriteByte('{')
			msg = msg[1:]
			i = strings.IndexByte(msg, '{')
			continue
		}
		msg = msg[end+1:]
		i = strings.IndexByte(msg, '{')
	}
	b.WriteString(msg)
	return b.String()
}
