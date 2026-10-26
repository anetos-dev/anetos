// SPDX-License-Identifier: Apache-2.0

package inertia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"time"

	"anetos.dev/anetos/internal/jsonfield"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
)

// Page is a page of the frontend, a component its props type P renders:
// declare one per page, once, at package level,
//
//	var postsIndex = inertia.NewPage[PostsIndex]("Posts/Index")
//
// and render it in the handler:
//
//	return postsIndex.Render(c, PostsIndex{Posts: posts})
type Page[P any] struct {
	component string
	plan      *plan
}

// NewPage declares the page component (the name Inertia's client
// resolves, such as "Posts/Index") with props P: a struct whose exported
// fields are the props, as encoding/json writes them (names from json
// tags, "omitempty" and "omitzero", embedded structs' fields, the
// shallowest field of a name winning), [Prop] fields included; or
// [Props]. NewPage reads P's fields once, here. It panics on a mistake in
// the program: an empty name, a P that isn't a struct or Props, two
// fields tagged with one name at one depth (encoding/json would leave both
// out), or a prop named "errors", which holds the validation errors.
func NewPage[P any](component string) *Page[P] {
	if component == "" {
		panic("inertia: NewPage needs the page's component name")
	}
	pl, err := planFor(reflect.TypeFor[P]())
	if err != nil {
		panic(fmt.Sprintf("inertia: NewPage(%q): %v", component, err))
	}
	return &Page[P]{component: component, plan: pl}
}

// Component returns the page's component name.
func (p *Page[P]) Component() string { return p.component }

// PropsType returns P, for tools.
func (p *Page[P]) PropsType() reflect.Type { return reflect.TypeFor[P]() }

// AnyPage is a [Page] of any props type, for [WithPages].
type AnyPage interface {
	// Component returns the page's component name.
	Component() string
	// PropsType returns the page's props type.
	PropsType() reflect.Type
}

// PropType reports whether t is a [Prop] type, and the type of its
// value, for tools: string for Prop[string].
func PropType(t reflect.Type) (reflect.Type, bool) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if !t.Implements(propType) {
		return nil, false
	}
	return reflect.Zero(t).Interface().(propValue).valueType(), true
}

// Render responds with the page and props (and the shared props of
// [WithShared]), status 200: the page object for a visit of Inertia's
// client, the root view's HTML for the first one. In development and
// tests, once the app lists pages ([WithPages]), a page it doesn't list
// is an error. Its [Prop] fields are
// resolved as the visit asks; an error of one fails the response, unless
// it is a rescued deferred prop. The Inertia is the context's ([From]).
func (p *Page[P]) Render(c *web.Ctx, props P) error {
	if in, err := From(c); err == nil && in.strict && in.listed != nil && !in.listed[p.component] {
		return fmt.Errorf("inertia: the page %q isn't in inertia.WithPages: add it to the list, so its props get TypeScript types", p.component)
	}
	return render(c, http.StatusOK, p.component, p.plan, reflect.ValueOf(props), nil)
}

// Props are a page's props without a type of their own, for [Render]: the
// frontend gets them untyped.
type Props map[string]any

var propsPlan = &plan{isMap: true}

// Render responds with the page component and props, without a declared
// [Page]: the frontend's TypeScript types don't know them. See
// [Page.Render].
func Render(c *web.Ctx, component string, props Props) error {
	return render(c, http.StatusOK, component, propsPlan, reflect.ValueOf(props), nil)
}

// ---- Props' plans ----

// plan is how a props type's values are read, computed once per type.
type plan struct {
	isMap  bool
	fields []field
}

type field struct {
	name                string
	index               []int
	omitEmpty, omitZero bool
	isProp              bool // an inertia.Prop
}

