// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"

	"anetos.dev/anetos/internal/naming"
)

// AdminModule is the admin interface's module.
const AdminModule = "anetos.dev/anetos/admin"

// AdminResult is what [MakeAdmin] did.
type AdminResult struct {
	// Created are the files written, relative to the project.
	Created []string
	// Wired says main.go now calls setupAdmin; when false, the app must
	// call it itself.
	Wired bool
	// RBAC says setupAdmin sets up roles and permissions; false when the
	// app already does (rbac.New).
	RBAC bool
	// Env are the settings files the ADMIN_* settings were added to.
	Env []string
	// Users says it wrote app/admin/users.go, the users resource.
	Users bool
	// Disabled and SessionKey say the User model has DisabledAt and
	// SessionKey (make:auth since AD1b): disabling and logging out
	// everywhere work.
	Disabled, SessionKey bool
	// Banner says the app's layout (views/layout.templ) now shows
	// admin.Banner.
	Banner bool
	// Audit and AI say the app keeps an audit log (audit.New) and
	// tracks AI usage (TrackUsage): setupAdmin adds the activity and the
	// AI usage widget.
	Audit, AI bool
}

// adminCall is what make:admin puts in setup in place of make:auth's call.
const adminCall = `	// Accounts (anetos make:auth): registration, login with a password,
	// Google or GitHub, email verification, password reset and API tokens.
	a, err := setupAuth(app, srv.Router(), sessions)
	if err != nil {
		return nil, err
	}
	// The admin interface (anetos make:admin) at ADMIN_PATH (/admin).
	if err := setupAdmin(app, srv.Router(), sessions, a); err != nil {
		return nil, err
	}
`

// adminSettings are the admin's settings.
const adminSettings = `
# The admin interface (anetos make:admin): its path (default /admin), or
# a host of its own (admin.example.com, at its root unless ADMIN_PATH is
# set).
ADMIN_PATH=
ADMIN_HOST=
`

// AdminInAPI is why make:admin refuses in an API project ([RefuseAPI]).
const AdminInAPI = "the admin is for web projects (anetos new --stack=web)"

