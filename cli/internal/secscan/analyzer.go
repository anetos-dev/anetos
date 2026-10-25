// SPDX-License-Identifier: Apache-2.0

package secscan

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

// found is a finding of the source rules, at a position.
type found struct {
	pos, end token.Pos
	rule     string
	severity string
	where    string // the function, after its package: Posts.Search
	message  string
}

// Analyzer runs the source rules on a package: its result is []found.
var Analyzer = &analysis.Analyzer{
	Name:       "anetossecurity",
	Doc:        "finds request data that reaches SQL, HTML, redirects, commands and files, and other mistakes the security check reports",
	Run:        run,
	ResultType: reflect.TypeFor[[]found](),
}

func run(pass *analysis.Pass) (any, error) {
	handlers := typedHandlers(pass)
	var out []found
	for _, file := range pass.Files {
		name := pass.Fset.Position(file.Pos()).Filename
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, decl := range file.Decls {
			s := &scan{pass: pass, node: decl, where: declName(decl)}
			s.flow = newFlow(pass.TypesInfo, decl, handlers)
			s.decl(decl)
			out = append(out, s.out...)
		}
	}
	return out, nil
}

// typedHandlers are the package's typed handlers: the functions and
// function literals given to web.H here.
func typedHandlers(pass *analysis.Pass) map[ast.Node]bool {
	out := map[ast.Node]bool{}
	funcs := map[types.Object]bool{}
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok || len(c.Args) != 1 {
				return true
			}
			if fn, _ := typeutil.Callee(pass.TypesInfo, c).(*types.Func); fn == nil || funcKey(fn) != webPkg+".H" {
				return true
			}
			switch a := ast.Unparen(c.Args[0]).(type) {
			case *ast.FuncLit:
				out[a] = true
			case *ast.Ident:
				funcs[pass.TypesInfo.Uses[a]] = true
			case *ast.SelectorExpr:
				funcs[pass.TypesInfo.Uses[a.Sel]] = true
			}
			return true
		})
	}
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && funcs[pass.TypesInfo.Defs[fd.Name]] {
				out[fd] = true
			}
		}
	}
	return out
}

// declName names a function as reports show it after its package:
// Posts.Search; "" for another declaration.
func declName(decl ast.Decl) string {
	name := ""
	fd, ok := decl.(*ast.FuncDecl)
	if !ok {
		return name
	}
	if fd.Recv != nil && len(fd.Recv.List) == 1 {
		t := fd.Recv.List[0].Type
		if s, ok := t.(*ast.StarExpr); ok {
			t = s.X
		}
		if ix, ok := t.(*ast.IndexExpr); ok {
			t = ix.X
		}
		if ix, ok := t.(*ast.IndexListExpr); ok {
			t = ix.X
		}
		if id, ok := t.(*ast.Ident); ok {
			name = id.Name + "."
		}
	}
	return name + fd.Name.Name
}

// scan applies the rules to one declaration.
type scan struct {
	pass  *analysis.Pass
	node  ast.Decl
	flow  *flow
	where string
	out   []found
}

func (s *scan) report(at ast.Node, rule, severity, format string, args ...any) {
	f := found{pos: at.Pos(), end: at.End(), rule: rule, severity: severity, where: s.where, message: fmt.Sprintf(format, args...)}
	s.out = append(s.out, f)
	s.pass.Report(analysis.Diagnostic{Pos: f.pos, End: f.end, Category: rule, Message: rule + " " + severity + ": " + f.message})
}