func planFor(t reflect.Type) (*plan, error) {
	if t.Kind() == reflect.Map && t.Key().Kind() == reflect.String {
		return &plan{isMap: true}, nil
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("props must be a struct or inertia.Props, not %s", t)
	}
	fields, ambiguous := jsonfield.Written(t) // as encoding/json writes them
	if len(ambiguous) > 0 {
		return nil, fmt.Errorf("%s has two fields tagged %q at one depth", t, ambiguous[0])
	}
	pl := &plan{}
	for _, f := range fields {
		if f.Name == "errors" {
			return nil, fmt.Errorf("%s.%s: the prop name errors is Inertia's, for the validation errors", t, f.Field.Name)
		}
		fd := field{name: f.Name, index: f.Index, isProp: f.Field.Type.Implements(propType)}
		_, opts, _ := strings.Cut(f.Field.Tag.Get("json"), ",")
		for o := range strings.SplitSeq(opts, ",") {
			switch o {
			case "omitempty":
				fd.omitEmpty = true
			case "omitzero":
				fd.omitZero = true
			}
		}
		pl.fields = append(pl.fields, fd)
	}
	return pl, nil
}

var propType = reflect.TypeFor[propValue]()

// empty reports whether encoding/json's omitempty leaves v out.
func empty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

// zero reports whether encoding/json's omitzero leaves v out.
func zero(v reflect.Value) bool {
	if z, ok := reflect.TypeAssert[interface{ IsZero() bool }](v); ok {
		return z.IsZero()
	}
	return v.IsZero()
}

// each calls fn with each prop of v, a value of the plan's type, and its
// Prop if it is one.
func (pl *plan) each(v reflect.Value, fn func(name string, val any, p *spec)) {
	if pl.isMap {
		if !v.IsValid() {
			return
		}
		for it := v.MapRange(); it.Next(); {
			val := it.Value().Interface()
			if pv, ok := val.(propValue); ok {
				fn(it.Key().String(), val, specOf(pv))
			} else {
				fn(it.Key().String(), val, nil)
			}
		}
		return
	}
	for _, f := range pl.fields {
		fv, err := v.FieldByIndexErr(f.index)
		if err != nil || f.omitEmpty && empty(fv) || f.omitZero && zero(fv) {
			continue // in a nil embedded pointer, or left out
		}
		val := fv.Interface()
		if f.isProp {
			fn(f.name, val, specOf(val.(propValue)))
		} else {
			fn(f.name, val, nil)
		}
	}
}

// unset is the spec of a Prop that wasn't set.
var unset = &spec{}

func specOf(pv propValue) *spec {
	if s := pv.propSpec(); s != nil && s.get != nil {
		return s
	}
	return unset
}

// ---- The page object ----

// PageObject is the page Inertia's client renders: a response's JSON, or
// the first visit's in the root view. [ReadPage] reads one, for tests.
type PageObject struct {
	Component        string                     `json:"component"`                  // the page component's name
	Props            map[string]json.RawMessage `json:"props"`                      // the props, errors included, as JSON
	URL              string                     `json:"url"`                        // the page's URL (path and query)
	Version          string                     `json:"version"`                    // the asset version
	EncryptHistory   bool                       `json:"encryptHistory,omitempty"`   // encrypt the page's history state
	ClearHistory     bool                       `json:"clearHistory,omitempty"`     // drop the history encryption key
	PreserveFragment bool                       `json:"preserveFragment,omitempty"` // keep the redirecting visit's URL fragment
	MergeProps       []string                   `json:"mergeProps,omitempty"`       // the props (and paths in them) to append to
	PrependProps     []string                   `json:"prependProps,omitempty"`     // the props (and paths in them) to prepend to
	DeepMergeProps   []string                   `json:"deepMergeProps,omitempty"`   // the props to merge at every depth
	MatchPropsOn     []string                   `json:"matchPropsOn,omitempty"`     // "<prop path>.<key field>" to match merged items on
	DeferredProps    map[string][]string        `json:"deferredProps,omitempty"`    // the deferred props left out, by group
	RescuedProps     []string                   `json:"rescuedProps,omitempty"`     // the deferred props that failed and were left out
	SharedProps      []string                   `json:"sharedProps,omitempty"`      // the shared props' names
	OnceProps        map[string]OnceProp        `json:"onceProps,omitempty"`        // the once props, by key
	Flash            map[string]json.RawMessage `json:"flash,omitempty"`            // the flash data
}

