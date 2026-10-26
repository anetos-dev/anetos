// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/mod/modfile"
)

// ViteVersion and TypeScriptVersion are the releases a new frontend's
// package.json asks for (at least; the lockfile npm writes pins them).
const (
	ViteVersion       = "8.3.5"
	TypeScriptVersion = "7.0.2"
)

// Languages are the values of AddVite's lang: TypeScript or JavaScript.
var Languages = []string{"ts", "js"}

// ViteResult is what [AddVite] did.
type ViteResult struct {
	// Created are the files it wrote, relative to the project.
	Created []string
	// Updated are the project's files it changed: main.go, routes/web.go,
	// views/layout.templ, .gitignore, .dockerignore, Dockerfile.
	Updated []string
	// Manual says what to add by hand, for the files it couldn't change
	// (an app's own edits moved what it looks for).
	Manual []string
	// Entry is the frontend's entry ("src/main.ts").
	Entry string
}

// ErrHasFrontend is AddVite's error for a project that has frontend/
// already.
var ErrHasFrontend = errors.New("the project has frontend/ already")

type viteData struct {
	Module, Title, Image, GoMinor  string
	TS                             bool
	Ext                            string // "ts" or "js"
	ViteVersion, TypeScriptVersion string
}

// AddVite gives the web project in root a frontend that Vite builds
// (design D341–D345): frontend/ (package.json, vite.config, src/main.ts
// or main.js, a go.mod that keeps the go command out of node_modules;
// tsconfig.json for TypeScript), and the app's side: vite.New in
// main.go's setup, the /build/ route in routes/web.go, @vite.Tags in the
// layout's head, the build output and node_modules in .gitignore and
// .dockerignore, and a Node stage in the Dockerfile. lang is "ts" or
// "js". It writes nothing in an API project or one with frontend/; a
// file it can't change safely is left alone and named in Manual, with
// what to add. It doesn't install the packages.
func AddVite(root, lang string) (ViteResult, error) {
	var res ViteResult
	switch lang {
	case "ts", "js":
	default:
		return res, fmt.Errorf("the frontend's language must be one of %s", strings.Join(Languages, ", "))
	}
	if IsAPI(root) {
		return res, errors.New("an api project has no pages to load a frontend: its frontend is a separate app (see the guide)")
	}
	if _, err := os.Stat(filepath.Join(root, "frontend")); err == nil {
		return res, ErrHasFrontend
	}
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return res, err
	}
	module := modfile.ModulePath(mod)
	name := filepath.Base(root)
	if abs, err := filepath.Abs(root); err == nil {
		name = filepath.Base(abs)
	}
	d := viteData{Module: module, Title: title(name), Image: strings.ToLower(name), GoMinor: goMinor,
		TS: lang == "ts", Ext: lang, ViteVersion: ViteVersion, TypeScriptVersion: TypeScriptVersion}
	res.Entry = "src/main." + lang

	// Render everything first, so a template error writes nothing.
	type file struct {
		rel     string
		content []byte
	}
	var files []file
	err = fs.WalkDir(templates, "templates/vite", func(src string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(src, "templates/vite/"), ".tmpl")
		switch rel {
		case "tsconfig.json":
			if !d.TS {
				return nil
			}
		case "vite.config", "src/main":
			rel += "." + lang
		}
		b, err := render(src, "", d)
		if err != nil {
			return err
		}
		files = append(files, file{path.Join("frontend", rel), b})
		return nil
	})
	if err != nil {
		return res, err
	}
	for _, f := range files {
		if err := writeNew(filepath.Join(root, filepath.FromSlash(f.rel)), f.content, 0o644); err != nil {
			for _, c := range res.Created {
				_ = os.Remove(filepath.Join(root, filepath.FromSlash(c)))
			}
			return ViteResult{}, err
		}
		res.Created = append(res.Created, f.rel)
	}

	patches := []struct {
		file   string
		patch  func([]byte) ([]byte, bool)
		manual string
	}{
		{"main.go", func(b []byte) ([]byte, bool) { return patchMainVite(b, module) },
			"main.go: in setup, before web.NewServer, call `if _, err := vite.New(app, public.Files); err != nil { return nil, err }` (imports anetos.dev/anetos/view/vite and " + module + "/public)"},
		{"routes/web.go", patchRoutesVite,
			"routes/web.go: route the build, `r.Get(\"/build/{path...}\", web.WrapHandler(vite.Handler()))` (import anetos.dev/anetos/view/vite)"},
		{"views/layout.templ", func(b []byte) ([]byte, bool) { return patchLayoutVite(b, res.Entry) },
			"views/layout.templ: in <head>, load the entry with `@vite.Tags(\"" + res.Entry + "\")` (import anetos.dev/anetos/view/vite), once main.go calls vite.New"},
		{".gitignore", func(b []byte) ([]byte, bool) { return appendLines(b, gitignoreVite) }, ""},
		{".dockerignore", func(b []byte) ([]byte, bool) { return appendLines(b, dockerignoreVite) }, ""},
		{"Dockerfile", patchDockerfileVite,
			"Dockerfile: build the frontend in a Node stage (its package manager's install, then its build script, in frontend/), copy its public/build into the Go stage after `COPY . .`, and run `go tool anetos build --no-frontend` there"},
	}
	mainPatched := false
	for _, p := range patches {
		full := filepath.Join(root, filepath.FromSlash(p.file))
		b, err := os.ReadFile(full)
		if errors.Is(err, os.ErrNotExist) && p.manual == "" {
			continue // no .gitignore: nothing to add to
		}
		var out []byte
		ok := false
		// The layout's tags need vite.New: without it, every page fails.
		if err == nil && (p.file != "views/layout.templ" || mainPatched) {
			out, ok = p.patch(lf(b)) // a Windows checkout's CRLF, kept
		}
		if !ok {
			if p.manual != "" {
				res.Manual = append(res.Manual, p.manual)
			}
			continue
		}
		if err := os.WriteFile(full, crlf(out, b), 0o644); err != nil {
			return res, err
		}
		mainPatched = mainPatched || p.file == "main.go"
		res.Updated = append(res.Updated, p.file)
	}
	return res, nil
}

