// SPDX-License-Identifier: Apache-2.0

// Package openapi describes an app's API as an OpenAPI 3.1 document,
// generated from its routes: each typed handler's ([web.H]) input
// (path, query and header parameters, the JSON body, with what their
// validate rules say), its result and the route's status
// ([web.Route.Status]), what its middleware asks for and may answer
// ([web.Documented]: package auth's Require is a bearer token), and
// errors as RFC 9457 problem details.
//
// [ForApp] adds the openapi command, which writes the document to a
// file (openapi.json) to commit beside the code, and serves it; [Check],
// in a test, fails when the file is out of date:
//
//	// routes/api.go
//	var OpenAPI = openapi.Config{Title: "Shop", Version: "1.0.0", Prefix: "/api/v1", Path: "/api/v1/openapi.json"}
//
//	// main.go's setup, after the routes
//	if err := openapi.ForApp(app, srv, routes.OpenAPI); err != nil {
//		return nil, err
//	}
//
// The document is built from types once, when asked for: nothing runs
// per request.
package openapi

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/internal/convert"
	"anetos.dev/anetos/internal/jsonfield"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"
)

// Config says what the document describes and where it goes.
type Config struct {
	// Title is the API's name (info.title). Required.
	Title string
	// Version is the document's version (info.version), "1.0.0" if empty.
	// It's the API's, not Anetos's or the app's build.
	Version string
	// Description introduces the API (info.description), in Markdown.
	Description string
	// Prefix limits the document to the routes whose path is Prefix or
	// below it ("/api/v1"); "" describes every typed route.
	Prefix string
	// File is where the openapi command writes the document and [Check]
	// reads it, relative to the working directory: "openapi.json" if
	// empty.
	File string
	// Path, if not empty, is where [ForApp] serves the document
	// ("/api/v1/openapi.json"), for clients and tools.
	Path string
	// Servers are the API's base URLs (servers), such as
	// "https://api.example.com"; none means "/": the document's own
	// host.
	Servers []string
	// SecuritySchemes are the security schemes that middleware may name
	// (web.MiddlewareDoc.Security) besides "bearer", which is built in.
	SecuritySchemes map[string]SecurityScheme
}

// SecurityScheme is an OpenAPI security scheme.
type SecurityScheme struct {
	// Type is "http" or "apiKey".
	Type string `json:"type"`
	// Scheme is an http scheme's name: "bearer", "basic".
	Scheme string `json:"scheme,omitempty"`
	// BearerFormat hints at a bearer token's format, for documentation.
	BearerFormat string `json:"bearerFormat,omitempty"`
	// In is where an apiKey goes: "header", "query" or "cookie".
	In string `json:"in,omitempty"`
	// Name is an apiKey's header, query parameter or cookie name.
	Name string `json:"name,omitempty"`
	// Description says how to get the credentials, in Markdown.
	Description string `json:"description,omitempty"`
}

var bearer = SecurityScheme{
	Type: "http", Scheme: "bearer",
	Description: "An API token, sent as \"Authorization: Bearer <token>\".",
}

func (cfg Config) withDefaults() Config {
	cfg.Version = cmp.Or(cfg.Version, "1.0.0")
	cfg.File = cmp.Or(cfg.File, "openapi.json")
	return cfg
}

// Spec returns the OpenAPI 3.1 document of r's routes, as indented
// JSON, and warnings about what it couldn't describe: routes that
// aren't typed handlers (left out), results that are a web.Responder
// (their bodies unknown), types with their own MarshalJSON. The same
// routes and Config always give the same bytes.
func Spec(r *web.Router, cfg Config) ([]byte, []string, error) {
	spec, warnings, _, err := build(r, cfg)
	return spec, warnings, err
}

