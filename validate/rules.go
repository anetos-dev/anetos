// SPDX-License-Identifier: Apache-2.0

package validate

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
)

// kind groups field types by how rules treat them.
type kind uint8

const (
	kOther kind = iota
	kString
	kNumeric
	kArray
	kBool
	kTime
	kFile
)

func (k kind) String() string {
	return [...]string{"other", "string", "numeric", "array", "bool", "time", "file"}[k]
}

// kinds is a set of kinds a rule accepts.
type kinds uint16

func of(ks ...kind) kinds {
	var s kinds
	for _, k := range ks {
		s |= 1 << k
	}
	return s
}

func (s kinds) has(k kind) bool { return s&(1<<k) != 0 }

// kindOf classifies t, looking through pointers.
func kindOf(t reflect.Type) kind {
	t = derefType(t)
	switch t {
	case fileHeaderType:
		return kFile
	case timeType, dateType:
		return kTime
	}
	switch t.Kind() {
	case reflect.String:
		return kString
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return kNumeric
	case reflect.Bool:
		return kBool
	case reflect.Slice, reflect.Array, reflect.Map:
		if reflect.PointerTo(t).Implements(textUnmarshalerType) {
			return kOther // a value like a UUID or net.IP, not a list
		}
		return kArray
	}
	return kOther
}

// elemKind is the kind of the elements of a slice or array type, or kOther.
func elemKind(t reflect.Type) kind {
	t = derefType(t)
	if kindOf(t) != kArray || t.Kind() == reflect.Map {
		return kOther
	}
	return kindOf(t.Elem())
}

// runFn is a compiled check on a non-empty field: v has pointers
// dereferenced and parent is the struct holding the field.
type runFn func(ctx context.Context, v, parent reflect.Value) (bool, error)

// spec describes a built-in rule.
type spec struct {
	implicit  bool  // runs on empty fields; build receives the raw field
	accepts   kinds // field kinds the rule applies to
	eachOf    kinds // also applies to slices of these kinds, element by element
	minParams int
	maxParams int // -1 for no limit
	sized     bool
	build     func(b *builder) (runFn, error)
}

// builder carries what a spec needs to compile one rule on one field.
type builder struct {
	name   string
	params []string
	field  reflect.StructField
	root   reflect.Type // struct the field (possibly promoted) belongs to
	kind   kind         // kind the rule works on: the field's, or its elements'
	typ    reflect.Type // type the rule works on, pointers removed
	args   []string     // message arguments; defaults to params
}

var builtins map[string]*spec

