// SPDX-License-Identifier: Apache-2.0

package secscan

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/types/typeutil"
)

// kind says what an expression carries.
type kind uint8

const (
	// fromRequest: text from the request (a parameter, the query, a
	// form, a header, the body, a typed handler's input).
	fromRequest kind = 1 << iota
	// idFromRequest: a number from the request (an ID parsed from the
	// path), for source.owner.
	idFromRequest
	// built: text built (+, fmt.Sprintf) from strings that aren't
	// constants, wherever they come from (advanced source.sql).
	built
)

const (
	webPkg     = "anetos.dev/anetos/web"
	dbPkg      = "anetos.dev/anetos/db"
	migratePkg = "anetos.dev/anetos/db/migrate"
	templPkg   = "github.com/a-h/templ"
)

// flow follows request data through one function (and the function
// literals in it), without looking into the functions it calls.
type flow struct {
	info    *types.Info
	vars    map[types.Object]kind
	inputs  map[types.Object]bool // typed handlers' inputs: everything in them is the client's
	reqs    map[types.Object]bool // the client's requests: handlers' *http.Request, c.Request()
	allowed map[exprKey]bool      // values checked against a list of constants
}

// exprKey names a variable or a field of one: in, in.Sort.
type exprKey struct {
	obj  types.Object
	path string
}

// newFlow follows the data of a declaration. A typed handler's input
// (handlers: those given to web.H in the package; else an exported
// func(c *web.Ctx, in T) (Out, error)) is the client's.
func newFlow(info *types.Info, root ast.Node, handlers map[ast.Node]bool) *flow {
	f := &flow{info: info, vars: map[types.Object]kind{}, inputs: map[types.Object]bool{}, reqs: map[types.Object]bool{}, allowed: map[exprKey]bool{}}
	ast.Inspect(root, func(n ast.Node) bool {
		var ft *ast.FuncType
		switch n := n.(type) {
		case *ast.FuncDecl:
			ft = n.Type
			if handlers[n] || n.Name.IsExported() && n.Type.Results != nil && n.Type.Results.NumFields() == 2 {
				f.handlerInput(n.Type)
			}
		case *ast.FuncLit:
			ft = n.Type
			if handlers[n] {
				f.handlerInput(n.Type)
			}
		}
		if ft != nil && ft.Params != nil {
			for _, field := range ft.Params.List {
				for _, name := range field.Names {
					if obj := info.Defs[name]; obj != nil && isNamed(obj.Type(), "net/http", "Request", true) {
						f.reqs[obj] = true
					}
				}
			}
		}
		return true
	})
	f.allowlists(root)
	f.solve(root)
	return f
}

// key is e's exprKey, when e is a variable or a field of one.
func (f *flow) key(e ast.Expr) (exprKey, bool) {
	path := ""
	for {
		switch x := ast.Unparen(e).(type) {
		case *ast.Ident:
			obj := f.info.Uses[x]
			if obj == nil {
				obj = f.info.Defs[x]
			}
			if _, ok := obj.(*types.Var); !ok {
				return exprKey{}, false
			}
			return exprKey{obj, path}, true
		case *ast.SelectorExpr:
			if f.info.Selections[x] == nil {
				return exprKey{}, false
			}
			path = "." + x.Sel.Name + path
			e = x.X
		default:
			return exprKey{}, false
		}
	}
}

// allowlists finds the values the function checks against constants,
// replacing them (or returning) when they aren't one:
//
//	switch sort { case "title", "created_at": default: sort = "created_at" }
//	if !slices.Contains(columns, sort) { return … }
//	if _, ok := allowed[sort]; !ok { sort = "id" }
func (f *flow) allowlists(root ast.Node) {
	isConst := func(e ast.Expr) bool { tv, ok := f.info.Types[e]; return ok && tv.Value != nil }
	ast.Inspect(root, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SwitchStmt:
			k, ok := f.key(n.Tag)
			if n.Tag == nil || !ok {
				return true
			}
			var def *ast.CaseClause
			for _, st := range n.Body.List {
				cc := st.(*ast.CaseClause)
				if cc.List == nil {
					def = cc
				}
				for _, e := range cc.List {
					if !isConst(e) {
						return true
					}
				}
			}
			if def != nil && f.resets(def.Body, k) {
				f.allowed[k] = true
			}
		case *ast.IfStmt:
			if k, ok := f.membership(n.Init, n.Cond); ok && f.resets(n.Body.List, k) {
				f.allowed[k] = true
			}
		}
		return true
	})
}