// OnceProp is a once prop's entry in [PageObject.OnceProps].
type OnceProp struct {
	Prop      string `json:"prop"`      // the prop's name on this page
	ExpiresAt *int64 `json:"expiresAt"` // when it expires, in Unix milliseconds; nil for never, or when the client holds it (it keeps its own)
}

// page is the page object as written.
type page struct {
	Component        string              `json:"component"`
	Props            map[string]any      `json:"props"`
	URL              string              `json:"url"`
	Version          string              `json:"version"`
	EncryptHistory   bool                `json:"encryptHistory,omitempty"`
	ClearHistory     bool                `json:"clearHistory,omitempty"`
	PreserveFragment bool                `json:"preserveFragment,omitempty"`
	MergeProps       []string            `json:"mergeProps,omitempty"`
	PrependProps     []string            `json:"prependProps,omitempty"`
	DeepMergeProps   []string            `json:"deepMergeProps,omitempty"`
	MatchPropsOn     []string            `json:"matchPropsOn,omitempty"`
	DeferredProps    map[string][]string `json:"deferredProps,omitempty"`
	RescuedProps     []string            `json:"rescuedProps,omitempty"`
	SharedProps      []string            `json:"sharedProps,omitempty"`
	OnceProps        map[string]OnceProp `json:"onceProps,omitempty"`
	Flash            map[string]any      `json:"flash,omitempty"`
}

// visit is what the request asks for.
type visit struct {
	partial                    bool
	only, except, reset, onces map[string]bool
}

func readVisit(r *http.Request, component string) visit {
	v := visit{onces: list(r.Header.Get("X-Inertia-Except-Once-Props"), keys)}
	if isInertia(r) && r.Header.Get("X-Inertia-Partial-Component") == component {
		v.partial = true
		v.only = list(r.Header.Get("X-Inertia-Partial-Data"), topLevel)
		v.except = list(r.Header.Get("X-Inertia-Partial-Except"), topOnly)
		v.reset = list(r.Header.Get("X-Inertia-Reset"), topOnly)
	}
	return v
}

// How list reads a header's entries.
const (
	keys     = iota // as they are (once props' keys)
	topLevel        // "a.b" is the prop a: asking for part of a sends a
	topOnly         // "a.b" is ignored: leaving out part of a still sends a
)

// list reads a comma-separated header.
func list(h string, mode int) map[string]bool {
	m := map[string]bool{}
	for k := range strings.SplitSeq(h, ",") {
		k = strings.TrimSpace(k)
		top, _, nested := strings.Cut(k, ".")
		switch {
		case k == "", nested && mode == topOnly:
			continue
		case mode == topLevel:
			k = top
		}
		m[k] = true
	}
	return m
}

func (v visit) selected(name string) bool {
	return !v.partial || (len(v.only) == 0 || v.only[name]) && !v.except[name]
}

