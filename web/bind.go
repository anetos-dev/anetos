// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/internal/convert"
	"anetos.dev/anetos/internal/jsonfield"
	"anetos.dev/anetos/validate"
)

// MaxMultipartMemory is how much of a multipart form is held in memory
// while binding; larger file parts are stored in temporary files. The total
// body size is limited separately by the BodyLimit middleware (HTTP_MAX_BODY).
const MaxMultipartMemory = 32 << 20

type source int

const (
	fromPath source = iota
	fromQuery
	fromHeader
	fromForm
)

func (s source) String() string {
	return [...]string{"path", "query", "header", "form"}[s]
}

type fieldPlan struct {
	index  []int
	src    source
	name   string
	set    convert.Setter // scalar (or element, for slices)
	kind   string         // the value's kind, for messages: binding.<kind>
	slice  bool
	file   bool // *multipart.FileHeader
	files  bool // []*multipart.FileHeader
	goName string
}

// bindPlan is computed once per input type, at route registration.
type bindPlan struct {
	typ       reflect.Type
	fields    []fieldPlan
	ordered   []*fieldPlan      // form fields first, then path/query/header
	bodyField bool              // has form fields or JSON-decodable fields
	nonBody   [][]int           // path/query/header-only fields, cleared after JSON decoding
	formKeys  map[string]string // validation key (json name) → form name, where they differ
}

var (
	fileHeaderType  = reflect.TypeFor[*multipart.FileHeader]()
	fileHeadersType = reflect.TypeFor[[]*multipart.FileHeader]()
)

func newBindPlan(t reflect.Type) (*bindPlan, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("web: input type %s must be a struct", t)
	}
	p := &bindPlan{typ: t}
	if err := p.addFields(t, nil); err != nil {
		return nil, err
	}
	for i := range p.fields {
		if p.fields[i].src == fromForm {
			p.ordered = append(p.ordered, &p.fields[i])
		}
	}
	for i := range p.fields {
		if p.fields[i].src != fromForm {
			p.ordered = append(p.ordered, &p.fields[i])
		}
	}
	return p, nil
}