// build is Spec, with the routes under the prefix it left out.
func build(r *web.Router, cfg Config) ([]byte, []string, []string, error) {
	if cfg.Title == "" {
		return nil, nil, nil, errors.New("openapi: Config.Title is empty")
	}
	cfg = cfg.withDefaults()
	g := &generator{cfg: cfg, schemas: newSchemas(), schemes: map[string]bool{}, ids: map[string]bool{}, responses: map[string]bool{}}
	paths := map[string]*object{}
	shapes := map[string]string{} // paths with their wildcards unnamed
	var order []string
	for _, rt := range r.Routes() {
		if !g.selected(rt) {
			continue
		}
		op, err := g.operation(rt)
		if err != nil {
			return nil, nil, nil, err
		}
		p := pathTemplate(rt.Pattern)
		shape := wildcard.ReplaceAllString(rt.Pattern, "{}")
		if other, ok := shapes[shape]; ok && other != p {
			return nil, nil, nil, fmt.Errorf("openapi: %s and %s are one path to OpenAPI: name their wildcards alike", other, p)
		}
		shapes[shape] = p
		if paths[p] == nil {
			paths[p] = &object{}
			order = append(order, p)
		}
		method := strings.ToLower(rt.Method)
		if slices.Contains(paths[p].keys, method) {
			return nil, nil, nil, fmt.Errorf("openapi: two routes are %s %s (for different hosts?): describe one, with Config.Prefix, or name their paths apart", rt.Method, p)
		}
		paths[p].add(method, op)
	}
	nameComponents(g.schemas.order)
	if len(g.schemes) > 0 {
		// Where some operations need credentials, say the others don't.
		for _, op := range g.public {
			op.add("security", []any{})
		}
	}

	var doc object
	doc.add("openapi", "3.1.0")
	var info object
	info.add("title", cfg.Title)
	info.add("version", cfg.Version)
	if cfg.Description != "" {
		info.add("description", cfg.Description)
	}
	doc.add("info", info)
	urls := cfg.Servers
	if len(urls) == 0 {
		urls = []string{"/"}
	}
	var servers []any
	for _, s := range urls {
		servers = append(servers, map[string]string{"url": s})
	}
	doc.add("servers", servers) // "/", OpenAPI's default, said, as linters want
	slices.Sort(order)
	var po object
	for _, p := range order {
		po.add(p, sortedMethods(paths[p]))
	}
	doc.add("paths", po)

	var comps object
	var schemaObj object
	sorted := slices.Clone(g.schemas.order)
	slices.SortFunc(sorted, func(a, b *component) int { return strings.Compare(a.name, b.name) })
	for _, c := range sorted {
		schemaObj.add(c.name, c.schema)
	}
	if g.problem || len(g.responses) > 0 {
		schemaObj = withSchema(schemaObj, "Problem", problemSchema)
	}
	comps.add("schemas", schemaObj)
	if len(g.responses) > 0 {
		var ro object
		for _, name := range slices.Sorted(maps.Keys(g.responses)) {
			ro.add(name, g.problemResponse(commonResponses[name]))
		}
		comps.add("responses", ro)
	}
	if len(g.schemes) > 0 {
		var so object
		for _, name := range slices.Sorted(maps.Keys(g.schemes)) {
			so.add(name, g.scheme(name))
		}
		comps.add("securitySchemes", so)
	}
	doc.add("components", comps)

	body, err := doc.marshal()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("openapi: %w", err)
	}
	var out bytes.Buffer
	if err := jsonIndent(&out, body); err != nil {
		return nil, nil, nil, fmt.Errorf("openapi: %w", err)
	}
	return out.Bytes(), slices.Sorted(maps.Keys(g.schemas.warnings)), g.leftOut, nil
}

// withSchema adds a schema to the components' schemas, in its place
// by name.
func withSchema(schemas object, name string, sc *schema) object {
	var out object
	added := false
	for i, k := range schemas.keys {
		if !added && k > name {
			out.add(name, sc)
			added = true
		}
		out.add(k, schemas.vals[i])
	}
	if !added {
		out.add(name, sc)
	}
	return out
}