const gitignoreVite = `
# The frontend's packages and build (anetos build makes it)
/frontend/node_modules/
/public/build/
`

const dockerignoreVite = `/frontend/node_modules/
/public/build/
`

// appendLines appends lines to a file that doesn't have them yet.
func appendLines(b []byte, lines string) ([]byte, bool) {
	if bytes.Contains(b, []byte("/public/build/")) {
		return nil, false
	}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	return append(b, lines...), true
}

var newServerLine = regexp.MustCompile(`(?m)^(\t)srv, err := web\.NewServer\(app\)\n`)

// patchMainVite adds vite.New to setup, before web.NewServer, and its
// imports.
func patchMainVite(src []byte, module string) ([]byte, bool) {
	s := string(src)
	loc := newServerLine.FindStringIndex(s)
	if loc == nil || strings.Contains(s, "vite.New(") || len(newServerLine.FindAllString(s, -1)) != 1 {
		return nil, false
	}
	block := "\t// The frontend Vite builds (frontend/, into public/build): @vite.Tags\n" +
		"\t// in views/layout.templ, and /build/ in routes/web.go.\n" +
		"\tif _, err := vite.New(app, public.Files); err != nil {\n\t\treturn nil, err\n\t}\n"
	s = s[:loc[0]] + block + s[loc[0]:]
	s, ok := addImports(s, `"anetos.dev/anetos/view/vite"`, `"`+module+`/public"`)
	if !ok {
		return nil, false
	}
	out, err := format.Source([]byte(s))
	if err != nil {
		return nil, false
	}
	return out, true
}

var assetsRoute = regexp.MustCompile(`(?m)^([ \t]*)r\.Get\("/assets/\{path\.\.\.\}"[^\n]*\n`)

// patchRoutesVite routes /build/ after /assets/ in routes/web.go.
func patchRoutesVite(src []byte) ([]byte, bool) {
	s := string(src)
	loc := assetsRoute.FindStringSubmatchIndex(s)
	if loc == nil || strings.Contains(s, "vite.Handler()") {
		return nil, false
	}
	indent := s[loc[2]:loc[3]]
	s = s[:loc[1]] + indent + "r.Get(\"/build/{path...}\", web.WrapHandler(vite.Handler()))\n" + s[loc[1]:]
	s, ok := addImports(s, `"anetos.dev/anetos/view/vite"`)
	if !ok {
		return nil, false
	}
	out, err := format.Source([]byte(s))
	if err != nil {
		return nil, false
	}
	return out, true
}

var uiHeadLine = regexp.MustCompile(`(?m)^([ \t]*)@ui\.Head\(\)[ \t]*\n`)