func (s *scan) decl(decl ast.Decl) {
	info := s.pass.TypesInfo
	var authz bool // the declaration checks access (rbac, a policy, a record's owner)
	ast.Inspect(decl, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if fn, _ := typeutil.Callee(info, n).(*types.Func); fn != nil && checksAccess(fn) {
				authz = true
			}
		case *ast.BinaryExpr:
			if (n.Op == token.EQL || n.Op == token.NEQ) && (ownerField(n.X) || ownerField(n.Y)) {
				authz = true
			}
		}
		return true
	})
	ast.Inspect(decl, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			s.callRules(n, authz)
			s.randRead(n)
		case *ast.CompositeLit:
			s.tlsLiteral(n)
		case *ast.AssignStmt:
			s.tlsAssign(n)
			s.randAssign(n.Lhs, n.Rhs)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, len(n.Names))
			for i, id := range n.Names {
				lhs[i] = id
			}
			s.randAssign(lhs, n.Values)
		case *ast.KeyValueExpr:
			s.randAssign([]ast.Expr{n.Key}, []ast.Expr{n.Value})
		case *ast.ReturnStmt:
			if fd, ok := decl.(*ast.FuncDecl); ok && secretName(fd.Name.Name) {
				for _, r := range n.Results {
					if at := s.mathRand(r); at != nil {
						s.report(at, "source.rand", "warning", "%s makes a secret with math/rand, which is predictable: use crypto/rand (rand.Text() for a token)", fd.Name.Name)
					}
				}
			}
		}
		return true
	})
}

// checksAccess reports whether a call looks like an access check:
// rbac's functions, auth's Authorize… and Allows… and a token's Can, or
// a function named so (CanEdit, AuthorizeOwner, postPolicy, ownsPost…).
func checksAccess(fn *types.Func) bool {
	if fn.Pkg() != nil {
		switch fn.Pkg().Path() {
		case "anetos.dev/anetos/rbac":
			return true
		case "anetos.dev/anetos/auth":
			return strings.HasPrefix(fn.Name(), "Authorize") || strings.HasPrefix(fn.Name(), "Allows") || strings.HasSuffix(fn.Name(), "Can")
		}
	}
	w := words(fn.Name())
	if len(w) == 0 {
		return false
	}
	switch w[0] {
	case "can", "authorize", "authorise", "allow", "allows", "owns", "permit", "permits", "forbid", "belongs", "visible": //nolint:misspell // apps' British names
		return true
	}
	for _, x := range w {
		if accessWords[x] {
			return true
		}
	}
	return false
}

// accessWords, in a function's name, say it checks access.
var accessWords = map[string]bool{"policy": true, "owner": true, "owns": true, "permission": true, "permissions": true,
	"access": true, "authorize": true, "authorise": true, "authorized": true, "allowed": true} //nolint:misspell // apps' British names

// ownerField reports whether e is a field naming a record's owner
// (post.UserID, doc.OwnerID…): a comparison with it checks access.
func ownerField(e ast.Expr) bool {
	sel, ok := ast.Unparen(e).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	w := words(sel.Sel.Name)
	if len(w) == 0 {
		return false
	}
	switch w[0] {
	case "user", "owner", "author", "account", "creator", "team", "tenant", "org", "organization", "member", "customer":
		return len(w) == 1 || len(w) == 2 && w[1] == "id"
	}
	return false
}