// resets reports whether stmts give k a constant, or leave.
func (f *flow) resets(stmts []ast.Stmt, k exprKey) bool {
	for _, st := range stmts {
		switch st := st.(type) {
		case *ast.ReturnStmt, *ast.BranchStmt:
			return true
		case *ast.ExprStmt:
			if c, ok := st.X.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "panic" {
					return true
				}
			}
		case *ast.AssignStmt:
			if len(st.Lhs) == 1 && len(st.Rhs) == 1 {
				if lk, ok := f.key(st.Lhs[0]); ok && lk == k {
					if tv, ok := f.info.Types[st.Rhs[0]]; ok && tv.Value != nil {
						return true
					}
				}
			}
		}
	}
	return false
}

// membership is the value an if's condition finds missing from a list:
// !slices.Contains(list, v), !allowed[v], !ok after _, ok := m[v], or
// v != "a" && v != "b".
func (f *flow) membership(init ast.Stmt, cond ast.Expr) (exprKey, bool) {
	not, ok := ast.Unparen(cond).(*ast.UnaryExpr)
	if !ok || not.Op != token.NOT {
		return f.notEqualAll(cond)
	}
	switch x := ast.Unparen(not.X).(type) {
	case *ast.CallExpr:
		if fn, _ := typeutil.Callee(f.info, x).(*types.Func); fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "slices" &&
			fn.Name() == "Contains" && len(x.Args) == 2 {
			return f.key(x.Args[1])
		}
	case *ast.IndexExpr:
		return f.key(x.Index)
	case *ast.Ident:
		if as, ok := init.(*ast.AssignStmt); ok && len(as.Lhs) == 2 && len(as.Rhs) == 1 {
			if id, ok := as.Lhs[1].(*ast.Ident); ok && id.Name == x.Name {
				if ix, ok := ast.Unparen(as.Rhs[0]).(*ast.IndexExpr); ok {
					return f.key(ix.Index)
				}
			}
		}
	case *ast.BinaryExpr: // !(v == "a" || v == "b")
		return f.equalAny(x)
	}
	return exprKey{}, false
}

// notEqualAll is v in v != "a" && v != "b".
func (f *flow) notEqualAll(e ast.Expr) (exprKey, bool) {
	return f.compareAll(e, token.LAND, token.NEQ)
}

// equalAny is v in v == "a" || v == "b".
func (f *flow) equalAny(e ast.Expr) (exprKey, bool) {
	return f.compareAll(e, token.LOR, token.EQL)
}

func (f *flow) compareAll(e ast.Expr, join, cmp token.Token) (exprKey, bool) {
	b, ok := ast.Unparen(e).(*ast.BinaryExpr)
	if !ok {
		return exprKey{}, false
	}
	if b.Op == join {
		l, ok1 := f.compareAll(b.X, join, cmp)
		r, ok2 := f.compareAll(b.Y, join, cmp)
		return l, ok1 && ok2 && l == r
	}
	if b.Op != cmp {
		return exprKey{}, false
	}
	if tv, ok := f.info.Types[b.Y]; ok && tv.Value != nil {
		return f.key(b.X)
	}
	if tv, ok := f.info.Types[b.X]; ok && tv.Value != nil {
		return f.key(b.Y)
	}
	return exprKey{}, false
}

// clientRequest reports whether e is the client's request: a handler's
// *http.Request, c.Request(), or a variable given one.
func (f *flow) clientRequest(e ast.Expr) bool {
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		return f.reqs[f.info.Uses[x]]
	case *ast.CallExpr:
		if sel, ok := ast.Unparen(x.Fun).(*ast.SelectorExpr); ok && sel.Sel.Name == "Request" {
			return isNamed(f.info.TypeOf(sel.X), webPkg, "Ctx", true)
		}
	}
	return false
}