func init() {
	all := of(kString, kNumeric, kArray, kBool, kTime, kFile, kOther)
	sized := of(kString, kNumeric, kArray)
	scalar := of(kString, kNumeric, kBool)

	builtins = map[string]*spec{
		// Presence.
		"required":         {implicit: true, accepts: all, build: buildRequired},
		"required_if":      {implicit: true, accepts: all, minParams: 2, maxParams: -1, build: buildRequiredIf(true)},
		"required_unless":  {implicit: true, accepts: all, minParams: 2, maxParams: -1, build: buildRequiredIf(false)},
		"required_with":    {implicit: true, accepts: all, minParams: 1, maxParams: -1, build: buildRequiredWith(true)},
		"required_without": {implicit: true, accepts: all, minParams: 1, maxParams: -1, build: buildRequiredWith(false)},
		"accepted":         {implicit: true, accepts: scalar, build: buildAccepted},

		// Size.
		"min":     {accepts: sized, sized: true, minParams: 1, maxParams: 1, build: buildSize},
		"max":     {accepts: sized, sized: true, minParams: 1, maxParams: 1, build: buildSize},
		"size":    {accepts: sized, sized: true, minParams: 1, maxParams: 1, build: buildSize},
		"between": {accepts: sized, sized: true, minParams: 2, maxParams: 2, build: buildSize},

		// String formats; on slices of strings every element must match.
		"email":     strRule(isEmail),
		"uuid":      strRule(isUUID),
		"alpha":     strRule(allRunes(func(r rune) bool { return unicode.IsLetter(r) || unicode.IsMark(r) })),
		"alpha_num": strRule(allRunes(func(r rune) bool { return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r) })),
		"alpha_dash": strRule(allRunes(func(r rune) bool {
			return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r) || r == '-' || r == '_'
		})),
		"ascii":       strRule(allRunes(func(r rune) bool { return r < utf8.RuneSelf })),
		"numeric":     strRule(isNumeric),
		"integer":     strRule(isInteger),
		"lowercase":   strRule(func(s string) bool { return s == strings.ToLower(s) }),
		"uppercase":   strRule(func(s string) bool { return s == strings.ToUpper(s) }),
		"ip":          strRule(func(s string) bool { _, ok := parseIP(s); return ok }),
		"ipv4":        strRule(func(s string) bool { a, ok := parseIP(s); return ok && a.Is4() }),
		"ipv6":        strRule(func(s string) bool { a, ok := parseIP(s); return ok && a.Is6() }),
		"json":        strRule(func(s string) bool { return json.Valid([]byte(s)) }),
		"date":        strRule(func(s string) bool { _, err := time.Parse(time.DateOnly, s); return err == nil }),
		"datetime":    strRule(func(s string) bool { _, err := time.Parse(time.RFC3339, s); return err == nil }),
		"url":         {accepts: of(kString), eachOf: of(kString), maxParams: -1, build: buildURL},
		"starts_with": {accepts: of(kString), eachOf: of(kString), minParams: 1, maxParams: -1, build: buildAffix(strings.HasPrefix)},
		"ends_with":   {accepts: of(kString), eachOf: of(kString), minParams: 1, maxParams: -1, build: buildAffix(strings.HasSuffix)},

		// Choice.
		"in":       {accepts: scalar, eachOf: scalar, minParams: 1, maxParams: -1, build: buildIn(true)},
		"not_in":   {accepts: scalar, eachOf: scalar, minParams: 1, maxParams: -1, build: buildIn(false)},
		"distinct": {accepts: of(kArray), build: buildDistinct},

		// Comparison.
		"same":            {accepts: all, minParams: 1, maxParams: 1, build: buildSame(true)},
		"different":       {accepts: all, minParams: 1, maxParams: 1, build: buildSame(false)},
		"confirmed":       {accepts: all, build: buildConfirmed},
		"after":           timeRule(func(c int) bool { return c > 0 }),
		"after_or_equal":  timeRule(func(c int) bool { return c >= 0 }),
		"before":          timeRule(func(c int) bool { return c < 0 }),
		"before_or_equal": timeRule(func(c int) bool { return c <= 0 }),

		// Files: *multipart.FileHeader or a slice of them.
		"max_size":   {accepts: of(kFile), eachOf: of(kFile), minParams: 1, maxParams: 1, build: buildFileSize(false)},
		"min_size":   {accepts: of(kFile), eachOf: of(kFile), minParams: 1, maxParams: 1, build: buildFileSize(true)},
		"mimetypes":  {accepts: of(kFile), eachOf: of(kFile), minParams: 1, maxParams: -1, build: buildMimetypes},
		"extensions": {accepts: of(kFile), eachOf: of(kFile), minParams: 1, maxParams: -1, build: buildExtensions},
		"image":      {accepts: of(kFile), eachOf: of(kFile), build: buildImage},
	}
}

// laravelHints explain Laravel rules that have no equivalent here.
var laravelHints = map[string]string{
	"nullable":  "not needed: rules other than required skip empty fields",
	"sometimes": "not needed: rules other than required skip empty fields",
	"bail":      "not needed: validation of a field always stops at its first failure",
	"present":   "use a pointer field and required",
	"filled":    "not needed: rules other than required skip empty fields",
	"string":    "not needed: the Go field type decides; binding rejects values that don't convert",
	"boolean":   "not needed: use a bool field",
	"array":     "not needed: use a slice field",
	"file":      "not needed: use a *multipart.FileHeader field",
	"regex":     "tag parameters can't hold arbitrary patterns; register a custom rule with validate.Register",
	"unique":    "the db package registers it: import anetos.dev/anetos/db",
	"exists":    "the db package registers it: import anetos.dev/anetos/db",
	"gt":        "use min or after with a field, or a Validate method",
	"lt":        "use max or before with a field, or a Validate method",
	"digits":    "use size with a numeric string rule, e.g. integer|size:6",
	"mimes":     "use extensions and mimetypes",
}