// patchLayoutVite loads the entry after @ui.Head() (the CSS framework's
// stylesheet), so the frontend's styles come later and win.
func patchLayoutVite(src []byte, entry string) ([]byte, bool) {
	s := string(src)
	loc := uiHeadLine.FindStringSubmatchIndex(s)
	if loc == nil || strings.Contains(s, "vite.Tags(") || len(uiHeadLine.FindAllString(s, -1)) != 1 {
		return nil, false
	}
	indent := s[loc[2]:loc[3]]
	s = s[:loc[1]] + indent + "@vite.Tags(\"" + entry + "\")\n" + s[loc[1]:]
	return addTemplImport(s, `"anetos.dev/anetos/view/vite"`)
}

// addImports adds Go imports to the first import block, beside the
// anetos.dev ones (or the module's own, for a path of the module), for
// gofmt to sort.
func addImports(s string, imports ...string) (string, bool) {
	start := strings.Index(s, "import (\n")
	if start < 0 {
		return s, false
	}
	end := strings.Index(s[start:], "\n)\n")
	if end < 0 {
		return s, false
	}
	end += start
	block := s[start+len("import (\n") : end+1]
	lines := strings.SplitAfter(block, "\n")
	for _, imp := range imports {
		if strings.Contains(block, imp) {
			continue
		}
		// Insert after the last line of the group whose first path segment
		// matches: anetos.dev/… for the framework's, the module's for its own.
		prefix := `"anetos.dev/`
		if !strings.HasPrefix(imp, prefix) {
			prefix = imp[:strings.Index(imp[1:], "/")+2]
		}
		at := -1
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), prefix) {
				at = i
			}
		}
		if at < 0 {
			return s, false
		}
		lines = append(lines[:at+1], append([]string{"\t" + imp + "\n"}, lines[at+1:]...)...)
	}
	return s[:start+len("import (\n")] + strings.Join(lines, "") + s[end+1:], true
}

// addTemplImport adds an import to a templ file's import block, sorted
// among the anetos.dev ones.
func addTemplImport(s, imp string) ([]byte, bool) {
	out, ok := addImports(s, imp)
	if !ok {
		return nil, false
	}
	// templ files aren't gofmt's: sort the group by hand.
	start := strings.Index(out, "import (\n") + len("import (\n")
	end := start + strings.Index(out[start:], "\n)\n")
	lines := strings.Split(out[start:end], "\n")
	groupStart := -1
	for i := 0; i <= len(lines); i++ {
		in := i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), `"anetos.dev/`)
		switch {
		case in && groupStart < 0:
			groupStart = i
		case !in && groupStart >= 0:
			sortLines(lines[groupStart:i])
			groupStart = -1
		}
	}
	return []byte(out[:start] + strings.Join(lines, "\n") + out[end:]), true
}

func sortLines(l []string) {
	for i := 1; i < len(l); i++ {
		for j := i; j > 0 && strings.TrimSpace(l[j]) < strings.TrimSpace(l[j-1]); j-- {
			l[j], l[j-1] = l[j-1], l[j]
		}
	}
}

const dockerFrontendStage = `# The frontend (frontend/): built by Vite into public/build, which the
# Go stage embeds. The packages first: in BuildKit's cache. With pnpm,
# yarn or bun, copy its lockfile and install with it instead.
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY frontend/ ./
RUN npm run build

`

var (
	golangStage = regexp.MustCompile(`(?m)^FROM golang:[^\n]*\n`)
	copyAll     = regexp.MustCompile(`(?m)^COPY \. \.\n`)
	anetosBuild = regexp.MustCompile(`go tool anetos build -o `)
)

// patchDockerfileVite adds the Node stage, copies its build into the Go
// stage, and builds the binary without building the frontend again.
func patchDockerfileVite(src []byte) ([]byte, bool) {
	s := string(src)
	if strings.Contains(s, "AS frontend") || len(golangStage.FindAllString(s, -1)) != 1 ||
		len(copyAll.FindAllString(s, -1)) != 1 || len(anetosBuild.FindAllString(s, -1)) != 1 {
		return nil, false
	}
	loc := copyAll.FindStringIndex(s)
	s = s[:loc[1]] + "COPY --from=frontend /src/public/build ./public/build\n" + s[loc[1]:]
	s = anetosBuild.ReplaceAllString(s, "go tool anetos build --no-frontend -o ")
	loc = golangStage.FindStringIndex(s)
	s = s[:loc[0]] + dockerFrontendStage + s[loc[0]:]
	return []byte(s), true
}