func (p *bindPlan) addFields(t reflect.Type, index []int) error {
	for i := range t.NumField() {
		sf := t.Field(i)
		idx := append(append([]int(nil), index...), i)
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct && !hasBindTag(sf) {
			if err := p.addFields(sf.Type, idx); err != nil {
				return err
			}
			continue
		}
		if sf.Anonymous && sf.Type.Kind() == reflect.Pointer && sf.Type.Elem().Kind() == reflect.Struct &&
			hasSourceTags(sf.Type.Elem(), map[reflect.Type]bool{}) {
			// Its fields could be filled from the JSON body despite their
			// path/query/header tags; refuse instead of binding unsafely.
			return fmt.Errorf("web: %s.%s: embedded pointer structs with path/query/header/form tags are not supported; embed the struct by value", t, sf.Name)
		}
		if !sf.IsExported() {
			continue
		}
		explicit := false
		for _, s := range []source{fromPath, fromQuery, fromHeader, fromForm} {
			name, ok := sf.Tag.Lookup(s.String())
			if !ok {
				continue
			}
			explicit = true
			if name == "-" {
				continue
			}
			if name == "" {
				return fmt.Errorf("web: %s.%s: empty %s tag", t, sf.Name, s)
			}
			if err := p.addField(t, sf, idx, s, name); err != nil {
				return err
			}
		}
		if form := sf.Tag.Get("form"); form != "" && form != "-" {
			if key := jsonName(sf); key != "" && key != form {
				if p.formKeys == nil {
					p.formKeys = map[string]string{}
				}
				p.formKeys[key] = form
			}
		}
		if explicit && !hasTag(sf, "form") {
			// path/query/header field: never taken from the body, even if
			// the JSON decoder matched its name.
			p.nonBody = append(p.nonBody, idx)
			continue
		}
		if sf.Type == fileHeaderType || sf.Type == fileHeadersType {
			p.nonBody = append(p.nonBody, idx) // files only come from multipart parts, never JSON
		}
		p.bodyField = true
		// A field without a form tag is filled from forms by its JSON name,
		// so one input struct works for both JSON and HTML forms. Types a
		// form value can't express (structs, maps, …) stay JSON-only.
		if !hasTag(sf, "form") {
			if name := jsonName(sf); name != "" && formBindable(sf.Type) {
				if err := p.addField(t, sf, idx, fromForm, name); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (p *bindPlan) addField(t reflect.Type, sf reflect.StructField, idx []int, s source, name string) error {
	fp, err := newFieldPlan(sf, idx, s, name)
	if err != nil {
		return fmt.Errorf("web: %s.%s: %w", t, sf.Name, err)
	}
	p.fields = append(p.fields, fp)
	return nil
}

// formBindable reports whether a form value can be converted to t.
func formBindable(t reflect.Type) bool {
	if t == fileHeaderType || t == fileHeadersType {
		return true
	}
	if t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8 {
		t = t.Elem()
	}
	_, err := convert.For(t)
	return err == nil
}

// hasSourceTags reports whether t (or a struct nested in it) has path,
// query, header or form tags.
func hasSourceTags(t reflect.Type, seen map[reflect.Type]bool) bool {
	if t.Kind() != reflect.Struct || seen[t] {
		return false
	}
	seen[t] = true
	for sf := range t.Fields() {
		for _, k := range []string{"path", "query", "header", "form"} {
			if hasTag(sf, k) {
				return true
			}
		}
		ft := sf.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if hasSourceTags(ft, seen) {
			return true
		}
	}
	return false
}

func hasTag(sf reflect.StructField, key string) bool {
	_, ok := sf.Tag.Lookup(key)
	return ok
}

func hasBindTag(sf reflect.StructField) bool {
	for _, k := range []string{"path", "query", "header", "form", "json"} {
		if _, ok := sf.Tag.Lookup(k); ok {
			return true
		}
	}
	return false
}

func jsonName(sf reflect.StructField) string {
	tag, ok := sf.Tag.Lookup("json")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return ""
	}
	if name == "" {
		return sf.Name
	}
	return name
}

func newFieldPlan(sf reflect.StructField, idx []int, s source, name string) (fieldPlan, error) {
	fp := fieldPlan{index: idx, src: s, name: name, goName: sf.Name}
	t := sf.Type
	switch {
	case t == fileHeaderType || t == fileHeadersType:
		if s != fromForm {
			return fp, errors.New("file fields need a form tag")
		}
		fp.file = t == fileHeaderType
		fp.files = t == fileHeadersType
		return fp, nil
	case t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8:
		if s == fromPath {
			return fp, errors.New("path parameters can't be slices; use a {name...} wildcard with a string")
		}
		set, err := convert.For(t.Elem())
		if err != nil {
			return fp, err
		}
		fp.set, fp.slice, fp.kind = set, true, valueKind(t.Elem())
		return fp, nil
	}
	set, err := convert.For(t)
	if err != nil {
		return fp, err
	}
	fp.set, fp.kind = set, valueKind(t)
	return fp, nil
}

// bind fills dst (a pointer to the plan's struct type) from the request.
// The body is decoded first; query, header and path values are applied
// afterwards, so a path parameter can't be overridden by the body.
func (p *bindPlan) bind(c *Ctx, dst reflect.Value) error {
	r := c.r
	var formErr error
	isForm := false

	if p.bodyField && r.Body != nil && r.Body != http.NoBody && r.Method != http.MethodGet && r.Method != http.MethodHead {
		ct := r.Header.Get("Content-Type")
		mt, _, _ := mime.ParseMediaType(ct)
		switch {
		case isJSONContentType(ct):
			if err := decodeJSON(r.Context(), r.Body, dst.Interface()); err != nil {
				return err
			}
			for _, idx := range p.nonBody {
				f := dst.Elem().FieldByIndex(idx)
				f.SetZero()
			}
		case mt == "application/x-www-form-urlencoded":
			isForm = true
			formErr = r.ParseForm()
		case mt == "multipart/form-data":
			isForm = true
			formErr = r.ParseMultipartForm(MaxMultipartMemory)
		case ct == "" && r.ContentLength == 0:
			// no body
		default:
			return Errorf(http.StatusUnsupportedMediaType, "unsupported content type %q; send JSON or a form", mt)
		}
	}
	if formErr != nil {
		if mbe, ok := errors.AsType[*http.MaxBytesError](formErr); ok {
			return Errorf(http.StatusRequestEntityTooLarge, "request body is larger than %d bytes", mbe.Limit)
		}
		return Error(http.StatusBadRequest, "malformed form data").Wrap(formErr)
	}

	v := dst.Elem()
	fieldErrs := map[string]string{}
	var query map[string][]string
	// Form fields first, then path/query/header, so the body can never
	// override a value that also comes from the URL or headers.
	for _, fp := range p.ordered {
		fv := v.FieldByIndex(fp.index)
		var vals []string
		switch fp.src {
		case fromPath:
			if s := r.PathValue(fp.name); s != "" {
				vals = []string{s}
			}
		case fromQuery:
			if query == nil {
				query = r.URL.Query()
			}
			vals = query[fp.name]
		case fromHeader:
			vals = r.Header.Values(fp.name)
		case fromForm:
			if !isForm {
				continue
			}
			if fp.file || fp.files {
				if r.MultipartForm != nil {
					fhs := r.MultipartForm.File[fp.name]
					if fp.file && len(fhs) > 0 {
						fv.Set(reflect.ValueOf(fhs[0]))
					} else if fp.files {
						fv.Set(reflect.ValueOf(fhs))
					}
				}
				continue
			}
			vals = r.PostForm[fp.name]
		}
		if fp.src == fromForm || fp.src == fromQuery {
			vals = dropEmpty(vals, fv.Type())
		}
		if len(vals) == 0 {
			continue
		}
		if err := fp.apply(fv, vals); err != nil {
			fieldErrs[fp.name] = i18n.T(r.Context(), "binding."+fp.kind)
		}
	}
	if len(fieldErrs) > 0 {
		return &HTTPError{Status: http.StatusBadRequest, Message: "The request has invalid values.", Key: "http.invalid_values", Fields: fieldErrs}
	}
	return nil
}

// dropEmpty treats empty form and query values as absent for non-string
// fields, so an empty optional input ("age=") leaves the field unset
// instead of failing to parse.
func dropEmpty(vals []string, t reflect.Type) []string {
	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.String {
		return vals
	}
	out := vals[:0:0]
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (fp *fieldPlan) apply(fv reflect.Value, vals []string) error {
	if !fp.slice {
		return fp.set(fv, vals[0])
	}
	s := reflect.MakeSlice(fv.Type(), len(vals), len(vals))
	for i, raw := range vals {
		if err := fp.set(s.Index(i), raw); err != nil {
			return err
		}
	}
	fv.Set(s)
	return nil
}

// valueKind names the kind of value a form, query, path or header value
// is converted to, for its message when it can't be (binding.<kind>).
func valueKind(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case dateType:
		return "date"
	case timeType:
		return "time"
	case durationType:
		return "duration"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	}
	return "value"
}

var (
	dateType     = reflect.TypeFor[anetos.Date]()
	timeType     = reflect.TypeFor[time.Time]()
	durationType = reflect.TypeFor[time.Duration]()
)

// bodyBuffers holds the buffers JSON bodies are read into.
var bodyBuffers = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// putBodyBuffer returns buf to the pool, unless it grew past 64 KiB.
func putBodyBuffer(buf *bytes.Buffer) {
	if buf.Cap() > 64<<10 {
		return
	}
	buf.Reset()
	bodyBuffers.Put(buf)
}

func decodeJSON(ctx context.Context, body io.Reader, dst any) error {
	buf := bodyBuffers.Get().(*bytes.Buffer)
	defer putBodyBuffer(buf)
	_, err := buf.ReadFrom(body)
	if mbe, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return Errorf(http.StatusRequestEntityTooLarge, "request body is larger than %d bytes", mbe.Limit)
	}
	if err != nil {
		return Error(http.StatusBadRequest, "The request body couldn't be read.").Wrap(err)
	}
	data := buf.Bytes()
	// The usual case without a decoder or a second copy; json.Unmarshal
	// copies what it keeps, so the buffer can be reused. A failure is
	// decoded again below, for its message.
	if len(bytes.TrimSpace(data)) > 0 && json.Unmarshal(data, dst) == nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	err = dec.Decode(dst)
	if errors.Is(err, io.EOF) {
		return nil // empty body
	}
	if err == nil {
		if _, err = dec.Token(); errors.Is(err, io.EOF) {
			return nil
		}
		if err == nil {
			return Error(http.StatusBadRequest, "The request body must contain a single JSON value.")
		}
	}
	// A value of the wrong type for its field, or one its type refuses
	// (a date that isn't one): each such top-level field, by its JSON
	// name. encoding/json reports at most one, without the field for a
	// type's own UnmarshalText or UnmarshalJSON error.
	if fields := jsonFieldErrors(ctx, data, reflect.TypeOf(dst)); len(fields) > 0 {
		return &HTTPError{
			Status:  http.StatusBadRequest,
			Message: "The request has invalid values.",
			Key:     "http.invalid_values",
			Fields:  fields,
			Err:     err,
		}
	}
	if ute, ok := errors.AsType[*json.UnmarshalTypeError](err); ok && ute.Field != "" {
		return &HTTPError{
			Status:  http.StatusBadRequest,
			Message: "The request has invalid values.",
			Key:     "http.invalid_values",
			Fields:  map[string]string{ute.Field: i18n.T(ctx, "binding."+jsonKind(ute.Type))},
			Err:     err,
		}
	}
	return Error(http.StatusBadRequest, "The request body is not valid JSON.").Wrap(err)
}

// jsonFieldErrors decodes each member of data, a JSON object, into a new
// value of its field of t (a pointer to a struct), and returns a message
// per member that fails; nil when data isn't an object.
func jsonFieldErrors(ctx context.Context, data []byte, t reflect.Type) map[string]string {
	var members map[string]json.RawMessage
	if json.Unmarshal(data, &members) != nil {
		return nil
	}
	fields := jsonfield.Of(t.Elem())
	out := map[string]string{}
	for key, raw := range members {
		i := slices.IndexFunc(fields, func(f jsonfield.Field) bool { return f.Name == key })
		if i < 0 { // encoding/json matches names regardless of case, too
			i = slices.IndexFunc(fields, func(f jsonfield.Field) bool { return strings.EqualFold(f.Name, key) })
		}
		if i < 0 {
			continue
		}
		ft := fields[i].Field.Type
		if json.Unmarshal(raw, reflect.New(ft).Interface()) != nil {
			out[key] = i18n.T(ctx, "binding."+jsonKind(ft)) // as the client named it
		}
	}
	return out
}

// jsonKind names the kind of a JSON value a field needs, for its message
// (binding.<kind>).
func jsonKind(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case dateType, timeType: // durations are numbers in JSON
		return valueKind(t)
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "list"
	case reflect.Struct, reflect.Map:
		return "object"
	}
	return "value"
}

// formErrors keys validation errors of a form post by form field name, so
// the form can show each message next to its input (a field's error key
// is otherwise its JSON name).
func (p *bindPlan) formErrors(c *Ctx, err error) error {
	if len(p.formKeys) == 0 || !isForm(c.r) {
		return err
	}
	ve, ok := err.(*validate.Errors) //nolint:errorlint // only an unwrapped *Errors is replaced; wrapped ones are left as they are
	if !ok {
		return err
	}
	renamed := &validate.Errors{}
	for _, k := range ve.Keys() {
		key := k
		if to, ok := p.formKeys[k]; ok {
			key = to
		}
		renamed.Add(key, ve.Get(k))
	}
	return renamed
}