// words splits a name into its lower-case words: sessionID is
// session, id; reset_token is reset, token.
func words(name string) []string {
	var out []string
	var cur []rune
	rs := []rune(name)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ':
			flush()
			continue
		case unicode.IsUpper(r) && len(cur) > 0 && (unicode.IsLower(cur[len(cur)-1]) || i+1 < len(rs) && unicode.IsLower(rs[i+1])):
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// sink is a call that must not get request data in an argument.
type sink struct {
	rule string
	args []int // the arguments checked; -1: every argument from the last listed
}

// sinks are the functions and methods (pkg.Func, pkg.Type.Method) the
// rules check, with the arguments that matter.
var sinks = map[string]sink{
	dbPkg + ".Raw":                      {"source.sql", []int{1}},
	dbPkg + ".RawFirst":                 {"source.sql", []int{1}},
	dbPkg + ".Exec":                     {"source.sql", []int{1}},
	dbPkg + ".SQL":                      {"source.sql", []int{0}},
	dbPkg + ".OrderRaw":                 {"source.sql", []int{0}},
	dbPkg + ".Select":                   {"source.sql", []int{1, -1}},
	dbPkg + ".Q.WhereRaw":               {"source.sql", []int{0}},
	dbPkg + ".Q.Join":                   {"source.sql", []int{0}},
	dbPkg + ".Q.GroupBy":                {"source.sql", []int{0, -1}},
	dbPkg + ".Column.SetRaw":            {"source.sql", []int{0}},
	migratePkg + ".Schema.Exec":         {"source.sql", []int{0}},
	"database/sql.DB.Query":             {"source.sql", []int{0}},
	"database/sql.DB.QueryContext":      {"source.sql", []int{1}},
	"database/sql.DB.QueryRow":          {"source.sql", []int{0}},
	"database/sql.DB.QueryRowContext":   {"source.sql", []int{1}},
	"database/sql.DB.Exec":              {"source.sql", []int{0}},
	"database/sql.DB.ExecContext":       {"source.sql", []int{1}},
	"database/sql.DB.Prepare":           {"source.sql", []int{0}},
	"database/sql.DB.PrepareContext":    {"source.sql", []int{1}},
	"database/sql.Tx.Query":             {"source.sql", []int{0}},
	"database/sql.Tx.QueryContext":      {"source.sql", []int{1}},
	"database/sql.Tx.QueryRow":          {"source.sql", []int{0}},
	"database/sql.Tx.QueryRowContext":   {"source.sql", []int{1}},
	"database/sql.Tx.Exec":              {"source.sql", []int{0}},
	"database/sql.Tx.ExecContext":       {"source.sql", []int{1}},
	"database/sql.Conn.QueryContext":    {"source.sql", []int{1}},
	"database/sql.Conn.QueryRowContext": {"source.sql", []int{1}},
	"database/sql.Conn.ExecContext":     {"source.sql", []int{1}},
	"database/sql.Conn.PrepareContext":  {"source.sql", []int{1}},
	templPkg + ".Raw":                   {"source.xss", []int{0}},
	webPkg + ".Ctx.HTML":                {"source.xss", []int{1}},
	webPkg + ".Ctx.Redirect":            {"source.redirect", []int{1}},
	webPkg + ".Redirect":                {"source.redirect", []int{0}},
	"net/http.Redirect":                 {"source.redirect", []int{2}},
	"os.Open":                           {"source.path", []int{0}},
	"os.OpenFile":                       {"source.path", []int{0}},
	"os.ReadFile":                       {"source.path", []int{0}},
	"os.WriteFile":                      {"source.path", []int{0}},
	"os.Create":                         {"source.path", []int{0}},
	"os.Remove":                         {"source.path", []int{0}},
	"os.RemoveAll":                      {"source.path", []int{0}},
	"os.Mkdir":                          {"source.path", []int{0}},
	"os.MkdirAll":                       {"source.path", []int{0}},
	"os.ReadDir":                        {"source.path", []int{0}},
	"os.Rename":                         {"source.path", []int{0, 1}},
	"net/http.ServeFile":                {"source.path", []int{2}},
	"net/http.Get":                      {"source.ssrf", []int{0}},
	"net/http.Head":                     {"source.ssrf", []int{0}},
	"net/http.Post":                     {"source.ssrf", []int{0}},
	"net/http.PostForm":                 {"source.ssrf", []int{0}},
	"net/http.NewRequest":               {"source.ssrf", []int{1}},
	"net/http.NewRequestWithContext":    {"source.ssrf", []int{2}},
	"net/http.Client.Get":               {"source.ssrf", []int{0}},
	"net/http.Client.Head":              {"source.ssrf", []int{0}},
	"net/http.Client.Post":              {"source.ssrf", []int{0}},
	"net/http.Client.PostForm":          {"source.ssrf", []int{0}},
	"net.Dial":                          {"source.ssrf", []int{1}},
}

// htmlTypes are html/template's types that mark text as safe.
var htmlTypes = map[string]bool{"HTML": true, "HTMLAttr": true, "JS": true, "JSStr": true, "CSS": true, "URL": true, "Srcset": true}

// funcKey names a function as sinks does: pkg.Func or pkg.Type.Method.
func funcKey(fn *types.Func) string {
	fn = fn.Origin()
	if fn.Pkg() == nil {
		return fn.Name()
	}
	sig, _ := fn.Type().(*types.Signature)
	if sig != nil && sig.Recv() != nil {
		t := sig.Recv().Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if n, ok := types.Unalias(t).(*types.Named); ok {
			return fn.Pkg().Path() + "." + n.Obj().Name() + "." + fn.Name()
		}
		return fn.Pkg().Path() + "." + fn.Name()
	}
	return fn.Pkg().Path() + "." + fn.Name()
}

func (s *scan) callRules(c *ast.CallExpr, authz bool) {
	info := s.pass.TypesInfo
	// html/template.HTML(s) and friends.
	if tv, ok := info.Types[c.Fun]; ok && tv.IsType() {
		if len(c.Args) == 1 {
			if n, ok := types.Unalias(tv.Type).(*types.Named); ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "html/template" && htmlTypes[n.Obj().Name()] {
				if s.flow.of(c.Args[0])&fromRequest != 0 {
					s.report(c, "source.xss", "problem", "request data becomes template.%s, which html/template doesn't escape: anyone can put a script in the page; let the template escape it", n.Obj().Name())
				}
			}
		}
		return
	}
	fn, _ := typeutil.Callee(info, c).(*types.Func)
	if fn == nil {
		return
	}
	key := funcKey(fn)
	switch key {
	case webPkg + ".H":
		s.massAssign(c)
		return
	case "os/exec.Command", "os/exec.CommandContext":
		s.exec(c, key)
		return
	case dbPkg + ".Find", dbPkg + ".Q.Find":
		s.owner(c, key, authz)
		return
	}
	sk, ok := sinks[key]
	if !ok {
		return
	}
	for _, i := range sinkArgs(sk.args, len(c.Args)) {
		a := c.Args[i]
		k := s.flow.of(a)
		switch {
		case k&fromRequest != 0:
			s.reportSink(c, a, sk.rule, fn.Name())
			return
		case sk.rule == "source.sql" && k&built != 0:
			s.report(c, "source.sql", "warning", "%s gets SQL built from strings that aren't constants: if one can come from a user, it can change the query; pass values as arguments (?), and choose column names from a list", fn.Name())
			return
		}
	}
}

func sinkArgs(args []int, n int) []int {
	var out []int
	for j, i := range args {
		if i == -1 {
			for k := args[j-1] + 1; k < n; k++ {
				out = append(out, k)
			}
			continue
		}
		if i < n {
			out = append(out, i)
		}
	}
	return out
}

func (s *scan) reportSink(c *ast.CallExpr, arg ast.Expr, rule, name string) {
	switch rule {
	case "source.sql":
		s.report(c, rule, "problem", "%s gets SQL built from request data: a client can change the query (SQL injection); pass values as arguments (?), and choose column names from a list", name)
	case "source.xss":
		s.report(c, rule, "problem", "%s writes request data as HTML, without escaping: anyone can put a script in the page; render it as text, or sanitize it", name)
	case "source.redirect":
		if s.ownURL(arg) || s.checksURL(arg) {
			return
		}
		s.report(c, rule, "problem", "%s redirects to a URL from the request: a link to your site can send visitors to any other (phishing); redirect only to your own paths (check that it starts with / and not //), or to a route by name", name)
	case "source.path":
		s.report(c, rule, "warning", "%s opens a file path from the request: ../ can reach any file the app can; keep the name only (filepath.Base) inside a known folder, or use os.Root", name)
	case "source.ssrf":
		s.report(c, rule, "warning", "%s requests a URL from the request: a client can make the app call internal services (169.254.169.254, localhost); allow only the hosts you expect", name)
	}
}

// ownURL reports whether a redirect's URL starts with a constant that
// keeps it on a known site: "/posts/"+id, fmt.Sprintf("/login?%s", q),
// "https://example.com/"+path.
func (s *scan) ownURL(e ast.Expr) bool {
	for {
		switch x := ast.Unparen(e).(type) {
		case *ast.BinaryExpr:
			if x.Op != token.ADD {
				return false
			}
			e = x.X
			continue
		case *ast.CallExpr:
			if fn, _ := typeutil.Callee(s.pass.TypesInfo, x).(*types.Func); fn != nil && funcKey(fn) == "fmt.Sprintf" && len(x.Args) > 0 {
				e = x.Args[0]
				continue
			}
		}
		break
	}
	prefix, ok := constString(s.pass.TypesInfo, e)
	if !ok {
		return false
	}
	if len(prefix) >= 2 && prefix[0] == '/' && prefix[1] != '/' && prefix[1] != '\\' {
		return true
	}
	for _, scheme := range []string{"https://", "http://"} {
		if rest, ok := strings.CutPrefix(prefix, scheme); ok && strings.IndexByte(rest, '/') > 0 {
			return true
		}
	}
	return false
}

// checksURL reports whether the function checks the redirect's URL
// first: strings.HasPrefix(next, "//") (or CutPrefix), or url.Parse of it
// with a look at the result's Host, Scheme or IsAbs.
func (s *scan) checksURL(arg ast.Expr) bool {
	info := s.pass.TypesInfo
	targets := s.refs(arg, true)
	if len(targets) == 0 {
		return false
	}
	parsed := map[types.Object]bool{} // url.Parse's results of a target
	ast.Inspect(s.node, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) != 1 || len(as.Lhs) == 0 {
			return true
		}
		c, ok := ast.Unparen(as.Rhs[0]).(*ast.CallExpr)
		if !ok || len(c.Args) == 0 {
			return true
		}
		if fn, _ := typeutil.Callee(info, c).(*types.Func); fn != nil && (funcKey(fn) == "net/url.Parse" || funcKey(fn) == "net/url.ParseRequestURI") {
			if obj := baseObject(info, as.Lhs[0]); obj != nil && (targets[obj] || s.refsAny(c.Args[0], targets)) {
				parsed[obj] = true
			}
		}
		return true
	})
	checked := false
	ast.Inspect(s.node, func(n ast.Node) bool {
		if checked {
			return false
		}
		switch n := n.(type) {
		case *ast.CallExpr:
			fn, _ := typeutil.Callee(info, n).(*types.Func)
			if fn == nil || len(n.Args) != 2 {
				return true
			}
			switch funcKey(fn) {
			case "strings.HasPrefix", "strings.CutPrefix":
				if p, ok := constString(info, n.Args[1]); ok && (p == "//" || p == "/\\") && s.refsAny(n.Args[0], targets) {
					checked = true
				}
			}
		case *ast.SelectorExpr:
			switch n.Sel.Name {
			case "Host", "Hostname", "IsAbs", "Scheme":
				if obj := baseObject(info, n.X); obj != nil && parsed[obj] {
					checked = true
				}
			}
		}
		return true
	})
	return checked
}