// handlerInput marks a typed handler's input (func(c *web.Ctx, in T)).
func (f *flow) handlerInput(ft *ast.FuncType) {
	if ft.Params == nil {
		return
	}
	var params []*ast.Ident
	for _, field := range ft.Params.List {
		if len(field.Names) == 0 {
			return
		}
		params = append(params, field.Names...)
	}
	if len(params) != 2 {
		return
	}
	first, second := f.info.Defs[params[0]], f.info.Defs[params[1]]
	if first == nil || second == nil || !isNamed(first.Type(), webPkg, "Ctx", true) {
		return
	}
	f.inputs[second] = true
}

// solve finds what each variable carries, until nothing changes.
func (f *flow) solve(root ast.Node) {
	for range 8 {
		changed := false
		mark := func(e ast.Expr, k kind) {
			obj := baseObject(f.info, e)
			if obj == nil || k == 0 {
				return
			}
			if key, ok := f.key(e); ok && f.allowed[key] {
				k &^= fromRequest | built
			}
			k = narrow(k, obj.Type())
			if f.vars[obj]|k != f.vars[obj] {
				f.vars[obj] |= k
				changed = true
			}
		}
		ast.Inspect(root, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				if len(n.Lhs) == len(n.Rhs) {
					for i := range n.Lhs {
						if f.clientRequest(n.Rhs[i]) {
							if obj := baseObject(f.info, n.Lhs[i]); obj != nil && !f.reqs[obj] {
								f.reqs[obj] = true
								changed = true
							}
						}
						mark(n.Lhs[i], f.of(n.Rhs[i]))
					}
				} else if len(n.Rhs) == 1 {
					k := f.of(n.Rhs[0])
					for _, l := range n.Lhs {
						mark(l, k)
					}
				}
			case *ast.ValueSpec:
				if len(n.Names) == len(n.Values) {
					for i := range n.Names {
						mark(n.Names[i], f.of(n.Values[i]))
					}
				} else if len(n.Values) == 1 {
					k := f.of(n.Values[0])
					for _, name := range n.Names {
						mark(name, k)
					}
				}
			case *ast.RangeStmt:
				k := f.of(n.X)
				if n.Key != nil {
					mark(n.Key, k)
				}
				if n.Value != nil {
					mark(n.Value, k)
				}
			case *ast.CallExpr:
				// A standard library call that writes what it reads into
				// an argument or its receiver: b.WriteString(s),
				// json.Unmarshal(body, &in), dec.Decode(&in).
				fn, _ := typeutil.Callee(f.info, n).(*types.Func)
				if fn == nil || fn.Pkg() == nil || !propagates(fn.Pkg().Path()) {
					break
				}
				var k kind
				recv := receiver(n)
				if recv != nil {
					k |= f.of(recv)
				}
				for _, a := range n.Args {
					k |= f.of(a)
				}
				if k == 0 {
					break
				}
				if recv != nil && buffer(f.info.TypeOf(recv)) {
					mark(recv, k)
				}
				for _, a := range n.Args {
					if u, ok := ast.Unparen(a).(*ast.UnaryExpr); ok && u.Op == token.AND {
						mark(u.X, k)
					}
				}
				if fn.Pkg().Path() == "fmt" && strings.HasPrefix(fn.Name(), "Fprint") && len(n.Args) > 0 && buffer(f.info.TypeOf(n.Args[0])) {
					mark(n.Args[0], k)
				}
			}
			return true
		})
		if !changed {
			return
		}
	}
}