// compileRule compiles rule name with params for struct field sf of root.
func compileRule(name string, params []string, sf reflect.StructField, root reflect.Type) (check, error) {
	s, ok := builtins[name]
	if !ok {
		cr, ok := lookupCustom(name)
		if !ok {
			if hint, ok := laravelHints[name]; ok {
				return check{}, fmt.Errorf("unknown rule %q: %s", name, hint)
			}
			return check{}, fmt.Errorf("unknown rule %q (register custom rules with validate.Register before routes are added)", name)
		}
		msg := cr.message
		if msg == "" {
			msg = messages["custom"]
		}
		return check{name: name, message: msg, args: params, custom: &cr, params: params}, nil
	}

	fk := kindOf(sf.Type)
	each := false
	switch {
	case s.accepts.has(fk):
	case fk == kArray && s.eachOf.has(elemKind(sf.Type)):
		each = true
	default:
		return check{}, fmt.Errorf("rule %q does not apply to %s fields", name, sf.Type)
	}
	switch {
	case len(params) < s.minParams:
		return check{}, fmt.Errorf("rule %q needs %s", name, paramCount(s.minParams, s.maxParams))
	case s.maxParams >= 0 && len(params) > s.maxParams:
		return check{}, fmt.Errorf("rule %q takes %s", name, paramCount(s.minParams, s.maxParams))
	}

	b := &builder{name: name, params: params, field: sf, root: root, kind: fk, typ: derefType(sf.Type), args: params}
	if each {
		b.kind, b.typ = elemKind(sf.Type), derefType(derefType(sf.Type).Elem())
	}
	fn, err := s.build(b)
	if err != nil {
		return check{}, fmt.Errorf("rule %q: %w", name, err)
	}
	if each {
		fn = eachElem(fn)
	}

	key := name
	if s.sized {
		key = name + "." + fk.String()
	}
	ch := check{name: name, implicit: s.implicit, message: messages[key], args: b.args}
	if s.implicit {
		ch.fn = fn
	} else {
		ch.fn = func(ctx context.Context, fv, parent reflect.Value) (bool, error) {
			v := deref(fv)
			if !v.IsValid() {
				return true, nil
			}
			return fn(ctx, v, parent)
		}
	}
	return ch, nil
}

func paramCount(minN, maxN int) string {
	plural := func(n int) string {
		if n == 1 {
			return "1 parameter"
		}
		return fmt.Sprintf("%d parameters", n)
	}
	switch {
	case maxN == 0:
		return "no parameters"
	case minN == maxN:
		return plural(minN)
	case maxN < 0:
		return "at least " + plural(minN)
	}
	return fmt.Sprintf("%d to %d parameters", minN, maxN)
}

// eachElem applies fn to every element of a slice or array, skipping
// empty ones (nil pointers, blank strings) like fields.
func eachElem(fn runFn) runFn {
	return func(ctx context.Context, v, parent reflect.Value) (bool, error) {
		for i := range v.Len() {
			e := deref(v.Index(i))
			if isEmpty(e) {
				continue
			}
			ok, err := fn(ctx, e, parent)
			if err != nil || !ok {
				return ok, err
			}
		}
		return true, nil
	}
}

// ---- siblings ----

// sibling is another field of the same struct that a rule refers to.
type sibling struct {
	index []int
	label string
	typ   reflect.Type
}

// findSibling looks up a field of root by Go name or request key.
func findSibling(root reflect.Type, name string) (sibling, error) {
	fields, err := structFields(root)
	if err != nil {
		return sibling{}, err
	}
	i := slices.IndexFunc(fields, func(f field) bool { return f.sf.Name == name })
	if i < 0 {
		i = slices.IndexFunc(fields, func(f field) bool { return f.key == name })
	}
	if i < 0 {
		return sibling{}, fmt.Errorf("no field %q in %s", name, root)
	}
	f := fields[i]
	label := f.sf.Tag.Get("label")
	if label == "" {
		label = humanize(f.key)
	}
	return sibling{index: f.sf.Index, label: label, typ: f.sf.Type}, nil
}