// MakeAdmin writes the admin interface's glue into the project at root:
// setupAdmin (admin.go), the app/admin package, with no resources yet,
// and a test (admin_test.go).
// It needs make:auth's users and setupAuth. When setup in main.go calls
// setupAuth as make:auth wrote it, it calls setupAdmin after it. It adds
// the ADMIN_* settings, empty, to .env and .env.example. The module
// itself is the command's to add (go get). It refuses in an API project.
func MakeAdmin(root string) (AdminResult, error) {
	var res AdminResult
	if err := RefuseAPI(root, "make:admin", AdminInAPI); err != nil {
		return res, err
	}
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return res, err
	}
	mod := modfile.ModulePath(b)
	if mod == "" {
		return res, errors.New("go.mod has no module line")
	}
	rootNames, err := declared(root)
	if err != nil {
		return res, err
	}
	if !rootNames["setupAuth"] {
		return res, errors.New("no setupAuth in package main: the admin's users are make:auth's, run anetos make:auth first")
	}
	if _, err := os.Stat(filepath.Join(root, "app", "models", "user.go")); err != nil {
		return res, errors.New("no app/models/user.go: the admin's users are make:auth's, run anetos make:auth first")
	}
	if rootNames["setupAdmin"] {
		return res, errors.New("package main already declares setupAdmin")
	}
	files := [][2]string{{"setup.go.tmpl", "admin.go"}, {"resources.go.tmpl", "app/admin/admin.go"}}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f[1]))); err == nil {
			return res, fmt.Errorf("%s exists: make:admin writes nothing over the app's files", f[1])
		}
	}
	adminNames, err := declared(filepath.Join(root, "app", "admin"))
	if err != nil {
		return res, err
	}
	if adminNames["Resources"] {
		return res, errors.New("app/admin already declares Resources")
	}
	// The users resource, for make:auth's User: with what it has.
	fields, _, err := modelFields(filepath.Join(root, "app", "models"), "User")
	if err != nil {
		return res, err
	}
	has := func(name string) bool {
		return slices.ContainsFunc(fields, func(f adminField) bool { return f.Name == name })
	}
	handlerNames, err := declared(filepath.Join(root, "app", "handlers"))
	if err != nil {
		return res, err
	}
	res.Users = has("Name") && has("Email") && !adminNames["Users"] && !adminNames["UserForm"]
	res.Disabled, res.SessionKey = has("DisabledAt"), has("SessionKey")
	if res.Users {
		files = append(files, [2]string{"users.go.tmpl", "app/admin/users.go"})
	}
	res.RBAC, err = noRBAC(root)
	if err != nil {
		return res, err
	}
	// The activity, with an audit log; AI usage, with tracked usage.
	if res.Audit, err = projectCalls(root, "audit.New(", "audit.ForApp("); err != nil {
		return res, err
	}
	if res.AI, err = projectCalls(root, "TrackUsage("); err != nil {
		return res, err
	}
	// The test logs in with make:auth's test helper, and gives the
	// admin role.
	if res.RBAC && rootNames["authRegister"] && !rootNames["TestAdminAccess"] {
		files = append(files, [2]string{"test.go.tmpl", "admin_test.go"})
	}
	envFiles := []string{".env", ".env.example"}
	envBefore := map[string][]byte{}
	for _, name := range envFiles {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return res, err
		}
		envBefore[name] = b
	}
	data := struct {
		Module                                               string
		RBAC, Users, Disabled, Verified, Handlers, Audit, AI bool
	}{mod, res.RBAC, res.Users, res.Disabled, has("EmailVerifiedAt"),
		handlerNames["SendVerification"] && handlerNames["SendPasswordReset"], res.Audit, res.AI}
	out := make([][]byte, len(files))
	for i, f := range files {
		if out[i], err = render("templates/admin/"+f[0], "", data); err != nil {
			return res, err
		}
	}
	layout := filepath.Join(root, "views", "layout.templ")
	layoutBefore, layoutErr := os.ReadFile(layout)
	undo := func(err error) (AdminResult, error) {
		if res.Banner {
			_ = os.WriteFile(layout, layoutBefore, 0o644)
		}
		for _, c := range res.Created {
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(c)))
		}
		_ = os.Remove(filepath.Join(root, "app", "admin")) // if make:admin created it, it's empty
		for _, name := range res.Env {
			_ = os.WriteFile(filepath.Join(root, name), envBefore[name], 0o600)
		}
		return AdminResult{}, fmt.Errorf("%w (the files written were removed)", err)
	}
	for i, f := range files {
		if err := writeNew(filepath.Join(root, filepath.FromSlash(f[1])), out[i], 0o644); err != nil {
			return undo(err)
		}
		res.Created = append(res.Created, f[1])
	}
	for _, name := range envFiles {
		if envBefore[name] == nil {
			continue
		}
		added, err := addSettings(filepath.Join(root, name), adminSettings)
		if added || err != nil {
			res.Env = append(res.Env, name)
		}
		if err != nil {
			return undo(err)
		}
	}
	// The banner while impersonating a user, in the app's layout.
	if layoutErr == nil {
		if patched, ok := addBanner(layoutBefore); ok {
			if err := os.WriteFile(layout, patched, 0o644); err != nil {
				_ = os.WriteFile(layout, layoutBefore, 0o644)
				return undo(err)
			}
			res.Banner = true
		}
	}
	main := filepath.Join(root, "main.go")
	src, err := os.ReadFile(main)
	switch {
	case err == nil && bytes.Count(src, []byte(authCall)) == 1:
		if err := os.WriteFile(main, bytes.Replace(src, []byte(authCall), []byte(adminCall), 1), 0o644); err != nil {
			_ = os.WriteFile(main, src, 0o644) // it may be half written
			return undo(err)
		}
		res.Wired = true
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return undo(err)
	}
	return res, nil
}

