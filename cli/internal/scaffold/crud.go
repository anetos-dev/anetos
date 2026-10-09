// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/modfile"

	"anetos.dev/anetos/internal/naming"
)

// CrudKinds are the field types make:crud knows.
var CrudKinds = []string{"string", "text", "email", "int", "float", "bool", "date"}

// crudField is a field of a make:crud model.
type crudField struct {
	Name     string // the column and the form's key: due_on
	Go       string // the struct field: DueOn
	Label    string // for people: Due on
	Kind     string // one of CrudKinds
	Optional bool   // may be empty
	Unique   bool   // no two rows share it
}

// GoType is the field's Go type.
func (f crudField) GoType() string {
	switch f.Kind {
	case "int":
		return "int64"
	case "float":
		return "float64"
	case "bool":
		return "bool"
	case "date":
		return "anetos.Date"
	}
	return "string"
}

// Column is the field's line in the migration.
func (f crudField) Column() string {
	var c string
	switch f.Kind {
	case "string", "email":
		c = fmt.Sprintf("t.String(%q, 255)", f.Name)
	case "text":
		c = fmt.Sprintf("t.Text(%q)", f.Name)
	case "int":
		c = fmt.Sprintf("t.BigInteger(%q)", f.Name)
	case "float":
		c = fmt.Sprintf("t.Float(%q)", f.Name)
	case "bool":
		c = fmt.Sprintf("t.Boolean(%q)", f.Name)
	case "date":
		c = fmt.Sprintf("t.Date(%q)", f.Name)
		if f.Optional {
			c += ".Nullable()" // the zero anetos.Date is NULL
		}
	}
	if f.Unique {
		c += ".Unique()"
	}
	return c
}

// Rules is the field's validate tag.
func (f crudField) Rules(table string) string {
	var rules []string
	required := !f.Optional && (f.Kind == "string" || f.Kind == "text" || f.Kind == "email" || f.Kind == "date")
	if required {
		rules = append(rules, "required")
	}
	switch f.Kind {
	case "email":
		rules = append(rules, "email", "max:255")
	case "string":
		rules = append(rules, "max:255")
	case "text":
		rules = append(rules, "max:10000")
	}
	if f.Unique {
		rules = append(rules, "unique:"+table+","+f.Name+",ID")
	}
	return strings.Join(rules, "|")
}

// Sample is a value of the field for the test's form, quoted.
func (f crudField) Sample() string {
	switch f.Kind {
	case "email":
		return `"ada@example.com"`
	case "int":
		return `"42"`
	case "float":
		return `"1.5"`
	case "bool":
		return `"1"`
	case "date":
		return `"2026-10-06"`
	}
	return fmt.Sprintf("%q", "Example "+strings.ToLower(f.Label))
}

// OptionalDate reports whether the field is an optional date, which an
// API answers as null when empty.
func (f crudField) OptionalDate() bool { return f.Kind == "date" && f.Optional }

// ResponseType is the field's type in an API's response struct.
func (f crudField) ResponseType() string {
	if f.OptionalDate() {
		return "*anetos.Date"
	}
	return f.GoType()
}

// Filterable reports whether an API's list filters on the field
// (?name=…): not texts, which need search, nor floats, which aren't
// compared for equality.
func (f crudField) Filterable() bool { return f.Kind != "text" && f.Kind != "float" }

// Sortable reports whether an API's list sorts by the field: not texts.
func (f crudField) Sortable() bool { return f.Kind != "text" }

// sample is a value of the field for tests, as text, and another one.
func (f crudField) sample() (string, string) {
	switch f.Kind {
	case "email":
		return "ada@example.com", "bob@example.com"
	case "int":
		return "42", "43"
	case "float":
		return "1.5", "2.5"
	case "bool":
		return "true", "false"
	case "date":
		return "2026-10-06", "2026-10-07"
	}
	return "Example " + strings.ToLower(f.Label), "Other " + strings.ToLower(f.Label)
}

// JSONSample is the field's sample as a Go value for a JSON body.
func (f crudField) JSONSample() string {
	v, _ := f.sample()
	switch f.Kind {
	case "int", "float", "bool":
		return v
	}
	return fmt.Sprintf("%q", v)
}

// FilterQuery and OtherFilterQuery are an API list's filter on the field
// with its sample, and with another value.
func (f crudField) FilterQuery() string {
	v, _ := f.sample()
	return url.Values{f.Name: {v}}.Encode()
}