// refs are the variables e reads (those carrying request data, with
// tainted).
func (s *scan) refs(e ast.Expr, tainted bool) map[types.Object]bool {
	out := map[types.Object]bool{}
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if obj, ok := s.pass.TypesInfo.Uses[id].(*types.Var); ok && (!tainted || s.flow.vars[obj]&fromRequest != 0 || s.flow.inputs[obj]) {
				out[obj] = true
			}
		}
		return true
	})
	return out
}

// refsAny reports whether e reads one of the variables.
func (s *scan) refsAny(e ast.Expr, vars map[types.Object]bool) bool {
	for obj := range s.refs(e, false) {
		if vars[obj] {
			return true
		}
	}
	return false
}

// shells run their -c argument as a command line.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "cmd": true, "cmd.exe": true, "powershell": true, "pwsh": true}

func (s *scan) exec(c *ast.CallExpr, key string) {
	args := c.Args
	if key == "os/exec.CommandContext" {
		if len(args) == 0 {
			return
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return
	}
	if s.flow.of(args[0])&fromRequest != 0 {
		s.report(c, "source.exec", "problem", "the program to run comes from the request: a client can run any command; choose it from a list")
		return
	}
	prog, _ := constString(s.pass.TypesInfo, args[0])
	for _, a := range args[1:] {
		if v, ok := constString(s.pass.TypesInfo, a); ok && v == "--" && !shells[prog] {
			return // what follows isn't an option
		}
		if s.flow.of(a)&fromRequest == 0 {
			continue
		}
		if shells[prog] {
			s.report(c, "source.exec", "problem", "request data in a %s command line: a client can run any command (; rm -rf …); run the program itself, with the value as one argument", prog)
		} else {
			s.report(c, "source.exec", "warning", "request data as an argument of %s: a value starting with - becomes an option; check it, and put -- before it", cmpOr(prog, "the command"))
		}
		return
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// owner finds a record loaded by an ID from the request, with no access
// check in the function, and no condition on the query.
func (s *scan) owner(c *ast.CallExpr, key string, authz bool) {
	if authz || len(c.Args) == 0 {
		return
	}
	id := c.Args[len(c.Args)-1]
	if s.flow.of(id)&(fromRequest|idFromRequest) == 0 {
		return
	}
	if key == dbPkg+".Q.Find" && s.scoped(receiver(c)) {
		return
	}
	s.report(c, "source.owner", "warning", "loads a record by an ID from the request, and checks nobody's access to it: any logged-in user can open any record by changing the ID; check it (rbac.Authorize, a policy), or scope the query to the user (Where); fine when every user may see every record")
}

// scoped reports whether a query has conditions: db.Query[T](ctx).Where(…).
func (s *scan) scoped(q ast.Expr) bool {
	for q != nil {
		c, ok := ast.Unparen(q).(*ast.CallExpr)
		if !ok {
			return false
		}
		fn, _ := typeutil.Callee(s.pass.TypesInfo, c).(*types.Func)
		if fn != nil {
			switch fn.Name() {
			case "Where", "WhereRaw", "Scope", "Join", "WhereHas", "OrWhere":
				return true
			}
		}
		q = receiver(c)
	}
	return false
}

// massAssign finds web.H given a handler whose input is a model.
func (s *scan) massAssign(c *ast.CallExpr) {
	fun := ast.Unparen(c.Fun)
	if ix, ok := fun.(*ast.IndexListExpr); ok {
		fun = ix.X
	} else if ix, ok := fun.(*ast.IndexExpr); ok {
		fun = ix.X
	}
	var id *ast.Ident
	switch f := fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	}
	inst, ok := s.pass.TypesInfo.Instances[id]
	if !ok || inst.TypeArgs.Len() == 0 {
		return
	}
	if model := modelName(inst.TypeArgs.At(0)); model != "" {
		s.report(c, "source.mass-assign", "warning", "the handler's input is the model %s: a client can set any of its fields (ID, owner, role…); bind into a struct with only the fields a form may change, then copy them", model)
	}
}

// modelName is t's name when it's a model (it embeds db.Model,
// db.Timestamps or db.SoftDeletes), else "".
func modelName(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return ""
	}
	st, ok := n.Underlying().(*types.Struct)
	if !ok {
		return ""
	}
	for f := range st.Fields() {
		if f.Embedded() && (isNamed(f.Type(), dbPkg, "Model", true) || isNamed(f.Type(), dbPkg, "Timestamps", true) || isNamed(f.Type(), dbPkg, "SoftDeletes", true)) {
			return n.Obj().Name()
		}
	}
	return ""
}

// tlsLiteral finds tls.Config{InsecureSkipVerify: true}.
func (s *scan) tlsLiteral(cl *ast.CompositeLit) {
	if !isNamed(s.pass.TypesInfo.TypeOf(cl), "crypto/tls", "Config", true) {
		return
	}
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "InsecureSkipVerify" && !s.isFalse(kv.Value) {
			s.report(kv, "source.tls", "problem", "InsecureSkipVerify: TLS connections accept any certificate, so anyone on the network can read and change them; trust the server's authority instead (RootCAs)")
		}
	}
}