// render responds with the page. errs, when not nil, are the validation
// errors instead of the session's (an error page's).
func render(c *web.Ctx, status int, component string, pl *plan, props reflect.Value, errs map[string]string) error {
	in, err := From(c)
	if err != nil {
		return err
	}
	r := c.Request()
	st := stateFrom(c)
	if st == nil {
		st = newState(c)
	}
	v := readVisit(r, component)
	pg := &page{Component: component, Props: map[string]any{}, URL: r.URL.RequestURI(), Version: in.version}

	resolve := func(name string, val any, s *spec) error {
		if s == nil {
			if v.selected(name) {
				pg.Props[name] = val
			}
			return nil
		}
		if s == unset {
			return nil
		}
		key := name
		if s.onceKey != "" {
			key = s.onceKey
		}
		held := s.once && !s.fresh && !v.partial && v.onces[key] // the client has it
		switch {
		case v.partial:
			if !s.always && !v.selected(name) {
				return nil
			}
		case s.optional:
			return nil
		case s.deferred:
			if held {
				pg.once(key, name, s, time.Time{})
				return nil
			}
			if pg.DeferredProps == nil {
				pg.DeferredProps = map[string][]string{}
			}
			pg.DeferredProps[s.group] = append(pg.DeferredProps[s.group], name)
			return nil
		}
		if held {
			pg.once(key, name, s, time.Time{})
			return nil
		}
		val, err := s.get(c)
		if err != nil {
			if s.deferred && s.rescue {
				c.Logger().Warn("inertia: a deferred prop failed; rescued", "prop", name, "component", component, "error", err)
				pg.RescuedProps = append(pg.RescuedProps, name)
				return nil
			}
			return fmt.Errorf("inertia: %s's prop %q: %w", component, name, err)
		}
		pg.Props[name] = val
		if !v.reset[name] {
			appendAt := s.appendAt
			if s.merge && len(s.appendAt)+len(s.prependAt) == 0 {
				appendAt = []string{""}
			}
			if s.deep { // the client deep-merges what it would append: only that
				pg.DeepMergeProps = append(pg.DeepMergeProps, name)
			} else {
				for _, p := range appendAt {
					pg.MergeProps = append(pg.MergeProps, path(name, p))
				}
				for _, p := range s.prependAt {
					pg.PrependProps = append(pg.PrependProps, path(name, p))
				}
			}
			for _, p := range s.matchOn {
				pg.MatchPropsOn = append(pg.MatchPropsOn, path(name, p))
			}
		}
		if s.once {
			pg.once(key, name, s, time.Now())
		}
		return nil
	}

	// The shared props, then the page's, which win over a shared one of
	// the same name.
	type entry struct {
		name   string
		val    any
		spec   *spec
		shared bool
	}
	var entries []entry
	at := map[string]int{}
	add := func(shared bool) func(string, any, *spec) {
		return func(name string, val any, s *spec) {
			i, ok := at[name]
			switch {
			case ok && s == unset: // an unset Prop leaves the shared one
			case ok:
				entries[i] = entry{name, val, s, shared}
			default:
				at[name] = len(entries)
				entries = append(entries, entry{name, val, s, shared})
			}
		}
	}
	for _, sh := range in.shared {
		sv, err := sh.get(c)
		if err != nil {
			return fmt.Errorf("inertia: shared props: %w", err)
		}
		sh.plan.each(sv, add(true))
	}
	pl.each(props, add(false))
	for _, e := range entries {
		if err := resolve(e.name, e.val, e.spec); err != nil {
			return err
		}
		if e.shared {
			pg.SharedProps = append(pg.SharedProps, e.name)
		}
	}

	pg.Props["errors"] = validationErrors(c, st, errs)
	pg.EncryptHistory = in.encrypt
	if st.encrypt != nil {
		pg.EncryptHistory = *st.encrypt
	}
	pg.ClearHistory = st.clear || st.prevClear
	pg.PreserveFragment = st.fragment || st.prevFragment
	if len(st.prevFlash)+len(st.flash) > 0 {
		pg.Flash = map[string]any{}
		for k, v := range st.prevFlash {
			pg.Flash[k] = v
		}
		maps.Copy(pg.Flash, st.flash)
	}
	// What this request left for the next page is on this one.
	if s := session.From(c); s != nil {
		if st.flash != nil {
			s.Delete(flashKey)
		}
		if st.clear {
			s.Delete(clearKey)
		}
		if st.fragment {
			s.Delete(fragmentKey)
		}
	}

	addVary(c.Writer().Header(), "X-Inertia")
	if isInertia(r) {
		b, err := json.Marshal(pg)
		if err != nil {
			return fmt.Errorf("inertia: %s's props: %w", component, err)
		}
		c.SetHeader("X-Inertia", "true")
		return c.Blob(status, "application/json; charset=utf-8", append(b, '\n'))
	}
	return in.renderRoot(c, status, pg)
}