// sortedMethods orders a path's operations as OpenAPI lists them.
func sortedMethods(o *object) object {
	rank := map[string]int{"get": 0, "put": 1, "post": 2, "delete": 3, "options": 4, "head": 5, "patch": 6, "trace": 7}
	idx := make([]int, len(o.keys))
	for i := range idx {
		idx[i] = i
	}
	slices.SortFunc(idx, func(a, b int) int { return cmp.Compare(rank[o.keys[a]], rank[o.keys[b]]) })
	var out object
	for _, i := range idx {
		out.add(o.keys[i], o.vals[i])
	}
	return out
}

type generator struct {
	cfg       Config
	schemas   *schemas
	schemes   map[string]bool // security schemes used
	ids       map[string]bool // operationIds used
	problem   bool            // the Problem schema is used
	public    []*object       // operations without security
	leftOut   []string        // routes under the prefix it can't describe
	responses map[string]bool // components/responses used
}

// omit records a route under Config.Prefix the document leaves out.
func (g *generator) omit(route string) {
	if g.cfg.Prefix != "" {
		g.leftOut = append(g.leftOut, route)
	}
}

// selected reports whether rt is described: under the prefix, not the
// document's own route. A route that can't be described is warned about.
func (g *generator) selected(rt web.RouteInfo) bool {
	if prefix := strings.TrimSuffix(g.cfg.Prefix, "/"); prefix != "" && rt.Pattern != prefix && !strings.HasPrefix(rt.Pattern, prefix+"/") {
		return false
	}
	if g.cfg.Path != "" && rt.Pattern == g.cfg.Path {
		return false
	}
	switch {
	case rt.Method == "":
		g.schemas.warn("%s: a route for every method isn't described", rt.Pattern)
		g.omit("any method " + rt.Pattern)
		return false
	case rt.Input == nil:
		g.schemas.warn("%s %s: not a typed handler (web.H, registered as web.H returns it): left out", rt.Method, rt.Pattern)
		g.omit(rt.Method + " " + rt.Pattern)
		return false
	}
	return true
}

var wildcard = regexp.MustCompile(`\{([^}]*?)(\.\.\.)?\}`)

// pathTemplate is a route's pattern as an OpenAPI path: "{path...}" is
// "{path}".
func pathTemplate(p string) string {
	return wildcard.ReplaceAllString(p, "{$1}")
}

var (
	responderType = reflect.TypeFor[web.Responder]()
	emptyType     = reflect.TypeFor[web.Empty]()
	validatorType = reflect.TypeFor[web.Validator]()
)

// param is a parameter of a typed handler's input.
type param struct {
	in, name string
	field    reflect.StructField
}

// params returns the path, query and header fields of input type t, as
// web.H binds them (embedded structs without binding tags included).
func params(t reflect.Type) []param {
	var out []param
	for sf := range t.Fields() {
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct && !hasBindTag(sf) {
			out = append(out, params(sf.Type)...)
			continue
		}
		if !sf.IsExported() {
			continue
		}
		for _, in := range []string{"path", "query", "header"} {
			if name, ok := sf.Tag.Lookup(in); ok && name != "-" && name != "" {
				out = append(out, param{in, name, sf})
			}
		}
	}
	return out
}

func hasBindTag(sf reflect.StructField) bool {
	for _, k := range []string{"path", "query", "header", "form", "json"} {
		if _, ok := sf.Tag.Lookup(k); ok {
			return true
		}
	}
	return false
}