// value returns the sibling's value in parent, or an invalid Value if it
// sits behind a nil embedded pointer.
func (s sibling) value(parent reflect.Value) reflect.Value {
	v, err := parent.FieldByIndexErr(s.index)
	if err != nil {
		return reflect.Value{}
	}
	return v
}

// number is a numeric tag parameter, kept exact for integers.
type number struct {
	i      int64
	u      uint64
	f      float64
	isInt  bool // exactly an int64
	isUint bool // exactly a uint64
}

func parseNumber(s string) (number, error) {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return number{i: i, u: uint64(i), f: float64(i), isInt: true, isUint: i >= 0}, nil
	}
	if u, err := strconv.ParseUint(s, 10, 64); err == nil {
		return number{u: u, f: float64(u), isUint: true}, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return number{}, fmt.Errorf("%q is not a number", s)
	}
	if !strings.ContainsAny(s, ".eE") {
		return number{}, fmt.Errorf("%s is out of range", s)
	}
	n := number{f: f}
	if f == math.Trunc(f) && math.Abs(f) < 1<<53 { // "3.0"
		n.i, n.isInt = int64(f), true
		n.u, n.isUint = uint64(n.i), n.i >= 0
	}
	return n, nil
}

// compareNum compares a numeric value with n, exactly for integers.
func compareNum(v reflect.Value, n number) int {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		x := v.Int()
		switch {
		case n.isInt:
			return cmp.Compare(x, n.i)
		case n.isUint:
			return -1 // n is above the int64 range
		}
		return cmp.Compare(float64(x), n.f)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		x := v.Uint()
		switch {
		case n.isUint:
			return cmp.Compare(x, n.u)
		case n.isInt:
			return 1 // n is negative
		}
		return cmp.Compare(float64(x), n.f)
	case reflect.Float32:
		return cmp.Compare(v.Float(), float64(float32(n.f)))
	}
	return cmp.Compare(v.Float(), n.f)
}

func isIntKind(k reflect.Kind) bool {
	return k >= reflect.Int && k <= reflect.Uintptr
}

// wholeFor checks that n can equal a value of integer type t.
func wholeFor(t reflect.Type, param string, n number) error {
	if isIntKind(t.Kind()) && !n.isInt && !n.isUint {
		return fmt.Errorf("%q is not a whole number, so a %s can never equal it", param, t)
	}
	return nil
}

// compareNumbers compares two parameters, exactly when both are integers.
func compareNumbers(a, b number) int {
	switch {
	case a.isInt && b.isInt:
		return cmp.Compare(a.i, b.i)
	case a.isUint && b.isUint:
		return cmp.Compare(a.u, b.u)
	case a.isInt && b.isUint: // b > MaxInt64
		return -1
	case a.isUint && b.isInt: // a > MaxInt64
		return 1
	}
	return cmp.Compare(a.f, b.f)
}

// matcher returns a function reporting whether a (dereferenced) value of
// type t equals one of params. Numbers compare by value ("3" matches 3.0),
// booleans accept strconv.ParseBool forms.
func matcher(t reflect.Type, params []string) (func(reflect.Value) bool, error) {
	switch kindOf(t) {
	case kString:
		return func(v reflect.Value) bool { return slices.Contains(params, v.String()) }, nil
	case kBool:
		want := make([]bool, len(params))
		for i, p := range params {
			b, err := strconv.ParseBool(p)
			if err != nil {
				return nil, fmt.Errorf("%q is not a boolean", p)
			}
			want[i] = b
		}
		return func(v reflect.Value) bool { return slices.Contains(want, v.Bool()) }, nil
	case kNumeric:
		nums := make([]number, len(params))
		for i, p := range params {
			n, err := parseNumber(p)
			if err != nil {
				return nil, err
			}
			if err := wholeFor(t, p, n); err != nil {
				return nil, err
			}
			nums[i] = n
		}
		return func(v reflect.Value) bool {
			for _, n := range nums {
				if compareNum(v, n) == 0 {
					return true
				}
			}
			return false
		}, nil
	}
	return nil, fmt.Errorf("%s values can't be compared with parameters", t)
}

// ---- presence ----

func buildRequired(*builder) (runFn, error) {
	return func(_ context.Context, fv, _ reflect.Value) (bool, error) {
		return !isBlank(fv), nil
	}, nil
}