func (s *scan) tlsAssign(a *ast.AssignStmt) {
	for i, l := range a.Lhs {
		sel, ok := l.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "InsecureSkipVerify" || !isNamed(s.pass.TypesInfo.TypeOf(sel.X), "crypto/tls", "Config", true) {
			continue
		}
		if i < len(a.Rhs) && !s.isFalse(a.Rhs[i]) {
			s.report(sel, "source.tls", "problem", "InsecureSkipVerify: TLS connections accept any certificate, so anyone on the network can read and change them; trust the server's authority instead (RootCAs)")
		}
	}
}

func (s *scan) isFalse(e ast.Expr) bool {
	tv, ok := s.pass.TypesInfo.Types[e]
	return ok && tv.Value != nil && tv.Value.String() == "false"
}

// secretWords, in a name, say it holds a secret; contextWords
// (session, reset…) do with a last word such as id, token or code.
var (
	secretWords  = map[string]bool{"token": true, "secret": true, "password": true, "passwd": true, "nonce": true, "salt": true, "otp": true, "csrf": true, "apikey": true, "passcode": true}
	contextWords = map[string]bool{"session": true, "reset": true, "verify": true, "verification": true, "recovery": true, "invite": true, "api": true, "auth": true}
	idWords      = map[string]bool{"id": true, "key": true, "code": true, "pin": true, "token": true, "secret": true}
)

