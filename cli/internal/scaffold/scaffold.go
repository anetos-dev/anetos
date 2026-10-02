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
const TemplVersion = "v0.3.1020"

// Databases are the databases `anetos new --db` accepts.
var Databases = []string{"sqlite", "postgres", "mysql"}

// Project describes a new project.
type Project struct {
	Dir     string // directory to create
	Module  string // Go module path; default: the directory's name
	DB      string // sqlite, postgres or mysql
	Replace string // local Anetos checkout to use through replace directives ("" to download)
}

// projectData is what the templates see.
type projectData struct {
	Name, Title, Module, DB, Key, Replace, DBName string
	// Replace paths, quoted for go.mod when needed.
	ReplaceCore, ReplaceCLI, ReplaceDriver string
	// ReplaceOthers are the checkout's other modules (drivers and
	// plugins the project may add): module path → replace path.
	ReplaceOthers []Replacement
}

// Replacement is a replace directive of a project's go.mod.
type Replacement struct{ Path, Dir string }

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
	err = fs.WalkDir(templates, "templates/new", func(src string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(src, "templates/new/"), ".tmpl")
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
		}
		out, err := render(src, dbEnv, data)
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

// checkoutModules returns the driver and plugin modules of a Anetos
// checkout, so that a --replace project can go get them (and anetos add
// them) from it.
func checkoutModules(checkout string) ([]Replacement, error) {
	var mods []Replacement
	for _, dir := range []string{"drivers", "plugins"} {
		files, err := filepath.Glob(filepath.Join(checkout, dir, "*", "go.mod"))
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