// operation describes rt, a typed handler's route.
func (g *generator) operation(rt web.RouteInfo) (*object, error) {
	op := &object{}
	if tag := handlerTag(rt.Handler); tag != "" {
		op.add("tags", []string{tag})
	}
	op.add("operationId", g.operationID(rt))

	// Parameters: the path's wildcards, then the input's query and
	// header fields.
	in := rt.Input
	ps := params(in)
	var parameters []any
	for _, m := range wildcard.FindAllStringSubmatch(rt.Pattern, -1) {
		name := m[1]
		var p object
		p.add("name", name)
		p.add("in", "path")
		p.add("required", true)
		i := slices.IndexFunc(ps, func(p param) bool { return p.in == "path" && p.name == name })
		if i < 0 {
			p.add("schema", &schema{typ: "string"})
		} else {
			g.describeParam(&p, ps[i])
		}
		parameters = append(parameters, p)
	}
	for _, pm := range ps {
		if pm.in == "path" {
			continue // described above if the pattern has it; else never set
		}
		var p object
		p.add("name", pm.name)
		p.add("in", pm.in)
		if jsonfield.Has(pm.field.Tag.Get("validate"), "required") {
			p.add("required", true)
		}
		g.describeParam(&p, pm)
		parameters = append(parameters, p)
	}
	if len(parameters) > 0 {
		op.add("parameters", parameters)
	}

	// The body: the input's other fields, as JSON, or a multipart form
	// when it has files.
	hasBody := false
	if body := g.requestBody(rt.Method, in); body != nil {
		op.add("requestBody", body)
		hasBody = true
	}

	// Responses: the result with the route's status, the errors of
	// binding and validation, the middleware's, then any other error.
	var res object
	g.success(&res, rt)
	errs := map[int][]string{}
	common := map[int]string{}
	if len(ps) > 0 || hasBody || wildcard.MatchString(rt.Pattern) {
		common[http.StatusBadRequest] = invalidValues
	}
	if rules, err := validate.Compile(in); err == nil && !rules.Empty() || reflect.PointerTo(in).Implements(validatorType) {
		common[http.StatusUnprocessableEntity] = invalidInput
	}
	security := map[string][]string{}
	var schemes []string
	for _, d := range rt.Middleware {
		for status, why := range d.Responses {
			if !slices.Contains(errs[status], why) {
				errs[status] = append(errs[status], why)
			}
		}
		if d.Security != "" {
			if _, ok := security[d.Security]; !ok {
				schemes = append(schemes, d.Security)
				security[d.Security] = []string{}
			}
			for _, s := range d.Scopes {
				if !slices.Contains(security[d.Security], s) {
					security[d.Security] = append(security[d.Security], s)
				}
			}
		}
	}
	statuses := map[int]bool{}
	for s := range errs {
		statuses[s] = true
	}
	for s := range common {
		statuses[s] = true
	}
	for _, status := range slices.Sorted(maps.Keys(statuses)) {
		switch name, ok := common[status]; {
		case ok && len(errs[status]) == 0:
			g.responses[name] = true
			res.add(strconv.Itoa(status), responseRef(name))
		case ok:
			// The middleware's reasons for the status, and binding's or
			// validation's.
			res.add(strconv.Itoa(status), g.problemResponse(strings.Join(append([]string{commonResponses[name]}, errs[status]...), " ")))
		default:
			res.add(strconv.Itoa(status), g.problemResponse(strings.Join(errs[status], " ")))
		}
	}
	g.responses[errorResponse] = true
	res.add("default", responseRef(errorResponse))
	op.add("responses", res)

	if len(schemes) > 0 {
		var req object
		for _, s := range schemes {
			if s != "bearer" && g.cfg.SecuritySchemes[s].Type == "" {
				return nil, fmt.Errorf("openapi: %s %s: the middleware asks for security scheme %q, which Config.SecuritySchemes doesn't have", rt.Method, rt.Pattern, s)
			}
			g.schemes[s] = true
			req.add(s, security[s])
		}
		op.add("security", []any{req})
	} else {
		g.public = append(g.public, op)
	}
	return op, nil
}

// describeParam adds a parameter's schema (and style, for lists).
func (g *generator) describeParam(p *object, pm param) {
	tag := pm.field.Tag.Get("validate")
	sc := g.schemas.of(pm.field.Type, tag, jsonfield.Has(tag, "required"), true)
	for _, s := range append([]*schema{sc}, sc.anyOf...) {
		s.nullable = false // a parameter is absent or has a value
		if len(s.enum) > 0 && s.enum[len(s.enum)-1] == nil {
			s.enum = s.enum[:len(s.enum)-1]
		}
	}
	if isDuration(pm.field.Type) {
		// From text, a duration is time.ParseDuration's: "30s", "5m".
		sc = &schema{typ: "string", description: `A duration: a number and a unit (ns, us, ms, s, m, h), such as "30s" or "1h30m".`}
	}
	if d := pm.field.Tag.Get("description"); d != "" {
		p.add("description", d)
	}
	p.add("schema", sc)
}