// bodyTag is the <body> line of an anetos new project's layout.
var bodyTag = regexp.MustCompile(`(?m)^([ \t]*)<body[^\n]*>[ \t]*\n`)

// addBanner adds admin.Banner after <body> in a templ layout, and its
// import, and reports whether it did.
func addBanner(src []byte) ([]byte, bool) {
	s := string(src)
	if strings.Contains(s, "admin.Banner()") {
		return nil, false
	}
	loc := bodyTag.FindStringSubmatchIndex(s)
	imp := strings.Index(s, "import (\n")
	if loc == nil || imp < 0 || strings.Count(s, "<body") != 1 {
		return nil, false
	}
	indent := s[loc[2]:loc[3]]
	s = s[:loc[1]] + indent + "\t@admin.Banner() // while impersonating a user (anetos make:admin)\n" + s[loc[1]:]
	s = s[:imp] + "import (\n\t\"anetos.dev/anetos/admin\"\n" + s[imp+len("import (\n"):]
	return []byte(s), true
}

// noRBAC reports whether no Go file of the project calls rbac.New.
func noRBAC(root string) (bool, error) {
	found, err := projectCalls(root, "rbac.New(", "rbac.ForApp(")
	return !found, err
}

// projectCalls reports whether a Go file of the project contains one of
// the calls (a name and the one it replaced, ForApp before v0.5).
func projectCalls(root string, calls ...string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "node_modules" || d.Name() == "tmp") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || found {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, c := range calls {
			found = found || bytes.Contains(b, []byte(c))
		}
		return nil
	})
	return found, err
}

// AdminReplace returns how the project at root gets the admin module from
// a local checkout: replaced is true if its go.mod replaces the module
// already; otherwise path is the replacement to add when go.mod replaces
// the core module with a checkout that has the admin ("" if not).
func AdminReplace(root string) (path string, replaced bool, err error) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", false, err
	}
	f, err := modfile.Parse("go.mod", b, nil)
	if err != nil {
		return "", false, err
	}
	var core string
	for _, r := range f.Replace {
		switch r.Old.Path {
		case AdminModule:
			return "", true, nil
		case "anetos.dev/anetos":
			if r.New.Version == "" {
				core = r.New.Path
			}
		}
	}
	if core == "" {
		return "", false, nil
	}
	dir := core
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "admin", "go.mod")); err != nil {
		return "", false, nil //nolint:nilerr // an older checkout: the module is downloaded
	}
	return strings.TrimSuffix(filepath.ToSlash(core), "/") + "/admin", false, nil
}

// adminField is a field of a model, as make:admin-resource uses it.
type adminField struct {
	Name     string // the Go name
	Column   string
	FormType string // the form field's type, "" if it has none
	pointer  bool
	kind     string // string, bool, number, time, date
}

// adminResourceData is what resource.go.tmpl gets.
type adminResourceData struct {
	Module, Type, Words, Func, Name string
	A                               string // the article of Words: a or an
	Fields                          []adminField
	Columns                         []struct{ Title, Column string }
	Search                          string
	Skipped                         string
	EditLines, ApplyLines           []string
	Date                            bool
}

// sensitive are the fields make:admin-resource leaves out of lists and
// forms, by their names in snake case.
var sensitive = regexp.MustCompile(`password|passwd|secret|token|credential|api_?key|private_?key|recovery|(^|_)otp(_|$)`)

// numberTypes are the number types a form edits.
var numberTypes = []string{"int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64"}