func buildRequiredIf(when bool) func(*builder) (runFn, error) {
	return func(b *builder) (runFn, error) {
		sib, err := findSibling(b.root, b.params[0])
		if err != nil {
			return nil, err
		}
		if sk := kindOf(sib.typ); sk != kString && sk != kNumeric && sk != kBool {
			return nil, fmt.Errorf("field %s must be a string, number or bool", b.params[0])
		}
		match, err := matcher(derefType(sib.typ), b.params[1:])
		if err != nil {
			return nil, err
		}
		b.args = []string{sib.label, strings.Join(b.params[1:], ", ")}
		return func(_ context.Context, fv, parent reflect.Value) (bool, error) {
			other := deref(sib.value(parent))
			matched := other.IsValid() && match(other)
			if matched != when {
				return true, nil
			}
			return !isBlank(fv), nil
		}, nil
	}
}

func buildRequiredWith(with bool) func(*builder) (runFn, error) {
	return func(b *builder) (runFn, error) {
		sibs := make([]sibling, len(b.params))
		labels := make([]string, len(b.params))
		for i, p := range b.params {
			s, err := findSibling(b.root, p)
			if err != nil {
				return nil, err
			}
			sibs[i], labels[i] = s, s.label
		}
		b.args = labels
		return func(_ context.Context, fv, parent reflect.Value) (bool, error) {
			trigger := false
			for _, s := range sibs {
				if isEmpty(s.value(parent)) != with {
					trigger = true
					break
				}
			}
			if !trigger {
				return true, nil
			}
			return !isBlank(fv), nil
		}, nil
	}
}

func buildAccepted(*builder) (runFn, error) {
	return func(_ context.Context, fv, _ reflect.Value) (bool, error) {
		v := deref(fv)
		if !v.IsValid() {
			return false, nil
		}
		switch v.Kind() {
		case reflect.Bool:
			return v.Bool(), nil
		case reflect.String:
			switch strings.ToLower(strings.TrimSpace(v.String())) {
			case "1", "true", "yes", "on":
				return true, nil
			}
			return false, nil
		}
		return compareNum(v, number{i: 1, u: 1, f: 1, isInt: true, isUint: true}) == 0, nil
	}, nil
}

// ---- size ----

func buildSize(b *builder) (runFn, error) {
	bounds := make([]number, len(b.params))
	for i, p := range b.params {
		if b.kind == kNumeric {
			n, err := parseNumber(p)
			if err != nil {
				return nil, err
			}
			if b.name == "size" {
				if err := wholeFor(b.typ, p, n); err != nil {
					return nil, err
				}
			}
			bounds[i] = n
			continue
		}
		n, err := strconv.ParseUint(p, 10, 31)
		if err != nil {
			return nil, fmt.Errorf("parameter %q must be a non-negative whole number", p)
		}
		bounds[i] = number{i: int64(n), u: n, f: float64(n), isInt: true, isUint: true}
	}
	measure := func(v reflect.Value, n number) int {
		switch v.Kind() {
		case reflect.String:
			return cmp.Compare(int64(utf8.RuneCountInString(v.String())), n.i)
		case reflect.Slice, reflect.Array, reflect.Map:
			return cmp.Compare(int64(v.Len()), n.i)
		}
		return compareNum(v, n)
	}
	var ok func(v reflect.Value) bool
	switch b.name {
	case "min":
		ok = func(v reflect.Value) bool { return measure(v, bounds[0]) >= 0 }
	case "max":
		ok = func(v reflect.Value) bool { return measure(v, bounds[0]) <= 0 }
	case "size":
		ok = func(v reflect.Value) bool { return measure(v, bounds[0]) == 0 }
	case "between":
		if compareNumbers(bounds[0], bounds[1]) > 0 {
			return nil, fmt.Errorf("%s is greater than %s", b.params[0], b.params[1])
		}
		ok = func(v reflect.Value) bool { return measure(v, bounds[0]) >= 0 && measure(v, bounds[1]) <= 0 }
	}
	return func(_ context.Context, v, _ reflect.Value) (bool, error) {
		return ok(v), nil
	}, nil
}

// ---- string formats ----