var durationType = reflect.TypeFor[time.Duration]()

func isDuration(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t == durationType
}

// requestBody describes the input's body, or returns nil without one:
// web.H reads none for GET and HEAD.
func (g *generator) requestBody(method string, in reflect.Type) any {
	if method == http.MethodGet || method == http.MethodHead {
		return nil
	}
	forms := formFields(in)
	hasFiles := slices.ContainsFunc(forms, func(f param) bool { return isFile(f.field.Type) })
	hasBody, required := false, false
	for _, f := range jsonfield.Of(in) {
		if !notInBody(f.Field) {
			hasBody = true
			required = required || jsonfield.Has(f.Field.Tag.Get("validate"), "required")
		}
	}
	if !hasBody && !hasFiles {
		return nil
	}
	var content object
	if hasFiles {
		// A multipart form: the fields web.H binds from forms, by their
		// form names, and the files.
		sc := &schema{typ: "object", properties: []property{}}
		required = false
		for _, f := range forms {
			tag := f.field.Tag.Get("validate")
			req := jsonfield.Has(tag, "required")
			ps := g.schemas.of(f.field.Type, tag, req, true)
			for _, s := range append([]*schema{ps}, ps.anyOf...) {
				s.nullable = false // a form field is there or not: never null
			}
			sc.properties = append(sc.properties, property{f.name, ps})
			if req {
				sc.required = append(sc.required, f.name)
				required = true
			}
		}
		var mt object
		mt.add("schema", sc)
		content.add("multipart/form-data", mt)
	} else {
		var mt object
		mt.add("schema", g.schemas.of(in, "", false, true))
		content.add("application/json", mt)
	}
	var body object
	if required {
		body.add("required", true)
	}
	body.add("content", content)
	return body
}

// formFields are the fields web.H binds from a form, by their names: a
// form tag's, else (for a field without path, query or header tags) the
// JSON name of a field a form value converts to.
func formFields(t reflect.Type) []param {
	var out []param
	for sf := range t.Fields() {
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct && !hasBindTag(sf) {
			out = append(out, formFields(sf.Type)...)
			continue
		}
		if !sf.IsExported() {
			continue
		}
		if name, ok := sf.Tag.Lookup("form"); ok {
			if name != "" && name != "-" {
				out = append(out, param{"form", name, sf})
			}
			continue
		}
		if hasAnyTag(sf, "path", "query", "header") {
			continue
		}
		tag, ok := sf.Tag.Lookup("json")
		name, _, _ := strings.Cut(tag, ",")
		if !ok || name == "-" || isFile(sf.Type) || !formBindable(sf.Type) {
			continue
		}
		out = append(out, param{"form", cmp.Or(name, sf.Name), sf})
	}
	return out
}

func hasAnyTag(sf reflect.StructField, keys ...string) bool {
	for _, k := range keys {
		if _, ok := sf.Tag.Lookup(k); ok {
			return true
		}
	}
	return false
}

// formBindable reports whether a form value converts to t, as web.H's
// binding does.
func formBindable(t reflect.Type) bool {
	if t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8 {
		t = t.Elem()
	}
	_, err := convert.For(t)
	return err == nil
}