// once lists a once prop; now is when it was sent (zero when it wasn't).
func (pg *page) once(key, name string, s *spec, now time.Time) {
	if pg.OnceProps == nil {
		pg.OnceProps = map[string]OnceProp{}
	}
	op := OnceProp{Prop: name}
	switch {
	case !s.expiresAt.IsZero():
		ms := s.expiresAt.UnixMilli()
		op.ExpiresAt = &ms
	case s.expiresIn > 0 && !now.IsZero():
		ms := now.Add(s.expiresIn).UnixMilli()
		op.ExpiresAt = &ms
	}
	pg.OnceProps[key] = op
}

func path(name, p string) string {
	if p == "" {
		return name
	}
	return name + "." + p
}

// validationErrors is the errors prop: the field errors the previous
// request flashed (the first message of each field), or errs; under the
// error bag the form named, if any.
func validationErrors(ctx context.Context, st *state, errs map[string]string) any {
	out := map[string]any{}
	if errs != nil {
		for k, v := range errs {
			out[k] = v
		}
	} else if s := session.From(ctx); s != nil {
		for _, fe := range s.Errors() {
			if _, ok := out[fe.Field]; !ok {
				out[fe.Field] = fe.Message
			}
		}
	}
	if st.prevBag != "" && len(out) > 0 {
		return map[string]any{st.prevBag: out}
	}
	return out
}

// ---- The first visit ----

type rootKey struct{}

// rendering is the page the root view holds.
type rendering struct {
	component string
	json      []byte
}

func (in *Inertia) renderRoot(c *web.Ctx, status int, pg *page) error {
	b, err := json.Marshal(pg)
	if err != nil {
		return fmt.Errorf("inertia: %s's props: %w", pg.Component, err)
	}
	// In a script element: json escapes <, > and &; Inertia's client
	// expects / escaped too.
	b = bytes.ReplaceAll(b, []byte("/"), []byte(`\/`))
	var buf bytes.Buffer
	if err := in.root.Render(context.WithValue(c, rootKey{}, &rendering{component: pg.Component, json: b}), &buf); err != nil {
		return err
	}
	return c.Blob(status, "text/html; charset=utf-8", buf.Bytes())
}

// App writes the page for Inertia's client, in the root view's body:
//
//	<body>
//		@inertia.App()
//	</body>
//
// a <script data-page="app" type="application/json"> element holding the
// page object, and the <div id="app"> the client renders it in. It fails
// outside a page's first visit.
func App() view.Component {
	return view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		rd, ok := ctx.Value(rootKey{}).(*rendering)
		if !ok {
			return errors.New("inertia: App renders in the root view of a page's response")
		}
		_, err := fmt.Fprintf(w, "<script data-page=\"app\" type=\"application/json\">%s</script>\n<div id=\"app\"></div>\n", rd.json)
		return err
	})
}

// Component returns the component of the page the root view renders, ""
// elsewhere: the root view can preload its module
// (@vite.Tags("src/app.ts", "src/pages/" + inertia.Component(ctx) + ".tsx")).
func Component(ctx context.Context) string {
	if rd, ok := ctx.Value(rootKey{}).(*rendering); ok {
		return rd.component
	}
	return ""
}

// ReadPage reads the page object of a response's body: the JSON of a
// visit of Inertia's client, or the first visit's HTML holding it. Tests
// use it (anetostest's AssertInertia).
func ReadPage(body []byte) (*PageObject, error) {
	b := bytes.TrimSpace(body)
	if !bytes.HasPrefix(b, []byte("{")) {
		const open = `<script data-page="app" type="application/json">`
		_, rest, ok := bytes.Cut(body, []byte(open))
		if !ok {
			return nil, errors.New("inertia: no page object in the response")
		}
		b, _, ok = bytes.Cut(rest, []byte("</script>"))
		if !ok {
			return nil, errors.New("inertia: the page object's script isn't closed")
		}
	}
	var p PageObject
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("inertia: read the page object: %w", err)
	}
	return &p, nil
}