// MakeAdminResource writes app/admin/<names>.go, the admin's resource for
// model name (a type of app/models), and adds it to Resources in
// app/admin/admin.go. It returns the files written or changed.
func MakeAdminResource(root, name string) ([]string, error) {
	t, err := typeName(name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	mod := modfile.ModulePath(b)
	list := filepath.Join(root, "app", "admin", "admin.go")
	src, err := os.ReadFile(list)
	if err != nil {
		return nil, errors.New("no app/admin/admin.go: run anetos make:admin first")
	}
	fields, embeds, err := modelFields(filepath.Join(root, "app", "models"), t)
	if err != nil {
		return nil, err
	}
	plural := naming.Plural(naming.Snake(t))
	d := adminResourceData{
		Module: mod, Type: t, Words: strings.ReplaceAll(naming.Snake(t), "_", " "), A: "a",
		Func: identifier(plural), Name: strings.ReplaceAll(plural, "_", "-"),
	}
	if strings.ContainsRune("aeio", rune(d.Words[0])) { // an issue, an order; a user, a unit
		d.A = "an"
	}
	rel := "app/admin/" + plural + ".go"
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
		return nil, fmt.Errorf("%s exists: make:admin-resource writes nothing over the app's files", rel)
	}
	names, err := declared(filepath.Join(root, "app", "admin"))
	if err != nil {
		return nil, err
	}
	if names[d.Func] || names[t+"Form"] {
		return nil, fmt.Errorf("app/admin already declares %s or %sForm", d.Func, t)
	}
	if embeds["Model"] {
		d.Columns = append(d.Columns, struct{ Title, Column string }{"ID", "id"})
	}
	var search, skipped []string
	edit := []string{"f := " + t + "Form{"}
	var after []string
	for _, f := range fields {
		if f.FormType == "" {
			skipped = append(skipped, f.Name)
			continue
		}
		if len(d.Columns) < 5 {
			d.Columns = append(d.Columns, struct{ Title, Column string }{titleOf(f.Name), f.Column})
		}
		if f.kind == "string" && len(search) < 3 {
			search = append(search, strconv.Quote(f.Column))
		}
		d.Fields = append(d.Fields, f)
		switch {
		case f.kind == "time" && f.pointer:
			after = append(after, "if m."+f.Name+" != nil {", "\tf."+f.Name+" = admin.DateTime{Time: *m."+f.Name+"}", "}")
			d.ApplyLines = append(d.ApplyLines, "m."+f.Name+" = nil", "if !in."+f.Name+".IsZero() {",
				"\tt := in."+f.Name+".Time", "\tm."+f.Name+" = &t", "}")
		case f.kind == "time":
			edit = append(edit, "\t"+f.Name+": admin.DateTime{Time: m."+f.Name+"},")
			d.ApplyLines = append(d.ApplyLines, "m."+f.Name+" = in."+f.Name+".Time")
		default:
			if f.kind == "date" {
				d.Date = true
			}
			edit = append(edit, "\t"+f.Name+": m."+f.Name+",")
			d.ApplyLines = append(d.ApplyLines, "m."+f.Name+" = in."+f.Name)
		}
	}
	if embeds["Model"] || embeds["Timestamps"] {
		d.Columns = append(d.Columns, struct{ Title, Column string }{"Created", "created_at"})
	}
	if len(d.Fields) == 0 {
		d.EditLines = []string{"return " + t + "Form{}"}
	} else {
		d.EditLines = append(append(append(edit, "}"), after...), "return f")
	}
	d.Search = strings.Join(search, ", ")
	d.Skipped = strings.Join(skipped, ", ")
	out, err := render("templates/admin/resource.go.tmpl", "", d)
	if err != nil {
		return nil, err
	}
	patched, err := addResource(src, d.Func)
	if err != nil {
		return nil, err
	}
	if err := writeNew(filepath.Join(root, filepath.FromSlash(rel)), out, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(list, patched, 0o644); err != nil {
		_ = os.Remove(filepath.Join(root, filepath.FromSlash(rel)))
		return nil, err
	}
	return []string{rel, "app/admin/admin.go"}, nil
}

// addResource adds fn to the Resources list of app/admin/admin.go, and
// formats the file.
func addResource(src []byte, fn string) ([]byte, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "admin.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("app/admin/admin.go: %w", err)
	}
	var lit *ast.CompositeLit
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if n.Name == "Resources" && i < len(vs.Values) {
					lit, _ = vs.Values[i].(*ast.CompositeLit)
				}
			}
		}
	}
	if lit == nil {
		return nil, fmt.Errorf("app/admin/admin.go has no Resources list (var Resources = []func(p *admin.Panel) error{…}): add %s to it yourself", fn)
	}
	for _, e := range lit.Elts {
		if id, ok := e.(*ast.Ident); ok && id.Name == fn {
			return src, nil // listed already
		}
	}
	// Offsets: the file set has this file alone, based at 1.
	at := int(lit.Rbrace) - 1
	head := strings.TrimRight(string(src[:at]), " \t\n")
	if n := len(lit.Elts); n > 0 {
		end := int(lit.Elts[n-1].End()) - 1
		if !strings.HasPrefix(strings.TrimLeft(head[end:], " \t\n"), ",") {
			head = head[:end] + "," + head[end:] // the last element had no comma
		}
	}
	out := head + "\n\t" + fn + ",\n" + string(src[at:])
	return format.Source([]byte(out))
}