// success adds the result's response: with the route's status, JSON,
// none for web.Empty and 204 and 205; unknown for a web.Responder.
func (g *generator) success(res *object, rt web.RouteInfo) {
	out := rt.Output
	if out.Implements(responderType) { // as web.H's result is: a value of Out
		g.schemas.warn("%s %s: its result is a web.Responder, whose status and body are chosen as it runs: described as any 2XX", rt.Method, rt.Pattern)
		var r object
		r.add("description", "The handler's response (a web.Responder).")
		res.add("2XX", r)
		return
	}
	if out.Kind() == reflect.Interface {
		g.schemas.warn("%s %s: its result is an interface (%s): described as any value; a nil one answers 204", rt.Method, rt.Pattern, out)
	}
	empty := out == emptyType
	status := rt.Status
	if status == 0 {
		status = http.StatusOK
		if empty {
			status = http.StatusNoContent
		}
	}
	var r object
	r.add("description", http.StatusText(status))
	if !empty && status != http.StatusNoContent && status != http.StatusResetContent {
		var mt object
		mt.add("schema", g.schemas.of(out, "", false, false))
		var content object
		content.add("application/json", mt)
		r.add("content", content)
	}
	res.add(strconv.Itoa(status), r)
}

// problemResponse is an error response: problem details.
func (g *generator) problemResponse(description string) object {
	g.problem = true
	var mt object
	mt.add("schema", map[string]string{"$ref": "#/components/schemas/Problem"})
	var content object
	content.add("application/problem+json", mt)
	var r object
	r.add("description", description)
	r.add("content", content)
	return r
}

// problemSchema is web's problem details.
var problemSchema = &schema{typ: "object", description: "Problem details (RFC 9457).", properties: []property{
	{"type", &schema{typ: "string", description: `"about:blank": the status says what happened.`}},
	{"title", &schema{typ: "string", description: "The status's title, in the request's language."}},
	{"status", &schema{typ: "integer", description: "The HTTP status."}},
	{"detail", &schema{typ: "string", description: "What went wrong, for people."}},
	{"errors", &schema{typ: "object", additional: &schema{typ: "string"}, description: "A message per field of the request that has a problem."}},
	{"request_id", &schema{typ: "string", description: "The request's ID, as in the logs."}},
}, required: []string{"type", "title", "status"}}

// The error responses every operation may have, in components/responses.
const (
	invalidValues = "InvalidValues" // 400: binding
	invalidInput  = "InvalidInput"  // 422: validation
	errorResponse = "Error"         // default
)

var commonResponses = map[string]string{
	invalidValues: "The request's values can't be read: problem details with an error per field.",
	invalidInput:  "The input isn't valid: problem details with an error per field.",
	errorResponse: "An error: problem details.",
}

func responseRef(name string) map[string]string {
	return map[string]string{"$ref": "#/components/responses/" + name}
}

// scheme returns a security scheme by its name.
func (g *generator) scheme(name string) SecurityScheme {
	if s, ok := g.cfg.SecuritySchemes[name]; ok {
		return s
	}
	return bearer
}

// operationID is the route's name, else its handler's (a method's
// "Products.Index", a function's "welcome"), else its method and path;
// unique.
func (g *generator) operationID(rt web.RouteInfo) string {
	id := rt.Name
	if id == "" {
		if tag := handlerTag(rt.Handler); tag != "" {
			id = strings.TrimPrefix(rt.Handler, rt.Handler[:strings.IndexByte(rt.Handler, '.')+1]) // "Products.Index"
		} else if strings.Count(rt.Handler, ".") == 1 {
			// A function: "welcome", without its package's name, which is
			// "main" with go run and the module's under go test.
			id = rt.Handler[strings.IndexByte(rt.Handler, '.')+1:]
		}
	}
	if id == "" {
		id = strings.ToLower(rt.Method) + strings.NewReplacer("/", "_", "{", "", "}", "", ".", "").Replace(rt.Pattern)
	}
	unique := id
	for i := 2; g.ids[unique]; i++ {
		unique = id + strconv.Itoa(i)
	}
	g.ids[unique] = true
	return unique
}

// handlerTag is the type of a method's receiver ("Products" for
// "handlers.Products.Index"), which groups operations; "" for functions.
func handlerTag(handler string) string {
	parts := strings.Split(handler, ".")
	if len(parts) != 3 || strings.HasPrefix(parts[2], "func") {
		return ""
	}
	return parts[1]
}