// of is what e carries.
func (f *flow) of(e ast.Expr) kind {
	if e == nil {
		return 0
	}
	t := f.info.TypeOf(e)
	if tv, ok := f.info.Types[e]; ok && tv.Value != nil {
		return 0 // a constant
	}
	if key, ok := f.key(e); ok && f.allowed[key] {
		return narrow(f.vars[key.obj]&idFromRequest, t)
	}
	var k kind
	switch e := e.(type) {
	case *ast.ParenExpr:
		return f.of(e.X)
	case *ast.Ident:
		obj := f.info.Uses[e]
		if obj == nil {
			obj = f.info.Defs[e]
		}
		if obj == nil {
			return 0
		}
		k = f.vars[obj]
		if f.inputs[obj] {
			k |= fromRequest | idFromRequest
		}
	case *ast.SelectorExpr:
		sel := f.info.Selections[e]
		if sel == nil {
			return 0 // a package's name
		}
		if f.clientRequest(e.X) {
			if sel.Kind() == types.FieldVal && requestFields[e.Sel.Name] {
				k = fromRequest
			}
			break
		}
		k = f.of(e.X)
	case *ast.IndexExpr:
		k = f.of(e.X)
	case *ast.IndexListExpr:
		k = f.of(e.X)
	case *ast.SliceExpr:
		k = f.of(e.X)
	case *ast.StarExpr:
		k = f.of(e.X)
	case *ast.UnaryExpr:
		k = f.of(e.X)
	case *ast.TypeAssertExpr:
		k = f.of(e.X)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return 0
		}
		k = f.of(e.X) | f.of(e.Y)
		if isString(t) && (f.dynamic(e.X) || f.dynamic(e.Y)) {
			k |= built
		}
	case *ast.CompositeLit:
		for _, el := range e.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				el = kv.Value
			}
			k |= f.of(el)
		}
	case *ast.CallExpr:
		k = f.call(e)
	}
	return narrow(k, t)
}

// requestFields are http.Request's fields the client sets.
var requestFields = map[string]bool{"URL": true, "Form": true, "PostForm": true, "MultipartForm": true,
	"Header": true, "Body": true, "Host": true, "RequestURI": true, "Trailer": true}

// dynamic reports whether e, in a string built with + or Sprintf, is a
// string that isn't a constant (nor a number written as text).
func (f *flow) dynamic(e ast.Expr) bool {
	if tv, ok := f.info.Types[e]; !ok || tv.Value != nil || !isString(tv.Type) {
		return false
	}
	if key, ok := f.key(e); ok && f.allowed[key] {
		return false // checked against a list of constants
	}
	if c, ok := ast.Unparen(e).(*ast.CallExpr); ok {
		if fn, _ := typeutil.Callee(f.info, c).(*types.Func); fn != nil && fn.Pkg() != nil {
			switch fn.Pkg().Path() + "." + fn.Name() {
			case "strconv.Itoa", "strconv.FormatInt", "strconv.FormatUint", "strconv.FormatFloat", "strconv.FormatBool", "strings.Repeat":
				return f.of(c)&fromRequest != 0
			}
		}
	}
	return true
}