// titleOf turns a Go name into a column title: CreatedAt → "Created at".
func titleOf(name string) string {
	s := strings.ReplaceAll(naming.Snake(name), "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

// modelFields reads the fields of type t in the models package at dir,
// and which of db.Model, db.Timestamps and db.SoftDeletes it embeds.
func modelFields(dir, t string) ([]adminField, map[string]bool, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, nil, err
	}
	var st *ast.StructType
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if ts, ok := n.(*ast.TypeSpec); ok && ts.Name.Name == t {
				st, _ = ts.Type.(*ast.StructType)
			}
			return st == nil
		})
		if st != nil {
			break
		}
	}
	if st == nil {
		return nil, nil, fmt.Errorf("no struct type %s in app/models (anetos make:model %s writes one)", t, t)
	}
	embeds := map[string]bool{}
	var out []adminField
	for _, fld := range st.Fields.List {
		var tag reflect.StructTag
		if fld.Tag != nil {
			if s, err := strconv.Unquote(fld.Tag.Value); err == nil {
				tag = reflect.StructTag(s)
			}
		}
		dbTag, hasTag := tag.Lookup("db")
		col, opts, _ := strings.Cut(dbTag, ",")
		if col == "-" {
			continue
		}
		if len(fld.Names) == 0 {
			if sel, ok := fld.Type.(*ast.SelectorExpr); ok && !hasTag {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "db" {
					embeds[sel.Sel.Name] = true
				}
			}
			continue
		}
		for _, n := range fld.Names {
			if !n.IsExported() {
				continue
			}
			f := adminField{Name: n.Name, Column: col}
			if f.Column == "" {
				f.Column = naming.Snake(n.Name)
			}
			o := "," + opts + ","
			if strings.Contains(o, ",pk,") || strings.Contains(o, ",json,") || strings.Contains(o, ",readonly,") ||
				sensitive.MatchString(naming.Snake(n.Name)) || slices.Contains([]string{"id", "created_at", "updated_at", "deleted_at"}, f.Column) {
				out = append(out, f) // no form type: left out
				continue
			}
			typ := fld.Type
			if star, ok := typ.(*ast.StarExpr); ok {
				f.pointer, typ = true, star.X
			}
			switch x := typ.(type) {
			case *ast.Ident:
				switch {
				case x.Name == "string":
					f.kind = "string"
				case x.Name == "bool":
					f.kind = "bool"
				case slices.Contains(numberTypes, x.Name):
					f.kind = "number"
				}
				if f.kind != "" {
					f.FormType = x.Name
				}
			case *ast.SelectorExpr:
				if pkg, ok := x.X.(*ast.Ident); ok {
					switch pkg.Name + "." + x.Sel.Name {
					case "time.Time":
						f.kind, f.FormType = "time", "admin.DateTime"
					case "anetos.Date":
						f.kind, f.FormType = "date", "anetos.Date"
					}
				}
			}
			if f.pointer && f.FormType != "" && f.kind != "time" {
				f.FormType = "*" + f.FormType
			}
			out = append(out, f)
		}
	}
	return out, embeds, nil
}
