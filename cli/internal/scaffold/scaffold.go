// SPDX-License-Identifier: Apache-2.0

// Package scaffold creates Anetos projects (anetos new) and adds files to
// them (anetos make:*), from the templates embedded in this package.
package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"go/build"
	"go/format"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"unicode"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"anetos.dev/anetos/internal/appkey"
	"anetos.dev/anetos/internal/naming"
)

//go:embed templates
var templates embed.FS

// TemplVersion is the templ release new projects use.
const TemplVersion = "v0.3.1070"

// Databases are the databases `anetos new --db` accepts.
var Databases = []string{"sqlite", "postgres", "mysql"}

// Project describes a new project.
type Project struct {
	Dir     string // directory to create
	Module  string // Go module path; default: the directory's name
	DB      string // sqlite, postgres or mysql
	Replace string // local Anetos checkout to use through replace directives ("" to download)
	// CSS is the design kit of the web stack (see [Kits]): "anetos" (the
	// default, "" too), Anetos's starter theme, or "none", components
	// writing plain HTML without classes and an empty
	// public/static/app.css. The api stack has no pages: it must be "".
	CSS string
	// Stack is the kind of app: "web" (the default, "" too), pages
	// rendered on the server with sessions and CSRF protection, or "api",
	// JSON only.
	Stack string
}

// Kits are the design kits: the values of [Project.CSS]. A kit is the
// app's views/ui package (the components the layout and the generators'
// pages call) and its public/static/app.css, from templates/kits/<kit>,
// with templates/kits/common's views/ui files (the types every kit's
// components take). A CSS framework's kit adds the framework's files,
// as released (files without .tmpl, copied as they are; KitVersions).
var Kits = []string{"anetos", "none", "pico", "bootstrap", "bulma"}

// KitVersions are the CSS frameworks' releases the kits carry, all
// MIT-licensed; scripts/update-kits.sh fetches a framework's files.
var KitVersions = map[string]string{
	"pico":      "2.1.1",
	"bootstrap": "5.3.8",
	"bulma":     "1.0.4",
}

// Stacks are the values of [Project.Stack].
var Stacks = []string{"web", "api"}

// stackLayers are the template layers (directories of templates/new) of
// each stack. No two layers of a stack may write the same file
// (TestStackLayersDisjoint); base files that differ between stacks do so
// through projectData's Web and API.
var stackLayers = map[string][]string{
	"web": {"base", "web"},
	"api": {"base", "api"},
}

// projectData is what the templates see.
type projectData struct {
	Name, Title, Module, DB, Key, Replace, DBName string
	Stack                                         string // web or api
	Web, API                                      bool   // the stack
	Kit                                           string // the design kit, for the web stack
	KitVersion                                    string // its CSS framework's (KitVersions)
	Bin                                           string // anetos build's binary (BinaryName)
	Image                                         string // the name in lower case, for Docker and platforms
	// GoMinor is the Go release of go.mod's go line ("1.26"), for the
	// Dockerfile's golang image.
	GoMinor string
	// Replace paths, quoted for go.mod when needed.
	ReplaceCore, ReplaceCLI, ReplaceDriver string
	// ReplaceOthers are the checkout's other modules (drivers and
	// plugins the project may add): module path → replace path.
	ReplaceOthers []Replacement
}

// goMinor is the Go release new projects need: go.mod.tmpl's go line.
const goMinor = "1.26"

// Replacement is a replace directive of a project's go.mod.
type Replacement struct{ Path, Dir string }

// dbEnvProd is the production settings' database lines.
const dbEnvProd = `[[define "db-env-prod"]]
[[- if eq .DB "sqlite" -]]
DB_CONNECTION=sqlite
# The database file is DB_DATABASE, which the Dockerfile and the systemd
# unit set, in the data directory (keep it on a volume or disk).
[[- else if eq .DB "postgres" -]]
DB_CONNECTION=postgres
# The connection string (DB_URL), or DB_HOST, DB_PORT, DB_DATABASE,
# DB_USERNAME and DB_PASSWORD (TLS on and verified unless the host is
# local; DB_TLS, DB_TLS_CA). verify-full checks the server's certificate:
# add sslrootcert=/path/ca.pem for a provider's own CA.
DB_URL=postgres://[[.DBName]]:password@db.example.com:5432/[[.DBName]]?sslmode=verify-full
[[- else -]]
DB_CONNECTION=mysql
# The connection string (DB_URL, the driver's DSN), or DB_HOST, DB_PORT,
# DB_DATABASE, DB_USERNAME and DB_PASSWORD (TLS on and verified unless
# the host is local; DB_TLS, DB_TLS_CA).
DB_URL=[[.DBName]]:password@tcp(db.example.com:3306)/[[.DBName]]?tls=true
SEARCH_LANGUAGE=simple
[[- end]]
[[- end]]`

