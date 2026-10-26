// SPDX-License-Identifier: Apache-2.0

package typescript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"anetos.dev/anetos/internal/jsonschema"
)

// tsType is a schema as a TypeScript type.
func tsType(sc *jsonschema.Schema) string {
	t := baseType(sc)
	if sc.Nullable && t != "unknown" && !strings.HasSuffix(t, " | null") {
		t += " | null"
	}
	return t
}

func baseType(sc *jsonschema.Schema) string {
	switch {
	case sc.Ref != nil:
		return sc.Ref.Name
	case sc.Any:
		return "unknown"
	case len(sc.AnyOf) > 0:
		var parts []string
		for _, s := range sc.AnyOf {
			t := tsType(s)
			if !slices.Contains(parts, t) {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, " | ")
	}
	if len(sc.Enum) > 0 {
		var parts []string
		for _, v := range sc.Enum {
			b, _ := json.Marshal(v)
			if !slices.Contains(parts, string(b)) {
				parts = append(parts, string(b))
			}
		}
		return strings.Join(parts, " | ")
	}
	switch sc.Type {
	case "string":
		if sc.Pattern == `^\s*$` && sc.MaxLength == nil && sc.Format == "" {
			return `""` // the blank value validate's rules let through
		}
		if sc.Format == "binary" {
			return "Blob"
		}
		return "string"
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		if sc.MaxItems != nil && *sc.MaxItems == 0 {
			return "[]"
		}
		item := tsType(sc.Items)
		if strings.ContainsAny(item, " |&") {
			return "Array<" + item + ">"
		}
		return item + "[]"
	case "object":
		if sc.MaxProps != nil && *sc.MaxProps == 0 {
			return "Record<string, never>"
		}
		if sc.Additional != nil {
			return "Record<string, " + tsType(sc.Additional) + ">"
		}
		return objectType(sc, "")
	}
	return "unknown"
}

// objectType is an object schema's literal type, its lines indented by
// indent.
func objectType(sc *jsonschema.Schema, indent string) string {
	if len(sc.Properties) == 0 {
		return "Record<string, never>"
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, p := range sc.Properties {
		writeField(&b, indent+"  ", p.Name, p.Schema, slices.Contains(sc.Required, p.Name))
	}
	b.WriteString(indent + "}")
	return b.String()
}

var identifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func propName(name string) string {
	if identifier.MatchString(name) {
		return name
	}
	b, _ := json.Marshal(name)
	return string(b)
}

// writeField writes an interface's field, with its description as a doc
// comment.
func writeField(b *strings.Builder, indent, name string, sc *jsonschema.Schema, required bool) {
	if d := description(sc); d != "" {
		fmt.Fprintf(b, "%s/** %s */\n", indent, d)
	}
	opt := "?"
	if required {
		opt = ""
	}
	t := tsType(sc)
	if sc.Ref == nil && sc.Type == "object" && sc.Additional == nil && len(sc.AnyOf) == 0 && !sc.Any {
		t = objectType(sc, indent)
		if sc.Nullable {
			t += " | null"
		}
	}
	fmt.Fprintf(b, "%s%s%s: %s\n", indent, propName(name), opt, t)
}

func description(sc *jsonschema.Schema) string {
	return strings.ReplaceAll(strings.ReplaceAll(sc.Description, "*/", "* /"), "\n", " ")
}

// pascal is an operation's ID as a type name's start: "posts.show" is
// PostsShow. Characters an identifier can't have separate words.
func pascal(id string) string {
	var b strings.Builder
	upper := true
	for _, r := range id {
		switch {
		case r != '$' && (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z'):
			upper = true
		case upper:
			b.WriteString(strings.ToUpper(string(r)))
			upper = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// camel is a segment of an operation's ID as a property: "Index" is
// index.
func camel(s string) string {
	p := pascal(s)
	if p == "" {
		return "op"
	}
	return strings.ToLower(p[:1]) + p[1:]
}

// ident makes s an identifier's start: "_" before a digit.
func ident(s string) string {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return "_" + s
	}
	return s
}

// typeName is a Go type's name as a TypeScript type's: itself if it is
// an identifier, else its words ("handlers.Order" is HandlersOrder).
func typeName(name string) string {
	if identifier.MatchString(name) {
		return name
	}
	return ident(pascal(name))
}

// ---- api.ts ----

func (g *generator) apiFile() []byte {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("// The types of the app's typed handlers (their inputs and results)")
	if len(g.ops) > 0 {
		b.WriteString(",\n// and a client for the API's routes")
	}
	b.WriteString(".\n\n")
	comps := slices.Clone(g.set.Components())
	slices.SortFunc(comps, func(a, b *jsonschema.Component) int { return strings.Compare(a.Name, b.Name) })
	for _, c := range comps {
		writeInterface(&b, c.Name, c.Schema)
	}
	b.WriteString(problemTS)
	for _, op := range g.ops {
		g.writeInput(&b, op)
	}
	if len(g.ops) > 0 {
		b.WriteString(clientTS)
		g.writeClient(&b)
	}
	return []byte(b.String())
}

func writeInterface(b *strings.Builder, name string, sc *jsonschema.Schema) {
	if d := description(sc); d != "" {
		fmt.Fprintf(b, "/** %s */\n", d)
	}
	if sc.Type != "object" || sc.Additional != nil {
		fmt.Fprintf(b, "export type %s = %s\n\n", name, tsType(sc))
		return
	}
	fmt.Fprintf(b, "export interface %s {\n", name)
	for _, p := range sc.Properties {
		writeField(b, "  ", p.Name, p.Schema, slices.Contains(sc.Required, p.Name))
	}
	b.WriteString("}\n\n")
}

// writeInput writes an operation's input: its parameters and its body's
// fields, by the names the client sends them under.
func (g *generator) writeInput(b *strings.Builder, op *operation) {
	fmt.Fprintf(b, "/** The input of %s %s. */\n", op.method, op.path)
	fmt.Fprintf(b, "export interface %s {\n", op.input)
	for _, p := range op.params {
		writeField(b, "  ", p.name, p.schema, p.required)
	}
	for _, f := range op.body {
		writeField(b, "  ", f.name, f.schema, f.required)
	}
	b.WriteString("}\n\n")
}

// node is a level of the client's object: operations by their IDs'
// segments.
type node struct {
	keys     []string
	children map[string]*node
	op       *operation
}

func (g *generator) writeClient(b *strings.Builder) {
	// A route name that is a function's and a group's prefix ("edge" and
	// "edge.sub"): the group's functions under the joined name (edgeSub),
	// whatever the routes' order.
	fn := map[string]bool{}
	for _, op := range g.ops {
		fn[op.id] = true
	}
	root := &node{children: map[string]*node{}}
	groups := map[string]*node{"": root}
	add := func(n *node, k string, child *node) {
		key := k
		for i := 2; n.children[key] != nil; i++ {
			key = k + fmt.Sprint(i)
		}
		n.keys = append(n.keys, key)
		n.children[key] = child
	}
	for _, op := range g.ops {
		n := root
		segs := strings.Split(op.id, ".")
		for i, s := range segs {
			path := strings.Join(segs[:i+1], ".")
			if i == len(segs)-1 || fn[path] {
				add(n, camel(strings.Join(segs[i:], "_")), &node{op: op})
				break
			}
			child := groups[path]
			if child == nil {
				child = &node{children: map[string]*node{}}
				groups[path] = child
				add(n, camel(s), child)
			}
			n = child
		}
	}
	b.WriteString("/**\n * createApi returns the API's client: a function per route, which\n * sends its input and resolves to its result, or rejects with an\n * ApiError.\n *\n *\tconst api = createApi()\n *\tconst post = await api.posts.show({ id: 1 })\n */\n")
	b.WriteString("export function createApi(options: ApiOptions = {}) {\n  return ")
	writeNode(b, root, "  ")
	b.WriteString("\n}\n")
}

func writeNode(b *strings.Builder, n *node, indent string) {
	b.WriteString("{\n")
	for _, k := range n.keys {
		c := n.children[k]
		if c.op != nil {
			op := c.op
			out := "void"
			if !op.empty {
				out = tsType(op.out)
			}
			spec := opSpec(op)
			arg := "input: " + op.input
			if !slices.ContainsFunc(op.params, func(p param) bool { return p.required }) &&
				!slices.ContainsFunc(op.body, func(f field) bool { return f.required }) {
				arg += " = {}" // nothing required
			}
			fmt.Fprintf(b, "%s  /** %s %s */\n", indent, op.method, op.path)
			fmt.Fprintf(b, "%s  %s: (%s, init?: RequestInit): Promise<%s> =>\n%s    call<%s>(options, %s, input, init),\n", indent, propName(k), arg, out, indent, out, spec)
			continue
		}
		fmt.Fprintf(b, "%s  %s: ", indent, propName(k))
		writeNode(b, c, indent+"  ")
		b.WriteString(",\n")
	}
	b.WriteString(indent + "}")
}

// opSpec is what the client's call needs of an operation.
func opSpec(op *operation) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "{ method: %s, path: %s", quoteTS(op.method), quoteTS(op.path))
	group := func(in string) []string {
		var out []string
		for _, p := range op.params {
			if p.in == in {
				out = append(out, quoteTS(p.name))
			}
		}
		return out
	}
	for _, in := range []string{"query", "header"} {
		if names := group(in); len(names) > 0 {
			fmt.Fprintf(&buf, ", %s: [%s]", in, strings.Join(names, ", "))
		}
	}
	if len(op.body) > 0 {
		var names []string
		for _, f := range op.body {
			names = append(names, quoteTS(f.name))
		}
		kind := "body"
		if op.form {
			kind = "form"
		}
		fmt.Fprintf(&buf, ", %s: [%s]", kind, strings.Join(names, ", "))
	}
	buf.WriteString(" }")
	return buf.String()
}

func quoteTS(s string) string {
	b, _ := json.Marshal(s)
	return "'" + strings.ReplaceAll(strings.Trim(string(b), `"`), "'", `\'`) + "'"
}

// problemTS is web's problem details, the body of every error.
const problemTS = `/** Problem details (RFC 9457): the body of an error response. */
export interface Problem {
  /** "about:blank": the status says what happened. */
  type: string
  /** The status's title, in the request's language. */
  title: string
  /** The HTTP status. */
  status: number
  /** What went wrong, for people. */
  detail?: string
  /** A message per field of the request that has a problem. */
  errors?: Record<string, string>
  /** The request's ID, as in the logs. */
  request_id?: string
}

`

// clientTS is the client's runtime: how a call is sent.
const clientTS = `/** ApiOptions configure createApi. */
export interface ApiOptions {
  /** Where the API is, when not on the page's origin ("https://api.example.com"). */
  baseUrl?: string
  /** An API token, sent as "Authorization: Bearer <token>". */
  token?: string | (() => string | undefined)
  /**
   * Whether cookies are sent: "same-origin" by default; "include" sends
   * them to another origin too. The XSRF header is sent when the page can
   * read the XSRF-TOKEN cookie: the API's own origin, or a cookie domain
   * both share.
   */
  credentials?: RequestCredentials
  /** Headers sent with every request. */
  headers?: Record<string, string>
  /** The fetch to use, for tests and other runtimes. */
  fetch?: typeof fetch
}

/** ApiError is a response with an error status, and its problem details. */
export class ApiError extends Error {
  readonly status: number
  readonly problem: Problem

  constructor(status: number, problem: Problem) {
    super(problem.detail || problem.title || 'HTTP ' + status)
    this.name = 'ApiError'
    this.status = status
    this.problem = problem
  }
}

interface Operation {
  method: string
  path: string
  query?: string[]
  header?: string[]
  body?: string[]
  form?: string[]
}

function cookie(name: string): string | undefined {
  if (typeof document === 'undefined') return undefined
  for (const c of document.cookie.split('; ')) {
    const i = c.indexOf('=')
    if (c.slice(0, i) === name) return decodeURIComponent(c.slice(i + 1))
  }
  return undefined
}

async function call<T>(options: ApiOptions, op: Operation, input: object, init: RequestInit = {}): Promise<T> {
  const values = input as Record<string, unknown>
  const path = op.path.replace(/\{([^}.]+)(\.\.\.)?\}/g, (_, name: string, rest?: string) => {
    const v = String(values[name] ?? '')
    return rest ? v.split('/').map(encodeURIComponent).join('/') : encodeURIComponent(v)
  })
  const query = new URLSearchParams()
  for (const name of op.query ?? []) {
    const v = values[name]
    if (v === undefined || v === null) continue
    for (const item of Array.isArray(v) ? v : [v]) query.append(name, String(item))
  }
  const headers = new Headers(options.headers)
  headers.set('Accept', 'application/json')
  for (const name of op.header ?? []) {
    if (values[name] !== undefined && values[name] !== null) headers.set(name, String(values[name]))
  }
  const token = typeof options.token === 'function' ? options.token() : options.token
  if (token) headers.set('Authorization', 'Bearer ' + token)
  const xsrf = cookie('XSRF-TOKEN')
  if (xsrf && op.method !== 'GET' && op.method !== 'HEAD') headers.set('X-XSRF-TOKEN', xsrf)
  let body: BodyInit | undefined
  if (op.form) {
    const form = new FormData()
    for (const name of op.form) {
      const v = values[name]
      if (v === undefined || v === null) continue
      for (const item of Array.isArray(v) ? v : [v]) form.append(name, item instanceof Blob ? item : String(item))
    }
    body = form
  } else if (op.body) {
    const fields: Record<string, unknown> = {}
    for (const name of op.body) if (values[name] !== undefined) fields[name] = values[name]
    headers.set('Content-Type', 'application/json')
    body = JSON.stringify(fields)
  }
  new Headers(init.headers).forEach((v, k) => headers.set(k, v))
  const q = query.toString()
  const res = await (options.fetch ?? fetch)((options.baseUrl ?? '').replace(/\/+$/, '') + path + (q ? '?' + q : ''), {
    credentials: options.credentials ?? 'same-origin',
    ...init,
    method: op.method,
    headers,
    body,
  })
  const text = await res.text()
  if (!res.ok) {
    let problem: Problem = { type: 'about:blank', title: res.statusText, status: res.status }
    try {
      problem = { ...problem, ...JSON.parse(text) }
    } catch {
      // not JSON: the status says what happened
    }
    throw new ApiError(res.status, problem)
  }
  return (text ? JSON.parse(text) : undefined) as T
}

`

// ---- pages.ts ----

// refs adds the names of the components sc refers to (not those they
// refer to: api.ts has them).
func refs(sc *jsonschema.Schema, names map[string]bool) {
	switch {
	case sc == nil:
	case sc.Ref != nil:
		names[sc.Ref.Name] = true
	default:
		for _, s := range sc.AnyOf {
			refs(s, names)
		}
		for _, p := range sc.Properties {
			refs(p.Schema, names)
		}
		refs(sc.Items, names)
		refs(sc.Additional, names)
	}
}

func (g *generator) pagesFile() []byte {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("// The props of the app's Inertia pages and the shared props, and their\n// types in Inertia's client (usePage).\n\n")
	// The components the props refer to come from api.ts.
	var body strings.Builder
	for _, pt := range g.pageTs {
		if pt.isMap {
			continue
		}
		// A type, not an interface: usePage<T> needs T's index signature.
		fmt.Fprintf(&body, "export type %s = {\n", pt.name)
		for _, f := range pt.fields {
			writeField(&body, "  ", f.name, f.schema, f.required)
		}
		body.WriteString("}\n\n")
	}
	body.WriteString("/** The props every page gets (inertia.WithShared). */\n")
	var shared []string
	for _, pt := range g.shared {
		if pt.isMap {
			shared = append(shared, "Record<string, unknown>")
		} else {
			shared = append(shared, pt.name)
		}
	}
	if len(shared) == 0 {
		shared = []string{"Record<string, never>"}
	}
	fmt.Fprintf(&body, "export type SharedProps = %s\n\n", strings.Join(shared, " & "))
	body.WriteString("/** The validation errors of the page's last form post, by field (or by error bag, then field). */\n")
	body.WriteString("export type Errors = Record<string, string | Record<string, string>>\n\n")
	body.WriteString("/** The pages, by component, and their props (inertia.WithPages). */\nexport interface Pages {\n")
	for _, p := range g.pages {
		if p.props.isMap {
			fmt.Fprintf(&body, "  %s: Record<string, unknown>\n", quoteTS(p.component))
		} else {
			fmt.Fprintf(&body, "  %s: %s\n", quoteTS(p.component), p.props.name)
		}
	}
	body.WriteString("}\n\n")
	body.WriteString("/** The props a page component gets: its own, the shared ones, and errors. */\n")
	body.WriteString("export type PageProps<C extends keyof Pages> = Pages[C] & SharedProps & { errors: Errors }\n\n")
	body.WriteString("declare module '@inertiajs/core' {\n  export interface InertiaConfig {\n    sharedPageProps: SharedProps\n  }\n}\n")

	text := body.String()
	seen := map[string]bool{}
	for _, pt := range g.pageTs {
		for _, f := range pt.fields {
			refs(f.schema, seen)
		}
	}
	imports := slices.Sorted(maps.Keys(seen))
	b.WriteString("import type {} from '@inertiajs/core' // for the augmentation below\n")
	if len(imports) > 0 {
		fmt.Fprintf(&b, "import type { %s } from './api'\n", strings.Join(imports, ", "))
	}
	b.WriteString("\n")
	b.WriteString(text)
	return []byte(b.String())
}