func (f *flow) call(c *ast.CallExpr) kind {
	// A conversion: string(b), template.HTML(s).
	if tv, ok := f.info.Types[c.Fun]; ok && tv.IsType() {
		if len(c.Args) == 1 {
			return f.of(c.Args[0])
		}
		return 0
	}
	if id, ok := ast.Unparen(c.Fun).(*ast.Ident); ok {
		if b, ok := f.info.Uses[id].(*types.Builtin); ok {
			if b.Name() == "append" {
				var k kind
				for _, a := range c.Args {
					k |= f.of(a)
				}
				return k
			}
			return 0
		}
	}
	fn, _ := typeutil.Callee(f.info, c).(*types.Func)
	if fn == nil {
		return 0
	}
	fn = fn.Origin()
	recv := receiver(c)
	if recv != nil {
		rt := f.info.TypeOf(recv)
		switch {
		case isNamed(rt, webPkg, "Ctx", true):
			switch fn.Name() {
			case "Param", "Query", "Header":
				return fromRequest
			}
			return 0
		case f.clientRequest(recv):
			switch fn.Name() {
			case "FormValue", "PostFormValue", "Referer", "UserAgent", "PathValue", "Cookie", "Cookies", "FormFile", "MultipartReader":
				return fromRequest
			}
			return 0
		}
	}
	if fn.Pkg() == nil {
		return 0
	}
	path, name := fn.Pkg().Path(), fn.Name()
	switch path + "." + name {
	case "strconv.Atoi", "strconv.ParseInt", "strconv.ParseUint", "strconv.ParseFloat":
		if len(c.Args) > 0 && f.of(c.Args[0])&fromRequest != 0 {
			return idFromRequest
		}
		return 0
	case "path.Base", "path/filepath.Base", "net/url.QueryEscape", "net/url.PathEscape", "html.EscapeString",
		"html/template.HTMLEscapeString", "html/template.JSEscapeString", "html/template.URLQueryEscaper",
		"strconv.Itoa", "strconv.FormatInt", "strconv.FormatUint", "strconv.FormatFloat", "strconv.FormatBool":
		return 0
	}
	if !propagates(path) {
		return 0
	}
	var k kind
	if recv != nil {
		k |= f.of(recv)
	}
	for _, a := range c.Args {
		k |= f.of(a)
	}
	if path == "fmt" && strings.HasPrefix(name, "Sprint") {
		for _, a := range c.Args {
			if f.dynamic(a) {
				k |= built
			}
		}
	}
	return k
}

// propagates reports whether a standard library package's functions
// return what they're given (strings, url, fmt, encoding/json…).
func propagates(path string) bool {
	switch path {
	case "fmt", "strings", "bytes", "path", "path/filepath", "net/url", "strconv", "io", "bufio",
		"encoding/json", "encoding/xml", "encoding/base64", "encoding/hex", "unicode/utf8", "mime/multipart", "net/textproto", "net/http":
		return true
	}
	return false
}

// buffer reports whether t collects what's written to it: strings.Builder,
// bytes.Buffer, url.Values, bufio.Writer.
func buffer(t types.Type) bool {
	return isNamed(t, "strings", "Builder", true) || isNamed(t, "bytes", "Buffer", true) ||
		isNamed(t, "net/url", "Values", true) || isNamed(t, "bufio", "Writer", true)
}

// receiver is the receiver of a method call, else nil.
func receiver(c *ast.CallExpr) ast.Expr {
	fun := ast.Unparen(c.Fun)
	if ix, ok := fun.(*ast.IndexExpr); ok {
		fun = ix.X
	}
	if sel, ok := fun.(*ast.SelectorExpr); ok {
		return sel.X
	}
	return nil
}

// narrow keeps what a value of type t can carry: text in strings,
// bytes and containers; numbers in numbers.
func narrow(k kind, t types.Type) kind {
	if t == nil || k == 0 {
		return k
	}
	if isNamed(t, "", "error", false) {
		return 0
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsString != 0:
			return k
		case u.Info()&types.IsNumeric != 0:
			return k & idFromRequest
		}
		return 0
	case *types.Signature, *types.Chan:
		return 0
	}
	return k
}

func isString(t types.Type) bool {
	if t == nil {
		return false
	}
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

// isNamed reports whether t (or *t, with ptr) is the named type path.name
// (path "" for a universe type).
func isNamed(t types.Type, path, name string, ptr bool) bool {
	if t == nil {
		return false
	}
	if p, ok := t.(*types.Pointer); ok && ptr {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	obj := n.Obj()
	if obj.Name() != name {
		return false
	}
	if obj.Pkg() == nil {
		return path == ""
	}
	return obj.Pkg().Path() == path
}

// baseObject is the variable an assignment's left side changes: x in x,
// x.f, x[i], *x.
func baseObject(info *types.Info, e ast.Expr) types.Object {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			if obj := info.Defs[x]; obj != nil {
				return obj
			}
			return info.Uses[x]
		case *ast.SelectorExpr:
			if info.Selections[x] == nil {
				return nil
			}
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.UnaryExpr:
			e = x.X
		default:
			return nil
		}
	}
}

// constString is e's value when it's a string constant.
func constString(info *types.Info, e ast.Expr) (string, bool) {
	tv, ok := info.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}