// OtherFilterQuery: see FilterQuery.
func (f crudField) OtherFilterQuery() string {
	_, v := f.sample()
	return url.Values{f.Name: {v}}.Encode()
}

// crudData is what the make:crud templates see.
type crudData struct {
	Module   string
	Model    string // Post
	Helper   string // post: the views' helpers are postFields and postYesNo
	Plural   string // Posts: the handler type
	Table    string // posts
	Key      string // posts: the locale's top key and its file's name
	Path     string // posts: the URL path and the routes' names
	Words    string // post: in sentences
	PluralW  string // posts: in sentences
	OneW     string // Post: starting a sentence
	TitleW   string // Posts: the list's title
	ID       string // the migration's ID
	Fields   []crudField
	TitleF   *crudField // the field that names a row (the first string), if any
	HasDate  bool
	HasNum   bool // int or float fields: strconv in the form
	HasEmail bool
	HasBool  bool // bool fields: the views' YesNo helper
}

// Required are the quoted names of the fields the form requires, for the
// test.
func (d crudData) Required() string {
	var names []string
	for _, f := range d.Fields {
		if strings.HasPrefix(f.Rules(d.Table), "required") {
			names = append(names, fmt.Sprintf("%q", f.Name))
		}
	}
	return strings.Join(names, ", ")
}

// SeeSample is the quoted sample of the field that names a row, which its
// page shows; "" without one.
func (d crudData) SeeSample() string {
	if d.TitleF == nil {
		return ""
	}
	return d.TitleF.Sample()
}

// SortValues are the values of an API list's ?sort=: id, created_at,
// updated_at and the sortable fields, each with - for descending.
func (d crudData) SortValues() string {
	names := []string{"id", "created_at", "updated_at"}
	for _, f := range d.Fields {
		if f.Sortable() {
			names = append(names, f.Name)
		}
	}
	var vals []string
	for _, n := range names {
		vals = append(vals, n, "-"+n)
	}
	return strings.Join(vals, ",")
}

// FilterF is the first field an API's list filters on, if any.
func (d crudData) FilterF() *crudField {
	for i, f := range d.Fields {
		if f.Filterable() {
			return &d.Fields[i]
		}
	}
	return nil
}

// FilterExample is a filter of an API's list, for its comment; "" without
// filterable fields.
func (d crudData) FilterExample() string {
	if f := d.FilterF(); f != nil {
		v, _ := f.sample()
		return f.Name + "=" + strings.ReplaceAll(v, " ", "+") // readable: @ stays
	}
	return ""
}

// ListFields are the fields shown in the list: the first five that
// aren't long text.
func (d crudData) ListFields() []crudField {
	var out []crudField
	for _, f := range d.Fields {
		if f.Kind != "text" && len(out) < 5 {
			out = append(out, f)
		}
	}
	return out
}

// CrudResult is what [MakeCrud] did.
type CrudResult struct {
	// Created are the files written, relative to the project.
	Created []string
	// Routed says routes/web.go's Register now adds the routes; when
	// false, the app calls routes.<Plural> itself.
	Routed bool
	// Linked says the layout's nav now links to the list.
	Linked bool
	// Plural is the name of the handler type and of the routes function.
	Plural string
	// Path is the list's URL path (in an API project, under /api/v1).
	Path string
	// API says the project is an API project ([IsAPI]): JSON endpoints,
	// routed in routes/api.go's api group.
	API bool
	// Kit is the design kit whose views/ui MakeCrud wrote first, in a
	// project made before v0.5, which had none (the pages call it); ""
	// when the project had views/ui. Its files are in Created.
	Kit string
}