const dbEnv = `[[define "db-env"]]
[[- if eq .DB "sqlite" -]]
DB_CONNECTION=sqlite
DB_DATABASE=database/app.db
[[- else if eq .DB "postgres" -]]
DB_CONNECTION=postgres
DB_HOST=127.0.0.1
DB_PORT=5432
DB_DATABASE=[[.DBName]]
DB_USERNAME=postgres
DB_PASSWORD=
[[- else -]]
DB_CONNECTION=mysql
DB_HOST=127.0.0.1
DB_PORT=3306
DB_DATABASE=[[.DBName]]
DB_USERNAME=root
DB_PASSWORD=
[[- end]]

# Full-text search (Search in queries, t.SearchIndex in migrations)
[[- if eq .DB "mysql"]]: MySQL matches
# words as written (simple), and has no BM25 ranking
SEARCH_LANGUAGE=simple
SEARCH_RANKING=default
[[- else]]: simple
# (default) matches words as written, in any language; english also
# matches their other forms (run, running)[[if eq .DB "postgres"]], as do PostgreSQL's other
# text search configurations (german, french…)[[end]]. Changing it needs
# go run . search:reindex
SEARCH_LANGUAGE=simple
# default, or bm25[[if eq .DB "postgres"]] (needs PostgreSQL 17+ with the pg_textsearch extension)[[end]]
SEARCH_RANKING=default
[[- end]]
[[end]]`