// jsonIndent indents body by two spaces, with a final newline.
func jsonIndent(out *bytes.Buffer, body []byte) error {
	if err := json.Indent(out, body, "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	return nil
}

// Check returns an error unless cfg.File holds r's document as [Spec]
// writes it, and the document describes every route at or below
// cfg.Prefix (when set): in a test, it catches a route or a type changed
// without the document, and a route under the prefix that isn't a typed
// handler. The error says how to fix it.
func Check(r *web.Router, cfg Config) error {
	spec, _, leftOut, err := build(r, cfg)
	if err != nil {
		return err
	}
	if len(leftOut) > 0 {
		return fmt.Errorf("openapi: the document can't describe %s, under %s: route it with web.H (as web.H returns it), or outside the prefix",
			strings.Join(leftOut, ", "), cfg.Prefix)
	}
	file := cfg.withDefaults().File
	have, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("openapi: %s doesn't exist: write it with `go run . openapi`", file)
	}
	if err != nil {
		return fmt.Errorf("openapi: %w", err)
	}
	have = bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n")) // a checkout with Windows line ends
	if !bytes.Equal(have, spec) {
		return fmt.Errorf("openapi: %s is out of date (line %d differs): update it with `go run . openapi`", file, firstDiff(have, spec))
	}
	return nil
}

// firstDiff is the number of the first line where a and b differ.
func firstDiff(a, b []byte) int {
	al, bl := bytes.Split(a, []byte("\n")), bytes.Split(b, []byte("\n"))
	for i := range min(len(al), len(bl)) {
		if !bytes.Equal(al[i], bl[i]) {
			return i + 1
		}
	}
	return min(len(al), len(bl)) + 1
}

// ForApp adds the openapi command to app, which writes the document of
// srv's routes to cfg.File (or, with --check, fails if the file is out
// of date), and serves the document at cfg.Path if it's set. Call it
// after adding the routes. The command builds no app: it needs no
// database.
func ForApp(app *anetos.App, srv *web.Server, cfg Config) error {
	if cfg.Title == "" {
		return errors.New("openapi: Config.Title is empty")
	}
	cfg = cfg.withDefaults()
	if cfg.Path != "" {
		var once sync.Once
		var spec []byte
		var err error
		srv.Router().Get(cfg.Path, func(c *web.Ctx) error {
			once.Do(func() { spec, _, err = Spec(srv.Router(), cfg) })
			if err != nil {
				return err
			}
			return c.Blob(http.StatusOK, "application/json", spec)
		})
	}
	return app.AddCommand(cmd.Command{
		Name:        "openapi",
		Usage:       "[--check] [--out=FILE]",
		Description: "Write the API's OpenAPI document (" + cfg.File + "), or check it's up to date",
		ManagesApp:  true, // the routes are all it needs
		Run: func(ctx context.Context, args *cmd.Args) error {
			return run(ctx, args, srv.Router(), cfg)
		},
	})
}

func run(_ context.Context, args *cmd.Args, r *web.Router, cfg Config) error {
	fs := flag.NewFlagSet("openapi", flag.ContinueOnError)
	check := fs.Bool("check", false, "fail if the file isn't the document of the routes, instead of writing it")
	out := fs.String("out", cfg.File, "the file to write (or check); - for the standard output")
	if err := args.Parse(fs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return cmd.Usagef("unexpected argument %q", fs.Arg(0))
	}
	cfg.File = *out
	spec, warnings, err := Spec(r, cfg)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintln(args.Stderr, "warning:", w)
	}
	switch {
	case *check:
		if cfg.File == "-" {
			return cmd.Usagef("--check needs a file")
		}
		if err := Check(r, cfg); err != nil {
			return err
		}
		fmt.Fprintf(args.Stdout, "%s is up to date\n", cfg.File)
		return nil
	case cfg.File == "-":
		_, err := args.Stdout.Write(spec)
		return err
	}
	if err := os.WriteFile(cfg.File, spec, 0o644); err != nil { //nolint:gosec // a document to commit, readable like the code
		return fmt.Errorf("openapi: %w", err)
	}
	fmt.Fprintf(args.Stdout, "wrote %s\n", cfg.File)
	return nil
}