var fieldRe = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// parseFields reads make:crud's field arguments: name:type, then
// :optional or :unique.
func parseFields(args []string) ([]crudField, error) {
	if len(args) == 0 {
		return nil, errors.New("give the model's fields as name:type, such as title:string body:text (types: " + strings.Join(CrudKinds, ", ") + ")")
	}
	var fields []crudField
	seen := map[string]bool{}
	for _, arg := range args {
		parts := strings.Split(arg, ":")
		if len(parts) < 2 {
			return nil, fmt.Errorf("%q: give the field as name:type, such as title:string", arg)
		}
		name := parts[0]
		if !fieldRe.MatchString(name) {
			return nil, fmt.Errorf("%q: a field's name is lower case words of letters and digits joined by single _, starting with a letter (due_on)", name)
		}
		// The checks go by the Go name, which the code uses: a_1 and a1
		// are both A1.
		switch goName(name) {
		case "ID", "CreatedAt", "UpdatedAt", "DeletedAt", "Model":
			return nil, fmt.Errorf("%q: the model has it already (db.Model)", name)
		}
		if seen[goName(name)] {
			return nil, fmt.Errorf("%q: given twice (or as another name with the same Go name, %s)", name, goName(name))
		}
		seen[goName(name)] = true
		f := crudField{Name: name, Go: goName(name), Label: label(name), Kind: parts[1]}
		if !slices.Contains(CrudKinds, f.Kind) {
			return nil, fmt.Errorf("%q: unknown type %q; make:crud knows %s (add other columns by hand)", arg, f.Kind, strings.Join(CrudKinds, ", "))
		}
		for _, m := range parts[2:] {
			switch m {
			case "optional":
				f.Optional = true
			case "unique":
				switch f.Kind {
				case "text", "bool", "int", "float":
					// Texts can't have a unique index on MySQL; blank
					// numbers are 0, which only one row could have.
					return nil, fmt.Errorf("%q: a %s can't be unique here; add the index by hand if you need it", arg, f.Kind)
				}
				f.Unique = true
			default:
				return nil, fmt.Errorf("%q: unknown %q; after the type come optional and unique", arg, m)
			}
		}
		if f.Unique && f.Optional && f.Kind != "date" {
			// Empty values are "", which only one row could have.
			return nil, fmt.Errorf("%q: an optional %s can't be unique", arg, f.Kind)
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// initialisms are written in capitals in Go names.
var initialisms = map[string]string{"id": "ID", "url": "URL", "uri": "URI", "api": "API", "html": "HTML", "http": "HTTP", "json": "JSON", "ip": "IP", "uuid": "UUID", "sql": "SQL", "sku": "SKU"}

// goName turns a column's name into a field's: due_on → DueOn, image_url
// → ImageURL.
func goName(name string) string {
	var b strings.Builder
	for w := range strings.SplitSeq(name, "_") {
		if w == "" {
			continue
		}
		if up, ok := initialisms[w]; ok {
			b.WriteString(up)
		} else {
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return b.String()
}

// listParams are the query parameters of an API project's list
// endpoint, besides the filters, which have the fields' names.
// By Go name: the fields of <Model>List.
var listParams = map[string]bool{"Page": true, "PerPage": true, "Sort": true}

// reservedTables are the tables the framework, its drivers and make:auth
// create.
var reservedTables = map[string]bool{
	"users": true, "api_tokens": true, "social_accounts": true, "sessions": true, "cache": true,
	"jobs": true, "failed_jobs": true, "migrations": true, "audit_log": true, "audit_bulk": true,
	"audit_bulk_items": true, "rbac_roles": true, "rbac_grants": true, "ai_conversations": true,
	"ai_messages": true, "ai_usage": true, "postmark_suppressions": true,
}

// words turns a name into words for sentences: api_key → API key,
// blog_post → blog post.
func words(name string) string {
	ws := strings.FieldsFunc(name, func(r rune) bool { return r == '_' })
	for i, w := range ws {
		if up, ok := initialisms[w]; ok {
			ws[i] = up
		}
	}
	return strings.Join(ws, " ")
}

// lowerCamel turns a name into an unexported Go name: blog_post →
// blogPost, api_key → apiKey.
func lowerCamel(name string) string {
	first, rest, _ := strings.Cut(name, "_")
	return first + goName(rest)
}

// label turns a column's name into words: due_on → Due on, image_url →
// Image URL.
func label(name string) string {
	s := words(name)
	return strings.ToUpper(s[:1]) + s[1:]
}

// MakeCrud writes a model with its table, and the pages to list, show,
// create, edit and delete its rows: the model, the migration, the
// handlers, the views, the routes, their English text and a test. It
// writes nothing if one of the files exists or a name it declares is
// taken; if a write fails, it removes what it wrote. When routes/web.go's
// Register has the pages group of an anetos new project, it adds the
// routes there; when the layout's nav has navLink, it links to the list.
// In an API project ([IsAPI]), it writes JSON endpoints instead (the
// model, the migration, the handlers, the routes and a test), added to
// routes/api.go's api group.
func MakeCrud(root, name string, args []string, now time.Time) (CrudResult, error) {
	res := CrudResult{API: IsAPI(root)}
	model, err := typeName(name)
	if err != nil {
		return res, err
	}
	fields, err := parseFields(args)
	if err != nil {
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
	dirs := []string{"app/models", "app/handlers", "views", "routes", "database/migrations", "locales"}
	if res.API {
		dirs = []string{"app/models", "app/handlers", "routes", "database/migrations"}
	}
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			return res, fmt.Errorf("no %s directory: make:crud adds to a project made with anetos new", dir)
		}
	}
	snake := naming.Snake(model)
	table := naming.Plural(snake)
	if m, ok := existingMigration(filepath.Join(root, "database", "migrations"), "_create_"+table+"_table.go"); ok {
		return res, fmt.Errorf("database/migrations/%s exists: the app has a %s table", m, table)
	}
	ts := now.UTC().Truncate(time.Second)
	if last, ok := latestMigration(filepath.Join(root, "database", "migrations")); ok && !ts.After(last) {
		ts = last.Add(time.Second)
	}
	d := crudData{
		Module: mod, Model: model, Helper: lowerCamel(snake), Plural: goName(table),
		Table: table, Key: table, Path: strings.ReplaceAll(table, "_", "-"),
		Words: words(snake), PluralW: words(table), OneW: label(snake), TitleW: label(table),
		ID: ts.Format(idLayout) + "_create_" + table + "_table", Fields: fields,
	}
	if d.Plural == model {
		return res, fmt.Errorf("%s's plural is %s too: give a name whose plural differs, such as %sItem", model, d.Plural, model)
	}
	if reservedTables[table] {
		return res, fmt.Errorf("the %s table is the framework's (or make:auth's): choose another name", table)
	}
	if res.API {
		for _, f := range fields {
			if listParams[f.Go] {
				return res, fmt.Errorf("%q: the list endpoint's query has page, per_page and sort (%s in Go); name the field otherwise", f.Name, f.Go)
			}
		}
	}
	for i, f := range fields {
		switch f.Kind {
		case "date":
			d.HasDate = true
		case "int", "float":
			d.HasNum = true
		case "email":
			d.HasEmail = true
		case "bool":
			d.HasBool = true
		}
		if d.TitleF == nil && !f.Optional && (f.Kind == "string" || f.Kind == "email") {
			d.TitleF = &fields[i]
		}
	}
	res.Plural, res.Path = d.Plural, "/"+d.Path
	if res.API {
		res.Path = "/api/v1/" + d.Path
	}

	files := [][2]string{
		{"model.go.tmpl", "app/models/" + snake + ".go"},
		{"migration.go.tmpl", "database/migrations/" + d.ID + ".go"},
		{"handlers.go.tmpl", "app/handlers/" + table + ".go"},
		{"views.templ.tmpl", "views/" + table + ".templ"},
		{"routes.go.tmpl", "routes/" + table + ".go"},
		{"test.go.tmpl", table + "_test.go"},
		{"locale.yaml.tmpl", "locales/en/" + table + ".yaml"},
	}
	if res.API {
		files = [][2]string{
			{"model.go.tmpl", "app/models/" + snake + ".go"},
			{"migration.go.tmpl", "database/migrations/" + d.ID + ".go"},
			{"api/handlers.go.tmpl", "app/handlers/" + table + ".go"},
			{"api/routes.go.tmpl", "routes/" + table + ".go"},
			{"api/test.go.tmpl", table + "_test.go"},
		}
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f[1]))); err == nil {
			return res, fmt.Errorf("%s exists: make:crud writes nothing over the app's files", f[1])
		}
	}
	names := map[string][]string{
		"app/models":   {model, model + "Cols", model + "Rels"},
		"app/handlers": {d.Plural, model + "List", model + "ID", model + "Input"},
		"views":        {d.Plural + "Page", model + "Page", model + "Form", d.Helper + "Fields", d.Helper + "YesNo"},
		"routes":       {d.Plural},
		"":             {"Test" + d.Plural},
	}
	if res.API {
		names["app/handlers"] = append(names["app/handlers"], model+"Response", d.Helper+"Response", d.Helper+"Sorts")
		delete(names, "views")
	}
	for dir, ns := range names {
		taken, err := declared(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			return res, err
		}
		for _, n := range ns {
			if taken[n] {
				return res, fmt.Errorf("%s already declares %s, which make:crud would add: choose another model name", dirLabel(dir), n)
			}
		}
	}
	out := make([][]byte, len(files))
	for i, f := range files {
		if out[i], err = render("templates/crud/"+f[0], "", d); err != nil {
			return res, err
		}
	}
	undo := func(err error) (CrudResult, error) {
		for _, c := range res.Created {
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(c)))
		}
		if res.Kit != "" {
			_ = os.Remove(filepath.Join(root, "views", "ui"))
		}
		return CrudResult{}, fmt.Errorf("%w (the files written were removed)", err)
	}
	if !res.API && !HasUI(root) {
		res.Kit = GuessKit(root)
		written, err := WriteUI(root, mod, res.Kit)
		res.Created = append(res.Created, written...)
		if err != nil {
			return undo(err)
		}
	}
	for i, f := range files {
		if err := writeNew(filepath.Join(root, filepath.FromSlash(f[1])), out[i], 0o644); err != nil {
			return undo(err)
		}
		res.Created = append(res.Created, f[1])
	}
	if res.API {
		res.Routed, err = addRoutesCall(filepath.Join(root, "routes", "api.go"), d.Plural, "api")
		return res, err
	}
	if res.Routed, err = addRoutesCall(filepath.Join(root, "routes", "web.go"), d.Plural, "pages"); err != nil {
		return res, err
	}
	res.Linked, err = addNavLink(filepath.Join(root, "views", "layout.templ"), d.Path+".index", d.Key+".title")
	return res, err
}