func strRule(pred func(string) bool) *spec {
	return &spec{
		accepts: of(kString),
		eachOf:  of(kString),
		build: func(*builder) (runFn, error) {
			return func(_ context.Context, v, _ reflect.Value) (bool, error) {
				return pred(v.String()), nil
			}, nil
		},
	}
}

func allRunes(pred func(rune) bool) func(string) bool {
	return func(s string) bool {
		for _, r := range s {
			if !pred(r) {
				return false
			}
		}
		return true
	}
}

// isEmail accepts a plain address (no display name or angle brackets) with
// a dot-atom local part and a domain of at least two labels. Letters beyond
// ASCII are allowed, for internationalized addresses; quoted local parts
// and IP-literal domains are not.
func isEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at < 1 || len(s) > 254 || at > 64 {
		return false
	}
	local, domain := s[:at], s[at+1:]
	if !dotSeparated(local, func(r rune) bool {
		return r < utf8.RuneSelf && strings.ContainsRune(atext, r) || r >= utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r))
	}) {
		return false
	}
	labels := 0
	for label := range strings.SplitSeq(domain, ".") {
		labels++
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if r != '-' && !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r) {
				return false
			}
		}
	}
	return labels >= 2
}

// atext are the ASCII characters RFC 5322 allows in a dot-atom.
const atext = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!#$%&'*+/=?^_`{|}~-"

// dotSeparated reports whether s is non-empty runs of ok runes joined by
// single dots.
func dotSeparated(s string, ok func(rune) bool) bool {
	prevDot := true // no leading dot
	for _, r := range s {
		if r == '.' {
			if prevDot {
				return false
			}
			prevDot = true
			continue
		}
		if r == utf8.RuneError || !ok(r) {
			return false
		}
		prevDot = false
	}
	return !prevDot
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}

func isNumeric(s string) bool {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) &&
		!strings.ContainsAny(s, "_xXpP") // no Go literal syntax
}

func isInteger(s string) bool {
	s = strings.TrimSpace(s)
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil && !strings.Contains(s, "_")
}

// parseIP accepts an IPv4 or IPv6 address without a zone ("%eth0").
func parseIP(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(s)
	return a, err == nil && a.Zone() == ""
}

func buildURL(b *builder) (runFn, error) {
	schemes := []string{"http", "https"} // javascript:, data: and friends need opting in
	if len(b.params) > 0 {
		schemes = make([]string, len(b.params))
		for i, p := range b.params {
			schemes[i] = strings.ToLower(p)
		}
	}
	return func(_ context.Context, v, _ reflect.Value) (bool, error) {
		s := v.String()
		if strings.ContainsAny(s, " \t\r\n") {
			return false, nil
		}
		u, err := url.Parse(s)
		valid := err == nil && u.Scheme != "" && u.Host != ""
		return valid && slices.Contains(schemes, strings.ToLower(u.Scheme)), nil
	}, nil
}

func buildAffix(match func(s, affix string) bool) func(*builder) (runFn, error) {
	return func(b *builder) (runFn, error) {
		return func(_ context.Context, v, _ reflect.Value) (bool, error) {
			s := v.String()
			for _, p := range b.params {
				if match(s, p) {
					return true, nil
				}
			}
			return false, nil
		}, nil
	}
}

// ---- choice ----

func buildIn(in bool) func(*builder) (runFn, error) {
	return func(b *builder) (runFn, error) {
		match, err := matcher(b.typ, b.params)
		if err != nil {
			return nil, err
		}
		return func(_ context.Context, v, _ reflect.Value) (bool, error) {
			return match(v) == in, nil
		}, nil
	}
}

func buildDistinct(b *builder) (runFn, error) {
	t := derefType(b.field.Type)
	if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
		return nil, errors.New("applies to slices and arrays")
	}
	if !strictlyComparable(derefType(t.Elem())) {
		return nil, fmt.Errorf("elements of type %s can't be compared", t.Elem())
	}
	return func(_ context.Context, v, _ reflect.Value) (bool, error) {
		n := v.Len()
		if n <= 32 { // pairwise comparison: no allocation for small lists
			for i := range n {
				a := deref(v.Index(i))
				if !a.IsValid() {
					continue
				}
				for j := i + 1; j < n; j++ {
					if b := deref(v.Index(j)); b.IsValid() && a.Equal(b) {
						return false, nil
					}
				}
			}
			return true, nil
		}
		seen := make(map[any]struct{}, n)
		for i := range n {
			e := deref(v.Index(i))
			if !e.IsValid() {
				continue
			}
			k := e.Interface()
			if _, dup := seen[k]; dup {
				return false, nil
			}
			seen[k] = struct{}{}
		}
		return true, nil
	}, nil
}