// secretName reports whether a name holds a secret: resetToken,
// session_id, apiKey (not sessionCount, footprintSize).
func secretName(name string) bool {
	w := words(name)
	for _, x := range w {
		if secretWords[x] {
			return true
		}
	}
	for i, x := range w {
		if contextWords[x] && i+1 < len(w) && idWords[w[len(w)-1]] {
			return true
		}
	}
	return false
}

// randAssign finds math/rand's values given to a secret's name.
func (s *scan) randAssign(lhs, rhs []ast.Expr) {
	for i, l := range lhs {
		if i >= len(rhs) {
			break
		}
		name := ""
		switch l := l.(type) {
		case *ast.Ident:
			name = l.Name
		case *ast.SelectorExpr:
			name = l.Sel.Name
		}
		if name == "" || !secretName(name) {
			continue
		}
		if at := s.mathRand(rhs[i]); at != nil {
			s.report(at, "source.rand", "warning", "%s is made with math/rand, which is predictable: use crypto/rand (rand.Text() for a token)", name)
		}
	}
}

// randRead finds math/rand's Read, which fills bytes for secrets
// (crypto/rand's job; it's deprecated).
func (s *scan) randRead(c *ast.CallExpr) {
	if fn, _ := typeutil.Callee(s.pass.TypesInfo, c).(*types.Func); fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "math/rand" && fn.Name() == "Read" {
		s.report(c, "source.rand", "warning", "math/rand's Read makes predictable bytes, and is deprecated: use crypto/rand.Read (or rand.Text() for a token)")
	}
}

// mathRand is e's call of math/rand (or math/rand/v2), else nil.
func (s *scan) mathRand(e ast.Expr) *ast.CallExpr {
	var call *ast.CallExpr
	ast.Inspect(e, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || call != nil {
			return call == nil
		}
		if fn, _ := typeutil.Callee(s.pass.TypesInfo, c).(*types.Func); fn != nil && fn.Pkg() != nil {
			if p := fn.Pkg().Path(); p == "math/rand" || p == "math/rand/v2" {
				call = c
			}
		}
		return true
	})
	return call
}