// Create writes the project's files. The directory must not exist, or be
// empty. It returns the files written, relative to the directory.
func Create(p Project) ([]string, error) {
	abs, err := filepath.Abs(p.Dir)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(abs)
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("anetos new: the directory's name %q is used as the app's name; use letters, digits, - and _ (starting with a letter)", name)
	}
	if p.Module == "" {
		p.Module = name
	}
	if err := module.CheckImportPath(p.Module); err != nil {
		return nil, fmt.Errorf("anetos new: %w; pass a valid --module", err)
	}
	if first, _, _ := strings.Cut(p.Module, "/"); !strings.Contains(first, ".") && isStd(first) {
		return nil, fmt.Errorf("anetos new: the module path %q clashes with the standard library's %s package; pass --module=example.com/%s", p.Module, first, p.Module)
	}
	switch p.DB {
	case "sqlite", "postgres", "mysql":
	default:
		return nil, fmt.Errorf("anetos new: --db must be one of %s", strings.Join(Databases, ", "))
	}
	switch p.Stack {
	case "":
		p.Stack = "web"
	case "web", "api":
	default:
		return nil, fmt.Errorf("anetos new: --stack must be one of %s", strings.Join(Stacks, ", "))
	}
	switch {
	case p.Stack == "api" && p.CSS != "":
		return nil, errors.New("anetos new: --css is for the web stack; an api project has no pages to style")
	case p.Stack == "api":
	case p.CSS == "":
		p.CSS = "anetos"
	case slices.Contains(Kits, p.CSS):
	default:
		return nil, fmt.Errorf("anetos new: --css must be one of %s", strings.Join(Kits, ", "))
	}
	if entries, err := os.ReadDir(p.Dir); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("anetos new: %s exists and isn't empty", p.Dir)
	}
	if p.Replace != "" {
		abs, err := filepath.Abs(p.Replace)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
			return nil, fmt.Errorf("anetos new: --replace %s: no Anetos checkout there (%w)", p.Replace, err)
		}
		p.Replace = filepath.ToSlash(abs)
	}
	data := projectData{
		Name: name, Title: title(name), Module: p.Module, DB: p.DB,
		Key: appkey.Generate(), Replace: p.Replace, DBName: naming.Snake(identifier(name)),
		GoMinor: goMinor, Bin: BinaryName(p.Module), Image: strings.ToLower(name),
		Stack: p.Stack, Web: p.Stack == "web", API: p.Stack == "api", Kit: p.CSS,
	}
	if p.Replace != "" {
		data.ReplaceCore = modfile.AutoQuote(p.Replace)
		data.ReplaceCLI = modfile.AutoQuote(p.Replace + "/cli")
		data.ReplaceDriver = modfile.AutoQuote(p.Replace + "/drivers/" + p.DB)
		others, err := checkoutModules(p.Replace)
		if err != nil {
			return nil, err
		}
		for _, o := range others {
			if o.Path != "anetos.dev/anetos/drivers/"+p.DB {
				data.ReplaceOthers = append(data.ReplaceOthers, Replacement{o.Path, modfile.AutoQuote(o.Dir)})
			}
		}
	}
	// Render everything first, so a template error writes nothing.
	type file struct {
		rel     string
		content []byte
		mode    os.FileMode
	}
	var files []file
	err = walkStack(p.Stack, func(src, rel string) error {
		switch rel {
		case "env":
			rel = ".env"
		case "env.example":
			rel = ".env.example"
		case "env.testing":
			if p.DB == "sqlite" {
				return nil // tests use an in-memory database
			}
			rel = ".env.testing"
		case "gitignore":
			rel = ".gitignore"
		case "dockerignore":
			rel = ".dockerignore"
		case "deploy/app.service":
			rel = "deploy/" + name + ".service"
		}
		out, err := render(src, dbEnv+dbEnvProd, data)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if rel == ".env" {
			mode = 0o600 // holds APP_KEY
		}
		files = append(files, file{rel, out, mode})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if p.Stack == "web" {
		kit, err := renderKit(p.CSS, data)
		if err != nil {
			return nil, err
		}
		for _, f := range kit {
			files = append(files, file{f.Rel, f.Content, 0o644})
		}
	}
	slices.SortFunc(files, func(a, b file) int { return strings.Compare(a.rel, b.rel) })
	_, statErr := os.Stat(abs)
	created := errors.Is(statErr, fs.ErrNotExist)
	var written []string
	for _, f := range files {
		if err := writeNew(filepath.Join(abs, filepath.FromSlash(f.rel)), f.content, f.mode); err != nil {
			if created {
				_ = os.RemoveAll(abs) // don't leave half a project
			}
			return nil, err
		}
		written = append(written, f.rel)
	}
	return written, nil
}

// KitFile is a file of a design kit, relative to the project.
type KitFile struct {
	Rel     string
	Content []byte
}

// renderKit renders a design kit's files: templates/kits/common's and
// the kit's own.
func renderKit(kit string, data projectData) ([]KitFile, error) {
	if !slices.Contains(Kits, kit) {
		return nil, fmt.Errorf("scaffold: no design kit %q (kits: %s)", kit, strings.Join(Kits, ", "))
	}
	data.KitVersion = KitVersions[kit]
	var files []KitFile
	for _, root := range []string{"templates/kits/common", "templates/kits/" + kit} {
		err := fs.WalkDir(templates, root, func(src string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel := strings.TrimPrefix(src, root+"/")
			if !strings.HasSuffix(src, ".tmpl") {
				// A CSS framework's own files, as they are released.
				out, err := fs.ReadFile(templates, src)
				if err != nil {
					return err
				}
				files = append(files, KitFile{rel, out})
				return nil
			}
			out, err := render(src, "", data)
			if err != nil {
				return err
			}
			files = append(files, KitFile{strings.TrimSuffix(rel, ".tmpl"), out})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// HasUI reports whether the project in root has a views/ui package (a
// design kit's components, ui.go among them), which the pages make:auth
// and make:crud write call; projects made before v0.5 have none.
func HasUI(root string) bool {
	_, err := os.Stat(filepath.Join(root, "views", "ui", "ui.go"))
	return err == nil
}

// GuessKit is the design kit closest to a project's stylesheet, for a
// project made before v0.5 (without views/ui): "anetos" when
// public/static/app.css has the starter theme's cards, else "none".
func GuessKit(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "public", "static", "app.css"))
	if err == nil && bytes.Contains(b, []byte(".card")) {
		return "anetos"
	}
	return "none"
}

// WriteUI writes a design kit's views/ui package into the project in
// root (module is its module path), for a project without one; the
// stylesheet is left as it is. It returns the files written.
func WriteUI(root, module, kit string) ([]string, error) {
	if HasUI(root) {
		return nil, fmt.Errorf("%s already exists", filepath.Join(root, "views", "ui", "ui.go"))
	}
	files, err := renderKit(kit, projectData{Module: module, Kit: kit})
	if err != nil {
		return nil, err
	}
	var written []string
	for _, f := range files {
		if !strings.HasPrefix(f.Rel, "views/ui/") {
			continue
		}
		if err := writeNew(filepath.Join(root, filepath.FromSlash(f.Rel)), f.Content, 0o644); err != nil {
			return written, err
		}
		written = append(written, f.Rel)
	}
	return written, nil
}

// walkStack calls fn for each template of a stack's layers, with its
// path in the embedded files and the path it writes (relative to the
// project, without .tmpl).
func walkStack(stack string, fn func(src, rel string) error) error {
	for _, layer := range stackLayers[stack] {
		root := "templates/new/" + layer
		err := fs.WalkDir(templates, root, func(src string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			return fn(src, strings.TrimSuffix(strings.TrimPrefix(src, root+"/"), ".tmpl"))
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// render executes a template (with [[ ]] delimiters) and formats Go
// output.
func render(src, extra string, data any) ([]byte, error) {
	text, err := fs.ReadFile(templates, src)
	if err != nil {
		return nil, err
	}
	t := template.New(path.Base(src)).Delims("[[", "]]")
	if extra != "" {
		if t, err = t.Parse(extra); err != nil {
			return nil, err
		}
	}
	if t, err = t.Parse(string(text)); err != nil {
		return nil, fmt.Errorf("scaffold: %s: %w", src, err)
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("scaffold: %s: %w", src, err)
	}
	if strings.HasSuffix(src, ".go.tmpl") {
		out, err := format.Source(b.Bytes())
		if err != nil {
			return nil, fmt.Errorf("scaffold: %s: %w", src, err)
		}
		return out, nil
	}
	return b.Bytes(), nil
}

// writeNew writes a file that must not exist yet.
func writeNew(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists", path)
		}
		return err
	}
	_, err = f.Write(content)
	return errors.Join(err, f.Close())
}

// title turns "my-blog" into "My Blog".
func title(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == ' ' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// identifier turns a name into CamelCase words: "blog-post" → "BlogPost",
// "blog_post" → "BlogPost", "blogPost" → "BlogPost".
func identifier(name string) string {
	var b strings.Builder
	up := true
	for _, r := range name {
		switch {
		case r == '-' || r == '_' || r == ' ' || r == '.':
			up = true
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if up {
				r = unicode.ToUpper(r)
				up = false
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isStd reports whether name is a standard library package (as far as the
// Go installation this tool was built with can tell).
func isStd(name string) bool {
	pkg, err := build.Default.Import(name, "", build.FindOnly)
	return err == nil && pkg.Goroot
}

// checkoutModules returns the admin, driver and plugin modules of an
// Anetos checkout, so that a --replace project can go get them (and
// anetos add them) from it.
func checkoutModules(checkout string) ([]Replacement, error) {
	var mods []Replacement
	for _, pattern := range []string{"admin", "drivers/*", "plugins/*"} {
		files, err := filepath.Glob(filepath.Join(checkout, filepath.FromSlash(pattern), "go.mod"))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			if path := modfile.ModulePath(b); path != "" {
				mods = append(mods, Replacement{path, filepath.ToSlash(filepath.Dir(f))})
			}
		}
	}
	return mods, nil
}

// BinaryName is go build's name for a module's binary, which anetos
// build writes to bin/: the path's last element, or the one before a
// major version suffix (example.com/blog/v2: blog).
func BinaryName(module string) string {
	name := path.Base(module)
	if len(name) > 1 && name[0] == 'v' && strings.Trim(name[1:], "0123456789") == "" {
		if dir := path.Dir(module); dir != "." && dir != "/" {
			name = path.Base(dir)
		}
	}
	if name == "" || name == "." || name == "/" {
		name = "app"
	}
	return name
}