// ---- comparison ----

func buildSame(same bool) func(*builder) (runFn, error) {
	return func(b *builder) (runFn, error) {
		sib, err := findSibling(b.root, b.params[0])
		if err != nil {
			return nil, err
		}
		if derefType(sib.typ) != derefType(b.field.Type) {
			return nil, fmt.Errorf("field %s is a %s, not a %s", b.params[0], sib.typ, b.field.Type)
		}
		b.args = []string{sib.label}
		comparable := strictlyComparable(derefType(sib.typ))
		return func(_ context.Context, v, parent reflect.Value) (bool, error) {
			return equal(v, sib.value(parent), comparable) == same, nil
		}, nil
	}
}

func buildConfirmed(b *builder) (runFn, error) {
	name := b.field.Name + "Confirmation"
	sib, err := findSibling(b.root, name)
	if err != nil {
		key := fieldKey(b.field) + "_confirmation"
		if sib, err = findSibling(b.root, key); err != nil {
			return nil, fmt.Errorf("needs a field named %s or keyed %q", name, key)
		}
	}
	if derefType(sib.typ) != derefType(b.field.Type) {
		return nil, fmt.Errorf("confirmation field is a %s, not a %s", sib.typ, b.field.Type)
	}
	comparable := strictlyComparable(derefType(sib.typ))
	return func(_ context.Context, v, parent reflect.Value) (bool, error) {
		return equal(v, sib.value(parent), comparable), nil
	}, nil
}

// equal compares two values of the same type with pointers dereferenced.
// comparable says whether == works on the type without panicking.
func equal(a, b reflect.Value, comparable bool) bool {
	a, b = deref(a), deref(b)
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}
	if t, ok := reflect.TypeAssert[time.Time](a); ok {
		u, _ := reflect.TypeAssert[time.Time](b)
		return t.Equal(u)
	}
	if comparable {
		return a.Equal(b)
	}
	return reflect.DeepEqual(a.Interface(), b.Interface())
}

// strictlyComparable reports whether == on values of t can never panic: no
// interfaces, slices, maps or funcs anywhere inside (pointers are compared
// by address).
func strictlyComparable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface, reflect.Slice, reflect.Map, reflect.Func:
		return false
	case reflect.Array:
		return strictlyComparable(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			if !strictlyComparable(t.Field(i).Type) {
				return false
			}
		}
	}
	return true
}

func timeRule(ok func(cmp int) bool) *spec {
	return &spec{
		accepts:   of(kTime),
		minParams: 1,
		maxParams: 1,
		build: func(b *builder) (runFn, error) {
			typ := derefType(b.field.Type)
			if b.params[0] == "now" {
				return func(ctx context.Context, v, _ reflect.Value) (bool, error) {
					now := anetos.Now(ctx) // the app's clock
					if typ == dateType {
						now = anetos.Today(ctx).In(time.UTC) // today, in the app's zone
					}
					return ok(instant(v).Compare(now)), nil
				}, nil
			}
			sib, err := findSibling(b.root, b.params[0])
			if err != nil {
				return nil, err
			}
			if derefType(sib.typ) != typ {
				return nil, fmt.Errorf("field %s is not of type %s", b.params[0], typ)
			}
			b.args = []string{sib.label}
			return func(_ context.Context, v, parent reflect.Value) (bool, error) {
				other := sib.value(parent)
				if isEmpty(other) {
					return true, nil // nothing to compare against; the other field's own rules report it
				}
				return ok(instant(v).Compare(instant(deref(other)))), nil
			}, nil
		},
	}
}

var dateType = reflect.TypeFor[anetos.Date]()

// instant returns a time.Time, or an anetos.Date as its midnight in
// UTC, which orders dates as days.
func instant(v reflect.Value) time.Time {
	if d, ok := reflect.TypeAssert[anetos.Date](v); ok {
		return d.In(time.UTC)
	}
	t, _ := reflect.TypeAssert[time.Time](v)
	return t
}