// addRoutesCall adds plural(group) at the end of Register in file
// (routes/web.go's pages, routes/api.go's api), when Register declares
// that group as anetos new writes it, and reports whether Register calls
// plural.
func addRoutesCall(file, plural, group string) (bool, error) {
	src, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	if err != nil {
		return false, nil //nolint:nilerr // not ours to fix: the app adds the call
	}
	var reg *ast.FuncDecl
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "Register" && fn.Body != nil {
			reg = fn
		}
	}
	if reg == nil {
		return false, nil
	}
	hasGroup, called := false, false
	ast.Inspect(reg.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE && len(n.Lhs) == 1 {
				if id, ok := n.Lhs[0].(*ast.Ident); ok && id.Name == group {
					hasGroup = true
				}
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == plural {
				called = true
			}
		}
		return true
	})
	if called {
		return true, nil
	}
	if !hasGroup {
		return false, nil
	}
	at := fset.Position(reg.Body.Rbrace).Offset
	// The closing brace starts its line: insert before that line.
	line := bytes.LastIndexByte(src[:at], '\n') + 1
	if strings.TrimSpace(string(src[line:at])) != "" {
		return false, nil
	}
	call := "\t" + plural + "(" + group + ") // anetos make:crud\n"
	patched := append(append(append([]byte(nil), src[:line]...), call...), src[line:]...)
	return true, os.WriteFile(file, patched, 0o644)
}

// addNavLink adds @navLink(route, i18n.T(ctx, key)) to the layout's
// header nav, at its end (navEnd), when the layout has anetos new's
// navLink, and reports whether the nav has the link.
func addNavLink(layout, route, key string) (bool, error) {
	src, err := os.ReadFile(layout)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	link := fmt.Sprintf("@navLink(%q, i18n.T(ctx, %q))", route, key)
	if bytes.Contains(src, []byte(link)) {
		return true, nil
	}
	if !templDecl.Match(src) || !bytes.Contains(src, []byte("templ navLink(")) {
		return false, nil
	}
	at, _, _, ok := navEnd(src)
	if !ok {
		return false, nil
	}
	// Indent as the line before it, the nav's last link.
	prev := bytes.LastIndexByte(src[:at-1], '\n') + 1
	indent := src[prev : prev+len(src[prev:at])-len(bytes.TrimLeft(src[prev:at], " \t"))]
	line := append(append(append([]byte(nil), indent...), link...), newline(src)...)
	patched := append(append(append([]byte(nil), src[:at]...), line...), src[at:]...)
	return true, os.WriteFile(layout, patched, 0o644)
}