// ---- files ----

func fileHeader(v reflect.Value) *multipart.FileHeader {
	if v.CanAddr() {
		return v.Addr().Interface().(*multipart.FileHeader)
	}
	fh := v.Interface().(multipart.FileHeader)
	return &fh
}

func buildFileSize(minimum bool) func(*builder) (runFn, error) {
	return func(b *builder) (runFn, error) {
		var limit config.ByteSize
		if err := limit.UnmarshalText([]byte(b.params[0])); err != nil {
			return nil, err
		}
		b.args = []string{limit.String()}
		return func(_ context.Context, v, _ reflect.Value) (bool, error) {
			size := fileHeader(v).Size
			if minimum {
				return size >= int64(limit), nil
			}
			return size <= int64(limit), nil
		}, nil
	}
}

// sniffable are the media types net/http.DetectContentType can report
// (parameters removed). mimetypes rejects others at compile time, since a
// rule naming them could never pass.
var sniffable = []string{
	"application/octet-stream", "application/ogg", "application/pdf", "application/postscript",
	"application/vnd.ms-fontobject", "application/wasm", "application/x-gzip",
	"application/x-rar-compressed", "application/zip",
	"audio/aiff", "audio/midi", "audio/mpeg", "audio/wave",
	"font/collection", "font/otf", "font/ttf", "font/woff", "font/woff2",
	"image/bmp", "image/gif", "image/jpeg", "image/png", "image/vnd.microsoft.icon", "image/webp", "image/x-icon",
	"text/html", "text/plain", "text/xml",
	"video/avi", "video/mp4", "video/webm",
}

func buildMimetypes(b *builder) (runFn, error) {
	allowed := make([]string, len(b.params))
	for i, p := range b.params {
		p = strings.ToLower(p)
		family, wildcard := strings.CutSuffix(p, "/*")
		known := slices.Contains(sniffable, p) ||
			wildcard && slices.ContainsFunc(sniffable, func(t string) bool { return strings.HasPrefix(t, family+"/") })
		if !known {
			return nil, fmt.Errorf("%q can't be detected from file content (detectable: %s; Office files are application/zip)", b.params[i], strings.Join(sniffable, ", "))
		}
		allowed[i] = p
	}
	return func(_ context.Context, v, _ reflect.Value) (bool, error) {
		mt, err := sniff(fileHeader(v))
		if err != nil {
			return false, err
		}
		for _, a := range allowed {
			if a == mt {
				return true, nil
			}
			if prefix, ok := strings.CutSuffix(a, "/*"); ok && strings.HasPrefix(mt, prefix+"/") {
				return true, nil
			}
		}
		return false, nil
	}, nil
}

func buildExtensions(b *builder) (runFn, error) {
	exts := make([]string, len(b.params))
	for i, p := range b.params {
		exts[i] = "." + strings.ToLower(strings.TrimPrefix(p, "."))
	}
	return func(_ context.Context, v, _ reflect.Value) (bool, error) {
		name := strings.ToLower(fileHeader(v).Filename)
		for _, ext := range exts {
			if strings.HasSuffix(name, ext) && len(name) > len(ext) { // "tar.gz" works too
				return true, nil
			}
		}
		return false, nil
	}, nil
}

// imageTypes are the raster formats "image" accepts. SVG is excluded: it
// can carry scripts.
var imageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

func buildImage(*builder) (runFn, error) {
	return func(_ context.Context, v, _ reflect.Value) (bool, error) {
		mt, err := sniff(fileHeader(v))
		if err != nil {
			return false, err
		}
		return slices.Contains(imageTypes, mt), nil
	}, nil
}

// sniff detects a file's media type from its first 512 bytes, ignoring the
// client-supplied Content-Type.
func sniff(fh *multipart.FileHeader) (string, error) {
	f, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("validate: open upload %q: %w", fh.Filename, err)
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("validate: read upload %q: %w", fh.Filename, err)
	}
	mt, _, err := mime.ParseMediaType(http.DetectContentType(buf[:n]))
	if err != nil {
		return "application/octet-stream", nil
	}
	return mt, nil
}
